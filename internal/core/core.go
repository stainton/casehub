package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrNotFound = errors.New("state not found")

type Repository interface {
	Load(context.Context) (State, error)
	Save(context.Context, State) error
}

type Version struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Mainline         bool      `json:"mainline"`
	BaseMainRevision int64     `json:"baseMainRevision"`
	CreatedBy        string    `json:"createdBy"`
	CreatedAt        time.Time `json:"createdAt"`
}

type Folder struct {
	ID, VersionID, ParentID, Name, CreatedBy string
	CreatedAt                                time.Time
}

type TestCase struct {
	ID, VersionID, FolderID                         string
	Title, Preconditions, Steps, Expected, Priority string
	CreatedBy, UpdatedBy, Result                    string
	CreatedAt, UpdatedAt                            time.Time
	BaseRevision, Revision                          int64
	Dirty                                           bool
}

type History struct {
	ID, CaseID, VersionID, SourceVersionID, Action, Author string
	Before, After                                          *TestCase
	CreatedAt                                              time.Time
}

type Record struct {
	ID, CaseID, VersionID, TaskID, Result, Note, Author string
	Submitted                                           bool
	CreatedAt, UpdatedAt                                time.Time
}

type Task struct {
	ID, VersionID, Name, CreatedBy string
	CaseIDs                        []string
	CreatedAt                      time.Time
}

type ReqFolder struct {
	ID, ParentID, Name, CreatedBy string
	CreatedAt                     time.Time
}

type ReqDoc struct {
	ID, FolderID         string
	Title, Content       string
	CreatedBy, UpdatedBy string
	CreatedAt, UpdatedAt time.Time
}

type PendingFolder struct {
	ID, ParentID, Name, CreatedBy string
	CreatedAt                     time.Time
}

type PendingCase struct {
	ID, FolderID                                    string
	Title, Preconditions, Steps, Expected, Priority string
	Review                                          string
	CreatedBy, UpdatedBy, ReviewedBy                string
	CreatedAt, UpdatedAt, ReviewedAt                time.Time
}

type State struct {
	MainRevision   int64           `json:"mainRevision"`
	Versions       []Version       `json:"versions"`
	Folders        []Folder        `json:"folders"`
	Cases          []TestCase      `json:"cases"`
	Histories      []History       `json:"histories"`
	Records        []Record        `json:"records"`
	Tasks          []Task          `json:"tasks"`
	ReqFolders     []ReqFolder     `json:"reqFolders"`
	ReqDocs        []ReqDoc        `json:"reqDocs"`
	PendingFolders []PendingFolder `json:"pendingFolders"`
	PendingCases   []PendingCase   `json:"pendingCases"`
}

type Action struct {
	Type, VersionID, FolderID, ParentID, CaseID, TaskID string
	Name, Title, Preconditions, Steps, Expected         string
	Priority, Result, Note, Author                      string
	CaseIDs                                             []string
	Submitted, Force                                    bool
	DocID, Content                                      string
	Review                                              string
}

type Result struct {
	State     State    `json:"state"`
	Warnings  []string `json:"warnings,omitempty"`
	Conflicts []string `json:"conflicts,omitempty"`
}

type ConflictError struct{ Cases []string }

func (e ConflictError) Error() string { return "主线已有更新，需要先同步并处理冲突" }

type Service struct {
	repo Repository
	mu   sync.Mutex
}

