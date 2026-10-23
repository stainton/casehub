package core_test

import (
	"casehub/internal/core"
	"casehub/internal/store"
	"context"
	"testing"
)

func kbFolder(st core.State, id string) *core.KnowledgeFolder {
	for i := range st.KnowledgeFolders {
		if st.KnowledgeFolders[i].ID == id {
			return &st.KnowledgeFolders[i]
		}
	}
	return nil
}

func kbDoc(st core.State, id string) *core.KnowledgeDoc {
	for i := range st.KnowledgeDocs {
		if st.KnowledgeDocs[i].ID == id {
			return &st.KnowledgeDocs[i]
		}
	}
	return nil
}

func productLibrary(t *testing.T, st core.State, name string) core.KnowledgeFolder {
	t.Helper()
	for _, f := range st.KnowledgeFolders {
		if f.Product != "" && f.Name == name {
			return f
		}
	}
	t.Fatalf("no product library %q in %+v", name, st.KnowledgeFolders)
	return core.KnowledgeFolder{}
}

func TestProductLibrariesFollowMainline(t *testing.T) {
	repo := store.NewMemory()
	svc := core.NewService(repo)
	ctx := context.Background()

	// A first read gives every product (child of 全部用例) a library, and stores it. Libraries start empty.
	st, err := svc.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if f := kbFolder(st, "kb-root"); f == nil || f.Name != "全局库" {
		t.Fatalf("the root must be the global library: %+v", f)
	}
	lib := productLibrary(t, st, "登录与认证")
	if lib.ParentID != "" || lib.Product != "auth" {
		t.Fatalf("a product library is a top-level folder tied to its product: %+v", lib)
	}
	if len(st.KnowledgeDocs) != 0 {
		t.Fatalf("libraries hold no generated documents: %+v", st.KnowledgeDocs)
	}
	if stored, _ := repo.Load(ctx); kbFolder(stored, lib.ID) == nil {
		t.Fatal("the library created on read must be saved")
	}

	// The library itself follows mainline, so it cannot be renamed or deleted by hand.
	for name, a := range map[string]core.Action{
		"rename library": {Type: "renameKnowledgeFolder", FolderID: lib.ID, Name: "x"},
		"delete library": {Type: "deleteKnowledgeFolder", FolderID: lib.ID},
	} {
		if _, err := svc.Apply(ctx, a); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
	st = apply(t, svc, core.Action{Type: "createKnowledgeDoc", FolderID: lib.ID, Title: "登录约束", Content: "验证码每晚关闭"})

	// A merge adds a product: its library appears in the same save.
	st = apply(t, svc, core.Action{Type: "createVersion", Name: "V1"})
	v := branchID(t, st, "V1")
	st = apply(t, svc, core.Action{Type: "createFolder", VersionID: v, ParentID: "root", Name: "支付"})
	folder := func(name string) string {
		for _, f := range st.Folders {
			if f.VersionID == v && f.Name == name {
				return f.ID
			}
		}
		t.Fatalf("no folder %s", name)
		return ""
	}
	st = apply(t, svc, core.Action{Type: "createCase", VersionID: v, FolderID: folder("支付"), Title: "微信支付"})
	pay := caseIDByTitle(t, st, v, "微信支付")
	st = apply(t, svc, core.Action{Type: "merge", VersionID: v})
	productLibrary(t, st, "支付")

	// 用例重整 moves everything into one new product: the emptied products leave mainline. An empty library goes
	// with its product; one holding hand-written documents stays as an ordinary folder.
	apply(t, svc, core.Action{Type: "archiveVersion", VersionID: v})
	st = apply(t, svc, core.Action{Type: "reorganizeMainline", Reorganize: reorg([][2]string{{"APP", "应用"}},
		asg("CASE-0001", "APP", "LOGIN", "FUNC", "登录"),
		asg("CASE-0002", "APP", "LOGIN", "FUNC", "登录"),
		asg(pay, "APP", "PAY", "FUNC", "支付"))})
	for _, f := range st.KnowledgeFolders {
		if f.Product != "" && f.Name != "应用" {
			t.Fatalf("library of a removed product is still tied to it: %+v", f)
		}
	}
	old := kbFolder(st, lib.ID)
	if old == nil || old.Name != "登录与认证（产品已移除）" || len(handWrittenDocs(st)) != 1 {
		t.Fatalf("removed product with notes: %+v", old)
	}
	if _, err := svc.Apply(ctx, core.Action{Type: "renameKnowledgeFolder", FolderID: lib.ID, Name: "旧登录"}); err != nil {
		t.Fatalf("a detached library is an ordinary folder: %v", err)
	}
	productLibrary(t, st, "应用")
}

func TestLegacyGeneratedDocumentsAreRemoved(t *testing.T) {
	repo := store.NewMemory()
	svc := core.NewService(repo)
	ctx := context.Background()
	st, err := svc.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lib := productLibrary(t, st, "登录与认证")
	st.KnowledgeDocs = append(st.KnowledgeDocs, core.KnowledgeDoc{ID: "KB-CASES-AUTH", FolderID: lib.ID, Title: "登录与认证 用例库", Generated: true})
	if err := repo.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	st, err = svc.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if kbDoc(st, "KB-CASES-AUTH") != nil {
		t.Fatalf("generated document must be removed: %+v", st.KnowledgeDocs)
	}
}
