package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"casehub/internal/core"
	"casehub/internal/store"
)

func apply(t *testing.T, service *core.Service, action core.Action) core.State {
	t.Helper()
	result, err := service.Apply(context.Background(), action)
	if err != nil {
		t.Fatalf("%s: %v", action.Type, err)
	}
	return result.State
}

func branchID(t *testing.T, state core.State, name string) string {
	t.Helper()
	for _, v := range state.Versions {
		if v.Name == name {
			return v.ID
		}
	}
	t.Fatalf("branch %q not found", name)
	return ""
}

func TestCompleteBranchWorkflow(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	state := apply(t, svc, core.Action{Type: "createVersion", Name: "迭代 A", Author: "alice"})
	branch := branchID(t, state, "迭代 A")
	if got := len(state.Cases); got != 4 {
		t.Fatalf("clone cases: got %d, want 4 total", got)
	}
	state = apply(t, svc, core.Action{Type: "createCase", VersionID: branch, FolderID: "auth", Title: "验证码登录", Priority: "P0", Author: "alice"})
	var added core.TestCase
	for _, c := range state.Cases {
		if c.VersionID == branch && c.Title == "验证码登录" {
			added = c
		}
	}
	if added.ID == "" || !added.Dirty {
		t.Fatal("new branch case was not created as dirty")
	}
	apply(t, svc, core.Action{Type: "submitRecord", VersionID: branch, CaseID: added.ID, Result: "passed", Note: "ok", Author: "tester"})
	state = apply(t, svc, core.Action{Type: "createTask", VersionID: branch, Name: "冒烟", CaseIDs: []string{added.ID}, Author: "tester"})
	if len(state.Records) != 1 || len(state.Tasks) != 1 {
		t.Fatalf("record/task missing")
	}
	state = apply(t, svc, core.Action{Type: "merge", VersionID: branch, Author: "alice"})
	found := false
	for _, c := range state.Cases {
		if c.VersionID == "main" && c.ID == added.ID {
			found = true
			if c.Result != "" {
				t.Fatal("branch result leaked into mainline")
			}
		}
	}
	if !found {
		t.Fatal("merged case missing from mainline")
	}
}

func TestMergeDetectsTextConflict(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	a := branchID(t, apply(t, svc, core.Action{Type: "createVersion", Name: "A"}), "A")
	b := branchID(t, apply(t, svc, core.Action{Type: "createVersion", Name: "B"}), "B")
	apply(t, svc, core.Action{Type: "editCase", VersionID: a, CaseID: "CASE-0001", Title: "A 修改", Priority: "P0"})
	apply(t, svc, core.Action{Type: "editCase", VersionID: b, CaseID: "CASE-0001", Title: "B 修改", Priority: "P0"})
	apply(t, svc, core.Action{Type: "merge", VersionID: a})
	_, err := svc.Apply(context.Background(), core.Action{Type: "merge", VersionID: b})
	var conflict core.ConflictError
	if !errors.As(err, &conflict) || len(conflict.Cases) != 1 {
		t.Fatalf("expected conflict, got %v", err)
	}
	apply(t, svc, core.Action{Type: "editCase", VersionID: b, CaseID: "CASE-0001", Title: "人工解决后的内容", Priority: "P0", Force: true})
	apply(t, svc, core.Action{Type: "merge", VersionID: b})
}

func TestMainlineIsReadOnly(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	_, err := svc.Apply(context.Background(), core.Action{Type: "createCase", VersionID: "main", FolderID: "auth", Title: "forbidden"})
	if err == nil {
		t.Fatal("mainline mutation must fail")
	}
}

