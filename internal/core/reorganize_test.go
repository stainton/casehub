package core_test

import (
	"casehub/internal/core"
	"casehub/internal/store"
	"context"
	"sort"
	"strings"
	"testing"
)

// versionFolderOnMainline merges a test version that filed one case under a folder named after the version — the
// structure 用例重整 removes — and returns that case's ID.
func versionFolderOnMainline(t *testing.T, svc *core.Service, name, title string) string {
	t.Helper()
	st := apply(t, svc, core.Action{Type: "createVersion", Name: name})
	v := branchID(t, st, name)
	st = apply(t, svc, core.Action{Type: "createFolder", VersionID: v, ParentID: "root", Name: name})
	f := ""
	for _, x := range st.Folders {
		if x.VersionID == v && x.Name == name {
			f = x.ID
		}
	}
	st = apply(t, svc, core.Action{Type: "createCase", VersionID: v, FolderID: f, Title: title})
	id := caseIDByTitle(t, st, v, title)
	apply(t, svc, core.Action{Type: "submitRecord", VersionID: v, CaseID: id, Note: "ok", Result: "passed"})
	apply(t, svc, core.Action{Type: "merge", VersionID: v})
	return id
}

func reorg(products [][2]string, assignments ...core.ReorgAssignment) *core.Reorganization {
	r := &core.Reorganization{Assignments: assignments}
	for _, p := range products {
		r.Products = append(r.Products, struct {
			Code string `json:"code"`
			Name string `json:"name"`
		}{p[0], p[1]})
	}
	return r
}

func mainPaths(st core.State) map[string]string {
	name, parent := map[string]string{}, map[string]string{}
	for _, f := range st.Folders {
		if f.VersionID == "main" {
			name[f.ID], parent[f.ID] = f.Name, f.ParentID
		}
	}
	out := map[string]string{}
	for _, c := range st.Cases {
		if c.VersionID != "main" {
			continue
		}
		var parts []string
		for id := c.FolderID; id != ""; id = parent[id] {
			parts = append([]string{name[id]}, parts...)
		}
		out[c.ID] = strings.Join(parts, "/")
	}
	return out
}

