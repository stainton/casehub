package core

// 用例重整（功能 AM）— temporary: revert this file, its Apply case and web/reorganize.js once case structure is
// fixed at the source.
//
// Test versions used to get a folder named after the version (V1, V2 …), and every version's design invented its
// own module codes, so mainline holds the same function filed and numbered several times. The auto-test planner,
// in organize mode, explores the products, reads what each case tests and classifies it by product, function path
// (模块 / 子功能 …) and test category; reorganizeMainline applies that in one destructive action:
//   - each case moves to 全部用例 / <产品> / <模块> / <子功能> … / <类别名>, missing folders created, existing reused;
//   - every leaf folder it touches gets one numbering rule: all of its cases, including ones an earlier batch put
//     there, are renumbered TC-<产品>-<功能缩写>-<类别>-001…N, so a folder never mixes ID schemes;
//   - everything on mainline that names a renamed case (history, records, scripts, tasks) follows it;
//   - every mainline folder left without cases is deleted.

import (
	"errors"
	"fmt"
	"sort"
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
	// Scope is set by 重整此目录: the folder being divided further and the person's limits for what may appear below it.
	Scope *ReorgScope `json:"scope,omitempty"`
}

// ReorgScope: every case stays beneath 全部用例 / <product> / <path…>; below that folder at most MaxDepth function
// levels, and no function folder with more than MaxChildren function sub-folders (category folders not counted).
type ReorgScope struct {
	Product     string   `json:"product"`
	Path        []string `json:"path"`
	MaxChildren int      `json:"maxChildren"`
	MaxDepth    int      `json:"maxDepth"`
}

type ReorgAssignment struct {
	CaseID       string   `json:"caseId"`
	Product      string   `json:"product"`
	FunctionCode string   `json:"functionCode"`
	Path         []string `json:"path"`
	Category     string   `json:"category"`
}

const reorgMaxDepth = 6

