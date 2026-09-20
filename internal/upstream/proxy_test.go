package upstream_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"casehub/internal/upstream"
)

func TestDisabledWithoutBaseURL(t *testing.T) {
	handler, enabled := upstream.New("", "planner", nil)
	if enabled {
		t.Fatal("expected disabled proxy")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/planner/jobs/abc", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	var out map[string]map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["error"]["code"] != "PLANNER_DISABLED" {
		t.Fatalf("unexpected error body: %s", w.Body.String())
	}
	handler, enabled = upstream.New("", "generator", nil)
	if enabled {
		t.Fatal("expected disabled proxy")
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/generator/jobs/abc", nil))
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusServiceUnavailable || out["error"]["code"] != "GENERATOR_DISABLED" {
		t.Fatalf("generator proxy must report its own disabled code: status=%d body=%s", w.Code, w.Body.String())
	}
}

// Each service is reached under its own prefix and forwarded to its own /v1 path.
func TestGeneratorPrefixIsForwardedToTheGeneratorService(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	handler, enabled := upstream.New(server.URL, "generator", nil)
	if !enabled {
		t.Fatal("expected enabled proxy")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/generator/jobs", nil))
	if gotPath != "/v1/generator/jobs" {
		t.Fatalf("upstream path = %q, want /v1/generator/jobs", gotPath)
	}
}

func TestForwardsWithoutTokenAndRewritesPath(t *testing.T) {
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	handler, enabled := upstream.New(server.URL, "planner", nil)
	if !enabled {
		t.Fatal("expected enabled proxy")
	}
	req := httptest.NewRequest(http.MethodPost, "/api/planner/jobs", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if gotPath != "/v1/planner/jobs" {
		t.Fatalf("upstream path = %q, want /v1/planner/jobs", gotPath)
	}
	if gotAuth != "" {
		t.Fatalf("unexpected upstream Authorization = %q", gotAuth)
	}
	if w.Code != http.StatusCreated || w.Body.String() != `{"ok":true}` {
		t.Fatalf("response not proxied through: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestStreamsSSEWithoutBuffering(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/planner/jobs/job-1/events" {
			http.NotFound(w, r)
			return
		}
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "event: snapshot\ndata: {\"status\":\"running\"}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprintf(w, "id: 1\nevent: progress\ndata: {\"message\":\"exploring\"}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	handler, _ := upstream.New(server.URL, "planner", nil)
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	resp, err := http.Get(proxyServer.URL + "/api/planner/jobs/job-1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := bufio.NewReader(resp.Body)
	line, err := body.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if line != "event: snapshot\n" {
		t.Fatalf("first flushed line = %q, want the snapshot event (proves it wasn't buffered until close)", line)
	}
	_, _ = io.Copy(io.Discard, body)
}

func TestUnreachableUpstreamReturnsStructuredError(t *testing.T) {
	handler, enabled := upstream.New("http://127.0.0.1:1", "planner", nil)
	if !enabled {
		t.Fatal("expected enabled")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/planner/jobs/does-not-matter", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 from an unreachable upstream", w.Code)
	}
	var out map[string]map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["error"]["code"] != "PLANNER_UNREACHABLE" {
		t.Fatalf("unexpected error body: %s", w.Body.String())
	}
}

// Every request that starts work carries the configuration CaseHub stores for that agent, so the
// service can overwrite its own setting.json instead of keeping a copy in step with CaseHub.
func TestPostRequestsCarryTheStoredAgentConfiguration(t *testing.T) {
	var got []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		if r.ContentLength != int64(len(got)) {
			t.Errorf("Content-Length = %d, forwarded body is %d bytes", r.ContentLength, len(got))
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	settings := func(context.Context) (string, string, error) { return "4", `{"model":"stored"}`, nil }
	handler, _ := upstream.New(server.URL, "planner", settings)
	post := func(body string) *httptest.ResponseRecorder {
		got = nil
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/planner/jobs", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(w, r)
		return w
	}
	post(`{"requirements":[]}`)
	var out struct {
		Requirements  []json.RawMessage `json:"requirements"`
		AgentSettings struct{ Revision, Content string }
	}
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatal(err)
	}
	if out.Requirements == nil || out.AgentSettings.Revision != "4" || out.AgentSettings.Content != `{"model":"stored"}` {
		t.Fatalf("configuration not injected beside the request: %s", got)
	}
	// A body the service must reject itself is forwarded byte for byte.
	post(`not json`)
	if string(got) != "not json" {
		t.Fatalf("unparseable body was rewritten: %s", got)
	}
	// Nothing stored yet: the agent keeps the configuration its image shipped.
	handler, _ = upstream.New(server.URL, "planner", func(context.Context) (string, string, error) { return "0", "", nil })
	post(`{"requirements":[]}`)
	if string(got) != `{"requirements":[]}` {
		t.Fatalf("request modified without a stored configuration: %s", got)
	}
	// Streaming and reading stay untouched; only requests that start work are rewritten.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/planner/jobs/abc", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET not proxied: %d", w.Code)
	}
	// A configuration CaseHub cannot read fails the request instead of silently running on the agent's own.
	handler, _ = upstream.New(server.URL, "planner", func(context.Context) (string, string, error) {
		return "", "", errors.New("database is down")
	})
	w = post(`{"requirements":[]}`)
	var failure map[string]map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &failure)
	if w.Code != http.StatusInternalServerError || failure["error"]["code"] != "PLANNER_SETTINGS_UNAVAILABLE" {
		t.Fatalf("unreadable configuration must fail the request: status=%d body=%s", w.Code, w.Body.String())
	}
}
