package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
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
	// Moved marks a branch folder whose ParentID was changed by moveFolder and
	// not yet merged: merge carries the move to mainline, sync leaves it alone.
	Moved bool
}

type TestCase struct {
	ID, VersionID, FolderID                         string
	Title, Preconditions, Steps, Expected, Priority string
	CreatedBy, UpdatedBy, Result                    string
	CreatedAt, UpdatedAt                            time.Time
	BaseRevision, Revision                          int64
	Dirty                                           bool
	// Description is the case summary written by a person during review (the
	// review initiator, after reading the case). It is deliberately never filled
	// by the AI planner — see describePendingCase.
	Description string
	// Cached human-readable rewrite of Preconditions/Steps/Expected (see simplifyCase).
	// SimplifiedFrom* snapshots the exact source text it was generated from, so the
	// frontend can tell a stale cache from a fresh one with a plain string
	// comparison against the case's current fields — no hashing needed.
	SimplifiedPreconditions, SimplifiedSteps, SimplifiedExpected             string
	SimplifiedFromPreconditions, SimplifiedFromSteps, SimplifiedFromExpected string
	SimplifiedAt                                                             time.Time
}

type History struct {
	ID, CaseID, VersionID, SourceVersionID, Action, Author string
	Before, After                                          *TestCase
	CreatedAt                                              time.Time
}

type Record struct {
	SourceVersionID, SourceVersionName                  string
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
	ID, FolderID   string
	Title, Content string
	// Code is the confirmed requirement abbreviation (e.g. LOGIN) used as the
	// second segment of AI-designed case IDs TC-<Code>-<MODULE>-<CATEGORY>-NNN.
	Code                 string
	CreatedBy, UpdatedBy string
	CreatedAt, UpdatedAt time.Time
}

type PendingFolder struct {
	ID, ParentID, Name, CreatedBy string
	// Code is the case-ID prefix the folder stands for (REQ, REQ-MODULE or
	// REQ-MODULE-CATEGORY); Name stays a human-readable Chinese label.
	Code      string
	CreatedAt time.Time
}

type PendingCase struct {
	ID, FolderID                                    string
	Title, Preconditions, Steps, Expected, Priority string
	Review                                          string
	Description                                     string // see TestCase.Description
	CreatedBy, UpdatedBy, ReviewedBy                string
	CreatedAt, UpdatedAt, ReviewedAt                time.Time
	// See TestCase's identical fields.
	SimplifiedPreconditions, SimplifiedSteps, SimplifiedExpected             string
	SimplifiedFromPreconditions, SimplifiedFromSteps, SimplifiedFromExpected string
	SimplifiedAt                                                             time.Time
}

// Script is the automation asset of one test case: the Playwright spec the
// auto-test generator produced for it. Scripts and cases are one to one, keyed
// by (VersionID, CaseID) — the 自动化管理 page derives the script tree from the
// case's own folder, so the two trees stay identically named and structured
// without a second folder hierarchy to keep in sync. A script belongs to the
// version its case lives in: it is not copied when a version is created and not
// carried by sync/merge, because a generated spec is only meaningful for the
// case text it was generated from, and it is deleted with its case or version.
type Script struct {
	// ID is stable per (VersionID, CaseID) so regenerating replaces in place.
	ID, VersionID, CaseID string
	// Title snapshots the case title so a script row reads correctly on its own.
	Title, FileName, Language, Code string
	// Status is "generated" or "blocked"; Summary says what the script verifies,
	// or for a blocked case what input the generator was missing.
	Status, Summary string
	// Deviations are places where the live app contradicted the case's expected
	// result; the spec asserts the observed behaviour (risk + short summary).
	Deviations []RiskNote
	// From* snapshots the exact case text the script was generated from, so the
	// frontend detects a stale script with a plain string comparison after the
	// case is edited — the same mechanism as TestCase.SimplifiedFrom*.
	FromPreconditions, FromSteps, FromExpected string
	JobID, CreatedBy, UpdatedBy                string
	CreatedAt, UpdatedAt                       time.Time
}

// RiskNote is the risk-ranked one-line note shape both auto-test services use
// (planner limitations/issues, generator deviations).
type RiskNote struct {
	Risk, Summary string
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
	Scripts        []Script        `json:"scripts"`
}