func reorganizeMainline(s *State, a Action) error {
	versionID := a.VersionID
	if versionID == "" {
		versionID = "main"
	} // older callers target the mainline
	if versionID != "main" {
		if _, err := requireBranch(s, versionID); err != nil {
			return err
		}
	}
	r := a.Reorganize
	if r == nil || len(r.Assignments) == 0 {
		return errors.New("缺少重整结果")
	}
	// A test version still open would undo this later: merging it copies back every folder mainline no longer
	// has and re-adds its cases under their old IDs. Finished (archived) versions are read-only until reopened.
	var open []string
	for _, v := range s.Versions {
		if versionID == "main" && !v.Mainline && !v.Archived {
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
	inBatch := map[string]bool{}
	for _, x := range r.Assignments {
		if inBatch[x.CaseID] {
			return fmt.Errorf("用例 %s 被归类了两次", x.CaseID)
		}
		inBatch[x.CaseID] = true
		if c, _ := caseAt(s, versionID, x.CaseID); c == nil {
			return fmt.Errorf("目标版本不存在用例 %s，请重新运行重整", x.CaseID)
		}
		if _, ok := products[x.Product]; !ok {
			return fmt.Errorf("用例 %s 的产品 %s 不在产品列表里", x.CaseID, x.Product)
		}
		if !codeRE.MatchString(x.FunctionCode) || testCategoryNames[x.Category] == "" {
			return fmt.Errorf("用例 %s 的功能缩写或测试类别无效", x.CaseID)
		}
		if len(x.Path) == 0 || len(x.Path) > reorgMaxDepth {
			return fmt.Errorf("用例 %s 的功能路径应为 1–%d 级", x.CaseID, reorgMaxDepth)
		}
		for _, f := range x.Path {
			if f = strings.TrimSpace(f); f == "" || strings.Contains(f, "/") || utf8.RuneCountInString(f) > 50 {
				return fmt.Errorf("用例 %s 的目录名「%s」无效", x.CaseID, f)
			}
		}
	}

	scopeFolder := ""
	if sc := r.Scope; sc != nil {
		if sc.MaxChildren == 0 {
			sc.MaxChildren = 8
		}
		if sc.MaxDepth == 0 {
			sc.MaxDepth = 2
		}
		if sc.MaxChildren < 1 || sc.MaxChildren > 30 || sc.MaxDepth < 1 || len(sc.Path)+sc.MaxDepth > reorgMaxDepth {
			return fmt.Errorf("子目录数量上限应为 1–30，深度应为 1–%d", reorgMaxDepth-len(sc.Path))
		}
		name, ok := products[sc.Product]
		if !ok {
			return fmt.Errorf("目录重整的产品 %s 不在产品列表里", sc.Product)
		}
		scopeFolder = versionChild("root", name, versionID, s)
		for _, n := range sc.Path {
			if scopeFolder != "" {
				scopeFolder = versionChild(scopeFolder, n, versionID, s)
			}
		}
		if scopeFolder == "" {
			return errors.New("要重整的目录已经不在目标版本中，请重新运行")
		}
		for _, x := range r.Assignments {
			if x.Product != sc.Product || len(x.Path) < len(sc.Path) || strings.Join(x.Path[:len(sc.Path)], "/") != strings.Join(sc.Path, "/") {
				return fmt.Errorf("用例 %s 被归到了要重整的目录之外", x.CaseID)
			}
			if len(x.Path)-len(sc.Path) > sc.MaxDepth {
				return fmt.Errorf("用例 %s 的目录比深度上限 %d 层更深", x.CaseID, sc.MaxDepth)
			}
		}
	}

	ensure := func(parent, name string) string {
		for _, f := range s.Folders {
			if f.VersionID == versionID && f.ParentID == parent && f.Name == name {
				return f.ID
			}
		}
		f := Folder{ID: ID(), VersionID: versionID, ParentID: parent, Name: name, CreatedBy: a.Author, CreatedAt: now()}
		s.Folders = append(s.Folders, f)
		return f.ID
	}
	// One leaf folder (product / path / category) stands for exactly one ID prefix, and the other way round.
	leafOf := map[string]string{}                                      // case ID -> leaf folder
	prefixOf, leafOfPrefix := map[string]string{}, map[string]string{} // leaf -> prefix, prefix -> leaf
	var leaves []string
	for _, x := range r.Assignments {
		folder := ensure("root", products[x.Product])
		for _, name := range x.Path {
			folder = ensure(folder, strings.TrimSpace(name))
		}
		folder = ensure(folder, testCategoryNames[x.Category])
		prefix := "TC-" + x.Product + "-" + x.FunctionCode + "-" + x.Category
		if p, ok := prefixOf[folder]; ok && p != prefix {
			return fmt.Errorf("目录「%s」同时被归为 %s 和 %s，一个目录只能有一种编号", strings.Join(x.Path, " / "), p, prefix)
		}
		if l, ok := leafOfPrefix[prefix]; ok && l != folder {
			return fmt.Errorf("功能缩写 %s 被用在了两个不同的功能目录上", x.FunctionCode)
		}
		if _, ok := prefixOf[folder]; !ok {
			leaves = append(leaves, folder)
		}
		prefixOf[folder], leafOfPrefix[prefix], leafOf[x.CaseID] = prefix, folder, folder
	}
	touched := map[string]bool{}
	for _, l := range leaves {
		touched[l] = true
	}
	// IDs that stay: the review area, and mainline cases neither in this batch nor in a touched leaf. Archived
	// versions keep their old copies and are not consulted (a reopened one must not be merged again).
	taken := map[string]bool{}
	for _, c := range s.PendingCases {
		taken[c.ID] = true
	}
	for _, c := range s.Cases {
		if c.VersionID != versionID || inBatch[c.ID] || touched[c.FolderID] {
			continue
		}
		taken[c.ID] = true
		for prefix := range leafOfPrefix {
			if strings.HasPrefix(c.ID, prefix+"-") {
				return fmt.Errorf("编号前缀 %s 已被其他目录的用例 %s 使用，请把它们放进同一批重新运行重整", prefix, c.ID)
			}
		}
	}
	// Each touched leaf is numbered from 001: the cases already there first, in their current numeric order, then
	// this batch's cases in the order the planner returned them.
	suffix := func(id string) int {
		if i := strings.LastIndex(id, "-"); i >= 0 {
			if n, err := strconv.Atoi(id[i+1:]); err == nil {
				return n
			}
		}
		return 1 << 30
	}
	rename := map[string]string{}
	for _, leaf := range leaves {
		var resident []string
		for _, c := range s.Cases {
			if c.VersionID == versionID && c.FolderID == leaf && !inBatch[c.ID] {
				resident = append(resident, c.ID)
			}
		}
		sort.SliceStable(resident, func(i, j int) bool {
			if a, b := suffix(resident[i]), suffix(resident[j]); a != b {
				return a < b
			}
			return resident[i] < resident[j]
		})
		members := resident
		for _, x := range r.Assignments {
			if leafOf[x.CaseID] == leaf {
				members = append(members, x.CaseID)
			}
		}
		n := 0
		for _, id := range members {
			for {
				n++
				if next := fmt.Sprintf("%s-%03d", prefixOf[leaf], n); !taken[next] {
					rename[id] = next
					break
				}
			}
		}
	}

	if versionID == "main" {
		s.MainRevision++
	}
	t := now()
	var histories []History
	for i := range s.Cases {
		c := &s.Cases[i]
		newID, renamed := rename[c.ID]
		if c.VersionID != versionID || !renamed {
			continue
		}
		folder := c.FolderID
		if l, ok := leafOf[c.ID]; ok {
			folder = l
		}
		if newID == c.ID && folder == c.FolderID && !inBatch[c.ID] {
			continue // a resident that keeps its number; a batch case is always recorded, it marks the case as classified
		}
		before := *c
		if versionID != "main" && c.ReorganizedFromID == "" && c.BaseRevision > 0 {
			c.ReorganizedFromID = c.ID
		}
		c.ID, c.FolderID, c.UpdatedBy, c.UpdatedAt = newID, folder, a.Author, t
		if versionID == "main" {
			c.Revision = s.MainRevision
		} else {
			c.Dirty = true
		}
		after := *c
		histories = append(histories, History{ID: ID(), CaseID: c.ID, VersionID: versionID, SourceVersionID: versionID, Action: "reorganize", Author: a.Author, Before: &before, After: &after, CreatedAt: t})
	}
	// Everything else on mainline that names a renamed case follows it (a case's ID is how all of these find it).
	// The map is applied once per entry, so swapped numbers inside a folder cannot chain.
	targetVersionID := versionID
	renamed := func(versionID, id string) (string, bool) {
		if versionID != targetVersionID {
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
			s.Scripts[i].FileName = renamedSpecFile(s.Scripts[i].FileName, s.Scripts[i].CaseID, n)
			s.Scripts[i].CaseID = n
		}
	}
	for i := range s.ScriptVersions {
		if n, ok := renamed(s.ScriptVersions[i].VersionID, s.ScriptVersions[i].CaseID); ok {
			s.ScriptVersions[i].FileName = renamedSpecFile(s.ScriptVersions[i].FileName, s.ScriptVersions[i].CaseID, n)
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
	pruneEmptyVersionFolders(s, versionID)
	if r.Scope != nil {
		return checkScopeLimits(s, versionID, scopeFolder, r.Scope)
	}
	return nil
}

func versionChild(parent, name, versionID string, s *State) string {
	for _, f := range s.Folders {
		if f.VersionID == versionID && f.ParentID == parent && f.Name == name {
			return f.ID
		}
	}
	return ""
}

// checkScopeLimits verifies the divided folder as it now stands — including any case this run did not move — so
// the limits hold for the folder, not just for this batch. Category folders (功能测试 …) are not function levels.
// An error discards the whole action.
func checkScopeLimits(s *State, versionID, scope string, sc *ReorgScope) error {
	category := map[string]bool{}
	for _, n := range testCategoryNames {
		category[n] = true
	}
	var walk func(id string, depth int) error
	walk = func(id string, depth int) error {
		var children []Folder
		for _, f := range s.Folders {
			if f.VersionID == versionID && f.ParentID == id && !category[f.Name] {
				children = append(children, f)
			}
		}
		if len(children) > sc.MaxChildren {
			name := strings.Join(sc.Path, " / ")
			if f, _ := folderAt(s, versionID, id); f != nil {
				name = f.Name
			}
			return fmt.Errorf("目录「%s」重整后会有 %d 个子目录，超过上限 %d", name, len(children), sc.MaxChildren)
		}
		for _, f := range children {
			if depth+1 > sc.MaxDepth {
				return fmt.Errorf("目录「%s」超过了深度上限 %d 层", f.Name, sc.MaxDepth)
			}
			if err := walk(f.ID, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(scope, 0)
}

// pruneEmptyMainFolders deletes every mainline folder (but the root) with no case anywhere beneath it — the old
// version-named folders once their cases have moved out.
// renamedSpecFile keeps a script's default file name (<case ID>.spec.ts, what saveScript uses when the generator
// gave none) in step with its case. The automation tree itself needs nothing more: it files each script under
// its case's folder, so the script moves with the case. A name the generator chose is left alone.
func renamedSpecFile(fileName, oldID, newID string) string {
	if fileName == oldID+".spec.ts" {
		return newID + ".spec.ts"
	}
	return fileName
}

func pruneEmptyVersionFolders(s *State, versionID string) {
	parent, used := map[string]string{}, map[string]bool{"root": true}
	for _, f := range s.Folders {
		if f.VersionID == versionID {
			parent[f.ID] = f.ParentID
		}
	}
	for _, c := range s.Cases {
		if c.VersionID != versionID {
			continue
		}
		for id, steps := c.FolderID, 0; id != "" && !used[id] && steps <= len(parent); steps++ {
			used[id] = true
			id = parent[id]
		}
	}
	kept := s.Folders[:0]
	for _, f := range s.Folders {
		if f.VersionID != versionID || used[f.ID] {
			kept = append(kept, f)
		}
	}
	s.Folders = kept
}