func TestEmptyCollectionsAreJSONArrays(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	state, err := svc.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"histories":[]`, `"records":[]`, `"tasks":[]`} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("response must contain %s: %s", want, payload)
		}
	}
}

func TestRequirementDocWorkflow(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	state := apply(t, svc, core.Action{Type: "createReqFolder", ParentID: "req-root", Name: "支付模块", Author: "alice"})
	var folderID string
	for _, f := range state.ReqFolders {
		if f.Name == "支付模块" {
			folderID = f.ID
		}
	}
	if folderID == "" {
		t.Fatal("requirement folder was not created")
	}
	state = apply(t, svc, core.Action{Type: "createReqDoc", FolderID: folderID, Title: "支付流程需求", Content: "初稿", Author: "alice"})
	var doc core.ReqDoc
	for _, d := range state.ReqDocs {
		if d.Title == "支付流程需求" {
			doc = d
		}
	}
	if doc.ID == "" {
		t.Fatal("requirement doc was not created")
	}
	state = apply(t, svc, core.Action{Type: "editReqDoc", DocID: doc.ID, Title: "支付流程需求 v2", Content: "## 更新\n\n补充退款流程", Author: "bob"})
	for _, d := range state.ReqDocs {
		if d.ID == doc.ID {
			doc = d
		}
	}
	if doc.Title != "支付流程需求 v2" || doc.Content != "## 更新\n\n补充退款流程" || doc.UpdatedBy != "bob" {
		t.Fatalf("requirement doc was not updated: %+v", doc)
	}
	if _, err := svc.Apply(context.Background(), core.Action{Type: "createReqDoc", FolderID: "missing-folder", Title: "x"}); err == nil {
		t.Fatal("createReqDoc should fail for missing folder")
	}
}

func TestPendingCaseReviewAndImport(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	state := apply(t, svc, core.Action{Type: "createPendingFolder", ParentID: "pending-root", Name: "支付", Author: "alice"})
	var folderID string
	for _, f := range state.PendingFolders {
		if f.Name == "支付" {
			folderID = f.ID
		}
	}
	if folderID == "" {
		t.Fatal("pending folder was not created")
	}
	state = apply(t, svc, core.Action{Type: "createPendingCase", FolderID: folderID, Title: "退款成功", Priority: "P1", Author: "alice"})
	var pending core.PendingCase
	for _, c := range state.PendingCases {
		if c.Title == "退款成功" {
			pending = c
		}
	}
	if pending.ID == "" {
		t.Fatal("pending case was not created")
	}

	state = apply(t, svc, core.Action{Type: "reviewPendingCase", CaseID: pending.ID, Review: "passed", Author: "bob"})
	for _, c := range state.PendingCases {
		if c.ID == pending.ID && c.Review != "passed" {
			t.Fatalf("review was not recorded: %+v", c)
		}
	}
	state = apply(t, svc, core.Action{Type: "editPendingCase", CaseID: pending.ID, Title: "退款成功（重审）", Priority: "P1", Author: "alice"})
	for _, c := range state.PendingCases {
		if c.ID == pending.ID && c.Review != "" {
			t.Fatal("editing a pending case must reset its review status")
		}
	}
	apply(t, svc, core.Action{Type: "reviewPendingCase", CaseID: pending.ID, Review: "passed", Author: "bob"})

	if _, err := svc.Apply(context.Background(), core.Action{Type: "importPendingCases", VersionID: "main"}); err == nil {
		t.Fatal("importing into mainline must fail")
	}

	branch := branchID(t, apply(t, svc, core.Action{Type: "createVersion", Name: "迭代 A", Author: "alice"}), "迭代 A")
	state = apply(t, svc, core.Action{Type: "createFolder", VersionID: branch, ParentID: "root", Name: "支付", Author: "alice"})
	var payFolderID string
	for _, f := range state.Folders {
		if f.VersionID == branch && f.Name == "支付" {
			payFolderID = f.ID
		}
	}
	apply(t, svc, core.Action{Type: "createCase", VersionID: branch, FolderID: payFolderID, Title: "退款成功（重审）", Author: "alice"})
	if _, err := svc.Apply(context.Background(), core.Action{Type: "importPendingCases", VersionID: branch}); err == nil {
		t.Fatal("importing a case that already exists by title in the resolved folder must fail")
	}
	afterConflict, err := svc.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(afterConflict.PendingCases) != 1 {
		t.Fatal("failed import must not touch the pending-review area")
	}

	state = apply(t, svc, core.Action{Type: "deletePendingCase", CaseID: pending.ID, Author: "alice"})
	if len(state.PendingCases) != 0 {
		t.Fatal("pending case was not deleted")
	}
	state = apply(t, svc, core.Action{Type: "createPendingCase", FolderID: folderID, Title: "退款失败提示", Priority: "P2", Author: "alice"})
	var second core.PendingCase
	for _, c := range state.PendingCases {
		if c.Title == "退款失败提示" {
			second = c
		}
	}
	if _, err := svc.Apply(context.Background(), core.Action{Type: "importPendingCases", VersionID: branch}); err == nil {
		t.Fatal("importing before review must fail")
	}
	apply(t, svc, core.Action{Type: "reviewPendingCase", CaseID: second.ID, Review: "passed", Author: "bob"})
	state = apply(t, svc, core.Action{Type: "importPendingCases", VersionID: branch, Author: "bob"})
	if len(state.PendingFolders) != 1 || len(state.PendingCases) != 0 {
		t.Fatalf("pending-review area must be cleared after import: %+v", state)
	}
	found := false
	for _, c := range state.Cases {
		if c.VersionID == branch && c.Title == "退款失败提示" {
			found = true
			var inFolder *core.Folder
			for i, f := range state.Folders {
				if f.VersionID == branch && f.ID == c.FolderID {
					inFolder = &state.Folders[i]
				}
			}
			if inFolder == nil || inFolder.Name != "支付" {
				t.Fatalf("imported case landed in wrong folder: %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("imported case missing from target version")
	}
}

func TestScopedImportLeavesRestOfReviewAreaAlone(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	state := apply(t, svc, core.Action{Type: "createPendingFolder", ParentID: "pending-root", Name: "支付", Author: "alice"})
	var payFolderID string
	for _, f := range state.PendingFolders {
		if f.Name == "支付" {
			payFolderID = f.ID
		}
	}
	state = apply(t, svc, core.Action{Type: "createPendingCase", FolderID: payFolderID, Title: "退款成功", Priority: "P1", Author: "alice"})
	state = apply(t, svc, core.Action{Type: "createPendingCase", FolderID: payFolderID, Title: "退款失败", Priority: "P1", Author: "alice"})
	var toImport, keep core.PendingCase
	for _, c := range state.PendingCases {
		switch c.Title {
		case "退款成功":
			toImport = c
		case "退款失败":
			keep = c
		}
	}
	apply(t, svc, core.Action{Type: "reviewPendingCase", CaseID: toImport.ID, Review: "passed", Author: "bob"})
	// keep is deliberately left unreviewed to prove it doesn't block a scoped import of toImport.

	branch := branchID(t, apply(t, svc, core.Action{Type: "createVersion", Name: "迭代 B", Author: "alice"}), "迭代 B")
	if _, err := svc.Apply(context.Background(), core.Action{Type: "importPendingCases", VersionID: branch, CaseIDs: []string{keep.ID}}); err == nil {
		t.Fatal("importing an unreviewed case must fail even when scoped")
	}
	state = apply(t, svc, core.Action{Type: "importPendingCases", VersionID: branch, CaseIDs: []string{toImport.ID}, Author: "bob"})

	if len(state.PendingCases) != 1 || state.PendingCases[0].ID != keep.ID {
		t.Fatalf("scoped import must only remove the imported case: %+v", state.PendingCases)
	}
	stillHasFolder := false
	for _, f := range state.PendingFolders {
		if f.ID == payFolderID {
			stillHasFolder = true
		}
	}
	if !stillHasFolder {
		t.Fatal("scoped import must not reset the pending-review folder tree")
	}
	found := false
	for _, c := range state.Cases {
		if c.VersionID == branch && c.ID == toImport.ID {
			found = true
			var inFolder *core.Folder
			for i, f := range state.Folders {
				if f.VersionID == branch && f.ID == c.FolderID {
					inFolder = &state.Folders[i]
				}
			}
			if inFolder == nil || inFolder.Name != "支付" {
				t.Fatalf("scoped import landed in wrong folder: %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("scoped import did not land the case in the target version")
	}
}