type Action struct {
	Type, VersionID, FolderID, ParentID, CaseID, TaskID string
	Name, Title, Preconditions, Steps, Expected         string
	Description                                         string
	Priority, Result, Note, Author                      string
	CaseIDs                                             []string
	Submitted, Force                                    bool
	DocID, Content, Code                                string
	Review                                              string
	TargetFolderID                                      string
	// importPendingCases: the review folder mapped onto TargetFolderID; its
	// subfolders are recreated beneath the target. Defaults to pending-root.
	SourceFolderID string
	// simplifyCase / simplifyPendingCase: the caller-supplied rewrite to persist.
	SimplifiedPreconditions, SimplifiedSteps, SimplifiedExpected string
	// saveScript: the generated spec to persist against CaseID in VersionID.
	// Prefixed because the plain names collide with requirement Code and record
	// Result/Note above; the frontend sends exactly these keys.
	ScriptFileName, ScriptLanguage, ScriptCode string
	ScriptStatus, ScriptSummary, ScriptJobID   string
	ScriptDeviations                           []RiskNote
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
		Scripts:      []Script{},
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
	if state.Scripts == nil {
		state.Scripts = []Script{}
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
	case "simplifyCase":
		err = simplifyCase(&st, a)
	case "saveScript":
		err = saveScript(&st, a)
	case "deleteScripts":
		err = deleteScripts(&st, a)
	case "deletePendingCases":
		err = deletePendingCases(&st, a)
	case "simplifyPendingCase":
		err = simplifyPendingCase(&st, a)
	case "describePendingCase":
		err = describePendingCase(&st, a)
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
	case "setReqDocCode":
		err = setReqDocCode(&st, a)
	case "editReqDoc":
		err = editReqDoc(&st, a)
	case "deleteReqFolder":
		err = deleteReqFolder(&st, a)
	case "deleteReqDoc":
		err = deleteReqDoc(&st, a)
	case "createPendingFolder":
		err = createPendingFolder(&st, a)
	case "renamePendingFolder":
		err = renamePendingFolder(&st, a)
	case "deletePendingFolder":
		err = deletePendingFolder(&st, a)
	case "createPendingCase":
		err = createPendingCase(&st, a)
	case "editPendingCase":
		err = editPendingCase(&st, a)
	case "deletePendingCase":
		err = deletePendingCase(&st, a)
	case "reviewPendingCase":
		err = reviewPendingCase(&st, a)
	case "moveFolder":
		err = moveFolder(&st, a)
	case "movePendingFolder":
		err = movePendingFolder(&st, a)
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
	case "mergeFolder":
		var conflicts []string
		conflicts, err = mergeFolder(&st, a)
		if len(conflicts) > 0 {
			return Result{State: st, Conflicts: conflicts}, ConflictError{Cases: conflicts}
		}
	case "mergeCases":
		var conflicts []string
		var merged int
		conflicts, merged, err = mergeCases(&st, a)
		if len(conflicts) > 0 {
			return Result{State: st, Conflicts: conflicts}, ConflictError{Cases: conflicts}
		}
		if err == nil && merged == 0 {
			warnings = append(warnings, "没有可合并的变更")
		}
	case "deleteVersion":
		err = deleteVersion(&st, a)
	case "deleteCases":
		err = deleteCases(&st, a)
	case "deleteFolder":
		err = deleteFolder(&st, a)
	case "moveCases":
		err = moveCases(&st, a)
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

// deleteVersion removes a non-mainline version and everything scoped to it:
// its folders, cases, tasks, execution records and local history. History
// entries recorded on other versions that merely cite this one as a merge
// source are left untouched — they document what mainline already absorbed.
func deleteVersion(s *State, a Action) error {
	v, err := requireBranch(s, a.VersionID)
	if err != nil {
		return err
	}
	id := v.ID
	folders := make([]Folder, 0, len(s.Folders))
	for _, f := range s.Folders {
		if f.VersionID != id {
			folders = append(folders, f)
		}
	}
	s.Folders = folders
	cases := make([]TestCase, 0, len(s.Cases))
	for _, c := range s.Cases {
		if c.VersionID != id {
			cases = append(cases, c)
		}
	}
	s.Cases = cases
	scripts := make([]Script, 0, len(s.Scripts))
	for _, x := range s.Scripts {
		if x.VersionID != id {
			scripts = append(scripts, x)
		}
	}
	s.Scripts = scripts
	tasks := make([]Task, 0, len(s.Tasks))
	for _, t := range s.Tasks {
		if t.VersionID != id {
			tasks = append(tasks, t)
		}
	}
	s.Tasks = tasks
	records := make([]Record, 0, len(s.Records))
	for _, r := range s.Records {
		if r.VersionID != id {
			records = append(records, r)
		}
	}
	s.Records = records
	histories := make([]History, 0, len(s.Histories))
	for _, h := range s.Histories {
		if h.VersionID != id {
			histories = append(histories, h)
		}
	}
	s.Histories = histories
	versions := make([]Version, 0, len(s.Versions))
	for _, x := range s.Versions {
		if x.ID != id {
			versions = append(versions, x)
		}
	}
	s.Versions = versions
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
	c := TestCase{ID: id, VersionID: a.VersionID, FolderID: a.FolderID, Title: strings.TrimSpace(a.Title), Preconditions: a.Preconditions, Steps: a.Steps, Expected: a.Expected, Description: a.Description, Priority: a.Priority, CreatedBy: a.Author, UpdatedBy: a.Author, CreatedAt: t, UpdatedAt: t, Dirty: true}
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
	c.Description = a.Description
	c.Priority = a.Priority
	c.UpdatedBy = a.Author
	c.UpdatedAt = now()
	c.Dirty = true
	after := *c
	s.Histories = append(s.Histories, History{ID: ID(), CaseID: c.ID, VersionID: c.VersionID, SourceVersionID: c.VersionID, Action: "edit", Author: a.Author, Before: &before, After: &after, CreatedAt: c.UpdatedAt})
	return nil, nil
}

// simplifyCase persists a human-friendly rewrite of a case's precondition/
// steps/expected — generated by the caller (CaseHub's own /api/planner proxy
// to the auto-test simplify endpoint) or hand-edited by a reviewer in the UI,
// and handed in ready-made here. Allowed
// on mainline too: it's a cached reading aid, not a content edit, so it
// doesn't touch Dirty/Revision and needs no branch. SimplifiedFrom* snapshots
// the case's CURRENT fields (not trusted from the caller) so the frontend can
// detect staleness after a later edit with a plain string comparison; it
// rides along for free on every existing full-struct case copy (branch,
// sync, merge), so it survives those unless the underlying text changes.
func simplifyCase(s *State, a Action) error {
	c, _ := caseAt(s, a.VersionID, a.CaseID)
	if c == nil {
		return errors.New("用例不存在")
	}
	steps := strings.TrimSpace(a.SimplifiedSteps)
	if steps == "" {
		return errors.New("阅读友好版步骤不能为空")
	}
	c.SimplifiedPreconditions = a.SimplifiedPreconditions
	c.SimplifiedSteps = steps
	c.SimplifiedExpected = a.SimplifiedExpected
	c.SimplifiedFromPreconditions = c.Preconditions
	c.SimplifiedFromSteps = c.Steps
	c.SimplifiedFromExpected = c.Expected
	c.SimplifiedAt = now()
	return nil
}

func scriptAt(s *State, versionID, caseID string) (*Script, int) {
	for i := range s.Scripts {
		if s.Scripts[i].VersionID == versionID && s.Scripts[i].CaseID == caseID {
			return &s.Scripts[i], i
		}
	}
	return nil, -1
}

// saveScript stores the Playwright spec the auto-test generator produced for one
// case. It is an upsert keyed by (VersionID, CaseID): regenerating a script
// replaces the previous one in place, since a case has exactly one script.
// Allowed on mainline too — a script is a generated asset of the case, not a
// case edit, so it touches neither Dirty nor Revision and needs no branch.
// From* snapshots the case's CURRENT text (never trusted from the caller) so the
// frontend can tell a stale script from a fresh one by string comparison after a
// later edit. A blocked result is stored as well: it carries the reason the
// generator could not automate the case, which is what the reviewer needs to see.
func saveScript(s *State, a Action) error {
	c, _ := caseAt(s, a.VersionID, a.CaseID)
	if c == nil {
		return errors.New("用例不存在")
	}
	status := strings.TrimSpace(a.ScriptStatus)
	if status == "" {
		status = "generated"
	}
	if status != "generated" && status != "blocked" {
		return errors.New("脚本状态必须是 generated 或 blocked")
	}
	code := strings.TrimSpace(a.ScriptCode)
	if status == "generated" && code == "" {
		return errors.New("脚本内容不能为空")
	}
	for _, d := range a.ScriptDeviations {
		if d.Risk != "high" && d.Risk != "medium" && d.Risk != "low" {
			return errors.New("偏差风险必须是 high、medium 或 low")
		}
		if strings.TrimSpace(d.Summary) == "" {
			return errors.New("偏差说明不能为空")
		}
	}
	fileName := strings.TrimSpace(a.ScriptFileName)
	if fileName == "" {
		fileName = c.ID + ".spec.ts"
	}
	language := strings.TrimSpace(a.ScriptLanguage)
	if language == "" {
		language = "typescript"
	}
	t := now()
	script, _ := scriptAt(s, a.VersionID, a.CaseID)
	if script == nil {
		s.Scripts = append(s.Scripts, Script{ID: ID(), VersionID: a.VersionID, CaseID: c.ID, CreatedBy: a.Author, CreatedAt: t})
		script = &s.Scripts[len(s.Scripts)-1]
	}
	script.Title = c.Title
	script.FileName = fileName
	script.Language = language
	script.Code = code
	script.Status = status
	script.Summary = strings.TrimSpace(a.ScriptSummary)
	script.Deviations = append([]RiskNote(nil), a.ScriptDeviations...)
	script.FromPreconditions = c.Preconditions
	script.FromSteps = c.Steps
	script.FromExpected = c.Expected
	script.JobID = a.ScriptJobID
	script.UpdatedBy = a.Author
	script.UpdatedAt = t
	return nil
}

// deleteScripts removes the scripts of the given cases in one version. Unlike
// case deletion this is allowed on mainline: the script is a regenerable asset,
// not case content.
func deleteScripts(s *State, a Action) error {
	if len(a.CaseIDs) == 0 {
		return errors.New("请选择脚本")
	}
	ids := make(map[string]bool, len(a.CaseIDs))
	for _, id := range a.CaseIDs {
		ids[id] = true
	}
	kept := make([]Script, 0, len(s.Scripts))
	removed := 0
	for _, x := range s.Scripts {
		if x.VersionID == a.VersionID && ids[x.CaseID] {
			removed++
			continue
		}
		kept = append(kept, x)
	}
	if removed == 0 {
		return errors.New("脚本不存在")
	}
	s.Scripts = kept
	return nil
}

// simplifyPendingCase mirrors simplifyCase for the review-area's PendingCase
// pool, which has no VersionID of its own.
func simplifyPendingCase(s *State, a Action) error {
	c, _ := pendingCaseAt(s, a.CaseID)
	if c == nil {
		return errors.New("用例不存在")
	}
	steps := strings.TrimSpace(a.SimplifiedSteps)
	if steps == "" {
		return errors.New("阅读友好版步骤不能为空")
	}
	c.SimplifiedPreconditions = a.SimplifiedPreconditions
	c.SimplifiedSteps = steps
	c.SimplifiedExpected = a.SimplifiedExpected
	c.SimplifiedFromPreconditions = c.Preconditions
	c.SimplifiedFromSteps = c.Steps
	c.SimplifiedFromExpected = c.Expected
	c.SimplifiedAt = now()
	return nil
}

// describePendingCase sets only the reviewer-written Description of a pending
// case. Unlike editPendingCase it keeps the review verdict: the summary
// describes the case, it doesn't change what reviewers approved.
func describePendingCase(s *State, a Action) error {
	c, _ := pendingCaseAt(s, a.CaseID)
	if c == nil {
		return errors.New("用例不存在")
	}
	c.Description = strings.TrimSpace(a.Description)
	c.UpdatedBy = a.Author
	c.UpdatedAt = now()
	return nil
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
				n.Moved = false
				s.Folders = append(s.Folders, n)
			}
		}
	}
	// Folders the branch hasn't moved follow mainline moves, unless that would
	// create a cycle with a move the branch made itself (the branch move wins).
	for _, mf := range mainFolders {
		if mf.VersionID != "main" {
			continue
		}
		if bf, _ := folderAt(s, v.ID, mf.ID); bf != nil && !bf.Moved && bf.ParentID != mf.ParentID && !folderCycle(s, v.ID, bf.ID, mf.ParentID) {
			bf.ParentID = mf.ParentID
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
				n.Moved = false
				s.Folders = append(s.Folders, n)
			}
		}
	}
	for _, f := range branchFolders {
		if f.VersionID != v.ID || !f.Moved {
			continue
		}
		if mf, _ := folderAt(s, "main", f.ID); mf != nil && mf.ParentID != f.ParentID {
			if folderCycle(s, "main", mf.ID, f.ParentID) {
				return nil, fmt.Errorf("文件夹「%s」的移动与主线目录结构冲突（会形成循环），请先拉取主线后重新移动", f.Name)
			}
			mf.ParentID = f.ParentID
		}
	}
	for i := range s.Folders {
		if s.Folders[i].VersionID == v.ID {
			s.Folders[i].Moved = false
		}
	}
	branchCases := []TestCase{}
	for _, c := range s.Cases {
		if c.VersionID == v.ID && c.Dirty {
			branchCases = append(branchCases, c)
		}
	}
	for _, c := range branchCases {
		mergeCaseInto(s, c, s.MainRevision, c.FolderID, a.Author)
	}
	for _, c := range s.Cases {
		if c.VersionID == v.ID {
			archiveCaseActivity(s, c)
		}
	}
	v.BaseMainRevision = s.MainRevision
	return nil, nil
}

