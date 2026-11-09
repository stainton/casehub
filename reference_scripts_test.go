package main

import (
	"encoding/json"
	"testing"
	"time"

	"casehub/internal/core"
)

func referenceIDs(t *testing.T, body map[string]json.RawMessage) []string {
	t.Helper()
	var context struct {
		ReferenceScripts []referenceScript `json:"referenceScripts"`
	}
	if raw, ok := body["context"]; ok {
		if err := json.Unmarshal(raw, &context); err != nil {
			t.Fatal(err)
		}
	}
	ids := []string{}
	for _, r := range context.ReferenceScripts {
		ids = append(ids, r.CaseID)
	}
	return ids
}

func TestReferenceScriptsPickSameRequirementNewestCopySameModuleFirst(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	state := core.State{
		Versions: []core.Version{{ID: "v1"}, {ID: "v2"}, {ID: "old", ScriptsArchived: true}},
		Scripts: []core.Script{
			{VersionID: "v1", CaseID: "TC-PRESET-SAVE-FUNC-002", Status: "generated", Code: "old copy", UpdatedAt: t0},
			{VersionID: "v2", CaseID: "TC-PRESET-SAVE-FUNC-002", Status: "generated", Code: "new copy", UpdatedAt: t0.Add(time.Hour)},
			{VersionID: "v1", CaseID: "TC-PRESET-LIST-FUNC-001", Status: "generated", Code: "other module", UpdatedAt: t0.Add(2 * time.Hour)},
			{VersionID: "v1", CaseID: "TC-PRESET-SAVE-FUNC-001", Status: "generated", Code: "the case itself", UpdatedAt: t0},
			{VersionID: "v1", CaseID: "TC-PRESET-SAVE-FUNC-003", Status: "blocked", Code: "", UpdatedAt: t0},
			{VersionID: "old", CaseID: "TC-PRESET-SAVE-FUNC-004", Status: "generated", Code: "archived", UpdatedAt: t0},
			{VersionID: "v1", CaseID: "TC-LOGIN-AUTH-FUNC-001", Status: "generated", Code: "other requirement", UpdatedAt: t0},
			{VersionID: "v1", CaseID: "TC-PRESET-SAVE-FUNC-005", Status: "generated", Code: "timed out", UpdatedAt: t0.Add(3 * time.Hour),
				Deviations: []core.RiskNote{{Risk: "medium", Summary: "生成超时，脚本尚未完成验证，请人工复核"}}},
		},
	}
	body := map[string]json.RawMessage{
		"cases":   json.RawMessage(`[{"id":"TC-PRESET-SAVE-FUNC-001"}]`),
		"context": json.RawMessage(`{"instructions":"keep me"}`),
	}
	addReferenceScripts(body, state)
	got := referenceIDs(t, body)
	if len(got) != 2 || got[0] != "TC-PRESET-SAVE-FUNC-002" || got[1] != "TC-PRESET-LIST-FUNC-001" {
		t.Fatalf("references = %v", got)
	}
	var context map[string]json.RawMessage
	_ = json.Unmarshal(body["context"], &context)
	if string(context["instructions"]) != `"keep me"` {
		t.Fatalf("existing context lost: %s", body["context"])
	}
	var refs []referenceScript
	_ = json.Unmarshal(context["referenceScripts"], &refs)
	if refs[0].Code != "new copy" {
		t.Fatalf("expected the newest copy, got %q", refs[0].Code)
	}
}

func TestReferenceScriptsLeaveRequestsAloneWhenNothingApplies(t *testing.T) {
	state := core.State{Scripts: []core.Script{{VersionID: "v1", CaseID: "TC-LOGIN-AUTH-FUNC-001", Status: "generated", Code: "x"}}}
	for name, body := range map[string]map[string]json.RawMessage{
		"no relevant script": {"cases": json.RawMessage(`[{"id":"TC-PRESET-SAVE-FUNC-001"}]`)},
		"caller already set": {"cases": json.RawMessage(`[{"id":"TC-LOGIN-AUTH-FUNC-002"}]`), "context": json.RawMessage(`{"referenceScripts":[]}`)},
		"unparseable cases":  {"cases": json.RawMessage(`"nope"`)},
	} {
		before, _ := json.Marshal(body)
		addReferenceScripts(body, state)
		after, _ := json.Marshal(body)
		if string(before) != string(after) {
			t.Fatalf("%s: request changed: %s", name, after)
		}
	}
}
