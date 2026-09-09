package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"casehub/internal/core"
	"casehub/internal/store"
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
	jsonOut(w, 200, s)
}
func (a *api) action(w http.ResponseWriter, r *http.Request) {
	var in core.Action
	if e := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); e != nil {
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
	jsonOut(w, 200, out)
}
func routes(a *api) http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/state", a.state)
	m.HandleFunc("POST /api/action", a.action)
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
	return m
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
	log.Printf("CaseHub listening on :%s (store=%s)", port, kind)
	log.Fatal(http.ListenAndServe(":"+port, routes(&api{service: core.NewService(repo)})))
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
