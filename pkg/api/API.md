# CaseHub Manager API

Wire-level contract for `cmd/manager`'s HTTP API — the only service (see
repo root `README.md` for the domain model: basecase/branch/merge
semantics, folder visibility rules, etc. This doc doesn't repeat that;
read it first if you're integrating something non-trivial).

Route constants live in [`manager_api.go`](manager_api.go) (`api.Manage*`);
shared request/response types not covered by `pkg/model` live in
[`types.go`](types.go). Both files are the source of truth — if this doc
and the code disagree, trust the code and file a fix.

A machine-readable version of this same contract (for codegen, Swagger
UI/ReDoc, Postman import, etc.) is [`openapi.yaml`](openapi.yaml) — keep the
two in sync when the API changes; this doc carries the prose/behavioral
notes that don't fit cleanly into an OpenAPI schema.

## Conventions

- Base path: `/api/manage`. All request/response bodies are JSON
  (`Content-Type: application/json`).
- CORS is wide open (`Access-Control-Allow-Origin: *`) — the frontend may be
  deployed on a different origin than the manager.
- **No authentication/authorization of any kind.** `binding:"required"` tags
  in `pkg/model` request structs are decorative (no validator library is
  wired up); actual validation is hand-written per handler and only checks
  the specific fields called out below. Put a gateway/auth layer in front if
  you expose this beyond a trusted network.
- Errors are `text/plain` bodies via `http.Error` (not JSON), with a status
  code — check the status code, don't try to parse the body as structured
  data. Common codes: `400` (bad input, e.g. missing required field or
  referencing a non-existent parent), `404` (referenced id/folder/version
  not found), `409` (version name already exists), `500` (unexpected/store
  error). Endpoints that don't return a body use `204`.
- Batch write endpoints (add/update/delete/migrate test case) never fail the
  whole request because one item is bad — each item gets its own
  `ok`/`error` in the response array; check per-item, not just the HTTP
  status.
- Writes to test cases always require a `version` (branch name) in the body
  — there is no way to write to the trunk directly. Trunk-only endpoints
  below are explicitly read-only.

## Data types

### TestCase (`pkg/model.TestCase`)

```jsonc
{
  "id": 1,                       // internal PK, no business meaning — ignore on write
  "uid": 42,                     // stable id shared by every revision of this case; server-assigned on create
  "case_id": "TC-LOGIN-1",       // human-assigned unique string id; required on create
  "case_name": "Login test",
  "priority": 1,
  "description": "",
  "pre_condition": "",
  "steps": ["open login page", "enter credentials", "submit"],
  "expected_result": ["page loads", "fields accepted", "redirected to home"],
  "state": "active",             // draft|active|deprecated|history — optional, defaults to "draft" on write
  "revision": 1,                 // meaning depends on read path — see "Revisions" note below; server-assigned
  "created_at": "2026-08-14T03:50:18.145076Z"
}
```

`steps`/`expected_result` are string arrays, not a single blob. `id`,
`revision`, `created_at` are always server-assigned — set by the server on
write, safe to omit in request bodies.

**Revisions are not globally comparable.** A trunk `TestCase.revision` and a
branch `TestCase.revision` for the same `uid` are independent numberlines;
never compare across branches or against trunk. See root README's "Domain
model" section for the exact rules.

### TestExecution (`pkg/model.TestExecution`)

```jsonc
{
  "id": 1,
  "uid": 42,
  "revision": 1,               // which case revision this execution was run against
  "content": "markdown report; screenshots are inlined as base64 data URIs",
  "executed_by": "alice",
  "executed_at": "2026-08-14T03:50:18.145076Z"
}
```

### CaseFolder (`pkg/model.CasesFloder`, sic)

```jsonc
{
  "folder_id": 1,
  "folder_name": "Product A",
  "parent_id": 0,               // 0 = top-level ("product") folder; there can be many, no single hardcoded root
  "case_uids": [4, 7, 12]       // uids directly in this folder — unversioned, shared across trunk and all branches
}
```

### BatchCreateResult (`api.BatchCreateResult`)

Response element for batch endpoints:

```jsonc
{ "index": 0, "ok": true, "case": { /* TestCase */ } }
{ "index": 1, "ok": false, "error": "case_id is required" }
```

### MergeResult (`api.MergeResult`)

Response body for merge:

```jsonc
{
  "merged": [4, 7],
  "conflicts": [
    { "uid": 12, "case_id": "TC-UI-1", "reason": "trunk moved past this branch's starting revision" }
  ]
}
```

## Folders

