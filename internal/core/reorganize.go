package core

// 用例重整（功能 AM）— temporary: revert this file, its Apply case and web/reorganize.js once case structure is
// fixed at the source.
//
// Test versions used to get a folder named after the version (V1, V2 …), and every version's design invented its
// own module codes, so mainline holds the same function filed and numbered several times. The auto-test planner,
// in organize mode, explores the products and classifies mainline's cases into 产品 / 模块 / optional sub-levels;
// reorganizeMainline applies that classification in one destructive action: it rebuilds mainline's folders under
// 全部用例 / <产品> / …, renames each case TC-<产品>-<模块>-<类别>-NNN together with everything on mainline that
// names it (history, records, scripts, tasks), and deletes every mainline folder left without cases.

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Reorganization is the planner's organize result, as the frontend forwards it (auto-test's wire names).
type Reorganization struct {
	Products []struct {
		Code string `json:"code"`
		Name string `json:"name"`
	} `json:"products"`
	Assignments []ReorgAssignment `json:"assignments"`
}

type ReorgAssignment struct {
	CaseID     string   `json:"caseId"`
	Product    string   `json:"product"`
	ModuleCode string   `json:"moduleCode"`
	Folders    []string `json:"folders"`
	Category   string   `json:"category"`
}

const reorgMaxDepth = 4

