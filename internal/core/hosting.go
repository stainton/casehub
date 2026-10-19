package core

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Hosting is one "AI 托管" run of a requirement: estimate → design → review area → passed → filed into a test
// version under 全部用例 / Product / ProductVersion / <requirement>. The hosting runner (internal/hosting) drives
// it on the server, one action per step, so it needs no browser and survives a CaseHub restart.
//
// A hosting started from 用例管理 (Source "cases") has no design part: it takes cases already in a test version
// (CaseIDs, picked one by one or as a folder, Label naming them) straight to script generation and the run.
type Hosting struct {
	ID, DocID, VersionID    string
	Source, Label           string // Source: "" (a requirement) or HostingFromCases
	Product, ProductVersion string
	Stage                   string // estimating, designing, reviewing, archiving, generating, running, done, stopped
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

	// Optional stages after filing: generate a Playwright script for each filed case (generator), then run
	// every generated script once (executor) and record its result as a test record. BaseURL is the system
	// under test the scripts run against (no secret, so it is kept; the test account is not).
	Generate, Execute bool
	BaseURL           string
	GenTaskID         string
	Generated         int               // scripts saved by the generator run
	Blocked           int               // cases the generator could not script
	ExecTasks         map[string]string `json:",omitempty"` // case ID -> executor task ID
	Results           map[string]string `json:",omitempty"` // case ID -> passed / failed / error (the run itself failed)
}

// GeneratorResult is the part of a generator job's result a hosting saves.
type GeneratorResult struct {
	Scripts []struct {
		CaseID        string              `json:"caseId"`
		FileName      string              `json:"fileName"`
		Language      string              `json:"language"`
		Code          string              `json:"code"`
		Status        string              `json:"status"`
		Summary       string              `json:"summary"`
		Deviations    []HostingLimitation `json:"deviations"`
		MissingInputs []string            `json:"missingInputs"`
	} `json:"scripts"`
	ExplorationRecords map[string]string `json:"explorationRecords"`
	ExplorationNotes   string            `json:"explorationNotes"`
	ProductExperience  *struct {
		Origin string `json:"origin"`
		Notes  string `json:"notes"`
	} `json:"productExperience"`
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
	HostingGenerating = "generating"
	HostingRunning    = "running"
	HostingDone       = "done"
	HostingStopped    = "stopped"
	// HostingFromCases is the Source of a hosting started from cases in a test version.
	HostingFromCases = "cases"
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
	case "hostingGenSubmitted":
		if h.Stage != HostingGenerating {
			return true, errors.New("托管不在脚本生成阶段")
		}
		h.GenTaskID = a.TaskID
	case "hostingScripts":
		err = hostingScripts(s, h, a)
	case "hostingExecSubmitted":
		if h.Stage != HostingRunning || a.CaseID == "" || a.TaskID == "" {
			return true, errors.New("托管不在自动化执行阶段")
		}
		if h.ExecTasks == nil {
			h.ExecTasks = map[string]string{}
		}
		h.ExecTasks[a.CaseID] = a.TaskID
	case "hostingRecord":
		err = hostingRecord(s, h, a)
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
		err = hostingRetry(h, a.Force)
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
	if in.Source == HostingFromCases {
		return startCaseHosting(s, a)
	}
	if in.Source != "" {
		return errors.New("未知的托管来源")
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
	if (in.Generate || in.Execute) && strings.TrimSpace(in.BaseURL) == "" {
		return errors.New("生成脚本和自动化执行需要被测系统 URL")
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
		Stage: HostingEstimating, ReqFolder: doc.Title, Generate: in.Generate || in.Execute, Execute: in.Execute, BaseURL: strings.TrimSpace(in.BaseURL),
		CreatedBy: a.Author, CreatedAt: t, UpdatedAt: t})
	return nil
}