| | |
|---|---|
| `POST /api/manage/casefolder` | Create a folder. Body: `{folder_name, parent_id}` — `parent_id:0` creates a new top-level folder (not a special/singleton root; you can have many). Non-zero `parent_id` must reference an existing folder (`404`... actually `400` if missing). Returns the created `CaseFolder`. |
| `PUT /api/manage/casefolder` | Rename. Body: `{folder_id, folder_name}`. `404` if folder doesn't exist. Returns the updated `CaseFolder`. |
| `DELETE /api/manage/casefolder` | Delete. Body: `{folder_id}`. `400` if it's top-level (`parent_id==0`, never deletable), has case uids, or has sub-folders — must be emptied first. Returns the deleted `CaseFolder`. |
| `POST /api/manage/casefolder/migrate` | Move or copy a folder to a new parent. Body: `{source_folder_id, target_parent_id, is_copy}`. `400` if source == target (can't be its own parent) or source is top-level (never movable/copyable). Copy only copies the folder's own name + case_uids, not sub-folders recursively. Returns the resulting `CaseFolder`. |
| `GET /api/manage/casefolder` | List **all** folders, flat (no tree nesting) — build the tree client-side from `parent_id`. |

## Trunk (read-only)

Trunk = merged content only. No `version` field applies here; there is no
write path.

| | |
|---|---|
| `GET /api/manage/testcase` | List merged trunk cases. Optional query filters, ANDed, exact/substring per `store` impl: `case_id`, `case_name`, `priority`, `state`, `description`. |
| `GET /api/manage/testcase/{id}` | `{id}` is `uid`. Latest merged `TestCase`. `404` if never merged (uid unknown or `revision==0`). |
| `GET /api/manage/testcase/{id}/history` | All merged revisions for this uid, oldest first (array of `TestCase`). |
| `GET /api/manage/testcase/{id}/executions?revision=N` | Test executions recorded against merged revision `N`. `revision` query param is **required** (`400` if missing/non-numeric). |

## Cases (write — `version` required in body)

All of these operate inside a version branch; nothing here touches trunk
until an explicit merge.

| | |
|---|---|
| `POST /api/manage/testcase` | Batch create. Body: `{folder_id, version, test_cases: [TestCase...]}`. Each case needs a non-empty `case_id` (server allocates `uid`; fails per-item if `case_id` already exists). `400` if `version` doesn't exist or folder not found. Response: `[BatchCreateResult...]`. |
| `PUT /api/manage/testcase` | Batch update. Same body shape. Resolves target case by `uid` if present in the item, else by `case_id` lookup against trunk's id registry (works even for uids created-but-not-yet-merged in this same branch). Response: `[BatchCreateResult...]`. |
| `DELETE /api/manage/testcase` | Body: `{folder_id, test_cases: [TestCase...]}` (only `.uid` of each item is used). Unlinks cases from `folder_id` — **does not delete case data or history**, just the folder association. No `version` needed (folder links aren't versioned). |
| `POST /api/manage/testcase/migrate` | Body: `{source_folder_id, target_folder_id, test_cases: [TestCase...], is_copy}`. Moves (or copies, if `is_copy`) folder association only — case content is untouched, there's only ever one copy of the data. |

## Branches

| | |
|---|---|
| `POST /api/manage/version` | Create a branch. Body: `{version}`. `409` if the name already exists. Echoes the request body back on success. |
| `GET /api/manage/version` | List all branch names (`string[]`). |
| `POST /api/manage/version/{name}/merge` | Merge into trunk. Body: `{uids?}` — omit/empty to merge every pending uid in the branch. `404` if `{name}` doesn't exist. Returns `MergeResult`. Per-uid: content and executions merge independently (see root README) — a content conflict on one uid never blocks another uid or that uid's execution records. |
| `POST /api/manage/version/{name}/pull` | Pull trunk's current merged content for the given uids into this branch as a starting point. Body: `{uids}` (required, non-empty). `404` if `{name}` doesn't exist. Idempotent at an unchanged trunk revision. Response: `[BatchCreateResult...]` (per-uid; a uid absent from trunk fails just that item). |

## Branch cases

| | |
|---|---|
| `GET /api/manage/version/{name}/testcase` | Latest local revision of every uid touched in this branch (merged or not). Add `?pending=true` to get only uids with unmerged content edits (i.e. merge candidates) — this excludes pulled-but-untouched cases. `404` if `{name}` doesn't exist. |
| `GET /api/manage/version/{name}/testcase/{id}` | `{id}` is `uid`. Latest local `TestCase` in this branch. `404` if branch doesn't exist, or uid was never touched in it. |
| `GET /api/manage/version/{name}/testcase/{id}/history` | All local revisions for this uid in this branch, oldest first. |
| `DELETE /api/manage/version/{name}/testcase/{id}/history/{revision}` | Delete one local (unmerged) edit — for undoing a mistake before merge. `204` on success. |

## Branch executions

| | |
|---|---|
| `POST /api/manage/version/{name}/testcase/{id}/executions` | Record a test run. Body: `{revision, content, executed_by}` — `revision` is the **branch-local** revision (from the branch case, not trunk). Returns the created `TestExecution`. |
| `GET /api/manage/version/{name}/testcase/{id}/executions?revision=N` | Executions recorded in this branch against local revision `N`. `revision` required. |
| `DELETE /api/manage/version/{name}/testcase/executions/{id}` | `{id}` is the execution's own row id (not uid). `204` on success. |

## Health

`GET /` (root, not under `/api/manage`) — `200` with no body. Used for
readiness/liveness probes; only registered by `NewManagerMux` (i.e. the real
`cmd/manager/main.go` binary), not by `RegisterManagerAPI` (used by
`cmd/manager/mock`, which serves the frontend at `/` instead).