// mergeCaseInto upserts a branch case into mainline at the given revision and
// folder, records the merge in history, and marks the source branch case as
// caught up. Shared by mergeVersion (folderID stays whatever the branch case
// already has), mergeFolder (same, scoped to a folder subtree) and mergeCases
// (folderID is forced to the chosen target, flattening structure).
// archiveCaseActivity snapshots branch activity on mainline. Stable archive IDs
// make repeated merges idempotent without sharing records with branch deletion.
func archiveCaseActivity(s *State, c TestCase) int {
	added := 0
	seen := map[string]bool{}
	for _, h := range s.Histories {
		seen[h.ID] = true
	}
	for _, r := range s.Records {
		seen[r.ID] = true
	}
	prefix := "archive:" + c.VersionID + ":"
	for _, h := range s.Histories {
		if h.VersionID != c.VersionID || h.CaseID != c.ID || seen[prefix+h.ID] {
			continue
		}
		h.ID = prefix + h.ID
		h.VersionID = "main"
		h.SourceVersionID = c.VersionID
		s.Histories = append(s.Histories, h)
		seen[h.ID] = true
		added++
	}
	for _, r := range s.Records {
		if r.VersionID != c.VersionID || r.CaseID != c.ID || seen[prefix+r.ID] {
			continue
		}
		r.ID = prefix + r.ID
		r.VersionID = "main"
		r.SourceVersionID = c.VersionID
		for _, v := range s.Versions {
			if v.ID == c.VersionID {
				r.SourceVersionName = v.Name
			}
		}
		s.Records = append(s.Records, r)
		seen[r.ID] = true
		added++
	}
	return added
}

