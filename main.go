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
	"path/filepath"
	"time"

	"casehub/internal/core"
	"casehub/internal/dispatch"
	"casehub/internal/hosting"
	"casehub/internal/store"
	"casehub/internal/tasks"
	"casehub/internal/upstream"
)

//go:embed web/*
var assets embed.FS

type api struct {
	service *core.Service
	// dispatcher owns the planner/generator/healer/executor tasks; nil in tests that only exercise the state API.
	dispatcher *dispatch.Dispatcher
	// hosting runs AI 托管; nil when the planner is not configured.
	hosting *hosting.Runner
}

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
	m.HandleFunc("GET /api/record-images/{id}", a.recordImageByID)
	m.HandleFunc("POST /api/action", a.action)
	a.hostingRoutes(m)
	for _, s := range services {
		enabled := s.enabled
		m.HandleFunc("GET /api/"+s.name+"/status", func(w http.ResponseWriter, r *http.Request) {
			jsonOut(w, 200, map[string]bool{"enabled": enabled})
		})
		m.Handle("/api/"+s.name+"/", s.proxy)
		// Tasks are dispatched to an idle node rather than proxied to a fixed one; everything else under the
		// service (synchronous calls, asset pushes) is still proxied, to any node of its pool.
		if a.dispatcher != nil && a.dispatcher.Enabled(s.name) {
			jobs := a.dispatcher.Jobs(s.name)
			m.Handle("/api/"+s.name+"/jobs", jobs)
			m.Handle("/api/"+s.name+"/jobs/", jobs)
		}
	}
	if a.dispatcher != nil {
		m.Handle("GET /api/tasks", a.dispatcher.Tasks())
		m.Handle("GET /metrics", a.dispatcher.Metrics())
		m.Handle("GET /api/queue", a.dispatcher.Queue())
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
	var taskStore tasks.Store
	var closeDB func()
	switch kind {
	case "memory":
		repo = store.NewMemory()
		taskStore = tasks.NewMemory()
	case "postgres":
		p, e := store.NewPostgres(ctx, *databaseURL)
		if e != nil {
			log.Fatal(e)
		}
		repo = p
		closeDB = func() { _ = p.Close() }
		if taskStore, e = tasks.NewPostgres(ctx, p.DB()); e != nil {
			log.Fatal(e)
		}
	default:
		kind = "file"
		dataFile := env("CASEHUB_DATA", "data/casehub.json")
		repo = store.NewFile(dataFile)
		// One JSON file per task beside the state file (data/tasks/), so a task update never rewrites the state.
		local, e := tasks.NewFile(filepath.Join(filepath.Dir(dataFile), "tasks"))
		if e != nil {
			log.Fatal(e)
		}
		taskStore = local
	}
	if closeDB != nil {
		defer closeDB()
	}
	port := env("PORT", "8080")
	svc := core.NewService(repo)
	lookup := assetSource(svc)
	// Planner and generator are separate auto-test services (pods); the healer
	// runs inside the generator. CASEHUB_AUTOMATION_URL points all three at the
	// combined single-process automation service instead.
	// Each address may be one node, a comma-separated list, or dns+http://name:port for every pod behind a
	// headless Service (see dispatch.Pool).
	plannerURL, generatorURL := serviceURLs(os.Getenv)
	healerURL := env("CASEHUB_HEALER_URL", generatorURL)
	pool := func(name, spec string) *dispatch.Pool {
		p, err := dispatch.ParsePool(spec)
		if err != nil {
			log.Fatalf("%s: %v", name, err)
		}
		return p
	}
	plannerPool, generatorPool, healerPool := pool("planner", plannerURL), pool("generator", generatorURL), pool("healer", healerURL)
	executorPool := pool("executor", env("CASEHUB_EXECUTOR_URL", "http://localhost:4504"))
	generalAgentPool := pool("general-agent", env("CASEHUB_GENERAL_AGENT_URL", "http://localhost:4503"))
	proxy := func(p *dispatch.Pool, name string, settings upstream.Settings, assets *upstream.Assets) (http.Handler, bool) {
		if p == nil {
			return upstream.NewPooled(nil, name, settings, assets)
		}
		return upstream.NewPooled(p.Nodes, name, settings, assets)
	}
	plannerProxy, plannerEnabled := proxy(plannerPool, "planner", agentSettingsOf(svc, "planner"), lookup)
	generatorProxy, generatorEnabled := proxy(generatorPool, "generator", agentSettingsOf(svc, "generator"), lookup)
	healerProxy, healerEnabled := proxy(healerPool, "healer", agentSettingsOf(svc, "healer"), lookup)
	executorProxy, executorEnabled := proxy(executorPool, "executor", agentSettingsOf(svc, "planner"), nil)
	generalAgentProxy, generalAgentEnabled := proxy(generalAgentPool, "general-agent", agentSettingsOf(svc, "general-agent"), nil)
	dispatcher := dispatch.New(taskStore, []*dispatch.Kind{
		{Name: "planner", Pool: plannerPool, Settings: agentSettingsOf(svc, "planner"), Assets: lookup, Continuable: true},
		{Name: "generator", Pool: generatorPool, Settings: agentSettingsOf(svc, "generator"), Assets: lookup},
		{Name: "healer", Pool: healerPool, Settings: agentSettingsOf(svc, "healer"), Assets: lookup},
		{Name: "executor", Pool: executorPool, Settings: agentSettingsOf(svc, "planner")},
	}, dispatchConfig())
	if err := dispatcher.Start(context.Background()); err != nil {
		log.Fatal(err)
	}
	var hostingRunner *hosting.Runner
	if plannerEnabled && dispatcher.Enabled("planner") {
		hostingRunner = hosting.New(svc, plannerForHosting{proxy: plannerProxy, dispatcher: dispatcher})
		if err := hostingRunner.Start(context.Background()); err != nil {
			log.Fatal(err)
		}
	}
	log.Printf("CaseHub listening on :%s (store=%s, planner=%v, generator=%v, healer=%v, executor=%v, general-agent=%v; nodes: %s)", port, kind, plannerEnabled, generatorEnabled, healerEnabled, executorEnabled, generalAgentEnabled, dispatcher)
	log.Fatal(http.ListenAndServe(":"+port, routes(&api{service: svc, dispatcher: dispatcher, hosting: hostingRunner},
		service{name: "planner", proxy: plannerProxy, enabled: plannerEnabled},
		service{name: "generator", proxy: generatorProxy, enabled: generatorEnabled},
		service{name: "healer", proxy: healerProxy, enabled: healerEnabled},
		service{name: "executor", proxy: executorProxy, enabled: executorEnabled},
		service{name: "general-agent", proxy: generalAgentProxy, enabled: generalAgentEnabled})))
}

