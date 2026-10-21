package core_test

import (
	"casehub/internal/core"
	"casehub/internal/store"
	"context"
	"strings"
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

func productLibrary(t *testing.T, st core.State, name string) (core.KnowledgeFolder, core.KnowledgeDoc) {
	t.Helper()
	for _, f := range st.KnowledgeFolders {
		if f.Product != "" && f.Name == name {
			for _, d := range st.KnowledgeDocs {
				if d.FolderID == f.ID && d.Generated {
					return f, d
				}
			}
			t.Fatalf("product library %q has no generated document", name)
		}
	}
	t.Fatalf("no product library %q in %+v", name, st.KnowledgeFolders)
	return core.KnowledgeFolder{}, core.KnowledgeDoc{}
}

func TestProductLibrariesFollowMainline(t *testing.T) {
	repo := store.NewMemory()
	svc := core.NewService(repo)
	ctx := context.Background()

	// A first read gives every product (child of 全部用例) a library, and stores it.
	st, err := svc.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if f := kbFolder(st, "kb-root"); f == nil || f.Name != "全局库" {
		t.Fatalf("the root must be the global library: %+v", f)
	}
	lib, doc := productLibrary(t, st, "登录与认证")
	if lib.ParentID != "" || lib.Product != "auth" {
		t.Fatalf("a product library is a top-level folder tied to its product: %+v", lib)
	}
	if !strings.Contains(doc.Content, "- `CASE-0001` 正确账号密码登录\n- `CASE-0002` 错误密码登录失败\n") || !strings.Contains(doc.Content, "共 2 条用例") {
		t.Fatalf("unexpected case tree:\n%s", doc.Content)
	}
	if stored, _ := repo.Load(ctx); kbFolder(stored, lib.ID) == nil {
		t.Fatal("the library created on read must be saved")
	}

	// Unrelated actions leave the document alone.
	st = apply(t, svc, core.Action{Type: "createIssue", RequirementID: "REQ-0001", Title: "x", Description: "x"})
	if got := kbDoc(st, doc.ID); !got.UpdatedAt.Equal(doc.UpdatedAt) {
		t.Fatal("an unrelated action must not rewrite the case tree")
	}

	// It is read-only, and so is the library itself; hand-written documents may live beside it.
	for name, a := range map[string]core.Action{
		"edit generated":   {Type: "editKnowledgeDoc", DocID: doc.ID, Title: "x", Content: "x"},
		"delete generated": {Type: "deleteKnowledgeDoc", DocID: doc.ID},
		"rename library":   {Type: "renameKnowledgeFolder", FolderID: lib.ID, Name: "x"},
		"delete library":   {Type: "deleteKnowledgeFolder", FolderID: lib.ID},
	} {
		if _, err := svc.Apply(ctx, a); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
	st = apply(t, svc, core.Action{Type: "createKnowledgeDoc", FolderID: lib.ID, Title: "登录约束", Content: "验证码每晚关闭"})

	// A merge adds a product and a sub-folder; both documents follow in the same save.
	st = apply(t, svc, core.Action{Type: "createVersion", Name: "V1"})
	v := branchID(t, st, "V1")
	st = apply(t, svc, core.Action{Type: "createFolder", VersionID: v, ParentID: "root", Name: "支付"})
	st = apply(t, svc, core.Action{Type: "createFolder", VersionID: v, ParentID: "auth", Name: "短信_登录"})
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
	st = apply(t, svc, core.Action{Type: "createCase", VersionID: v, FolderID: folder("短信_登录"), Title: "验证码 *过期*"})
	pay, sms := caseIDByTitle(t, st, v, "微信支付"), caseIDByTitle(t, st, v, "验证码 *过期*")
	before := st.MainRevision
	st = apply(t, svc, core.Action{Type: "merge", VersionID: v})
	if st.MainRevision == before {
		t.Fatal("merge must change mainline")
	}
	_, doc = productLibrary(t, st, "登录与认证")
	want := "- 📁 短信\\_登录\n  - `" + sms + "` 验证码 \\*过期\\*\n- `CASE-0001`"
	if !strings.Contains(doc.Content, want) || !strings.Contains(doc.Content, "共 3 条用例") {
		t.Fatalf("auth tree not refreshed, want %q in:\n%s", want, doc.Content)
	}
	_, payDoc := productLibrary(t, st, "支付")
	if !strings.Contains(payDoc.Content, "`"+pay+"` 微信支付") {
		t.Fatalf("new product's tree:\n%s", payDoc.Content)
	}

	// 用例重整 moves everything into one new product: the emptied products leave mainline. A library with nothing
	// but its generated document goes with it; one holding hand-written documents stays as an ordinary folder.
	apply(t, svc, core.Action{Type: "archiveVersion", VersionID: v})
	st = apply(t, svc, core.Action{Type: "reorganizeMainline", Reorganize: reorg([][2]string{{"APP", "应用"}},
		asg("CASE-0001", "APP", "LOGIN", "FUNC", "登录"),
		asg("CASE-0002", "APP", "LOGIN", "FUNC", "登录"),
		asg(sms, "APP", "SMS", "FUNC", "登录", "短信"),
		asg(pay, "APP", "PAY", "FUNC", "支付"))})
	for _, f := range st.KnowledgeFolders {
		if f.Product != "" && f.Name != "应用" {
			t.Fatalf("library of a removed product is still tied to it: %+v", f)
		}
	}
	old := kbFolder(st, lib.ID)
	if old == nil || old.Name != "登录与认证（产品已移除）" || kbDoc(st, doc.ID) != nil || len(handWrittenDocs(st)) != 1 {
		t.Fatalf("removed product with notes: %+v", old)
	}
	if _, err := svc.Apply(ctx, core.Action{Type: "renameKnowledgeFolder", FolderID: lib.ID, Name: "旧登录"}); err != nil {
		t.Fatalf("a detached library is an ordinary folder: %v", err)
	}
	_, app := productLibrary(t, st, "应用")
	for _, line := range []string{"- 📁 登录\n", "  - 📁 功能测试\n", "    - `TC-APP-LOGIN-FUNC-001` 正确账号密码登录\n", "      - `TC-APP-SMS-FUNC-001`", "共 4 条用例"} {
		if !strings.Contains(app.Content, line) {
			t.Fatalf("reorganized tree lacks %q:\n%s", line, app.Content)
		}
	}
}