// startCaseHosting hosts cases already in a test version: generate a script for each (and run them once when
// asked). A case can be in only one running hosting; a new run replaces the finished or stopped ones it covers.
func startCaseHosting(s *State, a Action) error {
	in := a.Hosting
	if _, err := requireBranch(s, in.VersionID); err != nil {
		return err
	}
	if strings.TrimSpace(in.BaseURL) == "" {
		return errors.New("生成脚本和自动化执行需要被测系统 URL")
	}
	// Only cases with steps can be scripted; the others are left out (a folder may hold some).
	var ids []string
	picked := map[string]bool{}
	for _, id := range in.CaseIDs {
		c, _ := caseAt(s, in.VersionID, id)
		if c == nil {
			return fmt.Errorf("用例 %s 不存在", id)
		}
		if !picked[id] && strings.TrimSpace(c.Steps) != "" {
			picked[id] = true
			ids = append(ids, id)
		}
	}
	if len(in.CaseIDs) == 0 {
		return errors.New("请选择要托管的用例")
	}
	if len(ids) == 0 {
		return errors.New("选中的用例都没有执行步骤，无法生成脚本")
	}
	busy := 0
	for _, h := range s.Hostings {
		if h.VersionID == in.VersionID && HostingActive(h) {
			for _, id := range h.CaseIDs {
				if picked[id] {
					busy++
				}
			}
		}
	}
	if busy > 0 {
		return fmt.Errorf("选中的用例中有 %d 条正在被其他托管处理", busy)
	}
	kept := s.Hostings[:0]
	for _, h := range s.Hostings {
		covered := h.Source == HostingFromCases && h.VersionID == in.VersionID
		for _, id := range h.CaseIDs {
			covered = covered && picked[id]
		}
		if !covered {
			kept = append(kept, h)
		}
	}
	label := strings.TrimSpace(in.Label)
	if label == "" {
		label = fmt.Sprintf("%d 条用例", len(ids))
	}
	t := now()
	s.Hostings = append(kept, Hosting{ID: ID(), VersionID: in.VersionID, Source: HostingFromCases, Label: label, CaseIDs: ids,
		Stage: HostingGenerating, Generate: true, Execute: in.Execute, BaseURL: strings.TrimSpace(in.BaseURL),
		CreatedBy: a.Author, CreatedAt: t, UpdatedAt: t})
	return nil
}

// HostingDoc is the requirement a hosted case belongs to: the hosting's own, or — for hosted cases — the one
// whose code the case ID carries (TC-<code>-…), as the automation page matches them. It may be nil.
func HostingDoc(s *State, h Hosting, caseID string) *ReqDoc {
	if h.DocID != "" {
		doc, _ := reqDocAt(s, h.DocID)
		return doc
	}
	if m := caseCodeRE.FindStringSubmatch(caseID); m != nil {
		for i := range s.ReqDocs {
			if s.ReqDocs[i].Code == m[1] {
				return &s.ReqDocs[i]
			}
		}
	}
	return nil
}

var caseCodeRE = regexp.MustCompile(`^TC-([A-Z][A-Z0-9]{1,11})-`)

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
		if err := setReqDocCode(s, Action{DocID: doc.ID, Code: freeReqCode(s, doc.ID, in.Code), Author: a.Author}); err != nil {
			return err
		}
	}
	if doc.Code == "" {
		return errors.New("没有得到可用的需求缩写")
	}
	h.Stage, h.Code, h.CaseCount, h.Rationale, h.Note, h.PlannerTaskID = HostingDesigning, doc.Code, in.CaseCount, in.Rationale, in.Note, ""
	return nil
}

// freeReqCode is the suggested code, or — when another requirement already has it — the first free
// variant with a digit appended (LOGIN → LOGIN2 …), still within the 12-character limit. Nobody is there to
// pick another code during a hosting, so a clash must not stop it.
func freeReqCode(s *State, docID, code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	taken := func(c string) bool {
		for _, d := range s.ReqDocs {
			if d.ID != docID && d.Code == c {
				return true
			}
		}
		return false
	}
	if !codeRE.MatchString(code) || !taken(code) {
		return code
	}
	for n := 2; n < 1000; n++ {
		suffix := fmt.Sprint(n)
		base := code
		if len(base)+len(suffix) > 12 {
			base = base[:12-len(suffix)]
		}
		if c := base + suffix; !taken(c) {
			return c
		}
	}
	return code
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
	h.Error, h.FailedAt = "", ""
	if h.Generate && len(h.CaseIDs) > 0 {
		h.Stage = HostingGenerating
	} else {
		h.Stage, h.FinishedAt = HostingDone, now()
	}
	return nil
}