func NewService(repo Repository) *Service { return &Service{repo: repo} }
func ID() string                          { b := make([]byte, 8); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func now() time.Time                      { return time.Now().UTC().Truncate(time.Millisecond) }
func actor(a string) string {
	if strings.TrimSpace(a) == "" {
		return "本地用户"
	}
	return strings.TrimSpace(a)
}
func caseAt(s *State, versionID, caseID string) (*TestCase, int) {
	for i := range s.Cases {
		if s.Cases[i].VersionID == versionID && s.Cases[i].ID == caseID {
			return &s.Cases[i], i
		}
	}
	return nil, -1
}
func folderAt(s *State, versionID, folderID string) (*Folder, int) {
	for i := range s.Folders {
		if s.Folders[i].VersionID == versionID && s.Folders[i].ID == folderID {
			return &s.Folders[i], i
		}
	}
	return nil, -1
}
func versionAt(s *State, id string) (*Version, int) {
	for i := range s.Versions {
		if s.Versions[i].ID == id {
			return &s.Versions[i], i
		}
	}
	return nil, -1
}
func reqFolderAt(s *State, id string) (*ReqFolder, int) {
	for i := range s.ReqFolders {
		if s.ReqFolders[i].ID == id {
			return &s.ReqFolders[i], i
		}
	}
	return nil, -1
}
func reqDocAt(s *State, id string) (*ReqDoc, int) {
	for i := range s.ReqDocs {
		if s.ReqDocs[i].ID == id {
			return &s.ReqDocs[i], i
		}
	}
	return nil, -1
}
func pendingFolderAt(s *State, id string) (*PendingFolder, int) {
	for i := range s.PendingFolders {
		if s.PendingFolders[i].ID == id {
			return &s.PendingFolders[i], i
		}
	}
	return nil, -1
}
func pendingCaseAt(s *State, id string) (*PendingCase, int) {
	for i := range s.PendingCases {
		if s.PendingCases[i].ID == id {
			return &s.PendingCases[i], i
		}
	}
	return nil, -1
}

func Seed() State {
	t := now()
	s := State{
		MainRevision: 1,
		Versions:     []Version{},
		Folders:      []Folder{},
		Cases:        []TestCase{},
		Histories:    []History{},
		Records:      []Record{},
		Tasks:        []Task{},
	}
	s.Versions = []Version{{ID: "main", Name: "主线", Mainline: true, BaseMainRevision: 1, CreatedBy: "system", CreatedAt: t}}
	s.Folders = []Folder{{ID: "root", VersionID: "main", Name: "全部用例", CreatedBy: "system", CreatedAt: t}, {ID: "auth", VersionID: "main", ParentID: "root", Name: "登录与认证", CreatedBy: "system", CreatedAt: t}}
	s.Cases = []TestCase{
		{ID: "CASE-0001", VersionID: "main", FolderID: "auth", Title: "正确账号密码登录", Preconditions: "用户账号已启用", Steps: "1. 打开登录页\n2. 输入正确账号和密码\n3. 点击登录", Expected: "进入系统首页", Priority: "P0", CreatedBy: "system", UpdatedBy: "system", CreatedAt: t, UpdatedAt: t, Revision: 1},
		{ID: "CASE-0002", VersionID: "main", FolderID: "auth", Title: "错误密码登录失败", Preconditions: "用户账号已启用", Steps: "输入正确账号和错误密码后提交", Expected: "提示账号或密码错误", Priority: "P1", CreatedBy: "system", UpdatedBy: "system", CreatedAt: t, UpdatedAt: t, Revision: 1},
	}
	s.ReqFolders = []ReqFolder{{ID: "req-root", Name: "全部需求", CreatedBy: "system", CreatedAt: t}}
	s.ReqDocs = []ReqDoc{
		{ID: "REQ-0001", FolderID: "req-root", Title: "登录模块需求说明", Content: "## 背景\n\n用户需要通过账号密码登录系统。\n\n## 需求描述\n\n- 支持账号密码登录\n- 登录失败提示错误信息\n", CreatedBy: "system", UpdatedBy: "system", CreatedAt: t, UpdatedAt: t},
	}
	s.PendingFolders = []PendingFolder{{ID: "pending-root", Name: "待评审用例", CreatedBy: "system", CreatedAt: t}}
	return s
}

// normalizeState upgrades legacy or partially populated repository data. Empty
// collections must be encoded as [] instead of null because they are consumed
// as arrays by API clients.
func normalizeState(state *State) {
	if state.Versions == nil {
		state.Versions = []Version{}
	}
	if state.Folders == nil {
		state.Folders = []Folder{}
	}
	if state.Cases == nil {
		state.Cases = []TestCase{}
	}
	if state.Histories == nil {
		state.Histories = []History{}
	}
	if state.Records == nil {
		state.Records = []Record{}
	}
	if state.Tasks == nil {
		state.Tasks = []Task{}
	}
	if state.ReqFolders == nil {
		state.ReqFolders = []ReqFolder{}
	}
	if state.ReqDocs == nil {
		state.ReqDocs = []ReqDoc{}
	}
	if state.PendingFolders == nil {
		state.PendingFolders = []PendingFolder{}
	}
	if state.PendingCases == nil {
		state.PendingCases = []PendingCase{}
	}
	hasPendingRoot := false
	for _, f := range state.PendingFolders {
		if f.ID == "pending-root" {
			hasPendingRoot = true
			break
		}
	}
	if !hasPendingRoot {
		// Repositories persisted before the review feature existed have no
		// PendingFolders at all; createPendingFolder/importPendingCases both
		// assume "pending-root" is always present as the review area's anchor.
		state.PendingFolders = append([]PendingFolder{{ID: "pending-root", Name: "待评审用例", CreatedBy: "system", CreatedAt: now()}}, state.PendingFolders...)
	}
}

func (s *Service) State(ctx context.Context) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.repo.Load(ctx)
	if errors.Is(err, ErrNotFound) {
		st = Seed()
		err = s.repo.Save(ctx, st)
	}
	normalizeState(&st)
	sort.SliceStable(st.Versions, func(i, j int) bool {
		return st.Versions[i].Mainline || (!st.Versions[j].Mainline && st.Versions[i].CreatedAt.Before(st.Versions[j].CreatedAt))
	})
	return st, err
}

