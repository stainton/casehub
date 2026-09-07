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