func mergeCaseInto(s *State, c TestCase, revision int64, folderID string, author string) {
	archiveCaseActivity(s, c)
	m, _ := caseAt(s, "main", c.ID)
	n := c
	n.VersionID = "main"
	n.FolderID = folderID
	n.Result = ""
	n.Dirty = false
	n.BaseRevision = 0
	n.Revision = revision
	n.UpdatedAt = now()
	if m == nil {
		s.Cases = append(s.Cases, n)
	} else {
		*m = n
	}
	s.Histories = append(s.Histories, History{ID: ID(), CaseID: c.ID, VersionID: "main", SourceVersionID: c.VersionID, Action: "merge", Author: author, After: &n, CreatedAt: now()})
	bc, _ := caseAt(s, c.VersionID, c.ID)
	bc.BaseRevision = revision
	bc.Dirty = false
}

func descendantFolderIDs(s *State, versionID, folderID string) []string {
	var out []string
	var walk func(id string)
	walk = func(id string) {
		for _, f := range s.Folders {
			if f.VersionID == versionID && f.ParentID == id {
				out = append(out, f.ID)
				walk(f.ID)
			}
		}
	}
	walk(folderID)
	return out
}

// mergeFolder merges one branch folder's subtree into mainline, preserving
// its internal structure, grafted under a chosen mainline parent folder. It
// only handles folders new to mainline (an already-merged folder cannot be
// re-grafted elsewhere) and aborts entirely, without writing anything, if the
// target parent already has a same-named child folder.
func mergeFolder(s *State, a Action) ([]string, error) {
	v, err := requireBranch(s, a.VersionID)
	if err != nil {
		return nil, err
	}
	f, _ := folderAt(s, v.ID, a.FolderID)
	if f == nil {
		return nil, errors.New("文件夹不存在")
	}
	if a.TargetFolderID == "" {
		return nil, errors.New("请选择目标文件夹")
	}
	target, _ := folderAt(s, "main", a.TargetFolderID)
	if target == nil {
		return nil, errors.New("主线目标文件夹不存在")
	}
	if mf, _ := folderAt(s, "main", f.ID); mf != nil {
		return nil, errors.New("该文件夹已在主线中，无法重复合并")
	}
	for _, x := range s.Folders {
		if x.VersionID == "main" && x.ParentID == target.ID && x.Name == f.Name {
			return nil, fmt.Errorf("主线目标目录下已存在同名文件夹，合并已取消：%s", f.Name)
		}
	}
	subtree := append([]string{f.ID}, descendantFolderIDs(s, v.ID, f.ID)...)
	inSubtree := map[string]bool{}
	for _, id := range subtree {
		inSubtree[id] = true
	}
	conflicts := []string{}
	for _, c := range s.Cases {
		if c.VersionID != v.ID || !c.Dirty || !inSubtree[c.FolderID] {
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
	for _, bf := range branchFolders {
		if bf.VersionID != v.ID || !inSubtree[bf.ID] {
			continue
		}
		n := bf
		n.VersionID = "main"
		n.Moved = false
		if n.ID == f.ID {
			n.ParentID = target.ID
		}
		s.Folders = append(s.Folders, n)
	}
	branchCases := []TestCase{}
	for _, c := range s.Cases {
		if c.VersionID == v.ID && c.Dirty && inSubtree[c.FolderID] {
			branchCases = append(branchCases, c)
		}
	}
	for _, c := range branchCases {
		mergeCaseInto(s, c, s.MainRevision, c.FolderID, a.Author)
	}
	return nil, nil
}

// mergeCases merges an explicit, possibly cross-folder set of branch cases
// into a single chosen mainline folder, flattening structure. Cases with no
// pending text change still archive new activity without overwriting mainline
// content; the caller surfaces a warning when neither content nor activity changed.
func mergeCases(s *State, a Action) ([]string, int, error) {
	v, err := requireBranch(s, a.VersionID)
	if err != nil {
		return nil, 0, err
	}
	if len(a.CaseIDs) == 0 {
		return nil, 0, errors.New("请选择用例")
	}
	if a.TargetFolderID == "" {
		return nil, 0, errors.New("请选择目标文件夹")
	}
	target, _ := folderAt(s, "main", a.TargetFolderID)
	if target == nil {
		return nil, 0, errors.New("主线目标文件夹不存在")
	}
	var toMerge []TestCase
	conflicts := []string{}
	for _, id := range a.CaseIDs {
		c, _ := caseAt(s, v.ID, id)
		if c == nil {
			return nil, 0, fmt.Errorf("用例 %s 不属于当前版本", id)
		}
		if !c.Dirty {
			continue
		}
		m, _ := caseAt(s, "main", c.ID)
		if m != nil && m.Revision > c.BaseRevision {
			conflicts = append(conflicts, c.ID)
			continue
		}
		toMerge = append(toMerge, *c)
	}
	if len(conflicts) > 0 {
		return conflicts, 0, nil
	}
	archivedCases := 0
	for _, id := range a.CaseIDs {
		c, _ := caseAt(s, v.ID, id)
		if !c.Dirty {
			if m, _ := caseAt(s, "main", id); m != nil && archiveCaseActivity(s, *c) > 0 {
				archivedCases++
			}
		}
	}
	if len(toMerge) == 0 {
		return nil, archivedCases, nil
	}
	s.MainRevision++
	for _, c := range toMerge {
		mergeCaseInto(s, c, s.MainRevision, target.ID, a.Author)
	}
	return nil, len(toMerge) + archivedCases, nil
}

// deleteCases removes one or more branch cases (single-row delete and bulk
// delete both call this with a.CaseIDs of length 1 or more). Their execution
// records and history are removed with them, and they're pruned from any
// task's CaseIDs so task case counts stay accurate; the folders that held
// them are left in place even if now empty.
func deleteCases(s *State, a Action) error {
	v, err := requireBranch(s, a.VersionID)
	if err != nil {
		return err
	}
	if len(a.CaseIDs) == 0 {
		return errors.New("请选择用例")
	}
	ids := make(map[string]bool, len(a.CaseIDs))
	for _, id := range a.CaseIDs {
		ids[id] = true
	}
	cases := make([]TestCase, 0, len(s.Cases))
	removed := 0
	for _, c := range s.Cases {
		if c.VersionID == v.ID && ids[c.ID] {
			removed++
			continue
		}
		cases = append(cases, c)
	}
	if removed == 0 {
		return errors.New("用例不存在")
	}
	s.Cases = cases
	scripts := make([]Script, 0, len(s.Scripts))
	for _, x := range s.Scripts {
		if x.VersionID == v.ID && ids[x.CaseID] {
			continue
		}
		scripts = append(scripts, x)
	}
	s.Scripts = scripts
	records := make([]Record, 0, len(s.Records))
	for _, r := range s.Records {
		if r.VersionID == v.ID && ids[r.CaseID] {
			continue
		}
		records = append(records, r)
	}
	s.Records = records
	histories := make([]History, 0, len(s.Histories))
	for _, h := range s.Histories {
		if h.VersionID == v.ID && ids[h.CaseID] {
			continue
		}
		histories = append(histories, h)
	}
	s.Histories = histories
	for i := range s.Tasks {
		if s.Tasks[i].VersionID != v.ID {
			continue
		}
		kept := make([]string, 0, len(s.Tasks[i].CaseIDs))
		for _, id := range s.Tasks[i].CaseIDs {
			if !ids[id] {
				kept = append(kept, id)
			}
		}
		s.Tasks[i].CaseIDs = kept
	}
	return nil
}

// deleteFolder removes an empty branch folder. Mirrors deleteReqFolder /
// deletePendingFolder's empty-folder guard for the case-tree folders.
func deleteFolder(s *State, a Action) error {
	v, err := requireBranch(s, a.VersionID)
	if err != nil {
		return err
	}
	f, i := folderAt(s, v.ID, a.FolderID)
	if f == nil {
		return errors.New("文件夹不存在")
	}
	if f.ID == "root" {
		return errors.New("不能删除根目录")
	}
	for _, x := range s.Folders {
		if x.VersionID == v.ID && x.ParentID == f.ID {
			return errors.New("只能删除空文件夹")
		}
	}
	for _, x := range s.Cases {
		if x.VersionID == v.ID && x.FolderID == f.ID {
			return errors.New("只能删除空文件夹")
		}
	}
	s.Folders = append(s.Folders[:i], s.Folders[i+1:]...)
	return nil
}

// moveCases relocates a set of branch cases into a different folder within
// the same branch, marking them Dirty so a later merge picks up the move.
// folderCycle reports whether giving folderID the parent newParent would make
// folderID its own ancestor within the version.
func folderCycle(s *State, versionID, folderID, newParent string) bool {
	for id, steps := newParent, 0; id != "" && steps <= len(s.Folders); steps++ {
		if id == folderID {
			return true
		}
		f, _ := folderAt(s, versionID, id)
		if f == nil {
			return false
		}
		id = f.ParentID
	}
	return false
}

func moveFolder(s *State, a Action) error {
	v, err := requireBranch(s, a.VersionID)
	if err != nil {
		return err
	}
	f, _ := folderAt(s, v.ID, a.FolderID)
	if f == nil {
		return errors.New("文件夹不存在")
	}
	if f.ParentID == "" {
		return errors.New("根目录不能移动")
	}
	target, _ := folderAt(s, v.ID, a.TargetFolderID)
	if target == nil {
		return errors.New("目标文件夹不存在")
	}
	if folderCycle(s, v.ID, f.ID, target.ID) {
		return errors.New("不能移动到自身或其子文件夹下")
	}
	if f.ParentID == target.ID {
		return nil
	}
	f.ParentID = target.ID
	f.Moved = true
	return nil
}

func movePendingFolder(s *State, a Action) error {
	f, _ := pendingFolderAt(s, a.FolderID)
	if f == nil {
		return errors.New("文件夹不存在")
	}
	if f.ID == "pending-root" || f.ParentID == "" {
		return errors.New("根目录不能移动")
	}
	target, _ := pendingFolderAt(s, a.TargetFolderID)
	if target == nil {
		return errors.New("目标文件夹不存在")
	}
	for id, steps := target.ID, 0; id != "" && steps <= len(s.PendingFolders); steps++ {
		if id == f.ID {
			return errors.New("不能移动到自身或其子文件夹下")
		}
		p, _ := pendingFolderAt(s, id)
		if p == nil {
			break
		}
		id = p.ParentID
	}
	f.ParentID = target.ID
	return nil
}

func moveCases(s *State, a Action) error {
	v, err := requireBranch(s, a.VersionID)
	if err != nil {
		return err
	}
	if len(a.CaseIDs) == 0 {
		return errors.New("请选择用例")
	}
	if a.TargetFolderID == "" {
		return errors.New("请选择目标文件夹")
	}
	target, _ := folderAt(s, v.ID, a.TargetFolderID)
	if target == nil {
		return errors.New("目标文件夹不存在")
	}
	var toMove []*TestCase
	for _, id := range a.CaseIDs {
		c, _ := caseAt(s, v.ID, id)
		if c == nil {
			return fmt.Errorf("用例 %s 不属于当前版本", id)
		}
		toMove = append(toMove, c)
	}
	t := now()
	for _, c := range toMove {
		if c.FolderID == target.ID {
			continue
		}
		before := *c
		c.FolderID = target.ID
		c.Dirty = true
		c.UpdatedBy = a.Author
		c.UpdatedAt = t
		after := *c
		s.Histories = append(s.Histories, History{ID: ID(), CaseID: c.ID, VersionID: v.ID, SourceVersionID: v.ID, Action: "move", Author: a.Author, Before: &before, After: &after, CreatedAt: t})
	}
	return nil
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

// Case ID scheme: TC-<REQ>-<MODULE>-<CATEGORY>-<NNN>. Codes are uppercase ASCII
// without "-", so an ID splits back into its parts. The review area mirrors it
// with folders coded REQ / REQ-MODULE / REQ-MODULE-CATEGORY (Chinese names,
// the code kept in PendingFolder.Code), and a pending case created in a
// REQ-MODULE-CATEGORY folder is numbered from that code.
var (
	codeRE           = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,11}$`)
	caseFolderRE     = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,11}-[A-Z][A-Z0-9]{1,11}-(FUNC|REL|PERF|SEC|COMPAT|UX)$`)
	folderCodeRE     = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,11}(-[A-Z][A-Z0-9]{1,11}(-(FUNC|REL|PERF|SEC|COMPAT|UX))?)?$`)
	caseNumberSuffix = regexp.MustCompile(`^\d{3,}$`)
)

// nextCaseID returns TC-<prefix>-NNN, one past the highest number already used
// by any pending or version case with that prefix, so IDs never collide.
func nextCaseID(s *State, prefix string) string {
	head, max := "TC-"+prefix+"-", 0
	consider := func(id string) {
		if rest, ok := strings.CutPrefix(id, head); ok && caseNumberSuffix.MatchString(rest) {
			if n, err := strconv.Atoi(rest); err == nil && n > max {
				max = n
			}
		}
	}
	for _, c := range s.PendingCases {
		consider(c.ID)
	}
	for _, c := range s.Cases {
		consider(c.ID)
	}
	return fmt.Sprintf("%s%03d", head, max+1)
}

func setReqDocCode(s *State, a Action) error {
	d, _ := reqDocAt(s, a.DocID)
	if d == nil {
		return errors.New("需求文档不存在")
	}
	code := strings.TrimSpace(a.Code)
	if code != "" && !codeRE.MatchString(code) {
		return errors.New("需求缩写需为 2–12 位大写英文字母或数字，以字母开头，不含 -")
	}
	for _, other := range s.ReqDocs {
		if code != "" && other.ID != d.ID && other.Code == code {
			return fmt.Errorf("需求缩写 %s 已被需求「%s」使用", code, other.Title)
		}
	}
	d.Code = code
	d.UpdatedBy = a.Author
	d.UpdatedAt = now()
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
func deleteReqFolder(s *State, a Action) error {
	f, i := reqFolderAt(s, a.FolderID)
	if f == nil {
		return errors.New("文件夹不存在")
	}
	if f.ID == "req-root" {
		return errors.New("不能删除根目录")
	}
	for _, x := range s.ReqFolders {
		if x.ParentID == f.ID {
			return errors.New("只能删除空文件夹")
		}
	}
	for _, x := range s.ReqDocs {
		if x.FolderID == f.ID {
			return errors.New("只能删除空文件夹")
		}
	}
	s.ReqFolders = append(s.ReqFolders[:i], s.ReqFolders[i+1:]...)
	return nil
}
func deleteReqDoc(s *State, a Action) error {
	d, i := reqDocAt(s, a.DocID)
	if d == nil {
		return errors.New("需求文档不存在")
	}
	s.ReqDocs = append(s.ReqDocs[:i], s.ReqDocs[i+1:]...)
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
	code := strings.TrimSpace(a.Code)
	if code != "" && !folderCodeRE.MatchString(code) {
		return errors.New("文件夹编号前缀格式应为 需求缩写[-模块缩写[-测试类别]]")
	}
	s.PendingFolders = append(s.PendingFolders, PendingFolder{ID: ID(), ParentID: a.ParentID, Name: strings.TrimSpace(a.Name), Code: code, CreatedBy: a.Author, CreatedAt: now()})
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
func deletePendingFolder(s *State, a Action) error {
	f, i := pendingFolderAt(s, a.FolderID)
	if f == nil {
		return errors.New("文件夹不存在")
	}
	if f.ID == "pending-root" {
		return errors.New("不能删除根目录")
	}
	for _, x := range s.PendingFolders {
		if x.ParentID == f.ID {
			return errors.New("只能删除空文件夹")
		}
	}
	for _, x := range s.PendingCases {
		if x.FolderID == f.ID {
			return errors.New("只能删除空文件夹")
		}
	}
	s.PendingFolders = append(s.PendingFolders[:i], s.PendingFolders[i+1:]...)
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
	id := "CASE-" + strings.ToUpper(ID()[:6])
	if f, _ := pendingFolderAt(s, a.FolderID); f != nil {
		code := f.Code
		if code == "" && caseFolderRE.MatchString(f.Name) {
			code = f.Name // folders created before Code existed were named by their code
		}
		if caseFolderRE.MatchString(code) {
			id = nextCaseID(s, code)
		}
	}
	c := PendingCase{ID: id, FolderID: a.FolderID, Title: strings.TrimSpace(a.Title), Preconditions: a.Preconditions, Steps: a.Steps, Expected: a.Expected, Description: a.Description, Priority: a.Priority, CreatedBy: a.Author, UpdatedBy: a.Author, CreatedAt: t, UpdatedAt: t}
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
	c.Description = a.Description
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

// deletePendingCases removes several pending cases at once. It is all or
// nothing: if any ID is unknown (e.g. already deleted elsewhere) nothing is
// removed and the missing IDs are reported.
func deletePendingCases(s *State, a Action) error {
	if len(a.CaseIDs) == 0 {
		return errors.New("请选择要删除的用例")
	}
	want := map[string]bool{}
	for _, id := range a.CaseIDs {
		want[id] = true
	}
	for _, c := range s.PendingCases {
		delete(want, c.ID)
	}
	if len(want) > 0 {
		missing := make([]string, 0, len(want))
		for id := range want {
			missing = append(missing, id)
		}
		sort.Strings(missing)
		return fmt.Errorf("用例不存在：%s", strings.Join(missing, "、"))
	}
	drop := map[string]bool{}
	for _, id := range a.CaseIDs {
		drop[id] = true
	}
	kept := s.PendingCases[:0]
	for _, c := range s.PendingCases {
		if !drop[c.ID] {
			kept = append(kept, c)
		}
	}
	s.PendingCases = kept
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

// importPendingCases copies pending-review cases into a branch version once
// they have passed review, preserving their folder structure. With CaseIDs
// empty it targets the entire pending-review tree (legacy/API behavior) and,
// since that empties the area, resets it to a bare pending-root; with CaseIDs
// set it is scoped to just those cases (used by the folder- and case-level
// "导入到版本" menu actions) and only removes the imported cases, leaving the
// rest of the review area untouched. Either way it is all-or-nothing within
// its scope: any name conflict with the target version aborts before
// anything is written.
func importPendingCases(s *State, a Action) error {
	v, err := requireBranch(s, a.VersionID)
	if err != nil {
		return err
	}
	scoped := len(a.CaseIDs) > 0
	var targets []PendingCase
	if scoped {
		want := map[string]bool{}
		for _, id := range a.CaseIDs {
			want[id] = true
		}
		for _, c := range s.PendingCases {
			if want[c.ID] {
				targets = append(targets, c)
			}
		}
	} else {
		targets = s.PendingCases
	}
	if len(targets) == 0 {
		return errors.New("没有可导入的用例")
	}
	var unreviewed []string
	for _, c := range targets {
		if c.Review != "passed" {
			unreviewed = append(unreviewed, c.Title)
		}
	}
	if len(unreviewed) > 0 {
		return fmt.Errorf("存在未通过评审的用例：%s", strings.Join(unreviewed, "、"))
	}

	targetRoot := "root"
	if a.TargetFolderID != "" {
		targetRoot = a.TargetFolderID
	}
	if f, _ := folderAt(s, v.ID, targetRoot); f == nil {
		return errors.New("目标文件夹不存在")
	}
	source := "pending-root"
	if a.SourceFolderID != "" {
		source = a.SourceFolderID
	}
	if _, i := pendingFolderAt(s, source); i < 0 {
		return errors.New("来源文件夹不存在")
	}
	if !scoped && source != "pending-root" {
		return errors.New("导入整个待评审区时不能指定来源文件夹")
	}
	// inSource reports whether a pending folder is the source or lies beneath it.
	inSource := func(folderID string) bool {
		for id, steps := folderID, 0; id != "" && steps <= len(s.PendingFolders); steps++ {
			if id == source {
				return true
			}
			pf, _ := pendingFolderAt(s, id)
			if pf == nil {
				return false
			}
			id = pf.ParentID
		}
		return false
	}
	var outside []string
	for _, c := range targets {
		if !inSource(c.FolderID) {
			outside = append(outside, c.Title)
		}
	}
	if len(outside) > 0 {
		return fmt.Errorf("用例不在来源文件夹内：%s", strings.Join(outside, "、"))
	}

	// The source folder maps onto the target folder; folders beneath it are
	// recreated (or reused by name) under the target, e.g. source A with
	// cases in A/B/C/D lands as <target>/B/C/D.
	targetFolderID := map[string]string{source: targetRoot} // pending folder ID -> resolved target folder ID
	newFolders := []Folder{}
	var conflicts []string
	var resolve func(pf PendingFolder) string
	resolve = func(pf PendingFolder) string {
		if id, ok := targetFolderID[pf.ID]; ok {
			return id
		}
		parentID := targetRoot
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
	if !scoped {
		for _, pf := range s.PendingFolders {
			if pf.ID == "pending-root" {
				continue
			}
			resolve(pf)
		}
	}
	for _, pc := range targets {
		folderID := targetRoot
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
	imported := map[string]bool{}
	for _, pc := range targets {
		folderID, ok := targetFolderID[pc.FolderID]
		if !ok {
			folderID = targetRoot
		}
		c := TestCase{ID: pc.ID, VersionID: v.ID, FolderID: folderID, Title: pc.Title, Preconditions: pc.Preconditions, Steps: pc.Steps, Expected: pc.Expected, Description: pc.Description, Priority: pc.Priority, CreatedBy: a.Author, UpdatedBy: a.Author, CreatedAt: t, UpdatedAt: t, Dirty: true,
			SimplifiedPreconditions: pc.SimplifiedPreconditions, SimplifiedSteps: pc.SimplifiedSteps, SimplifiedExpected: pc.SimplifiedExpected,
			SimplifiedFromPreconditions: pc.SimplifiedFromPreconditions, SimplifiedFromSteps: pc.SimplifiedFromSteps, SimplifiedFromExpected: pc.SimplifiedFromExpected,
			SimplifiedAt: pc.SimplifiedAt}
		s.Cases = append(s.Cases, c)
		after := c
		s.Histories = append(s.Histories, History{ID: ID(), CaseID: c.ID, VersionID: v.ID, SourceVersionID: v.ID, Action: "create", Author: a.Author, After: &after, CreatedAt: t})
		imported[pc.ID] = true
	}
	if scoped {
		remaining := make([]PendingCase, 0, len(s.PendingCases)-len(imported))
		for _, c := range s.PendingCases {
			if !imported[c.ID] {
				remaining = append(remaining, c)
			}
		}
		s.PendingCases = remaining
	} else {
		s.PendingFolders = []PendingFolder{{ID: "pending-root", Name: "待评审用例", CreatedBy: "system", CreatedAt: t}}
		s.PendingCases = []PendingCase{}
	}
	return nil
}