func (s *Service) Apply(ctx context.Context, a Action) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.repo.Load(ctx)
	if errors.Is(err, ErrNotFound) {
		st = Seed()
	} else if err != nil {
		return Result{}, err
	}
	normalizeState(&st)
	a.Author = actor(a.Author)
	var warnings []string
	switch a.Type {
	case "createVersion":
		err = createVersion(&st, a)
	case "createFolder":
		err = createFolder(&st, a)
	case "renameFolder":
		err = renameFolder(&st, a)
	case "createCase":
		err = createCase(&st, a)
	case "editCase":
		warnings, err = editCase(&st, a)
	case "saveRecord", "submitRecord":
		warnings, err = recordCase(&st, a)
	case "createTask":
		err = createTask(&st, a)
	case "createReqFolder":
		err = createReqFolder(&st, a)
	case "renameReqFolder":
		err = renameReqFolder(&st, a)
	case "createReqDoc":
		err = createReqDoc(&st, a)
	case "editReqDoc":
		err = editReqDoc(&st, a)
	case "createPendingFolder":
		err = createPendingFolder(&st, a)
	case "renamePendingFolder":
		err = renamePendingFolder(&st, a)
	case "createPendingCase":
		err = createPendingCase(&st, a)
	case "editPendingCase":
		err = editPendingCase(&st, a)
	case "deletePendingCase":
		err = deletePendingCase(&st, a)
	case "reviewPendingCase":
		err = reviewPendingCase(&st, a)
	case "importPendingCases":
		err = importPendingCases(&st, a)
	case "sync":
		var conflicts []string
		conflicts, err = syncVersion(&st, a)
		if len(conflicts) > 0 {
			warnings = append(warnings, fmt.Sprintf("%d 个文本冲突未被覆盖：%s", len(conflicts), strings.Join(conflicts, ", ")))
		}
	case "merge":
		var conflicts []string
		conflicts, err = mergeVersion(&st, a)
		if len(conflicts) > 0 {
			return Result{State: st, Conflicts: conflicts}, ConflictError{Cases: conflicts}
		}
	default:
		err = fmt.Errorf("unknown action %q", a.Type)
	}
	if err != nil {
		return Result{State: st}, err
	}
	if err = s.repo.Save(ctx, st); err != nil {
		return Result{}, err
	}
	return Result{State: st, Warnings: warnings}, nil
}

