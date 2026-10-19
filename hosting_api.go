package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"casehub/internal/core"
	"casehub/internal/dispatch"
	"casehub/internal/hosting"
	"casehub/internal/tasks"
)

// servicesForHosting gives the hosting runner the auto-test services the same way the frontend reaches them:
// the planner's estimate through its proxy (agent settings and node choice included), tasks of every kind
// through the dispatcher.
type servicesForHosting struct {
	proxy      http.Handler
	dispatcher *dispatch.Dispatcher
}

// bufferedResponse collects an in-process handler's reply.
type bufferedResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (b *bufferedResponse) Header() http.Header         { return b.header }
func (b *bufferedResponse) Write(p []byte) (int, error) { return b.body.Write(p) }
func (b *bufferedResponse) WriteHeader(status int)      { b.status = status }

func (p servicesForHosting) Estimate(ctx context.Context, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/planner/estimate", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res := &bufferedResponse{header: http.Header{}, status: http.StatusOK}
	p.proxy.ServeHTTP(res, req)
	if res.status >= 300 {
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		if json.Unmarshal(res.body.Bytes(), &e) == nil && e.Error.Message != "" {
			return nil, errors.New(e.Error.Message)
		}
		return nil, errors.New(http.StatusText(res.status))
	}
	return res.body.Bytes(), nil
}

func (p servicesForHosting) Enabled(kind string) bool { return p.dispatcher.Enabled(kind) }

func (p servicesForHosting) Submit(ctx context.Context, kind string, body []byte) (tasks.Task, error) {
	return p.dispatcher.Submit(ctx, kind, body)
}

func (p servicesForHosting) Get(ctx context.Context, kind, id string) (tasks.Task, error) {
	t, err := p.dispatcher.Get(ctx, kind, id)
	var h *dispatch.HTTPError
	if errors.As(err, &h) && h.Status == http.StatusNotFound {
		return t, hosting.ErrTaskGone
	}
	return t, err
}

func (p servicesForHosting) Cancel(ctx context.Context, kind, id string) error {
	_, err := p.dispatcher.Cancel(ctx, kind, id)
	return err
}

func (p servicesForHosting) MarkHandled(ctx context.Context, kind, id string) {
	handled := true
	_, _ = p.dispatcher.Update(ctx, kind, id, nil, &handled)
}

func (a *api) hostingRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/hostings", func(w http.ResponseWriter, r *http.Request) {
		st, err := a.service.State(r.Context())
		if err != nil {
			jsonOut(w, 500, map[string]string{"error": err.Error()})
			return
		}
		// enabled: a requirement can be hosted (planner); cases: cases can be hosted from 用例管理 (generator).
		jsonOut(w, 200, map[string]any{"enabled": a.hosting != nil && a.dispatcher.Enabled("planner"), "cases": a.hosting != nil && a.dispatcher.Enabled("generator"), "hostings": st.Hostings})
	})
	if a.hosting == nil {
		return
	}
	// Errors use the services' {error: {code, message}} shape, which the frontend's serviceRequest reads.
	refuse := func(w http.ResponseWriter, err error) {
		jsonOut(w, 400, map[string]any{"error": map[string]string{"code": "HOSTING_REJECTED", "message": err.Error()}})
	}
	reply := func(w http.ResponseWriter, h core.Hosting, err error) {
		if err != nil {
			refuse(w, err)
			return
		}
		jsonOut(w, 202, h)
	}
	m.HandleFunc("POST /api/hostings", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			DocID, VersionID, Product, ProductVersion, Author string
			// Source "cases" hosts CaseIDs of VersionID (Label names them) from script generation on.
			Source, Label string
			CaseIDs       []string
			// Generate / Execute add script generation and one automated run of each script; BaseURL is the
			// system under test the scripts run against.
			// (Named apart from Request.Generate, the generator body: JSON keys match case-insensitively.)
			Generate bool `json:"withScripts"`
			Execute  bool `json:"withRun"`
			BaseURL  string
			hosting.Request
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&in) != nil {
			refuse(w, errors.New("无效的 JSON 请求"))
			return
		}
		h, err := a.hosting.Begin(r.Context(), core.Hosting{DocID: in.DocID, VersionID: in.VersionID, Product: in.Product, ProductVersion: in.ProductVersion, Source: in.Source, Label: in.Label, CaseIDs: in.CaseIDs, Generate: in.Generate, Execute: in.Execute, BaseURL: in.BaseURL}, in.Request, in.Author)
		reply(w, h, err)
	})
	m.HandleFunc("POST /api/hostings/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		var in *hosting.Request
		if r.ContentLength != 0 {
			in = &hosting.Request{}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(in) != nil {
				refuse(w, errors.New("无效的 JSON 请求"))
				return
			}
		}
		h, err := a.hosting.Retry(r.Context(), r.PathValue("id"), in)
		reply(w, h, err)
	})
	m.HandleFunc("DELETE /api/hostings/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := a.hosting.Abandon(r.Context(), r.PathValue("id")); err != nil {
			refuse(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
