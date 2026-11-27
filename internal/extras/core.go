// Package extras runs optional features NEXT TO Koolo, in their own process (koolo-extras.exe).
//
// Design goals:
//   - An extra can never crash Koolo: it talks to Koolo only through Koolo's MCP server.
//   - An extra can't crash the other extras: each module runs under a supervisor that recovers
//     panics, restarts it with backoff, and switches it off after repeated crashes.
//   - Every module can be switched on/off in config/extras.yaml and fixed on its own.
//
// Once a module has proven itself it can be promoted into Koolo itself.
package extras

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

type Config struct {
	KooloURL      string `yaml:"kooloURL"`
	InstallDir    string `yaml:"installDir"` // Koolo AI folder; default: next to koolo-extras.exe
	RepoDir       string `yaml:"repoDir"`    // kooloforai git checkout, needed for auto-fix and autopilot
	DashboardPort int    `yaml:"dashboardPort"`
	ClaudeCommand string `yaml:"claudeCommand"` // Claude Code CLI, default "claude"

	Challenge struct {
		Name      string `yaml:"name"`
		StartDate string `yaml:"startDate"` // 2006-01-02, for "Day N" on the dashboard
	} `yaml:"challenge"`

	Stats struct {
		Enabled         bool  `yaml:"enabled"`
		IntervalSeconds int   `yaml:"intervalSeconds"`
		MilestoneLevels []int `yaml:"milestoneLevels"`
		DeathStreak     int   `yaml:"deathStreak"`
	} `yaml:"stats"`

	Watchdog struct {
		Enabled         bool   `yaml:"enabled"`
		RestartKoolo    bool   `yaml:"restartKoolo"`
		Autofix         string `yaml:"autofix"` // code | settings | off
		MaxFixesPerDay  int    `yaml:"maxFixesPerDay"`
		StallMinutes    int    `yaml:"stallMinutes"`
		ErrorBurst      int    `yaml:"errorBurst"` // this many new error lines within 10 minutes triggers a fix
		VerifyMinutes   int    `yaml:"verifyMinutes"`
		IntervalSeconds int    `yaml:"intervalSeconds"`
	} `yaml:"watchdog"`

	Autopilot struct {
		Enabled    bool `yaml:"enabled"`
		EveryGames int  `yaml:"everyGames"`
		MinMinutes int  `yaml:"minMinutes"`
		MaxPerDay  int  `yaml:"maxPerDay"`
	} `yaml:"autopilot"`

	PatchWatch struct {
		Enabled        bool `yaml:"enabled"`
		IntervalMinute int  `yaml:"intervalMinutes"`
	} `yaml:"patchWatch"`

	Alerts struct {
		Enabled        bool   `yaml:"enabled"`
		DiscordWebhook string `yaml:"discordWebhook"`
	} `yaml:"alerts"`

	Session struct {
		Enabled                bool `yaml:"enabled"`
		ScreenshotEverySeconds int  `yaml:"screenshotEverySeconds"`
		IdleMinutesToEnd       int  `yaml:"idleMinutesToEnd"`
		OBS                    struct {
			Enabled  bool   `yaml:"enabled"`
			Host     string `yaml:"host"`
			Port     int    `yaml:"port"`
			Password string `yaml:"password"`
		} `yaml:"obs"`
	} `yaml:"session"`
}

func DefaultConfig() Config {
	var c Config
	c.KooloURL = "http://localhost:8087/mcp"
	c.DashboardPort = 8088
	c.ClaudeCommand = "claude"
	c.Challenge.Name = "AI plays Diablo II"
	c.Stats.Enabled = true
	c.Stats.IntervalSeconds = 30
	c.Stats.MilestoneLevels = []int{5, 10, 15, 20, 25, 30, 40, 50, 60, 70, 80, 90, 99}
	c.Stats.DeathStreak = 3
	c.Watchdog.Enabled = true
	c.Watchdog.RestartKoolo = true
	c.Watchdog.Autofix = "code"
	c.Watchdog.MaxFixesPerDay = 3
	c.Watchdog.StallMinutes = 25
	c.Watchdog.ErrorBurst = 15
	c.Watchdog.VerifyMinutes = 30
	c.Watchdog.IntervalSeconds = 20
	c.Autopilot.EveryGames = 10
	c.Autopilot.MinMinutes = 30
	c.Autopilot.MaxPerDay = 12
	c.PatchWatch.Enabled = true
	c.PatchWatch.IntervalMinute = 5
	c.Alerts.Enabled = true
	c.Session.Enabled = true
	c.Session.ScreenshotEverySeconds = 90
	c.Session.IdleMinutesToEnd = 5
	c.Session.OBS.Host = "localhost"
	c.Session.OBS.Port = 4455
	return c
}