func reorganizeMainline(s *State, a Action) error {
	r := a.Reorganize
	if r == nil || len(r.Assignments) == 0 {
		return errors.New("缺少重整结果")
	}
	// A test version still open would undo this later: merging it copies back every folder mainline no longer
	// has and re-adds its cases under their old IDs. Finished (archived) versions are read-only until reopened.
	var open []string
	for _, v := range s.Versions {
		if !v.Mainline && !v.Archived {
			open = append(open, v.Name)
		}
	}
	if len(open) > 0 {
		return fmt.Errorf("还有未完成的测试版本：%s。请先合并需要保留的修改，再把它们标记为完成（或删除）后重整", strings.Join(open, "、"))
	}
	products := map[string]string{} // code -> folder name
	for _, p := range r.Products {
		name := strings.TrimSpace(p.Name)
		if !codeRE.MatchString(p.Code) || name == "" || strings.Contains(name, "/") {
			return fmt.Errorf("产品「%s」的缩写或名称无效", p.Name)
		}
		if _, dup := products[p.Code]; dup {
			return fmt.Errorf("产品缩写 %s 重复", p.Code)
		}
		products[p.Code] = name
	}
	seen := map[string]bool{}
	for _, x := range r.Assignments {
		if seen[x.CaseID] {
			return fmt.Errorf("用例 %s 被归类了两次", x.CaseID)
		}
		seen[x.CaseID] = true
		if c, _ := caseAt(s, "main", x.CaseID); c == nil {
			return fmt.Errorf("主线不存在用例 %s，请重新运行重整", x.CaseID)
		}
		if _, ok := products[x.Product]; !ok {
			return fmt.Errorf("用例 %s 的产品 %s 不在产品列表里", x.CaseID, x.Product)
		}
		if !codeRE.MatchString(x.ModuleCode) || testCategoryNames[x.Category] == "" {
			return fmt.Errorf("用例 %s 的模块缩写或测试类别无效", x.CaseID)
		}
		if len(x.Folders) == 0 || len(x.Folders) > reorgMaxDepth {
			return fmt.Errorf("用例 %s 的目录层级应为 1–%d 级", x.CaseID, reorgMaxDepth)
		}
		for _, f := range x.Folders {
			if f = strings.TrimSpace(f); f == "" || strings.Contains(f, "/") || utf8.RuneCountInString(f) > 50 {
				return fmt.Errorf("用例 %s 的目录名「%s」无效", x.CaseID, f)
			}
		}
	}

	// IDs in use anywhere else stay taken: archived versions and the review area keep their copies, and a case not
	// in this batch keeps its ID. Numbering continues after the highest number already used for a prefix, so a
	// later batch never reuses one, and a case already carrying its target prefix keeps its ID (a re-run is a no-op).
	taken := map[string]bool{}
	for _, c := range s.Cases {
		if c.VersionID != "main" || !seen[c.ID] {
			taken[c.ID] = true
		}
	}
	for _, c := range s.PendingCases {
		taken[c.ID] = true
	}
	next := map[string]int{}
	nextID := func(prefix string) string {
		n, ok := next[prefix]
		if !ok {
			for id := range taken {
				if rest, ok := strings.CutPrefix(id, prefix+"-"); ok && caseNumberSuffix.MatchString(rest) {
					if v, err := strconv.Atoi(rest); err == nil && v > n {
						n = v
					}
				}
			}
		}
		for {
			n++
			if id := fmt.Sprintf("%s-%03d", prefix, n); !taken[id] {
				next[prefix] = n
				return id
			}
		}
	}
	prefixOf := func(x ReorgAssignment) string { return "TC-" + x.Product + "-" + x.ModuleCode + "-" + x.Category }
	rename := map[string]string{}
	for _, x := range r.Assignments { // keepers first, so no renamed case is numbered onto one
		if rest, ok := strings.CutPrefix(x.CaseID, prefixOf(x)+"-"); ok && caseNumberSuffix.MatchString(rest) {
			rename[x.CaseID], taken[x.CaseID] = x.CaseID, true
		}
	}
	for _, x := range r.Assignments {
		if _, kept := rename[x.CaseID]; !kept {
			rename[x.CaseID] = nextID(prefixOf(x))
			taken[rename[x.CaseID]] = true
		}
	}

	ensure := func(parent, name string) string {
		for _, f := range s.Folders {
			if f.VersionID == "main" && f.ParentID == parent && f.Name == name {
				return f.ID
			}
		}
		f := Folder{ID: ID(), VersionID: "main", ParentID: parent, Name: name, CreatedBy: a.Author, CreatedAt: now()}
		s.Folders = append(s.Folders, f)
		return f.ID
	}
	s.MainRevision++
	t := now()
	var histories []History
	for _, x := range r.Assignments {
		folder := ensure("root", products[x.Product])
		for _, name := range x.Folders {
			folder = ensure(folder, strings.TrimSpace(name))
		}
		c, _ := caseAt(s, "main", x.CaseID)
		before := *c
		c.ID, c.FolderID, c.Revision, c.UpdatedBy, c.UpdatedAt = rename[x.CaseID], folder, s.MainRevision, a.Author, t
		after := *c
		histories = append(histories, History{ID: ID(), CaseID: c.ID, VersionID: "main", SourceVersionID: "main", Action: "reorganize", Author: a.Author, Before: &before, After: &after, CreatedAt: t})
	}
	// Everything else on mainline that names a renamed case follows it (a case's ID is how all of these find it).
	renamed := func(versionID, id string) (string, bool) {
		if versionID != "main" {
			return "", false
		}
		n, ok := rename[id]
		return n, ok && n != id
	}
	for i := range s.Histories {
		if n, ok := renamed(s.Histories[i].VersionID, s.Histories[i].CaseID); ok {
			s.Histories[i].CaseID = n
		}
	}
	for i := range s.Records {
		if n, ok := renamed(s.Records[i].VersionID, s.Records[i].CaseID); ok {
			s.Records[i].CaseID = n
		}
	}
	for i := range s.Scripts {
		if n, ok := renamed(s.Scripts[i].VersionID, s.Scripts[i].CaseID); ok {
			s.Scripts[i].CaseID = n
		}
	}
	for i := range s.ScriptVersions {
		if n, ok := renamed(s.ScriptVersions[i].VersionID, s.ScriptVersions[i].CaseID); ok {
			s.ScriptVersions[i].CaseID = n
		}
	}
	for i := range s.Tasks {
		for j, id := range s.Tasks[i].CaseIDs {
			if n, ok := renamed(s.Tasks[i].VersionID, id); ok {
				s.Tasks[i].CaseIDs[j] = n
			}
		}
	}
	s.Histories = append(s.Histories, histories...)
	pruneEmptyMainFolders(s)
	return nil
}

// pruneEmptyMainFolders deletes every mainline folder (but the root) with no case anywhere beneath it — the old
// version-named folders once their cases have moved out.
func pruneEmptyMainFolders(s *State) {
	parent, used := map[string]string{}, map[string]bool{"root": true}
	for _, f := range s.Folders {
		if f.VersionID == "main" {
			parent[f.ID] = f.ParentID
		}
	}
	for _, c := range s.Cases {
		if c.VersionID != "main" {
			continue
		}
		for id, steps := c.FolderID, 0; id != "" && !used[id] && steps <= len(parent); steps++ {
			used[id] = true
			id = parent[id]
		}
	}
	kept := s.Folders[:0]
	for _, f := range s.Folders {
		if f.VersionID != "main" || used[f.ID] {
			kept = append(kept, f)
		}
	}
	s.Folders = kept
}