func TestReorganizeMainlineRebuildsFoldersAndRenamesCases(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	ctx := context.Background()
	v1 := versionFolderOnMainline(t, svc, "V1", "资产菜单同步")
	v2 := versionFolderOnMainline(t, svc, "V2", "批量同步")
	products := [][2]string{{"AIGC", "AIGC 平台"}}
	plan := reorg(products,
		core.ReorgAssignment{CaseID: v1, Product: "AIGC", ModuleCode: "ASSET", Folders: []string{"资产区", "同步"}, Category: "FUNC"},
		core.ReorgAssignment{CaseID: v2, Product: "AIGC", ModuleCode: "ASSET", Folders: []string{"资产区", "同步"}, Category: "FUNC"},
		core.ReorgAssignment{CaseID: "CASE-0001", Product: "AIGC", ModuleCode: "LOGIN", Folders: []string{"登录"}, Category: "FUNC"})

	// Both versions are still open: merging either later would bring the old folders and IDs back.
	if _, err := svc.Apply(ctx, core.Action{Type: "reorganizeMainline", Reorganize: plan}); err == nil || !strings.Contains(err.Error(), "V1") {
		t.Fatalf("open versions must block the reorganization, got %v", err)
	}
	st := apply(t, svc, core.Action{Type: "archiveVersion", VersionID: branchID(t, st0(t, svc), "V1")})
	st = apply(t, svc, core.Action{Type: "archiveVersion", VersionID: branchID(t, st, "V2")})
	before := st.MainRevision
	st = apply(t, svc, core.Action{Type: "reorganizeMainline", Reorganize: plan})

	got := mainPaths(st)
	want := map[string]string{
		"TC-AIGC-ASSET-FUNC-001": "全部用例/AIGC 平台/资产区/同步",
		"TC-AIGC-ASSET-FUNC-002": "全部用例/AIGC 平台/资产区/同步",
		"TC-AIGC-LOGIN-FUNC-001": "全部用例/AIGC 平台/登录",
		"CASE-0002":              "全部用例/登录与认证", // not in this batch: stays where it was
	}
	if len(got) != len(want) {
		t.Fatalf("mainline cases = %v", got)
	}
	for id, path := range want {
		if got[id] != path {
			t.Fatalf("%s at %q, want %q (all: %v)", id, got[id], path, got)
		}
	}
	var names []string
	for _, f := range st.Folders {
		if f.VersionID == "main" {
			names = append(names, f.Name)
		}
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "AIGC 平台,全部用例,同步,登录,登录与认证,资产区" {
		t.Fatalf("emptied version folders must be deleted, mainline folders = %v", names)
	}
	if st.MainRevision != before+1 {
		t.Fatalf("main revision %d, want %d", st.MainRevision, before+1)
	}
	// Mainline's own records and history follow the case; the archived versions keep their old IDs untouched.
	moved := 0
	for _, r := range st.Records {
		if r.VersionID == "main" && r.CaseID == "TC-AIGC-ASSET-FUNC-001" {
			moved++
		}
		if r.VersionID == "main" && r.CaseID == v1 {
			t.Fatalf("mainline record still names %s", v1)
		}
	}
	if moved != 1 {
		t.Fatalf("archived record of %s not renamed (%d)", v1, moved)
	}
	reorganized := 0
	for _, h := range st.Histories {
		if h.VersionID == "main" && h.CaseID == v1 {
			t.Fatalf("mainline history still names %s", v1)
		}
		if h.Action == "reorganize" && h.CaseID == "TC-AIGC-ASSET-FUNC-001" && h.Before.ID == v1 {
			reorganized++
		}
	}
	if reorganized != 1 {
		t.Fatal("the rename must be recorded in history")
	}
	if c := caseIn(st, branchID(t, st, "V1"), v1); c == nil {
		t.Fatal("archived version must keep its case under the old ID")
	}

	// A second batch continues the numbering; a re-run of an already reorganized case keeps its ID.
	st = apply(t, svc, core.Action{Type: "reorganizeMainline", Reorganize: reorg(products,
		core.ReorgAssignment{CaseID: "CASE-0002", Product: "AIGC", ModuleCode: "LOGIN", Folders: []string{"登录"}, Category: "FUNC"},
		core.ReorgAssignment{CaseID: "TC-AIGC-LOGIN-FUNC-001", Product: "AIGC", ModuleCode: "LOGIN", Folders: []string{"登录"}, Category: "FUNC"})})
	got = mainPaths(st)
	if got["TC-AIGC-LOGIN-FUNC-002"] != "全部用例/AIGC 平台/登录" || got["TC-AIGC-LOGIN-FUNC-001"] != "全部用例/AIGC 平台/登录" {
		t.Fatalf("second batch: %v", got)
	}
	for _, f := range st.Folders {
		if f.VersionID == "main" && f.Name == "登录与认证" {
			t.Fatal("the last emptied folder must be deleted")
		}
	}
}

func TestReorganizeMainlineRejectsInvalidPlans(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	ok := core.ReorgAssignment{CaseID: "CASE-0001", Product: "AIGC", ModuleCode: "LOGIN", Folders: []string{"登录"}, Category: "FUNC"}
	products := [][2]string{{"AIGC", "AIGC 平台"}}
	for name, plan := range map[string]*core.Reorganization{
		"empty":           reorg(products),
		"unknown case":    reorg(products, core.ReorgAssignment{CaseID: "NOPE", Product: "AIGC", ModuleCode: "LOGIN", Folders: []string{"登录"}, Category: "FUNC"}),
		"twice":           reorg(products, ok, ok),
		"unknown product": reorg([][2]string{{"JOY", "JoyData"}}, ok),
		"bad code":        reorg([][2]string{{"a-b", "x"}}, ok),
		"bad category":    reorg(products, core.ReorgAssignment{CaseID: "CASE-0001", Product: "AIGC", ModuleCode: "LOGIN", Folders: []string{"登录"}, Category: "X"}),
		"slash":           reorg(products, core.ReorgAssignment{CaseID: "CASE-0001", Product: "AIGC", ModuleCode: "LOGIN", Folders: []string{"a/b"}, Category: "FUNC"}),
		"too deep":        reorg(products, core.ReorgAssignment{CaseID: "CASE-0001", Product: "AIGC", ModuleCode: "LOGIN", Folders: []string{"a", "b", "c", "d", "e"}, Category: "FUNC"}),
	} {
		if _, err := svc.Apply(context.Background(), core.Action{Type: "reorganizeMainline", Reorganize: plan}); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := svc.Apply(context.Background(), core.Action{Type: "reorganizeMainline"}); err == nil {
		t.Error("a missing plan must be refused")
	}
}

func st0(t *testing.T, svc *core.Service) core.State {
	t.Helper()
	st, err := svc.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func caseIn(st core.State, versionID, id string) *core.TestCase {
	for i := range st.Cases {
		if st.Cases[i].VersionID == versionID && st.Cases[i].ID == id {
			return &st.Cases[i]
		}
	}
	return nil
}
