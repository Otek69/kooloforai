package extras

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

//go:embed dashboard.html
var dashboardHTML []byte

// Dashboard serves http://localhost:8088/ (stats, module health, alerts, fixes) and /overlay
// (a small "Day N / level / deaths" banner for OBS). Only this PC can open it.
type Dashboard struct {
	Sup *Supervisor
}

func (d *Dashboard) Name() string { return "dashboard" }

type DashboardState struct {
	Challenge struct {
		Name string `json:"name"`
		Day  int    `json:"day"`
	} `json:"challenge"`
	Modules   []ModuleHealth   `json:"modules"`
	Summary   []CharSummary    `json:"summary"`
	Alerts    []Event          `json:"alerts"`
	Fixes     []FixRecord      `json:"fixes"`
	Autopilot []map[string]any `json:"autopilot"`
	Updated   time.Time        `json:"updated"`
}

func (d *Dashboard) State(env *Env) DashboardState {
	var s DashboardState
	now := env.Now()
	s.Updated = now
	s.Challenge.Name = env.Cfg.Challenge.Name
	if t, err := time.ParseInLocation("2006-01-02", env.Cfg.Challenge.StartDate, now.Location()); err == nil {
		s.Challenge.Day = int(now.Sub(t).Hours()/24) + 1
	}
	if d.Sup != nil {
		s.Modules = d.Sup.Health()
	}
	s.Summary = Summarize(env.Store, now)
	alerts, _ := ReadAll[Event](env.Store, "alerts.jsonl")
	s.Alerts = lastN(alerts, 30)
	fixes, _ := ReadAll[FixRecord](env.Store, "fixes.jsonl")
	s.Fixes = lastN(fixes, 20)
	ap, _ := ReadAll[map[string]any](env.Store, "autopilot.jsonl")
	s.Autopilot = lastN(ap, 10)
	return s
}

func lastN[T any](xs []T, n int) []T {
	if len(xs) > n {
		xs = xs[len(xs)-n:]
	}
	out := make([]T, len(xs))
	for i := range xs { // newest first
		out[i] = xs[len(xs)-1-i]
	}
	return out
}

func (d *Dashboard) Handler(env *Env) http.Handler {
	mux := http.NewServeMux()
	page := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(dashboardHTML)
	}
	mux.HandleFunc("/", page)
	mux.HandleFunc("/overlay", page) // same page; it switches to banner mode on /overlay
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(d.State(env))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || !net.ParseIP(host).IsLoopback() {
			http.Error(w, "local only", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (d *Dashboard) Run(ctx context.Context, env *Env) error {
	srv := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", env.Cfg.DashboardPort), Handler: d.Handler(env),
		ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
