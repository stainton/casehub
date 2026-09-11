package planner_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"casehub/internal/planner"
)

func TestDisabledWithoutBaseURL(t *testing.T) {
	handler, enabled := planner.New("")
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
}

func TestForwardsWithoutTokenAndRewritesPath(t *testing.T) {
	var gotPath, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	handler, enabled := planner.New(upstream.URL)
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	defer upstream.Close()

	handler, _ := planner.New(upstream.URL)
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
	handler, enabled := planner.New("http://127.0.0.1:1")
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