// dispatchConfig reads the task queue's limits; each is a Go duration such as 30m or 168h.
func dispatchConfig() dispatch.Config {
	duration := func(key string, fallback time.Duration) time.Duration {
		v := os.Getenv(key)
		if v == "" {
			return fallback
		}
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			log.Fatalf("%s must be a positive duration such as 30m: %q", key, v)
		}
		return d
	}
	return dispatch.Config{
		QueueTimeout:  duration("CASEHUB_TASK_QUEUE_TIMEOUT", 30*time.Minute),
		Retention:     duration("CASEHUB_TASK_RETENTION", 7*24*time.Hour),
		NodeLostAfter: duration("CASEHUB_NODE_LOST_AFTER", 2*time.Minute),
	}
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

// serviceURLs resolves the planner and generator addresses. An explicit
// per-service URL wins; otherwise CASEHUB_AUTOMATION_URL serves both; otherwise
// each falls back to its own local default port.
func serviceURLs(getenv func(string) string) (planner, generator string) {
	pick := func(key, fallback string) string {
		if v := getenv(key); v != "" {
			return v
		}
		if v := getenv("CASEHUB_AUTOMATION_URL"); v != "" {
			return v
		}
		return fallback
	}
	return pick("CASEHUB_PLANNER_URL", "http://localhost:4501"), pick("CASEHUB_GENERATOR_URL", "http://localhost:4502")
}
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