// hostingScripts saves a finished generator run the way the automation page does: one script per case
// (blocked ones too, with what they lack), the run's exploration notes and product experience, and every
// expectation the app failed as a defect — then moves on to running the scripts, or finishes.
func hostingScripts(s *State, h *Hosting, a Action) error {
	if h.Stage != HostingGenerating {
		return errors.New("托管不在脚本生成阶段")
	}
	r := a.Generator
	if r == nil {
		return errors.New("缺少脚本生成结果")
	}
	if h.DocID != "" {
		if doc, _ := reqDocAt(s, h.DocID); doc == nil {
			return errors.New("需求文档不存在")
		}
	}
	// Exploration notes go to each requirement the scripted cases belong to; the run's general notes only
	// when there is exactly one.
	var docIDs []string
	for _, id := range h.CaseIDs {
		if doc := HostingDoc(s, *h, id); doc != nil && !slices.Contains(docIDs, doc.ID) {
			docIDs = append(docIDs, doc.ID)
		}
	}
	for _, docID := range docIDs {
		notes := r.ExplorationRecords[docID]
		if notes == "" && len(docIDs) == 1 {
			notes = r.ExplorationNotes
		}
		if strings.TrimSpace(notes) != "" {
			if err := saveReqExploration(s, Action{DocID: docID, ExplorationNotes: notes}); err != nil {
				return err
			}
		}
	}
	if p := r.ProductExperience; p != nil && p.Origin != "" && strings.TrimSpace(p.Notes) != "" {
		_ = recordProductExploration(s, Action{Origin: p.Origin, ExplorationNotes: p.Notes})
	}
	generated, blocked := 0, 0
	var reports []IssueReport
	for _, sc := range r.Scripts {
		var devs []RiskNote
		for _, d := range sc.Deviations {
			devs = append(devs, RiskNote{Risk: d.Risk, Summary: d.Summary})
		}
		if err := saveScript(s, Action{VersionID: h.VersionID, CaseID: sc.CaseID, ScriptFileName: sc.FileName, ScriptLanguage: sc.Language, ScriptCode: sc.Code,
			ScriptStatus: sc.Status, ScriptSummary: sc.Summary, ScriptJobID: h.GenTaskID, ScriptDeviations: devs, ScriptMissingInputs: sc.MissingInputs, Author: a.Author}); err != nil {
			return fmt.Errorf("保存 %s 的脚本：%w", sc.CaseID, err)
		}
		if sc.Status == "blocked" {
			blocked++
			continue
		}
		generated++
		title := ""
		if c, _ := caseAt(s, h.VersionID, sc.CaseID); c != nil {
			title = c.Title
		}
		doc := HostingDoc(s, *h, sc.CaseID)
		if doc == nil {
			continue // a defect is filed against a requirement; a case with none keeps it on its script only
		}
		for _, d := range sc.Deviations {
			label := riskLabels[d.Risk]
			if label == "" {
				label = "未评级"
			}
			reports = append(reports, IssueReport{RequirementID: doc.ID, Title: "[" + label + "] " + sc.CaseID + " " + d.Summary,
				Description: "**用例**：" + sc.CaseID + " " + title + "\n\n**缺陷**：" + d.Summary + "\n\n**风险**：" + label + "\n\n来源：脚本生成发现的预期与实际不符，脚本已按预期断言，运行会失败。生成任务 " + h.GenTaskID + "（AI 托管）",
				Source:      "generator:" + sc.CaseID + ":" + d.Summary})
		}
	}
	if err := reportIssues(s, Action{Reports: reports, Author: a.Author}); err != nil {
		return err
	}
	h.Generated, h.Blocked, h.ExecTasks, h.Results = generated, blocked, nil, nil
	if h.Execute && generated > 0 {
		h.Stage = HostingRunning
	} else {
		h.Stage, h.FinishedAt = HostingDone, now()
	}
	return nil
}