func requireBranch(s *State, id string) (*Version, error) {
	v, _ := versionAt(s, id)
	if v == nil {
		return nil, errors.New("版本不存在")
	}
	if v.Mainline {
		return nil, errors.New("主线只读")
	}
	return v, nil
}
func createVersion(s *State, a Action) error {
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("版本名称不能为空")
	}
	for _, v := range s.Versions {
		if strings.EqualFold(v.Name, a.Name) {
			return errors.New("版本名称已存在")
		}
	}
	t := now()
	v := Version{ID: ID(), Name: strings.TrimSpace(a.Name), BaseMainRevision: s.MainRevision, CreatedBy: a.Author, CreatedAt: t}
	s.Versions = append(s.Versions, v)
	baseFolders := append([]Folder(nil), s.Folders...)
	baseCases := append([]TestCase(nil), s.Cases...)
	for _, f := range baseFolders {
		if f.VersionID == "main" {
			n := f
			n.VersionID = v.ID
			n.CreatedAt = t
			n.CreatedBy = a.Author
			s.Folders = append(s.Folders, n)
		}
	}
	for _, c := range baseCases {
		if c.VersionID == "main" {
			n := c
			n.VersionID = v.ID
			n.BaseRevision = c.Revision
			n.Revision = 0
			n.Dirty = false
			n.Result = ""
			n.CreatedAt = t
			n.UpdatedAt = t
			n.CreatedBy = a.Author
			n.UpdatedBy = a.Author
			s.Cases = append(s.Cases, n)
		}
	}
	return nil
}
func createFolder(s *State, a Action) error {
	if _, err := requireBranch(s, a.VersionID); err != nil {
		return err
	}
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("文件夹名称不能为空")
	}
	if a.ParentID != "" {
		if f, _ := folderAt(s, a.VersionID, a.ParentID); f == nil {
			return errors.New("父文件夹不存在")
		}
	}
	s.Folders = append(s.Folders, Folder{ID: ID(), VersionID: a.VersionID, ParentID: a.ParentID, Name: strings.TrimSpace(a.Name), CreatedBy: a.Author, CreatedAt: now()})
	return nil
}
func renameFolder(s *State, a Action) error {
	if _, err := requireBranch(s, a.VersionID); err != nil {
		return err
	}
	f, _ := folderAt(s, a.VersionID, a.FolderID)
	if f == nil {
		return errors.New("文件夹不存在")
	}
	for _, x := range s.Folders {
		if x.VersionID == a.VersionID && x.ParentID == f.ID {
			return errors.New("只能重命名空文件夹")
		}
	}
	for _, x := range s.Cases {
		if x.VersionID == a.VersionID && x.FolderID == f.ID {
			return errors.New("只能重命名空文件夹")
		}
	}
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("文件夹名称不能为空")
	}
	f.Name = strings.TrimSpace(a.Name)
	return nil
}
func createCase(s *State, a Action) error {
	if _, err := requireBranch(s, a.VersionID); err != nil {
		return err
	}
	if f, _ := folderAt(s, a.VersionID, a.FolderID); f == nil {
		return errors.New("文件夹不存在")
	}
	if strings.TrimSpace(a.Title) == "" {
		return errors.New("用例标题不能为空")
	}
	id := "CASE-" + strings.ToUpper(ID()[:6])
	if a.CaseID != "" {
		id = a.CaseID
	}
	if c, _ := caseAt(s, a.VersionID, id); c != nil {
		return errors.New("当前版本已存在相同用例 ID")
	}
	t := now()
	c := TestCase{ID: id, VersionID: a.VersionID, FolderID: a.FolderID, Title: strings.TrimSpace(a.Title), Preconditions: a.Preconditions, Steps: a.Steps, Expected: a.Expected, Priority: a.Priority, CreatedBy: a.Author, UpdatedBy: a.Author, CreatedAt: t, UpdatedAt: t, Dirty: true}
	s.Cases = append(s.Cases, c)
	after := c
	s.Histories = append(s.Histories, History{ID: ID(), CaseID: id, VersionID: a.VersionID, SourceVersionID: a.VersionID, Action: "create", Author: a.Author, After: &after, CreatedAt: t})
	return nil
}
func stale(s *State, c *TestCase) bool {
	m, _ := caseAt(s, "main", c.ID)
	return m != nil && m.Revision > c.BaseRevision
}
func editCase(s *State, a Action) ([]string, error) {
	if _, err := requireBranch(s, a.VersionID); err != nil {
		return nil, err
	}
	c, _ := caseAt(s, a.VersionID, a.CaseID)
	if c == nil {
		return nil, errors.New("用例不存在")
	}
	if stale(s, c) && !a.Force {
		return nil, ConflictError{Cases: []string{c.ID}}
	}
	if stale(s, c) && a.Force {
		mainCase, _ := caseAt(s, "main", c.ID)
		c.BaseRevision = mainCase.Revision
	}
	before := *c
	c.Title = strings.TrimSpace(a.Title)
	if c.Title == "" {
		return nil, errors.New("用例标题不能为空")
	}
	c.Preconditions = a.Preconditions
	c.Steps = a.Steps
	c.Expected = a.Expected
	c.Priority = a.Priority
	c.UpdatedBy = a.Author
	c.UpdatedAt = now()
	c.Dirty = true
	after := *c
	s.Histories = append(s.Histories, History{ID: ID(), CaseID: c.ID, VersionID: c.VersionID, SourceVersionID: c.VersionID, Action: "edit", Author: a.Author, Before: &before, After: &after, CreatedAt: c.UpdatedAt})
	return nil, nil
}
func recordCase(s *State, a Action) ([]string, error) {
	if _, err := requireBranch(s, a.VersionID); err != nil {
		return nil, err
	}
	c, _ := caseAt(s, a.VersionID, a.CaseID)
	if c == nil {
		return nil, errors.New("用例不存在")
	}
	if stale(s, c) && !a.Force {
		return nil, ConflictError{Cases: []string{c.ID}}
	}
	if a.Result != "passed" && a.Result != "failed" && a.Result != "blocked" {
		return nil, errors.New("测试结果必须是通过、失败或阻塞")
	}
	t := now()
	submitted := a.Type == "submitRecord" || a.Submitted
	s.Records = append(s.Records, Record{ID: ID(), CaseID: c.ID, VersionID: c.VersionID, TaskID: a.TaskID, Result: a.Result, Note: a.Note, Author: a.Author, Submitted: submitted, CreatedAt: t, UpdatedAt: t})
	c.Result = a.Result
	return nil, nil
}
func createTask(s *State, a Action) error {
	if _, err := requireBranch(s, a.VersionID); err != nil {
		return err
	}
	if strings.TrimSpace(a.Name) == "" || len(a.CaseIDs) == 0 {
		return errors.New("任务名称和用例不能为空")
	}
	seen := map[string]bool{}
	for _, id := range a.CaseIDs {
		if seen[id] {
			return errors.New("任务内用例不能重复")
		}
		seen[id] = true
		if c, _ := caseAt(s, a.VersionID, id); c == nil {
			return fmt.Errorf("用例 %s 不属于当前版本", id)
		}
	}
	s.Tasks = append(s.Tasks, Task{ID: ID(), VersionID: a.VersionID, Name: strings.TrimSpace(a.Name), CreatedBy: a.Author, CaseIDs: append([]string(nil), a.CaseIDs...), CreatedAt: now()})
	return nil
}
func syncVersion(s *State, a Action) ([]string, error) {
	v, _ := versionAt(s, a.VersionID)
	if v == nil || v.Mainline {
		return nil, errors.New("只能同步测试版本")
	}
	conflicts := []string{}
	mainFolders := append([]Folder(nil), s.Folders...)
	mainCases := append([]TestCase(nil), s.Cases...)
	for _, mf := range mainFolders {
		if mf.VersionID == "main" {
			if f, _ := folderAt(s, v.ID, mf.ID); f == nil {
				n := mf
				n.VersionID = v.ID
				s.Folders = append(s.Folders, n)
			}
		}
	}
	for _, mc := range mainCases {
		if mc.VersionID != "main" {
			continue
		}
		bc, _ := caseAt(s, v.ID, mc.ID)
		if bc == nil {
			n := mc
			n.VersionID = v.ID
			n.BaseRevision = mc.Revision
			n.Revision = 0
			n.Dirty = false
			n.Result = ""
			s.Cases = append(s.Cases, n)
			continue
		}
		if mc.Revision > bc.BaseRevision && bc.Dirty {
			conflicts = append(conflicts, bc.ID)
			continue
		}
		if mc.Revision > bc.BaseRevision {
			res := bc.Result
			*bc = mc
			bc.VersionID = v.ID
			bc.BaseRevision = mc.Revision
			bc.Revision = 0
			bc.Result = res
			bc.Dirty = false
		}
	}
	v.BaseMainRevision = s.MainRevision
	return conflicts, nil
}
func mergeVersion(s *State, a Action) ([]string, error) {
	v, _ := versionAt(s, a.VersionID)
	if v == nil || v.Mainline {
		return nil, errors.New("只能合并测试版本")
	}
	conflicts := []string{}
	for i := range s.Cases {
		c := &s.Cases[i]
		if c.VersionID != v.ID || !c.Dirty {
			continue
		}
		m, _ := caseAt(s, "main", c.ID)
		if m != nil && m.Revision > c.BaseRevision {
			conflicts = append(conflicts, c.ID)
		}
	}
	if len(conflicts) > 0 {
		return conflicts, nil
	}
	s.MainRevision++
	branchFolders := append([]Folder(nil), s.Folders...)
	for _, f := range branchFolders {
		if f.VersionID == v.ID {
			if mf, _ := folderAt(s, "main", f.ID); mf == nil {
				n := f
				n.VersionID = "main"
				s.Folders = append(s.Folders, n)
			}
		}
	}
	branchCases := []TestCase{}
	for _, c := range s.Cases {
		if c.VersionID == v.ID && c.Dirty {
			branchCases = append(branchCases, c)
		}
	}
	for _, c := range branchCases {
		m, _ := caseAt(s, "main", c.ID)
		n := c
		n.VersionID = "main"
		n.Result = ""
		n.Dirty = false
		n.BaseRevision = 0
		n.Revision = s.MainRevision
		n.UpdatedAt = now()
		if m == nil {
			s.Cases = append(s.Cases, n)
		} else {
			*m = n
		}
		s.Histories = append(s.Histories, History{ID: ID(), CaseID: c.ID, VersionID: "main", SourceVersionID: v.ID, Action: "merge", Author: a.Author, After: &n, CreatedAt: now()})
		bc, _ := caseAt(s, v.ID, c.ID)
		bc.BaseRevision = s.MainRevision
		bc.Dirty = false
	}
	v.BaseMainRevision = s.MainRevision
	return nil, nil
}
func createReqFolder(s *State, a Action) error {
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("文件夹名称不能为空")
	}
	if a.ParentID != "" {
		if f, _ := reqFolderAt(s, a.ParentID); f == nil {
			return errors.New("父文件夹不存在")
		}
	}
	s.ReqFolders = append(s.ReqFolders, ReqFolder{ID: ID(), ParentID: a.ParentID, Name: strings.TrimSpace(a.Name), CreatedBy: a.Author, CreatedAt: now()})
	return nil
}
func renameReqFolder(s *State, a Action) error {
	f, _ := reqFolderAt(s, a.FolderID)
	if f == nil {
		return errors.New("文件夹不存在")
	}
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("文件夹名称不能为空")
	}
	f.Name = strings.TrimSpace(a.Name)
	return nil
}
func createReqDoc(s *State, a Action) error {
	if f, _ := reqFolderAt(s, a.FolderID); f == nil {
		return errors.New("文件夹不存在")
	}
	if strings.TrimSpace(a.Title) == "" {
		return errors.New("需求文档标题不能为空")
	}
	t := now()
	s.ReqDocs = append(s.ReqDocs, ReqDoc{ID: "REQ-" + strings.ToUpper(ID()[:6]), FolderID: a.FolderID, Title: strings.TrimSpace(a.Title), Content: a.Content, CreatedBy: a.Author, UpdatedBy: a.Author, CreatedAt: t, UpdatedAt: t})
	return nil
}
func editReqDoc(s *State, a Action) error {
	d, _ := reqDocAt(s, a.DocID)
	if d == nil {
		return errors.New("需求文档不存在")
	}
	title := strings.TrimSpace(a.Title)
	if title == "" {
		return errors.New("需求文档标题不能为空")
	}
	d.Title = title
	d.Content = a.Content
	d.UpdatedBy = a.Author
	d.UpdatedAt = now()
	return nil
}
func createPendingFolder(s *State, a Action) error {
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("文件夹名称不能为空")
	}
	if a.ParentID != "" {
		if f, _ := pendingFolderAt(s, a.ParentID); f == nil {
			return errors.New("父文件夹不存在")
		}
	}
	s.PendingFolders = append(s.PendingFolders, PendingFolder{ID: ID(), ParentID: a.ParentID, Name: strings.TrimSpace(a.Name), CreatedBy: a.Author, CreatedAt: now()})
	return nil
}
func renamePendingFolder(s *State, a Action) error {
	f, _ := pendingFolderAt(s, a.FolderID)
	if f == nil {
		return errors.New("文件夹不存在")
	}
	for _, x := range s.PendingFolders {
		if x.ParentID == f.ID {
			return errors.New("只能重命名空文件夹")
		}
	}
	for _, x := range s.PendingCases {
		if x.FolderID == f.ID {
			return errors.New("只能重命名空文件夹")
		}
	}
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("文件夹名称不能为空")
	}
	f.Name = strings.TrimSpace(a.Name)
	return nil
}
func createPendingCase(s *State, a Action) error {
	if f, _ := pendingFolderAt(s, a.FolderID); f == nil {
		return errors.New("文件夹不存在")
	}
	if strings.TrimSpace(a.Title) == "" {
		return errors.New("用例标题不能为空")
	}
	t := now()
	c := PendingCase{ID: "CASE-" + strings.ToUpper(ID()[:6]), FolderID: a.FolderID, Title: strings.TrimSpace(a.Title), Preconditions: a.Preconditions, Steps: a.Steps, Expected: a.Expected, Priority: a.Priority, CreatedBy: a.Author, UpdatedBy: a.Author, CreatedAt: t, UpdatedAt: t}
	s.PendingCases = append(s.PendingCases, c)
	return nil
}
func editPendingCase(s *State, a Action) error {
	c, _ := pendingCaseAt(s, a.CaseID)
	if c == nil {
		return errors.New("用例不存在")
	}
	title := strings.TrimSpace(a.Title)
	if title == "" {
		return errors.New("用例标题不能为空")
	}
	c.Title = title
	c.Preconditions = a.Preconditions
	c.Steps = a.Steps
	c.Expected = a.Expected
	c.Priority = a.Priority
	c.UpdatedBy = a.Author
	c.UpdatedAt = now()
	c.Review = ""
	c.ReviewedBy = ""
	c.ReviewedAt = time.Time{}
	return nil
}
func deletePendingCase(s *State, a Action) error {
	c, i := pendingCaseAt(s, a.CaseID)
	if c == nil {
		return errors.New("用例不存在")
	}
	s.PendingCases = append(s.PendingCases[:i], s.PendingCases[i+1:]...)
	return nil
}
func reviewPendingCase(s *State, a Action) error {
	c, _ := pendingCaseAt(s, a.CaseID)
	if c == nil {
		return errors.New("用例不存在")
	}
	if a.Review != "passed" && a.Review != "rejected" {
		return errors.New("评审结果必须是通过或不通过")
	}
	c.Review = a.Review
	c.ReviewedBy = a.Author
	c.ReviewedAt = now()
	return nil
}

