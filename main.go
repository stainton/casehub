package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"casehub/internal/core"
	"casehub/internal/store"
	"casehub/internal/upstream"
)

//go:embed web/*
var assets embed.FS

type api struct{ service *core.Service }

func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (a *api) state(w http.ResponseWriter, r *http.Request) {
	s, e := a.service.State(r.Context())
	if e != nil {
		jsonOut(w, 500, map[string]string{"error": e.Error()})
		return
	}
	s.AgentSettings = nil
	jsonOut(w, 200, s)
}
func (a *api) action(w http.ResponseWriter, r *http.Request) {
	var in core.Action
	if e := json.NewDecoder(r.Body).Decode(&in); e != nil {
		jsonOut(w, 400, map[string]string{"error": "无效的 JSON 请求"})
		return
	}
	out, e := a.service.Apply(r.Context(), in)
	if e != nil {
		status := 400
		var conflict core.ConflictError
		if errors.As(e, &conflict) {
			status = 409
		}
		jsonOut(w, status, map[string]any{"error": e.Error(), "conflicts": out.Conflicts})
		return
	}
	out.State.AgentSettings = nil
	jsonOut(w, 200, out)
}

// service is one upstream auto-test service: its /api/<name>/ proxy plus the
// status endpoint the frontend checks before offering the feature.
type service struct {
	name    string
	proxy   http.Handler
	enabled bool
}

func routes(a *api, services ...service) http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/state", a.state)
	m.HandleFunc("GET /api/agent-settings/{id}", a.agentSettings)
	m.HandleFunc("PUT /api/agent-settings/{id}", a.agentSettings)
	m.HandleFunc("GET /api/agent-settings/{id}/runtime", a.agentRuntimeSettings)
	m.HandleFunc("PUT /api/agent-settings/{id}/runtime", a.agentRuntimeSettings)
	m.HandleFunc("GET /api/assets", a.assets)
	m.HandleFunc("POST /api/assets", a.assets)
	m.HandleFunc("GET /api/assets/{id}", a.assetByID)
	m.HandleFunc("DELETE /api/assets/{id}", a.assetByID)
	m.HandleFunc("POST /api/action", a.action)
	for _, s := range services {
		enabled := s.enabled
		m.HandleFunc("GET /api/"+s.name+"/status", func(w http.ResponseWriter, r *http.Request) {
			jsonOut(w, 200, map[string]bool{"enabled": enabled})
		})
		m.Handle("/api/"+s.name+"/", s.proxy)
	}
	fs := http.FileServerFS(assets)
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			b, _ := assets.ReadFile("web/index.html")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(b)
			return
		}
		fs.ServeHTTP(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Last-Event-ID")
		w.Header().Set("Access-Control-Expose-Headers", "Location")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		m.ServeHTTP(w, r)
	})
}
func main() {
	databaseURL := flag.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL connection URL (defaults to DATABASE_URL)")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	kind := storageKind(os.Getenv("CASEHUB_STORE"), *databaseURL)
	var repo core.Repository
	var closeDB func()
	switch kind {
	case "memory":
		repo = store.NewMemory()
	case "postgres":
		p, e := store.NewPostgres(ctx, *databaseURL)
		if e != nil {
			log.Fatal(e)
		}
		repo = p
		closeDB = func() { _ = p.Close() }
	default:
		kind = "file"
		repo = store.NewFile(env("CASEHUB_DATA", "data/casehub.json"))
	}
	if closeDB != nil {
		defer closeDB()
	}
	port := env("PORT", "8080")
	svc := core.NewService(repo)
	lookup := assetSource(svc)
	plannerProxy, plannerEnabled := upstream.New(env("CASEHUB_PLANNER_URL", "http://localhost:4501"), "planner", agentSettingsOf(svc, "planner"), lookup)
	generatorProxy, generatorEnabled := upstream.New(env("CASEHUB_GENERATOR_URL", "http://localhost:4502"), "generator", agentSettingsOf(svc, "generator"), lookup)
	log.Printf("CaseHub listening on :%s (store=%s, planner=%v, generator=%v)", port, kind, plannerEnabled, generatorEnabled)
	log.Fatal(http.ListenAndServe(":"+port, routes(&api{service: svc},
		service{name: "planner", proxy: plannerProxy, enabled: plannerEnabled},
		service{name: "generator", proxy: generatorProxy, enabled: generatorEnabled})))
}
func storageKind(kind, databaseURL string) string {
	if kind != "" {
		return kind
	}
	if databaseURL != "" {
		return "postgres"
	}
	return "file"
}
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
