package core

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Hosting is one "AI 托管" run of a requirement: estimate → design → review area → passed → filed into a test
// version under 全部用例 / Product / ProductVersion / <requirement>. The hosting runner (internal/hosting) drives
// it on the server, one action per step, so it needs no browser and survives a CaseHub restart.
type Hosting struct {
	ID, DocID, VersionID    string
	Product, ProductVersion string
	Stage                   string // estimating, designing, reviewing, archiving, done, stopped
	FailedAt, Error, Note   string // FailedAt: the stage a stopped hosting stopped in
	Code, Rationale         string
	CaseCount               int
	PlannerTaskID           string
	Continuable             bool // the failed design task kept its session and can be continued
	ReqFolder               string
	CaseIDs                 []string
	Issues                  []HostingIssue      `json:",omitempty"`
	Limitations             []HostingLimitation `json:",omitempty"`
	CreatedBy               string
	CreatedAt, UpdatedAt    time.Time
	FinishedAt              time.Time `json:",omitempty"`
}

// HostingIssue and HostingLimitation keep the planner's wire names so the frontend renders them like a
// manual design's result.
type HostingIssue struct {
	Risk     string `json:"risk"`
	Scenario string `json:"scenario"`
	Symptom  string `json:"symptom"`
}
type HostingLimitation struct {
	Risk    string `json:"risk"`
	Summary string `json:"summary"`
}

// PlannerResult is the part of a planner job's result a hosting files away.
type PlannerResult struct {
	Modules []struct {
		Requirement string `json:"requirement"`
		Code        string `json:"code"`
		Name        string `json:"name"`
	} `json:"modules"`
	Cases []struct {
		Request      string `json:"request"`
		Name         string `json:"name"`
		CaseID       string `json:"case_id"`
		Priority     string `json:"priority"`
		Precondition string `json:"precondition"`
		Steps        string `json:"steps"`
		Expects      string `json:"expects"`
	} `json:"cases"`
	Issues            []HostingIssue      `json:"issues"`
	Limitations       []HostingLimitation `json:"limitations"`
	ExplorationNotes  string              `json:"explorationNotes"`
	ProductExperience *struct {
		Origin string `json:"origin"`
		Notes  string `json:"notes"`
	} `json:"productExperience"`
}

const (
	HostingEstimating = "estimating"
	HostingDesigning  = "designing"
	HostingImporting  = "importing" // only ever a FailedAt: importing is a single action from designing to reviewing
	HostingReviewing  = "reviewing"
	HostingArchiving  = "archiving"
	HostingDone       = "done"
	HostingStopped    = "stopped"
	// HostingAuthor is who the hosting's own changes are recorded as (reviewer of the cases it passes).
	HostingAuthor = "AI 托管"
)

func HostingActive(h Hosting) bool { return h.Stage != HostingDone && h.Stage != HostingStopped }

func hostingAt(s *State, id string) *Hosting {
	for i := range s.Hostings {
		if s.Hostings[i].ID == id {
			return &s.Hostings[i]
		}
	}
	return nil
}

// applyHosting handles every hosting action; ok is false for a type that is not one.
func applyHosting(s *State, a Action) (ok bool, err error) {
	switch a.Type {
	case "startHosting":
		return true, startHosting(s, a)
	case "deleteHosting":
		return true, deleteHosting(s, a)
	}
	if !strings.HasPrefix(a.Type, "hosting") {
		return false, nil
	}
	h := hostingAt(s, a.HostingID)
	if h == nil {
		return true, errors.New("托管不存在")
	}
	switch a.Type {
	case "hostingDesigning":
		err = hostingDesigning(s, h, a)
	case "hostingSubmitted":
		if h.Stage != HostingDesigning {
			return true, errors.New("托管不在设计阶段")
		}
		h.PlannerTaskID = a.TaskID
	case "hostingImport":
		err = hostingImport(s, h, a)
	case "hostingReview":
		err = hostingReview(s, h, a)
	case "hostingArchive":
		err = hostingArchive(s, h, a)
	case "hostingStop":
		if !HostingActive(*h) {
			return true, errors.New("托管已结束")
		}
		h.FailedAt, h.Stage, h.Error = h.Stage, HostingStopped, a.Note
		if a.Hosting != nil {
			if a.Hosting.FailedAt != "" {
				h.FailedAt = a.Hosting.FailedAt
			}
			h.Continuable = a.Hosting.Continuable
		}
	case "hostingRetry":
		err = hostingRetry(h)
	default:
		return true, fmt.Errorf("unknown action %q", a.Type)
	}
	if err == nil {
		h.UpdatedAt = now()
	}
	return true, err
}

