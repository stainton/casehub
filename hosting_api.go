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

// plannerForHosting gives the hosting runner the planner the same way the frontend reaches it: the estimate
// through the planner proxy (agent settings and node choice included), tasks through the dispatcher.
type plannerForHosting struct {
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

func (p plannerForHosting) Estimate(ctx context.Context, body []byte) ([]byte, error) {
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

func (p plannerForHosting) Submit(ctx context.Context, body []byte) (tasks.Task, error) {
	return p.dispatcher.Submit(ctx, "planner", body)
}

func (p plannerForHosting) Get(ctx context.Context, id string) (tasks.Task, error) {
	t, err := p.dispatcher.Get(ctx, "planner", id)
	var h *dispatch.HTTPError
	if errors.As(err, &h) && h.Status == http.StatusNotFound {
		return t, hosting.ErrTaskGone
	}
	return t, err
}

func (p plannerForHosting) Cancel(ctx context.Context, id string) error {
	_, err := p.dispatcher.Cancel(ctx, "planner", id)
	return err
}

func (p plannerForHosting) MarkHandled(ctx context.Context, id string) {
	handled := true
	_, _ = p.dispatcher.Update(ctx, "planner", id, nil, &handled)
}

func (a *api) hostingRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/hostings", func(w http.ResponseWriter, r *http.Request) {
		st, err := a.service.State(r.Context())
		if err != nil {
			jsonOut(w, 500, map[string]string{"error": err.Error()})
			return
		}
		jsonOut(w, 200, map[string]any{"enabled": a.hosting != nil, "hostings": st.Hostings})
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
			hosting.Request
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&in) != nil {
			refuse(w, errors.New("无效的 JSON 请求"))
			return
		}
		h, err := a.hosting.Begin(r.Context(), core.Hosting{DocID: in.DocID, VersionID: in.VersionID, Product: in.Product, ProductVersion: in.ProductVersion}, in.Request, in.Author)
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
