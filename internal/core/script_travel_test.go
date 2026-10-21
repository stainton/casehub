package core_test

import (
	"casehub/internal/core"
	"casehub/internal/store"
	"testing"
	"time"
)

func codeOf(st core.State, version, caseID string) string {
	for _, s := range st.Scripts {
		if s.VersionID == version && s.CaseID == caseID {
			return s.Code
		}
	}
	return ""
}

// A script travels with its case the way the case does: copied down on createVersion and sync, up on merge.
func TestScriptsTravelWithTheirCases(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	save := func(version, id, code string) core.State {
		time.Sleep(3 * time.Millisecond) // UpdatedAt has millisecond resolution; copyScript compares it
		return apply(t, svc, core.Action{Type: "saveScript", VersionID: version, CaseID: id, ScriptCode: code, ScriptSummary: "x"})
	}
	save("main", "CASE-0001", "main v1")
	st := apply(t, svc, core.Action{Type: "createVersion", Name: "v1"})
	v := branchID(t, st, "v1")
	if codeOf(st, v, "CASE-0001") != "main v1" || codeOf(st, v, "CASE-0002") != "" {
		t.Fatalf("a new version copies the scripts of its cases: %+v", st.Scripts)
	}

	// Regenerated on the branch (a clean case, so the case itself does not merge): the script still reaches mainline.
	save(v, "CASE-0001", "branch v2")
	save(v, "CASE-0002", "branch only")
	st = apply(t, svc, core.Action{Type: "merge", VersionID: v})
	if codeOf(st, "main", "CASE-0001") != "branch v2" || codeOf(st, "main", "CASE-0002") != "branch only" {
		t.Fatalf("merge must carry the scripts to mainline: %+v", st.Scripts)
	}
	archived := 0
	for _, x := range st.ScriptVersions {
		if x.VersionID == "main" && x.CaseID == "CASE-0001" && x.Code == "main v1" {
			archived++
		}
	}
	if archived != 1 {
		t.Fatalf("the replaced mainline script must be archived, got %d", archived)
	}

	// Another version, regenerated on mainline later: sync pulls it, and an older copy never overwrites a newer one.
	st = apply(t, svc, core.Action{Type: "createVersion", Name: "v2"})
	w := branchID(t, st, "v2")
	save("main", "CASE-0001", "main v3")
	st = apply(t, svc, core.Action{Type: "sync", VersionID: w})
	if codeOf(st, w, "CASE-0001") != "main v3" {
		t.Fatalf("sync must pull the newer mainline script: %+v", st.Scripts)
	}
	st = apply(t, svc, core.Action{Type: "merge", VersionID: v})
	if codeOf(st, "main", "CASE-0001") != "main v3" {
		t.Fatalf("an older branch script must not overwrite a newer mainline one, got %q", codeOf(st, "main", "CASE-0001"))
	}
}