func LoadConfig(path string) (Config, error) {
	c := DefaultConfig()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// ---------------------------------------------------------------------------
// Bus: modules talk to each other through events, never by calling each other.
// ---------------------------------------------------------------------------

const (
	EvAlert      = "alert"       // Text: message for the user (Discord + dashboard)
	EvFixRequest = "fix_request" // Text: what's wrong (context for Claude)
	EvNewGame    = "new_game"    // a character finished a game
	EvMilestone  = "milestone"
	EvPatch      = "patch"
)

type Event struct {
	Time time.Time         `json:"time"`
	Kind string            `json:"kind"`
	Text string            `json:"text"`
	Data map[string]string `json:"data,omitempty"`
}

type Bus struct {
	mu   sync.Mutex
	subs map[string][]chan Event
}

func NewBus() *Bus { return &Bus{subs: map[string][]chan Event{}} }

// Subscribe returns a buffered channel for one event kind.
func (b *Bus) Subscribe(kind string) <-chan Event {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subs[kind] = append(b.subs[kind], ch)
	b.mu.Unlock()
	return ch
}

// Publish never blocks: a slow subscriber loses events instead of stalling everyone.
func (b *Bus) Publish(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs[e.Kind] {
		select {
		case ch <- e:
		default:
		}
	}
}

// ---------------------------------------------------------------------------
// Store: small JSONL files under extras/data.
// ---------------------------------------------------------------------------

type Store struct {
	Dir string
	mu  sync.Mutex
}

func (s *Store) Append(file string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, file), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(v)
	_, err = f.Write(append(b, '\n'))
	return err
}

// ReadAll decodes every line of a JSONL file into newT() values.
func ReadAll[T any](s *Store, file string) ([]T, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(filepath.Join(s.Dir, file))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []T
	dec := json.NewDecoder(bytesReader(raw))
	for dec.More() {
		var v T
		if err := dec.Decode(&v); err != nil {
			break
		}
		out = append(out, v)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Modules and the supervisor
// ---------------------------------------------------------------------------

type Env struct {
	Cfg    Config
	Koolo  Koolo
	Runner Runner
	Bus    *Bus
	Store  *Store
	Log    *slog.Logger
	Now    func() time.Time
}

type Module interface {
	Name() string
	// Run does the module's work until ctx is cancelled. Returning an error (or panicking) makes the
	// supervisor restart it after a pause.
	Run(ctx context.Context, env *Env) error
}

type ModuleHealth struct {
	Name      string    `json:"name"`
	State     string    `json:"state"` // running | restarting | disabled | stopped
	Restarts  int       `json:"restarts"`
	LastError string    `json:"lastError,omitempty"`
	LastErrAt time.Time `json:"lastErrorAt,omitempty"`
}

type Supervisor struct {
	env      *Env
	mu       sync.Mutex
	health   map[string]*ModuleHealth
	MaxFails int           // failures within FailWindow before a module is switched off
	Window   time.Duration // the window
	Backoff  func(attempt int) time.Duration
}

func NewSupervisor(env *Env) *Supervisor {
	return &Supervisor{
		env: env, health: map[string]*ModuleHealth{}, MaxFails: 3, Window: 10 * time.Minute,
		Backoff: func(n int) time.Duration { return time.Duration(min(n, 6)) * 10 * time.Second },
	}
}

func (s *Supervisor) setHealth(name string, f func(h *ModuleHealth)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.health[name]
	if !ok {
		h = &ModuleHealth{Name: name}
		s.health[name] = h
	}
	f(h)
}

func (s *Supervisor) Health() []ModuleHealth {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ModuleHealth, 0, len(s.health))
	for _, h := range s.health {
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Start runs every module in its own goroutine; it returns at once.
func (s *Supervisor) Start(ctx context.Context, modules []Module, wg *sync.WaitGroup) {
	for _, m := range modules {
		s.setHealth(m.Name(), func(h *ModuleHealth) { h.State = "running" })
		wg.Add(1)
		go func(m Module) {
			defer wg.Done()
			s.supervise(ctx, m)
		}(m)
	}
}

func (s *Supervisor) supervise(ctx context.Context, m Module) {
	var fails []time.Time
	for attempt := 0; ; attempt++ {
		err := s.runOnce(ctx, m)
		if ctx.Err() != nil {
			s.setHealth(m.Name(), func(h *ModuleHealth) { h.State = "stopped" })
			return
		}
		now := s.env.Now()
		if err == nil {
			err = fmt.Errorf("module stopped unexpectedly")
		}
		s.env.Log.Error("extra failed", "module", m.Name(), "error", err)
		fails = append(fails, now)
		var recent []time.Time
		for _, t := range fails {
			if now.Sub(t) <= s.Window {
				recent = append(recent, t)
			}
		}
		fails = recent
		if len(fails) >= s.MaxFails {
			s.setHealth(m.Name(), func(h *ModuleHealth) {
				h.State, h.LastError, h.LastErrAt = "disabled", err.Error(), now
			})
			s.env.Bus.Publish(Event{Kind: EvAlert, Text: fmt.Sprintf("Extra %q crashed %d times and was switched off. Koolo keeps running. Last error: %v",
				m.Name(), len(fails), err)})
			return
		}
		s.setHealth(m.Name(), func(h *ModuleHealth) {
			h.State, h.Restarts, h.LastError, h.LastErrAt = "restarting", h.Restarts+1, err.Error(), now
		})
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.Backoff(attempt + 1)):
		}
		s.setHealth(m.Name(), func(h *ModuleHealth) { h.State = "running" })
	}
}

// runOnce turns a panic into an error so one broken module can't take the process down.
func (s *Supervisor) runOnce(ctx context.Context, m Module) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
		}
	}()
	return m.Run(ctx, s.env)
}

// sleep waits d or until ctx ends; returns false if ctx ended.
func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
