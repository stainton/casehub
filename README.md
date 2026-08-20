# casehub

Test case management with a git-like branching model: cases live on a read-only
baseline ("basecase"/trunk) and are edited only inside named version branches,
merged back explicitly. Backend: Go + Postgres. Frontend: vanilla JS SPA.

This doc is written for an AI picking up the repo cold — architecture and
non-obvious invariants, not a tutorial. Read the linked source files for
exact SQL/logic; don't re-derive what's stated here.

## Status: which service is live

- **`cmd/manager`** is the only service. Owns `case_folders` (directory
  tree) and the basecase/branch tables (below). All frontend traffic goes
  here.
- There used to be a second, standalone case-CRUD service (`cmd/api`) from
  an earlier design, plus an HTTP client for it (`cmd/manager/app/client`).
  Manager never called it (no wiring in `cmd/manager/app/app.go`) — both
  were deleted as dead code; see git history if you need to resurrect them.
  The manager-owns-everything design is a deliberate choice made this
  project — don't reintroduce a second case-CRUD service without the user
  asking.

## Domain model (the part worth understanding before touching anything)

Package `cmd/manager/app/basecase` is the **sole** gateway to these tables —
table/SQL names are unexported, enforced by package structure not runtime
checks.

**Trunk** (`base_cases`, `case_history`, `test_executions`): read-only from
outside this package. `base_cases.revision` is the pointer to the currently
active `case_history` row per uid. `revision=0` means never merged (case
invisible everywhere until it is).

**Branches**: `POST /api/manage/version` registers a name in `case_versions`
and creates two physical tables per branch, `history_<name>` /
`executions_<name>` (same shape as the trunk tables + `merged`/`is_pull`
bookkeeping columns — see below). All case creation/editing/execution-
recording happens only inside a branch; nothing lands on trunk until merged.
A branch's local `revision` is independent per-branch bookkeeping, not the
same numberline as trunk's.

**Pull** (`PullFromBase`, `version.go`): copies a case's current trunk
content into a branch as its starting point. The local revision recorded is
**exactly trunk's current revision, not +1** — before any edit it's
byte-identical to trunk, not a new revision. That row is flagged
`is_pull=TRUE`. Idempotent: re-pulling at an unchanged trunk revision is a
no-op (checked via `selectVersionHasRevisionTpl`), never a duplicate/error.

**Edit** (`UpsertVersionCase`): local revision = `MAX(unmerged local
revision) + 1`, or `trunk_revision + 1` if this is the first touch. This
number is the conflict-detection anchor — see merge below.

**Merge** (`MergeVersion`/`mergeOne`, `merge.go`) processes each uid
independently, in one of two orthogonal tracks that never block each other:

1. **Content**: only rows that are real edits (`NOT merged AND NOT
   is_pull`) count as pending. If none, uid is silently skipped — not a
   conflict, nothing to do (a pulled-but-never-edited case merges nothing;
   see below for its executions). Otherwise: if
   `trunk.revision >= branch's oldest pending local revision`, trunk moved
   since this branch started editing → **conflict**, reported, not merged.
   Else the pending edits are renumbered sequentially onto trunk
   (`trunk.revision+1, +2, ...`) and `base_cases.revision` advances.
2. **Executions**: tracked independently per-row via their own `merged`
   flag on `executions_<version>`, **not tied to whether the case content
   changed**. `mergeUnpulledExecutions` specifically handles "case content
   untouched (is_pull) but new test records exist": copies them straight
   onto trunk's *current* revision (no new case_history row — content
   didn't change, nothing to version) as long as trunk hasn't moved past
   the pull point. Never blocked by a content conflict on the same uid.
   Each execution is marked merged individually (not by revision range) so
   new ones recorded later on the same revision are still pickable next
   merge.

Merged rows are **marked `merged=TRUE`, never deleted**. A branch's own
browsing views (`ListVersionCases`, history, detail) never filter by
`merged`/`is_pull` — a branch keeps showing everything it ever touched,
merged or not. Only merge-candidate queries filter. This means: **a branch
never "loses" content after merging**, and it's explicitly a frozen fork —
trunk's later changes never sync back to it automatically.

**Folders** (`case_folders`, owned by `cmd/manager/app/store`): flat table,
`parent_id`, unversioned/shared across trunk and every branch — the *same*
row set, not duplicated per branch. `parent_id=0` marks a top-level
("product") folder; there can be many side-by-side (create via
`POST /api/manage/casefolder` with `parent_id:0`), not one hardcoded root.
Top-level folders can be renamed but never deleted/migrated
(`folder.ParentID == 0` guard in `svr.go`, checked dynamically — no
special-cased ID). No `-root-folder-name` startup flag or auto-seeded root
row anymore; a fresh DB starts with zero folders.

**Frontend-only folder-visibility filter** (`web/app.js`, not a backend
concept): the *trunk* view hides any folder with zero merged content
(recursively) — merging content is what makes a folder "exist" from
trunk's perspective. Each *branch's own* view hides trunk-inherited folders
that this specific branch hasn't touched (pulled into or created), but a
branch's own newly-created (still-empty) folders stay visible immediately —
git-like "no empty directories are inherited for free, but your own
workspace is always visible."

## Directory map