// importPendingCases copies the entire pending-review tree into a branch
// version once every pending case has passed review, preserving the folder
// structure. It is all-or-nothing: any name conflict with the target
// version aborts before anything is written, and success clears the
// pending-review area since it has served its purpose.
func importPendingCases(s *State, a Action) error {
	v, err := requireBranch(s, a.VersionID)
	if err != nil {
		return err
	}
	if len(s.PendingCases) == 0 {
		return errors.New("没有待导入的用例")
	}
	var unreviewed []string
	for _, c := range s.PendingCases {
		if c.Review != "passed" {
			unreviewed = append(unreviewed, c.Title)
		}
	}
	if len(unreviewed) > 0 {
		return fmt.Errorf("存在未通过评审的用例：%s", strings.Join(unreviewed, "、"))
	}

	targetFolderID := map[string]string{} // pending folder ID -> resolved target folder ID
	targetFolderID["pending-root"] = "root"
	newFolders := []Folder{}
	var conflicts []string
	var resolve func(pf PendingFolder) string
	resolve = func(pf PendingFolder) string {
		if id, ok := targetFolderID[pf.ID]; ok {
			return id
		}
		parentID := "root"
		if pf.ParentID != "" {
			if parent, _ := pendingFolderAt(s, pf.ParentID); parent != nil {
				parentID = resolve(*parent)
			}
		}
		for _, f := range s.Folders {
			if f.VersionID == v.ID && f.ParentID == parentID && f.Name == pf.Name {
				targetFolderID[pf.ID] = f.ID
				return f.ID
			}
		}
		for _, nf := range newFolders {
			if nf.ParentID == parentID && nf.Name == pf.Name {
				targetFolderID[pf.ID] = nf.ID
				return nf.ID
			}
		}
		nf := Folder{ID: ID(), VersionID: v.ID, ParentID: parentID, Name: pf.Name, CreatedBy: a.Author, CreatedAt: now()}
		newFolders = append(newFolders, nf)
		targetFolderID[pf.ID] = nf.ID
		return nf.ID
	}
	for _, pf := range s.PendingFolders {
		if pf.ID == "pending-root" {
			continue
		}
		resolve(pf)
	}
	for _, pc := range s.PendingCases {
		folderID := "root"
		if pf, _ := pendingFolderAt(s, pc.FolderID); pf != nil {
			folderID = resolve(*pf)
		}
		for _, c := range s.Cases {
			if c.VersionID == v.ID && c.FolderID == folderID && c.Title == pc.Title {
				conflicts = append(conflicts, pc.Title)
			}
		}
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("目标版本已存在同名用例，导入已取消：%s", strings.Join(conflicts, "、"))
	}

	s.Folders = append(s.Folders, newFolders...)
	t := now()
	for _, pc := range s.PendingCases {
		folderID, _ := targetFolderID[pc.FolderID]
		if folderID == "" {
			folderID = "root"
		}
		c := TestCase{ID: pc.ID, VersionID: v.ID, FolderID: folderID, Title: pc.Title, Preconditions: pc.Preconditions, Steps: pc.Steps, Expected: pc.Expected, Priority: pc.Priority, CreatedBy: a.Author, UpdatedBy: a.Author, CreatedAt: t, UpdatedAt: t, Dirty: true}
		s.Cases = append(s.Cases, c)
		after := c
		s.Histories = append(s.Histories, History{ID: ID(), CaseID: c.ID, VersionID: v.ID, SourceVersionID: v.ID, Action: "create", Author: a.Author, After: &after, CreatedAt: t})
	}
	s.PendingFolders = []PendingFolder{{ID: "pending-root", Name: "待评审用例", CreatedBy: "system", CreatedAt: t}}
	s.PendingCases = []PendingCase{}
	return nil
}
