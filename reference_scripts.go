package main

import (
	"context"
	"encoding/json"
	"log"
	"regexp"
	"sort"
	"strings"

	"casehub/internal/core"
)

// Reference scripts let the generator reuse what earlier generations already verified (login, navigation,
// locators, fixtures) instead of exploring the same pages again for every case: CaseHub already stores each
// case's spec, so it hands the relevant ones to the node with the request (the node stays stateless).
// Only scripts of the same requirement are sent (case IDs are TC-<requirement>-<module>-…), same module
// first; the generator trims further per case. Off unless CASEHUB_REFERENCE_SCRIPTS=1, because a generator
// older than the field rejects requests that carry it: deploy auto-test first, then turn this on.
const (
	maxReferenceScripts = 20
	maxReferenceChars   = 240000
	// The generator's own per-field limits (contract.mjs); a script edited by hand past them is not sent.
	maxReferenceCode  = 120000
	maxReferenceTitle = 500
)

var caseIDPrefix = regexp.MustCompile(`^TC-([A-Z0-9]+)-([A-Z0-9]+)-`)

type referenceScript struct {
	CaseID string `json:"caseId"`
	Title  string `json:"title,omitempty"`
	Code   string `json:"code"`
}

func referenceScriptsEnricher(svc *core.Service) func(context.Context, map[string]json.RawMessage) {
	return func(ctx context.Context, body map[string]json.RawMessage) {
		state, err := svc.State(ctx)
		if err != nil {
			log.Printf("reference scripts skipped: %v", err)
			return
		}
		addReferenceScripts(body, state)
	}
}

// addReferenceScripts sets context.referenceScripts on a generator request; a request that already carries
// them, has no recognisable case IDs or finds nothing relevant is left exactly as it was.
func addReferenceScripts(body map[string]json.RawMessage, state core.State) {
	var cases []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body["cases"], &cases) != nil || len(cases) == 0 {
		return
	}
	context := map[string]json.RawMessage{}
	if raw, ok := body["context"]; ok && json.Unmarshal(raw, &context) != nil {
		return
	}
	if _, ok := context["referenceScripts"]; ok {
		return
	}
	batch, requirements, modules := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, c := range cases {
		batch[c.ID] = true
		if m := caseIDPrefix.FindStringSubmatch(c.ID); m != nil {
			requirements[m[1]] = true
			modules[m[1]+"-"+m[2]] = true
		}
	}
	archived := map[string]bool{}
	for _, v := range state.Versions {
		archived[v.ID] = v.ScriptsArchived
	}
	// A case's script is copied between versions; keep its newest copy. A script with deviations is one the
	// generator returned unverified (a timeout recovery) and is not something to copy.
	latest := map[string]core.Script{}
	for _, s := range state.Scripts {
		m := caseIDPrefix.FindStringSubmatch(s.CaseID)
		if s.Status != "generated" || len(s.Deviations) > 0 || strings.TrimSpace(s.Code) == "" || len(s.Code) > maxReferenceCode || batch[s.CaseID] || archived[s.VersionID] || m == nil || !requirements[m[1]] {
			continue
		}
		if old, ok := latest[s.CaseID]; !ok || s.UpdatedAt.After(old.UpdatedAt) {
			latest[s.CaseID] = s
		}
	}
	picked := make([]core.Script, 0, len(latest))
	for _, s := range latest {
		picked = append(picked, s)
	}
	sameModule := func(s core.Script) bool {
		m := caseIDPrefix.FindStringSubmatch(s.CaseID)
		return modules[m[1]+"-"+m[2]]
	}
	sort.Slice(picked, func(i, j int) bool {
		if a, b := sameModule(picked[i]), sameModule(picked[j]); a != b {
			return a
		}
		if !picked[i].UpdatedAt.Equal(picked[j].UpdatedAt) {
			return picked[i].UpdatedAt.After(picked[j].UpdatedAt)
		}
		return picked[i].CaseID < picked[j].CaseID
	})
	refs, chars := []referenceScript{}, 0
	for _, s := range picked {
		if len(refs) == maxReferenceScripts {
			break
		}
		if chars+len(s.Code) > maxReferenceChars {
			continue
		}
		title := []rune(s.Title)
		if len(title) > maxReferenceTitle {
			title = title[:maxReferenceTitle]
		}
		refs = append(refs, referenceScript{CaseID: s.CaseID, Title: string(title), Code: s.Code})
		chars += len(s.Code)
	}
	if len(refs) == 0 {
		return
	}
	encoded, err := json.Marshal(refs)
	if err != nil {
		return
	}
	context["referenceScripts"] = encoded
	if encoded, err = json.Marshal(context); err == nil {
		body["context"] = encoded
	}
}