```
cmd/manager/
  app/
    app.go                Bootstrap() + Run() — wires store+basecase+HTTP
    flag.go                Options / CLI flags
    svr.go                 all HTTP handlers, registerManagerAPI() route table
    basecase/               trunk + branch data layer (THE domain logic — read this first)
      base.go               connect/bootstrap, ensureVersionTables (idempotent migrations)
      trunk.go               trunk reads: GetCase, QueryCases, GetExecutions, ResolveUID
      version.go              branch writes/reads: NewVersion, UpsertVersionCase, PullFromBase,
                               GetVersionCase(History), ListVersionCases/ListPendingVersionCases
      merge.go                MergeVersion/mergeOne — content+execution merge logic (read the domain model above first)
      scan.go                 pgx row -> model.TestCase/TestExecution scanning helpers
      sqls.go                 every SQL string in this package, heavily commented
    store/
      store.go               Store interface (case_folders only)
      postgres/               postgres implementation
  main.go                    real entry point: manager API only, no bundled frontend
  mock/                      GITIGNORED — bundles manager API + web/ in one process, for local debug only
pkg/
  api/                       route-string constants + shared request/response types; API.md is the full wire-level contract, openapi.yaml the machine-readable version
  model/                      TestCase/TestExecution/CasesFloder + manager Request* structs
web/                          GITIGNORED — frontend (vanilla JS SPA), see below
build/                        Dockerfiles + release scripts (build/mock-release.sh, Dockerfile.manager-mock also gitignored)
```

## HTTP API (manager, `cmd/manager/app/svr.go`)

All under `/api/manage/`. Write endpoints (`ManageAddTestCase`,
`ManageUpdateTestCase`) require a `version` field in the JSON body — there
is no way to write to trunk directly.

| Area | Routes |
|---|---|
| Folders | `POST/PUT/DELETE /casefolder`, `POST /casefolder/migrate`, `GET /casefolder` (flat list; frontend builds the tree) |
| Trunk (read-only) | `GET /testcase`, `GET /testcase/{id}`, `GET /testcase/{id}/history`, `GET /testcase/{id}/executions` |
| Cases (write, `version` required in body) | `POST/PUT/DELETE /testcase`, `POST /testcase/migrate` |
| Branches | `POST/GET /version`, `POST /version/{name}/merge`, `POST /version/{name}/pull` |
| Branch cases | `GET /version/{name}/testcase` (add `?pending=true` for merge candidates only), `GET .../testcase/{id}`, `GET .../testcase/{id}/history`, `DELETE .../testcase/{id}/history/{revision}` |
| Branch executions | `POST/GET .../testcase/{id}/executions`, `DELETE .../testcase/executions/{id}` |

`pkg/api/manager_api.go` is the source of truth for exact patterns.

## Frontend (`web/`, gitignored — not in a fresh clone)

Single `app.js` (~2000 lines), no build step, no framework. Key state to
know before editing:

- `currentContext` — `{type:"basecase"}` or `{type:"version",name}`; almost
  everything branches on this.
- `caseCacheByContext`, `expandedFolderIDsByContext` — **per-context**
  Maps, not global Sets. A folder expanded while viewing branch A must not
  appear expanded when you switch to branch B or trunk — they're
  independent even though `case_folders` rows are shared.
- `topLevelFolders()` — enumerate multi-root product folders; never assume
  a single `ROOT_FOLDER_ID`.
- `folderVisible(folder, ctx)` / `versionFolderInclusion` /
  `basecaseIncludedFolderIDs` — the folder-visibility filters described in
  the domain model above.
- `loadFolders()` is the standard "something changed, refresh everything"
  path (folders, versions, per-branch inclusion sets, currently-expanded
  case caches). Mutation handlers should call it, not hand-patch caches —
  a prior hand-patched pull-picker refresh was a real bug (stale folder
  visibility after pulling) fixed by switching it to `loadFolders()`.
- Right-click context menus (`rowContextMenu`/`showContextMenu`) are the
  only per-row actions now; there's no hover-icon-button UI left. Trunk
  rows always return `[]` from their menu builder (fully read-only, not
  just visually).

## Building & running

```sh
go build ./...                 # whole module
go vet ./... && gofmt -l .     # keep both clean before calling anything done
```

Real deployment: `build/release.sh` builds `cmd/manager/main.go` (no
frontend) into a Docker image, `-db-name`/`-db-host`/etc. as CLI flags (see
`flag.go`; no env var support at all).

Local debug/demo (frontend bundled, single process — what this session's
verification work always used):

```sh
go build -o /tmp/mock ./cmd/manager/mock
/tmp/mock -db-host=127.0.0.1 -db-name=<db> -http-port=<port> -web-dir=web
```

`-web-dir` must be an absolute path or resolved relative to the process's
actual cwd, not the repo root — a recurring source of silent 404s when
launched from a different working directory.

## Local Postgres setup used for manual verification

No sudo in this environment; use `su postgres -c "psql ..."`. A fresh test
DB needs the `appuser`/`ChangeMe_2026` role (matches `flag.go` defaults)
and `GRANT ALL ON SCHEMA public TO appuser` — `CREATE DATABASE` alone
isn't enough on newer Postgres. UI verification in this project has used
`playwright-core` + a cached Chromium at
`/root/.cache/ms-playwright/chromium-1234/chrome-linux/chrome` with
`--no-sandbox`.

## Gotchas worth not re-discovering

- Backend `binding:"required"` struct tags in `pkg/model` are
  **decorative** — nothing in this codebase enforces them (no
  validator/gin binding library). Actual validation is hand-written in
  each `svr.go` handler.
- `merged`/`is_pull` columns on `history_<version>`/`executions_<version>`
  are added via idempotent `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` in
  `ensureVersionTables`, run on every startup for every known branch — this
  is how already-deployed branch tables pick up new columns without a
  separate migration step. Follow this pattern for future schema
  additions to per-branch tables.
- A branch's revision numbers are **not** comparable across branches or to
  trunk except via the specific conflict-check inequality in `mergeOne` —
  don't assume "higher revision = more recent" globally.
- Frontend tree rows that are expanded by default on load (trunk root,
  `ROOT_FOLDER_ID`) will *collapse* if a test script clicks them expecting
  to expand — check actual state before clicking in Playwright scripts.
