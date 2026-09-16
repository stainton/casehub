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

func folderID(t *testing.T, state core.State, versionID, name string) string {
	t.Helper()
	for _, f := range state.Folders {
		if f.VersionID == versionID && f.Name == name {
			return f.ID
		}
	}
	t.Fatalf("folder %q not found in version %q", name, versionID)
	return ""
}

func caseIDByTitle(t *testing.T, state core.State, versionID, title string) string {
	t.Helper()
	for _, c := range state.Cases {
		if c.VersionID == versionID && c.Title == title {
			return c.ID
		}
	}
	t.Fatalf("case %q not found in version %q", title, versionID)
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

func TestMergeFolderPreservesStructureUnderChosenTarget(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	state := apply(t, svc, core.Action{Type: "createVersion", Name: "迭代 A", Author: "alice"})
	branch := branchID(t, state, "迭代 A")
	state = apply(t, svc, core.Action{Type: "createFolder", VersionID: branch, ParentID: "root", Name: "支付", Author: "alice"})
	payFolder := folderID(t, state, branch, "支付")
	state = apply(t, svc, core.Action{Type: "createFolder", VersionID: branch, ParentID: payFolder, Name: "退款", Author: "alice"})
	refundFolder := folderID(t, state, branch, "退款")
	state = apply(t, svc, core.Action{Type: "createCase", VersionID: branch, FolderID: refundFolder, Title: "退款成功", Priority: "P1", Author: "alice"})
	caseID := caseIDByTitle(t, state, branch, "退款成功")

	state = apply(t, svc, core.Action{Type: "mergeFolder", VersionID: branch, FolderID: payFolder, TargetFolderID: "auth", Author: "alice"})

	var mainPay, mainRefund *core.Folder
	for i := range state.Folders {
		f := &state.Folders[i]
		if f.VersionID != "main" {
			continue
		}
		if f.ID == payFolder {
			mainPay = f
		}
		if f.ID == refundFolder {
			mainRefund = f
		}
	}
	if mainPay == nil || mainPay.ParentID != "auth" {
		t.Fatalf("payment folder not grafted under chosen target: %+v", mainPay)
	}
	if mainRefund == nil || mainRefund.ParentID != payFolder {
		t.Fatalf("nested folder structure not preserved: %+v", mainRefund)
	}
	var mainCase *core.TestCase
	for i := range state.Cases {
		if state.Cases[i].VersionID == "main" && state.Cases[i].ID == caseID {
			mainCase = &state.Cases[i]
		}
	}
	if mainCase == nil || mainCase.FolderID != refundFolder || mainCase.Dirty {
		t.Fatalf("case not merged into mainline correctly: %+v", mainCase)
	}
}

func TestMergeFolderRejectsNameCollisionAtTarget(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	// Both branches fork before either merges, so neither's "支付" folder is
	// the same one the other created — a genuine two-branch name collision.
	state := apply(t, svc, core.Action{Type: "createVersion", Name: "A"})
	a := branchID(t, state, "A")
	state = apply(t, svc, core.Action{Type: "createVersion", Name: "B"})
	b := branchID(t, state, "B")
	state = apply(t, svc, core.Action{Type: "createFolder", VersionID: a, ParentID: "root", Name: "支付", Author: "alice"})
	payA := folderID(t, state, a, "支付")
	state = apply(t, svc, core.Action{Type: "createFolder", VersionID: b, ParentID: "root", Name: "支付", Author: "bob"})
	payB := folderID(t, state, b, "支付")
	apply(t, svc, core.Action{Type: "mergeFolder", VersionID: a, FolderID: payA, TargetFolderID: "auth", Author: "alice"})

	_, err := svc.Apply(context.Background(), core.Action{Type: "mergeFolder", VersionID: b, FolderID: payB, TargetFolderID: "auth", Author: "bob"})
	if err == nil || !strings.Contains(err.Error(), "同名") {
		t.Fatalf("expected name-collision error, got %v", err)
	}
}

func TestMergeFolderRejectsFolderAlreadyInMainline(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	state := apply(t, svc, core.Action{Type: "createVersion", Name: "A"})
	a := branchID(t, state, "A")
	state = apply(t, svc, core.Action{Type: "createFolder", VersionID: a, ParentID: "root", Name: "支付", Author: "alice"})
	pay := folderID(t, state, a, "支付")
	apply(t, svc, core.Action{Type: "mergeFolder", VersionID: a, FolderID: pay, TargetFolderID: "auth", Author: "alice"})

	_, err := svc.Apply(context.Background(), core.Action{Type: "mergeFolder", VersionID: a, FolderID: pay, TargetFolderID: "auth", Author: "alice"})
	if err == nil || !strings.Contains(err.Error(), "已在主线中") {
		t.Fatalf("expected already-merged error, got %v", err)
	}
}

func TestMergeFolderDetectsStaleCaseInSubtree(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	state := apply(t, svc, core.Action{Type: "createVersion", Name: "D"})
	d := branchID(t, state, "D")
	state = apply(t, svc, core.Action{Type: "createFolder", VersionID: d, ParentID: "root", Name: "历史记录", Author: "dana"})
	history := folderID(t, state, d, "历史记录")
	apply(t, svc, core.Action{Type: "moveCases", VersionID: d, CaseIDs: []string{"CASE-0001"}, TargetFolderID: history, Author: "dana"})

	state = apply(t, svc, core.Action{Type: "createVersion", Name: "E"})
	e := branchID(t, state, "E")
	apply(t, svc, core.Action{Type: "editCase", VersionID: e, CaseID: "CASE-0001", Title: "E 修改", Priority: "P0", Author: "erin"})
	apply(t, svc, core.Action{Type: "merge", VersionID: e, Author: "erin"})

	_, err := svc.Apply(context.Background(), core.Action{Type: "mergeFolder", VersionID: d, FolderID: history, TargetFolderID: "auth", Author: "dana"})
	var conflict core.ConflictError
	if !errors.As(err, &conflict) || len(conflict.Cases) != 1 || conflict.Cases[0] != "CASE-0001" {
		t.Fatalf("expected stale-case conflict, got %v", err)
	}
}

func TestMergeCasesFlattensSelectionIntoTargetFolder(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	state := apply(t, svc, core.Action{Type: "createVersion", Name: "F"})
	f := branchID(t, state, "F")
	state = apply(t, svc, core.Action{Type: "createFolder", VersionID: f, ParentID: "root", Name: "杂项", Author: "frank"})
	misc := folderID(t, state, f, "杂项")
	state = apply(t, svc, core.Action{Type: "createCase", VersionID: f, FolderID: misc, Title: "新用例", Priority: "P2", Author: "frank"})
	newCase := caseIDByTitle(t, state, f, "新用例")
	apply(t, svc, core.Action{Type: "editCase", VersionID: f, CaseID: "CASE-0002", Title: "错误密码登录失败-改", Priority: "P1", Author: "frank"})

	out, err := svc.Apply(context.Background(), core.Action{Type: "mergeCases", VersionID: f, CaseIDs: []string{newCase, "CASE-0002"}, TargetFolderID: "root", Author: "frank"})
	if err != nil {
		t.Fatalf("mergeCases: %v", err)
	}
	state = out.State
	for _, id := range []string{newCase, "CASE-0002"} {
		var mc *core.TestCase
		for i := range state.Cases {
			if state.Cases[i].VersionID == "main" && state.Cases[i].ID == id {
				mc = &state.Cases[i]
			}
		}
		if mc == nil || mc.FolderID != "root" || mc.Dirty {
			t.Fatalf("case %s not flattened into target: %+v", id, mc)
		}
	}
}

func TestMergeCasesSkipsUnchangedCasesWithWarning(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	state := apply(t, svc, core.Action{Type: "createVersion", Name: "G"})
	g := branchID(t, state, "G")
	out, err := svc.Apply(context.Background(), core.Action{Type: "mergeCases", VersionID: g, CaseIDs: []string{"CASE-0001"}, TargetFolderID: "root", Author: "grace"})
	if err != nil {
		t.Fatalf("mergeCases: %v", err)
	}
	if len(out.Warnings) == 0 {
		t.Fatal("expected a warning when nothing was merged")
	}
	for _, c := range out.State.Cases {
		if c.VersionID == "main" && c.ID == "CASE-0001" && c.FolderID == "root" {
			t.Fatal("unchanged case must not be relocated in mainline")
		}
	}
}

func TestMergeCasesDetectsStaleConflict(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	state := apply(t, svc, core.Action{Type: "createVersion", Name: "H"})
	h := branchID(t, state, "H")
	state = apply(t, svc, core.Action{Type: "createVersion", Name: "I"})
	i := branchID(t, state, "I")
	apply(t, svc, core.Action{Type: "editCase", VersionID: h, CaseID: "CASE-0001", Title: "H 修改", Priority: "P0", Author: "henry"})
	apply(t, svc, core.Action{Type: "editCase", VersionID: i, CaseID: "CASE-0001", Title: "I 修改", Priority: "P0", Author: "iris"})
	apply(t, svc, core.Action{Type: "mergeCases", VersionID: h, CaseIDs: []string{"CASE-0001"}, TargetFolderID: "auth", Author: "henry"})

	_, err := svc.Apply(context.Background(), core.Action{Type: "mergeCases", VersionID: i, CaseIDs: []string{"CASE-0001"}, TargetFolderID: "auth", Author: "iris"})
	var conflict core.ConflictError
	if !errors.As(err, &conflict) || len(conflict.Cases) != 1 {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func TestDeleteFolderRequiresEmptyBranchFolder(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	state := apply(t, svc, core.Action{Type: "createVersion", Name: "J"})
	j := branchID(t, state, "J")
	state = apply(t, svc, core.Action{Type: "createFolder", VersionID: j, ParentID: "root", Name: "空文件夹", Author: "jack"})
	empty := folderID(t, state, j, "空文件夹")

	state = apply(t, svc, core.Action{Type: "deleteFolder", VersionID: j, FolderID: empty, Author: "jack"})
	for _, f := range state.Folders {
		if f.VersionID == j && f.ID == empty {
			t.Fatal("empty folder should have been deleted")
		}
	}

	if _, err := svc.Apply(context.Background(), core.Action{Type: "deleteFolder", VersionID: j, FolderID: "auth", Author: "jack"}); err == nil || !strings.Contains(err.Error(), "空文件夹") {
		t.Fatalf("expected non-empty folder rejection, got %v", err)
	}
	if _, err := svc.Apply(context.Background(), core.Action{Type: "deleteFolder", VersionID: "main", FolderID: "auth", Author: "jack"}); err == nil {
		t.Fatal("mainline folder deletion must fail")
	}
}

func TestDeleteVersionRemovesEverythingScopedToIt(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	state := apply(t, svc, core.Action{Type: "createVersion", Name: "L"})
	l := branchID(t, state, "L")
	var caseID string
	for _, c := range state.Cases {
		if c.VersionID == l {
			caseID = c.ID
			break
		}
	}
	if caseID == "" {
		t.Fatal("branch should have inherited at least one case from mainline")
	}
	state = apply(t, svc, core.Action{Type: "createTask", VersionID: l, Name: "冒烟测试", CaseIDs: []string{caseID}, Author: "leo"})
	var taskID string
	for _, tk := range state.Tasks {
		if tk.VersionID == l {
			taskID = tk.ID
		}
	}
	state = apply(t, svc, core.Action{Type: "saveRecord", VersionID: l, CaseID: caseID, TaskID: taskID, Result: "passed", Author: "leo"})
	mainCasesBefore := 0
	for _, c := range state.Cases {
		if c.VersionID == "main" {
			mainCasesBefore++
		}
	}

	state = apply(t, svc, core.Action{Type: "deleteVersion", VersionID: l, Author: "leo"})
	for _, v := range state.Versions {
		if v.ID == l {
			t.Fatal("version should have been deleted")
		}
	}
	for _, f := range state.Folders {
		if f.VersionID == l {
			t.Fatal("branch folders should have been deleted")
		}
	}
	for _, c := range state.Cases {
		if c.VersionID == l {
			t.Fatal("branch cases should have been deleted")
		}
	}
	for _, tk := range state.Tasks {
		if tk.VersionID == l {
			t.Fatal("branch tasks should have been deleted")
		}
	}
	for _, r := range state.Records {
		if r.VersionID == l {
			t.Fatal("branch records should have been deleted")
		}
	}
	mainCasesAfter := 0
	for _, c := range state.Cases {
		if c.VersionID == "main" {
			mainCasesAfter++
		}
	}
	if mainCasesAfter != mainCasesBefore {
		t.Fatalf("mainline cases must be untouched: before %d, after %d", mainCasesBefore, mainCasesAfter)
	}

	if _, err := svc.Apply(context.Background(), core.Action{Type: "deleteVersion", VersionID: "main", Author: "leo"}); err == nil {
		t.Fatal("mainline deletion must fail")
	}
	if _, err := svc.Apply(context.Background(), core.Action{Type: "deleteVersion", VersionID: "does-not-exist", Author: "leo"}); err == nil {
		t.Fatal("deleting a nonexistent version must fail")
	}
}

func TestDeleteCasesRemovesRecordsHistoryAndTaskRefs(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	state := apply(t, svc, core.Action{Type: "createVersion", Name: "M"})
	m := branchID(t, state, "M")
	var caseA, caseB string
	for _, c := range state.Cases {
		if c.VersionID != m {
			continue
		}
		if caseA == "" {
			caseA = c.ID
		} else if caseB == "" {
			caseB = c.ID
			break
		}
	}
	if caseA == "" || caseB == "" {
		t.Fatal("branch should have inherited at least two cases from mainline")
	}
	state = apply(t, svc, core.Action{Type: "createTask", VersionID: m, Name: "回归任务", CaseIDs: []string{caseA, caseB}, Author: "mia"})
	var taskID string
	for _, tk := range state.Tasks {
		if tk.VersionID == m {
			taskID = tk.ID
		}
	}
	state = apply(t, svc, core.Action{Type: "saveRecord", VersionID: m, CaseID: caseA, TaskID: taskID, Result: "passed", Author: "mia"})
	state = apply(t, svc, core.Action{Type: "editCase", VersionID: m, CaseID: caseA, Title: "改过的标题", Author: "mia"})

	state = apply(t, svc, core.Action{Type: "deleteCases", VersionID: m, CaseIDs: []string{caseA}, Author: "mia"})
	foundA, foundB := false, false
	for _, c := range state.Cases {
		if c.VersionID != m {
			continue
		}
		if c.ID == caseA {
			foundA = true
		}
		if c.ID == caseB {
			foundB = true
		}
	}
	if foundA {
		t.Fatal("deleted case should be gone")
	}
	if !foundB {
		t.Fatal("only the targeted case should be removed, case B must remain")
	}
	for _, r := range state.Records {
		if r.VersionID == m && r.CaseID == caseA {
			t.Fatal("records for the deleted case should be gone")
		}
	}
	for _, h := range state.Histories {
		if h.VersionID == m && h.CaseID == caseA {
			t.Fatal("history for the deleted case should be gone")
		}
	}
	for _, tk := range state.Tasks {
		if tk.ID != taskID {
			continue
		}
		for _, id := range tk.CaseIDs {
			if id == caseA {
				t.Fatal("deleted case should be pruned from task.CaseIDs")
			}
		}
		if len(tk.CaseIDs) != 1 || tk.CaseIDs[0] != caseB {
			t.Fatalf("task should retain only case B, got %v", tk.CaseIDs)
		}
	}

	if _, err := svc.Apply(context.Background(), core.Action{Type: "deleteCases", VersionID: "main", CaseIDs: []string{"CASE-0001"}, Author: "mia"}); err == nil {
		t.Fatal("mainline case deletion must fail")
	}
	if _, err := svc.Apply(context.Background(), core.Action{Type: "deleteCases", VersionID: m, CaseIDs: []string{}, Author: "mia"}); err == nil {
		t.Fatal("deleting with no case ids must fail")
	}
}

func TestDeleteCasesLeavesMergedMainlineCopyAndRecordsAlone(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	state := apply(t, svc, core.Action{Type: "createVersion", Name: "N"})
	n := branchID(t, state, "N")
	state = apply(t, svc, core.Action{Type: "createCase", VersionID: n, FolderID: "root", Title: "待合并用例", Priority: "P1", Author: "nina"})
	caseID := caseIDByTitle(t, state, n, "待合并用例")
	state = apply(t, svc, core.Action{Type: "createTask", VersionID: n, Name: "回归任务", CaseIDs: []string{caseID}, Author: "nina"})
	var taskID string
	for _, tk := range state.Tasks {
		if tk.VersionID == n {
			taskID = tk.ID
		}
	}
	state = apply(t, svc, core.Action{Type: "saveRecord", VersionID: n, CaseID: caseID, TaskID: taskID, Result: "passed", Author: "nina"})

	state = apply(t, svc, core.Action{Type: "mergeCases", VersionID: n, CaseIDs: []string{caseID}, TargetFolderID: "root", Author: "nina"})
	var mainCaseBefore *core.TestCase
	mainHistoriesBefore := 0
	for i := range state.Cases {
		if state.Cases[i].VersionID == "main" && state.Cases[i].ID == caseID {
			mainCaseBefore = &state.Cases[i]
		}
	}
	for _, h := range state.Histories {
		if h.VersionID == "main" && h.CaseID == caseID {
			mainHistoriesBefore++
		}
	}
	if mainCaseBefore == nil {
		t.Fatal("case should have been merged into mainline")
	}
	if mainHistoriesBefore == 0 {
		t.Fatal("merge should have recorded a mainline history entry")
	}

	state = apply(t, svc, core.Action{Type: "deleteCases", VersionID: n, CaseIDs: []string{caseID}, Author: "nina"})

	var mainCaseAfter *core.TestCase
	mainHistoriesAfter := 0
	for i := range state.Cases {
		if state.Cases[i].VersionID == "main" && state.Cases[i].ID == caseID {
			mainCaseAfter = &state.Cases[i]
		}
	}
	for _, h := range state.Histories {
		if h.VersionID == "main" && h.CaseID == caseID {
			mainHistoriesAfter++
		}
	}
	if mainCaseAfter == nil {
		t.Fatal("deleting the branch case must not remove the merged mainline copy")
	}
	if mainHistoriesAfter != mainHistoriesBefore {
		t.Fatalf("mainline history for the merged case must be untouched: before %d, after %d", mainHistoriesBefore, mainHistoriesAfter)
	}
	for _, c := range state.Cases {
		if c.VersionID == n && c.ID == caseID {
			t.Fatal("branch copy should have been deleted")
		}
	}
	for _, r := range state.Records {
		if r.CaseID == caseID && r.VersionID == n {
			t.Fatal("the branch record must be gone while its mainline archive survives")
		}
	}
}

func TestSimplifyCasePersistsAndDetectsStaleness(t *testing.T) {
	svc := core.NewService(store.NewMemory())

	// Mainline is normally read-only for content edits, but simplification is
	// a cached reading aid, not a content edit, so it must work there too.
	state := apply(t, svc, core.Action{Type: "simplifyCase", VersionID: "main", CaseID: "CASE-0001",
		SimplifiedPreconditions: "账号已启用", SimplifiedSteps: "1. 打开登录页\n2. 输入账号密码\n3. 登录", SimplifiedExpected: "进入首页", Author: "sam"})
	mainCase, _ := findBranchCase(state, "main", "CASE-0001")
	if mainCase == nil || mainCase.SimplifiedSteps == "" || mainCase.SimplifiedAt.IsZero() {
		t.Fatalf("simplified content was not persisted on the mainline case: %+v", mainCase)
	}
	if mainCase.SimplifiedFromPreconditions != mainCase.Preconditions || mainCase.SimplifiedFromSteps != mainCase.Steps || mainCase.SimplifiedFromExpected != mainCase.Expected {
		t.Fatalf("snapshot of source text must match the case's current fields right after simplifying: %+v", mainCase)
	}
	snapshotSteps := mainCase.SimplifiedFromSteps

	// A branch created afterwards inherits the cached simplification (and its
	// snapshot) unchanged, still matching the case's current (unedited) fields.
	state = apply(t, svc, core.Action{Type: "createVersion", Name: "P"})
	p := branchID(t, state, "P")
	branchCase, _ := findBranchCase(state, p, "CASE-0001")
	if branchCase == nil || branchCase.SimplifiedSteps == "" || branchCase.SimplifiedFromSteps != snapshotSteps {
		t.Fatalf("branch case should have inherited the cached simplification: %+v", branchCase)
	}
	if branchCase.SimplifiedFromSteps != branchCase.Steps {
		t.Fatal("freshly branched case's simplification must not already read as stale")
	}

	// Editing the branch case's real content does not touch the cached text or
	// its snapshot, so the two now disagree with the case's live Steps — exactly
	// the plain string comparison the frontend uses to detect staleness.
	state = apply(t, svc, core.Action{Type: "editCase", VersionID: p, CaseID: "CASE-0001", Title: "改过的标题", Steps: "1. 新步骤", Priority: "P0", Author: "sam"})
	branchCase, _ = findBranchCase(state, p, "CASE-0001")
	if branchCase.SimplifiedSteps == "" {
		t.Fatal("editing content must not silently wipe the cached simplified text")
	}
	if branchCase.SimplifiedFromSteps == branchCase.Steps {
		t.Fatal("cached simplification should now read as stale: snapshot must no longer match the edited Steps")
	}

	if _, err := svc.Apply(context.Background(), core.Action{Type: "simplifyCase", VersionID: "main", CaseID: "CASE-0001", SimplifiedSteps: "  ", Author: "sam"}); err == nil {
		t.Fatal("empty simplified steps must be rejected")
	}
	if _, err := svc.Apply(context.Background(), core.Action{Type: "simplifyCase", VersionID: "main", CaseID: "no-such-case", SimplifiedSteps: "1. x", Author: "sam"}); err == nil {
		t.Fatal("simplifying a nonexistent case must fail")
	}
}

func findBranchCase(state core.State, versionID, caseID string) (*core.TestCase, bool) {
	for i := range state.Cases {
		if state.Cases[i].VersionID == versionID && state.Cases[i].ID == caseID {
			return &state.Cases[i], true
		}
	}
	return nil, false
}

func TestSimplifyPendingCasePersistsAndDetectsStaleness(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	state := apply(t, svc, core.Action{Type: "createPendingCase", FolderID: "pending-root", Title: "新用例",
		Preconditions: "无", Steps: "1. 打开页面", Expected: "看到页面", Priority: "P2", Author: "pat"})
	var pc *core.PendingCase
	for i := range state.PendingCases {
		pc = &state.PendingCases[i]
	}
	if pc == nil {
		t.Fatal("pending case was not created")
	}
	state = apply(t, svc, core.Action{Type: "simplifyPendingCase", CaseID: pc.ID,
		SimplifiedPreconditions: "无", SimplifiedSteps: "1. 打开页面", SimplifiedExpected: "看到页面", Author: "pat"})
	for i := range state.PendingCases {
		if state.PendingCases[i].ID == pc.ID {
			pc = &state.PendingCases[i]
		}
	}
	if pc.SimplifiedSteps == "" || pc.SimplifiedFromSteps != pc.Steps {
		t.Fatalf("simplified content was not persisted on the pending case: %+v", pc)
	}

	state = apply(t, svc, core.Action{Type: "editPendingCase", CaseID: pc.ID, Title: "新用例", Preconditions: "无",
		Steps: "1. 打开页面\n2. 等待加载", Expected: "看到页面", Priority: "P2", Author: "pat"})
	for i := range state.PendingCases {
		if state.PendingCases[i].ID == pc.ID {
			pc = &state.PendingCases[i]
		}
	}
	if pc.SimplifiedSteps == "" {
		t.Fatal("editing content must not silently wipe the cached simplified text")
	}
	if pc.SimplifiedFromSteps == pc.Steps {
		t.Fatal("cached simplification should now read as stale after the edit")
	}
}

func TestMoveCasesMarksDirtyAndRecordsHistory(t *testing.T) {
	svc := core.NewService(store.NewMemory())
	state := apply(t, svc, core.Action{Type: "createVersion", Name: "K"})
	k := branchID(t, state, "K")
	state = apply(t, svc, core.Action{Type: "createFolder", VersionID: k, ParentID: "root", Name: "归档", Author: "kate"})
	archive := folderID(t, state, k, "归档")

	state = apply(t, svc, core.Action{Type: "moveCases", VersionID: k, CaseIDs: []string{"CASE-0001"}, TargetFolderID: archive, Author: "kate"})
	var moved *core.TestCase
	for i := range state.Cases {
		if state.Cases[i].VersionID == k && state.Cases[i].ID == "CASE-0001" {
			moved = &state.Cases[i]
		}
	}
	if moved == nil || moved.FolderID != archive || !moved.Dirty {
		t.Fatalf("case was not moved/dirtied: %+v", moved)
	}
	found := false
	for _, hi := range state.Histories {
		if hi.CaseID == "CASE-0001" && hi.VersionID == k && hi.Action == "move" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a move history entry")
	}

	state = apply(t, svc, core.Action{Type: "merge", VersionID: k, Author: "kate"})
	for i := range state.Cases {
		if state.Cases[i].VersionID == "main" && state.Cases[i].ID == "CASE-0001" && state.Cases[i].FolderID != archive {
			t.Fatalf("moved case did not land in the merged folder: %+v", state.Cases[i])
		}
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
	state = apply(t, svc, core.Action{Type: "simplifyPendingCase", CaseID: second.ID,
		SimplifiedPreconditions: "无", SimplifiedSteps: "退款失败时提示原因", SimplifiedExpected: "看到失败提示", Author: "bob"})
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
			if c.SimplifiedSteps != "退款失败时提示原因" || c.SimplifiedFromSteps != c.Steps {
				t.Fatalf("import must carry the pending case's simplified reading version along, it is part of the case: %+v", c)
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
