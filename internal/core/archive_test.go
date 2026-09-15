package core_test

import (
	"casehub/internal/core"
	"casehub/internal/store"
	"testing"
)

func TestMergeArchivesActivitySurvivingBranchDeletion(t *testing.T) {
	for _, mode := range []string{"merge", "mergeCases", "mergeFolder"} {
		for _, deletion := range []string{"deleteCases", "deleteVersion"} {
			t.Run(mode+"/"+deletion, func(t *testing.T) {
				svc := core.NewService(store.NewMemory())
				st := apply(t, svc, core.Action{Type: "createVersion", Name: "archive branch"})
				v := branchID(t, st, "archive branch")
				st = apply(t, svc, core.Action{Type: "createFolder", VersionID: v, ParentID: "root", Name: "archive folder"})
				f := ""
				for _, x := range st.Folders {
					if x.VersionID == v && x.Name == "archive folder" {
						f = x.ID
					}
				}
				st = apply(t, svc, core.Action{Type: "createCase", VersionID: v, FolderID: f, Title: "original", Author: "author"})
				id := caseIDByTitle(t, st, v, "original")
				st = apply(t, svc, core.Action{Type: "editCase", VersionID: v, CaseID: id, Title: "edited", Steps: "step", Expected: "result", Author: "editor"})
				st = apply(t, svc, core.Action{Type: "saveRecord", VersionID: v, CaseID: id, Note: "draft", Result: "blocked", Author: "tester"})
				st = apply(t, svc, core.Action{Type: "submitRecord", VersionID: v, CaseID: id, Note: "**passed**", Result: "passed", Author: "tester"})
				st = apply(t, svc, core.Action{Type: mode, VersionID: v, FolderID: f, TargetFolderID: "root", CaseIDs: []string{id}})
				// A second merge of unchanged content must collect new activity without duplicates.
				st = apply(t, svc, core.Action{Type: "submitRecord", VersionID: v, CaseID: id, Note: "later", Result: "failed"})
				for i := 0; i < 2; i++ {
					st = apply(t, svc, core.Action{Type: "mergeCases", VersionID: v, TargetFolderID: "root", CaseIDs: []string{id}})
				}
				// Activity after the last merge must not leak into the archived snapshot.
				st = apply(t, svc, core.Action{Type: "saveRecord", VersionID: v, CaseID: id, Result: "passed", Note: "not merged"})
				st = apply(t, svc, core.Action{Type: deletion, VersionID: v, CaseIDs: []string{id}})
				records := 0
				edits := 0
				creates := 0
				for _, r := range st.Records {
					if r.CaseID != id {
						continue
					}
					if r.VersionID != "main" {
						t.Fatal("branch record survived")
					}
					records++
					if r.SourceVersionID != v || r.SourceVersionName != "archive branch" || r.Note == "not merged" {
						t.Fatalf("bad archive: %+v", r)
					}
				}
				for _, h := range st.Histories {
					if h.CaseID != id || h.VersionID != "main" {
						continue
					}
					if h.Action == "edit" {
						edits++
						if h.Before.Title != "original" || h.After.Title != "edited" || h.Author != "editor" {
							t.Fatal("lost edit snapshot")
						}
					}
					if h.Action == "create" {
						creates++
					}
				}
				if records != 3 || edits != 1 || creates != 1 {
					t.Fatalf("records=%d edits=%d creates=%d", records, edits, creates)
				}
			})
		}
	}
}