// HostingRunnable are the cases whose scripts a hosting runs: the ones its generator run scripted.
func HostingRunnable(s State, h Hosting) []Script {
	var out []Script
	for _, id := range h.CaseIDs {
		if sc, _ := scriptAt(&s, h.VersionID, id); sc != nil && sc.Status == "generated" && sc.Code != "" && sc.JobID == h.GenTaskID {
			out = append(out, *sc)
		}
	}
	return out
}

// hostingRecord files one script run: a submitted test record (passed / failed) like a manual run's, or —
// when the run itself failed — only a note on the hosting. The hosting is done once every runnable case has
// an outcome.
func hostingRecord(s *State, h *Hosting, a Action) error {
	if h.Stage != HostingRunning {
		return errors.New("托管不在自动化执行阶段")
	}
	// With no CaseID it only checks whether the run is complete (scripts may have been deleted meanwhile).
	switch {
	case a.CaseID == "", a.Result == "error":
	case a.Result == "passed" || a.Result == "failed":
		if _, err := recordCase(s, Action{Type: "submitRecord", VersionID: h.VersionID, CaseID: a.CaseID, TaskID: a.TaskID, Result: a.Result, Note: a.Note, Submitted: true, Force: true, Author: a.Author}); err != nil {
			return err
		}
	default:
		return errors.New("执行结果必须是 passed、failed 或 error")
	}
	if a.CaseID != "" {
		if h.Results == nil {
			h.Results = map[string]string{}
		}
		h.Results[a.CaseID] = a.Result
	}
	errored := 0
	for _, sc := range HostingRunnable(*s, *h) {
		switch h.Results[sc.CaseID] {
		case "":
			return nil
		case "error":
			errored++
		}
	}
	// A run that itself failed (browser crash, node lost) says nothing about the case: stop so a person can
	// re-run just those scripts. Passed and failed assertions are already recorded.
	if errored > 0 {
		h.FailedAt, h.Stage, h.Error = HostingRunning, HostingStopped, fmt.Sprintf("%d 条用例的脚本执行出错（执行任务本身失败，未产生测试记录），可「重试这一步」重新执行它们", errored)
		return nil
	}
	h.Stage, h.FinishedAt = HostingDone, now()
	return nil
}

// hostingRetry restarts a stopped hosting at the step it stopped in. Steps after the design need nothing
// from the person; estimating and designing need the request again (it carries test credentials, which are
// never stored), so the runner calls this only together with a fresh request.
//
// clearTask (Force on the action) drops a failed generator task so the runner submits a new one.
func hostingRetry(h *Hosting, clearTask bool) error {
	if h.Stage != HostingStopped {
		return errors.New("只有已停止的托管可以重试")
	}
	switch h.FailedAt {
	case HostingImporting:
		h.Stage = HostingDesigning
	case HostingReviewing, HostingArchiving:
		h.Stage = h.FailedAt
	case HostingGenerating:
		h.Stage = HostingGenerating // the runner re-reads a finished generator run, or submits a new one
		if clearTask {
			h.GenTaskID = ""
		}
	case HostingRunning:
		// Run again only what has no outcome yet or whose run itself failed; recorded passes/fails stay.
		h.Stage = HostingRunning
		for id, r := range h.Results {
			if r == "error" {
				delete(h.Results, id)
				delete(h.ExecTasks, id)
			}
		}
	case HostingDesigning:
		h.Stage, h.PlannerTaskID = HostingDesigning, ""
	default:
		if h.Source == HostingFromCases { // nothing before the generation to go back to
			h.Stage, h.GenTaskID = HostingGenerating, ""
			break
		}
		h.Stage = HostingEstimating
	}
	h.Error, h.Continuable = "", false
	return nil
}