func startHosting(s *State, a Action) error {
	in := a.Hosting
	if in == nil {
		return errors.New("缺少托管参数")
	}
	doc, _ := reqDocAt(s, in.DocID)
	if doc == nil {
		return errors.New("需求文档不存在")
	}
	if _, err := requireBranch(s, in.VersionID); err != nil {
		return err
	}
	product, version := strings.TrimSpace(in.Product), strings.TrimSpace(in.ProductVersion)
	if product == "" || version == "" {
		return errors.New("请填写产品和产品版本")
	}
	if strings.ContainsAny(product+version, `/\`) {
		return errors.New("产品和产品版本名称里不能有 /")
	}
	for _, h := range s.Hostings {
		if h.DocID == doc.ID && HostingActive(h) {
			return errors.New("这份需求已有进行中的托管")
		}
	}
	// One record per requirement: a new run replaces the finished or stopped one.
	kept := s.Hostings[:0]
	for _, h := range s.Hostings {
		if h.DocID != doc.ID {
			kept = append(kept, h)
		}
	}
	t := now()
	s.Hostings = append(kept, Hosting{ID: ID(), DocID: doc.ID, VersionID: in.VersionID, Product: product, ProductVersion: version,
		Stage: HostingEstimating, ReqFolder: doc.Title, CreatedBy: a.Author, CreatedAt: t, UpdatedAt: t})
	return nil
}

// deleteHosting is "放弃托管": only a hosting that is not running can be dropped (the runner cancels a running
// one's design task first). Cases it already put in the review area stay there.
func deleteHosting(s *State, a Action) error {
	for i, h := range s.Hostings {
		if h.ID == a.HostingID {
			if HostingActive(h) && !a.Force {
				return errors.New("托管进行中，不能直接删除")
			}
			s.Hostings = append(s.Hostings[:i], s.Hostings[i+1:]...)
			return nil
		}
	}
	return errors.New("托管不存在")
}

// hostingDesigning takes the estimate as-is: the requirement keeps its code, or gets the suggested one.
func hostingDesigning(s *State, h *Hosting, a Action) error {
	if h.Stage != HostingEstimating {
		return errors.New("托管不在评估阶段")
	}
	in := a.Hosting
	if in == nil || in.CaseCount < 1 {
		return errors.New("缺少评估结果")
	}
	doc, _ := reqDocAt(s, h.DocID)
	if doc == nil {
		return errors.New("需求文档不存在")
	}
	if doc.Code == "" {
		if err := setReqDocCode(s, Action{DocID: doc.ID, Code: in.Code, Author: a.Author}); err != nil {
			return err
		}
	}
	if doc.Code == "" {
		return errors.New("没有得到可用的需求缩写")
	}
	h.Stage, h.Code, h.CaseCount, h.Rationale, h.Note, h.PlannerTaskID = HostingDesigning, doc.Code, in.CaseCount, in.Rationale, in.Note, ""
	return nil
}

var (
	aiCaseIDRE        = regexp.MustCompile(`^TC-([A-Z][A-Z0-9]{1,11})-([A-Z][A-Z0-9]{1,11})-(FUNC|REL|PERF|SEC|COMPAT|UX)-\d{3,}$`)
	testCategoryNames = map[string]string{"FUNC": "功能测试", "REL": "可靠性测试", "PERF": "性能测试", "SEC": "安全测试", "COMPAT": "兼容性测试", "UX": "易用性测试"}
	riskLabels        = map[string]string{"high": "高", "medium": "中", "low": "低"}
)

// ensurePendingFolder finds the review-area folder under parent standing for code (or, without a code, named
// name), creating it when missing. Folders from before Code existed were named by their code.
func ensurePendingFolder(s *State, parent, code, name, author string) string {
	for _, f := range s.PendingFolders {
		if f.ParentID != parent {
			continue
		}
		if code != "" && (f.Code == code || f.Code == "" && f.Name == code) || code == "" && f.Name == name {
			return f.ID
		}
	}
	f := PendingFolder{ID: ID(), ParentID: parent, Name: name, Code: code, CreatedBy: author, CreatedAt: now()}
	s.PendingFolders = append(s.PendingFolders, f)
	return f.ID
}

// hostingImport files a finished design run the way the manual "确认放入用例评审" does — review area /
// requirement / module / category, numbered by the folder codes — together with the run's exploration notes,
// product experience and defects, in one change.
func hostingImport(s *State, h *Hosting, a Action) error {
	if h.Stage != HostingDesigning {
		return errors.New("托管不在设计阶段")
	}
	r := a.Planner
	if r == nil || len(r.Cases) == 0 {
		return errors.New("设计结果中没有可导入的用例")
	}
	doc, _ := reqDocAt(s, h.DocID)
	if doc == nil {
		return errors.New("需求文档不存在")
	}
	if strings.TrimSpace(r.ExplorationNotes) != "" {
		if err := saveReqExploration(s, Action{DocID: doc.ID, ExplorationNotes: r.ExplorationNotes}); err != nil {
			return err
		}
	}
	if p := r.ProductExperience; p != nil && p.Origin != "" && strings.TrimSpace(p.Notes) != "" {
		_ = recordProductExploration(s, Action{Origin: p.Origin, ExplorationNotes: p.Notes}) // optional knowledge; never blocks filing
	}
	var created []string
	reqFolder := ""
	for _, c := range r.Cases {
		var folderID, top string
		if m := aiCaseIDRE.FindStringSubmatch(c.CaseID); m != nil {
			req, mod, cat := m[1], m[2], m[3]
			moduleName := mod
			for _, x := range r.Modules {
				if x.Requirement == c.Request && x.Code == mod && x.Name != "" {
					moduleName = x.Name
				}
			}
			reqName := req
			if doc.Code == req {
				reqName = doc.Title
			}
			top = ensurePendingFolder(s, "pending-root", req, reqName, a.Author)
			folderID = ensurePendingFolder(s, top, req+"-"+mod, moduleName, a.Author)
			folderID = ensurePendingFolder(s, folderID, req+"-"+mod+"-"+cat, testCategoryNames[cat], a.Author)
		} else {
			top = ensurePendingFolder(s, "pending-root", doc.Code, doc.Title, a.Author)
			folderID = top
		}
		if reqFolder == "" {
			f, _ := pendingFolderAt(s, top)
			reqFolder = f.Name
		}
		if err := createPendingCase(s, Action{FolderID: folderID, Title: c.Name, Priority: c.Priority, Preconditions: c.Precondition, Steps: c.Steps, Expected: c.Expects, Author: a.Author}); err != nil {
			return fmt.Errorf("「%s」：%w", c.Name, err)
		}
		created = append(created, s.PendingCases[len(s.PendingCases)-1].ID)
	}
	var reports []IssueReport
	for _, i := range r.Issues {
		label := riskLabels[i.Risk]
		if label == "" {
			label = "未评级"
		}
		reports = append(reports, IssueReport{RequirementID: doc.ID, Title: "[" + label + "] " + i.Scenario,
			Description: "**场景**：" + i.Scenario + "\n\n**现象**：" + i.Symptom + "\n\n来源：用例设计探索（风险 " + label + "，AI 托管）",
			Source:      "planner:" + doc.ID + ":" + i.Scenario})
	}
	if err := reportIssues(s, Action{Reports: reports, Author: a.Author}); err != nil {
		return err
	}
	h.Stage, h.CaseIDs, h.ReqFolder, h.Issues, h.Limitations = HostingReviewing, created, reqFolder, r.Issues, r.Limitations
	return nil
}

// liveHostingCases are the hosting's cases still in the review area (a person may have deleted some meanwhile).
func liveHostingCases(s *State, h *Hosting) []string {
	var ids []string
	for _, id := range h.CaseIDs {
		if c, _ := pendingCaseAt(s, id); c != nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func hostingReview(s *State, h *Hosting, a Action) error {
	if h.Stage != HostingReviewing {
		return errors.New("托管不在评审阶段")
	}
	if ids := liveHostingCases(s, h); len(ids) > 0 {
		if err := reviewPendingCases(s, Action{CaseIDs: ids, Review: "passed", Author: a.Author}); err != nil {
			return err
		}
	}
	h.Stage = HostingArchiving
	return nil
}

// hostingArchive moves the passed cases into the test version under 全部用例 / Product / ProductVersion,
// keeping the review area's requirement folder (and its module/category folders) beneath it. Missing
// folders are created and existing ones reused by name; a name clash with a case already there aborts the
// whole step, folders included.
func hostingArchive(s *State, h *Hosting, a Action) error {
	if h.Stage != HostingArchiving {
		return errors.New("托管不在归档阶段")
	}
	if ids := liveHostingCases(s, h); len(ids) > 0 {
		v, err := requireBranch(s, h.VersionID)
		if err != nil {
			return fmt.Errorf("目标测试版本：%w", err)
		}
		ensure := func(parent, name string) string {
			for _, f := range s.Folders {
				if f.VersionID == v.ID && f.ParentID == parent && f.Name == name {
					return f.ID
				}
			}
			f := Folder{ID: ID(), VersionID: v.ID, ParentID: parent, Name: name, CreatedBy: a.Author, CreatedAt: now()}
			s.Folders = append(s.Folders, f)
			return f.ID
		}
		target := ensure(ensure("root", h.Product), h.ProductVersion)
		if err := importPendingCases(s, Action{VersionID: v.ID, TargetFolderID: target, SourceFolderID: "pending-root", CaseIDs: ids, Author: a.Author}); err != nil {
			return err
		}
	}
	h.Stage, h.Error, h.FailedAt, h.FinishedAt = HostingDone, "", "", now()
	return nil
}

// hostingRetry restarts a stopped hosting at the step it stopped in. Steps after the design need nothing
// from the person; estimating and designing need the request again (it carries test credentials, which are
// never stored), so the runner calls this only together with a fresh request.
func hostingRetry(h *Hosting) error {
	if h.Stage != HostingStopped {
		return errors.New("只有已停止的托管可以重试")
	}
	switch h.FailedAt {
	case HostingImporting:
		h.Stage = HostingDesigning
	case HostingReviewing, HostingArchiving:
		h.Stage = h.FailedAt
	case HostingDesigning:
		h.Stage, h.PlannerTaskID = HostingDesigning, ""
	default:
		h.Stage = HostingEstimating
	}
	h.Error, h.Continuable = "", false
	return nil
}
