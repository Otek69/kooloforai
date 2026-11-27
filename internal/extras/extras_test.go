package extras

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- fakes ----------------------------------------------------------------

type fakeKoolo struct {
	mu      sync.Mutex
	down    bool
	status  KooloStatus
	metrics map[string]CharMetrics
	logs    string
	doctor  string
	events  string
	calls   []string
}

func (f *fakeKoolo) Call(ctx context.Context, tool string, args map[string]any) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, tool)
	if f.down {
		return "", ErrKooloDown
	}
	switch tool {
	case "koolo_status":
		b, _ := json.Marshal(f.status)
		return string(b), nil
	case "koolo_metrics":
		b, _ := json.Marshal(f.metrics)
		return string(b), nil
	case "koolo_logs":
		return f.logs, nil
	case "koolo_doctor":
		return f.doctor, nil
	case "koolo_events":
		if f.events == "" {
			return "[]", nil
		}
		return f.events, nil
	}
	return "ok", nil
}

func (f *fakeKoolo) Image(ctx context.Context, tool string, args map[string]any) ([]byte, error) {
	return []byte{0xff, 0xd8, 0xff, 0xd9}, nil
}

func (f *fakeKoolo) set(fn func(f *fakeKoolo)) { f.mu.Lock(); fn(f); f.mu.Unlock() }

type fakeRunner struct {
	mu      sync.Mutex
	runs    []string
	starts  []string
	outputs map[string]string // prefix of "name args" -> output
	fail    map[string]bool
}

func (r *fakeRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cmd := name + " " + strings.Join(args, " ")
	r.runs = append(r.runs, cmd)
	for p, out := range r.outputs {
		if strings.HasPrefix(cmd, p) {
			if r.fail[p] {
				return out, errors.New("failed")
			}
			return out, nil
		}
	}
	return "", nil
}

func (r *fakeRunner) Start(dir, name string, args ...string) error {
	r.mu.Lock()
	r.starts = append(r.starts, name)
	r.mu.Unlock()
	return nil
}

func (r *fakeRunner) ran(prefix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.runs {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

func newEnv(t *testing.T) (*Env, *fakeKoolo, *fakeRunner, *clock) {
	t.Helper()
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.InstallDir = dir
	cfg.RepoDir = filepath.Join(dir, "repo")
	k := &fakeKoolo{status: KooloStatus{Version: "v1", Characters: map[string]CharStatus{}}, metrics: map[string]CharMetrics{}}
	r := &fakeRunner{outputs: map[string]string{"git rev-parse": "main\n", "claude": "Cause: x\nChange: y\nVerified: ok\n"}, fail: map[string]bool{}}
	c := &clock{t: time.Date(2026, 11, 28, 12, 0, 0, 0, time.Local)}
	env := &Env{Cfg: cfg, Koolo: k, Runner: r, Bus: NewBus(), Store: &Store{Dir: filepath.Join(dir, "extras", "data")},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: c.now}
	return env, k, r, c
}

func drain(ch <-chan Event) []Event {
	var out []Event
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}

// ---- supervisor: crashes are contained --------------------------------------

type panicky struct{ n int }

func (p *panicky) Name() string { return "panicky" }
func (p *panicky) Run(ctx context.Context, env *Env) error {
	p.n++
	panic("boom")
}

type steady struct{ ticks int }

func (s *steady) Name() string { return "steady" }
func (s *steady) Run(ctx context.Context, env *Env) error {
	for {
		s.ticks++
		if !sleep(ctx, 5*time.Millisecond) {
			return nil
		}
	}
}

func TestSupervisorIsolatesAndDisablesCrashingModule(t *testing.T) {
	env, _, _, _ := newEnv(t)
	env.Now = time.Now
	alerts := env.Bus.Subscribe(EvAlert)
	sup := NewSupervisor(env)
	sup.Backoff = func(int) time.Duration { return time.Millisecond }
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	p, s := &panicky{}, &steady{}
	sup.Start(ctx, []Module{p, s}, &wg)
	time.Sleep(150 * time.Millisecond)

	health := map[string]ModuleHealth{}
	for _, h := range sup.Health() {
		health[h.Name] = h
	}
	if health["panicky"].State != "disabled" || p.n != 3 {
		t.Fatalf("panicky: state %q after %d runs, want disabled after 3", health["panicky"].State, p.n)
	}
	if !strings.Contains(health["panicky"].LastError, "boom") {
		t.Fatalf("last error not recorded: %q", health["panicky"].LastError)
	}
	if health["steady"].State != "running" || s.ticks < 5 {
		t.Fatalf("steady module was affected: %+v ticks=%d", health["steady"], s.ticks)
	}
	if got := drain(alerts); len(got) != 1 || !strings.Contains(got[0].Text, "switched off") {
		t.Fatalf("want one switched-off alert, got %v", got)
	}
	cancel()
	wg.Wait()
}

// ---- stats ------------------------------------------------------------------

func TestStatsRecordsGamesMilestonesAndStreaks(t *testing.T) {
	env, k, _, c := newEnv(t)
	env.Cfg.Stats.DeathStreak = 2
	alerts, games := env.Bus.Subscribe(EvAlert), env.Bus.Subscribe(EvNewGame)
	s := &Stats{prev: map[string]CharStatus{}, prevLevel: map[string]int{}, deathStreak: map[string]int{}}
	ctx := context.Background()

	k.set(func(f *fakeKoolo) {
		f.status.Characters["Sorcaa"] = CharStatus{Status: "In game", Games: 0}
		f.metrics["Sorcaa"] = CharMetrics{InGame: true, Level: 9, Experience: 10000}
	})
	s.tick(ctx, env)
	c.add(30 * time.Minute)
	k.set(func(f *fakeKoolo) {
		f.status.Characters["Sorcaa"] = CharStatus{Status: "In game", Games: 2, Deaths: 2, LastEnd: "death"}
		f.metrics["Sorcaa"] = CharMetrics{InGame: true, Level: 10, Experience: 40000}
	})
	s.tick(ctx, env)

	if g := drain(games); len(g) != 2 {
		t.Fatalf("want 2 new-game events, got %v", g)
	}
	texts := ""
	for _, a := range drain(alerts) {
		texts += a.Text + "\n"
	}
	if !strings.Contains(texts, "reached level 10") || !strings.Contains(texts, "died 2 games in a row") {
		t.Fatalf("missing milestone or streak alert:\n%s", texts)
	}
	sum := Summarize(env.Store, c.now())
	if len(sum) != 1 || sum[0].GamesTotal != 2 || sum[0].DeathsTotal != 2 || sum[0].Level != 10 {
		t.Fatalf("bad summary %+v", sum)
	}
	if sum[0].ExpPerHour != 60000 { // 30000 exp in 30 minutes
		t.Fatalf("exp/hour %d, want 60000", sum[0].ExpPerHour)
	}
}

// ---- watchdog -----------------------------------------------------------------

func newWatchdog(env *Env) *Watchdog {
	w := &Watchdog{seenCrashes: map[string]bool{}, progress: map[string]progressMark{}}
	return w
}

func writeCrash(t *testing.T, env *Env, name string) {
	dir := filepath.Join(env.Cfg.InstallDir, "crash-reports")
	_ = os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, name), []byte("Panic: nil map\nStack: ..."), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWatchdogFixesCrashOnBranchAndRollsBackIfItRecurs(t *testing.T) {
	env, k, r, c := newEnv(t)
	ctx := context.Background()
	k.set(func(f *fakeKoolo) { f.status.Characters["Sorcaa"] = CharStatus{Status: "In game"} })
	w := newWatchdog(env)

	writeCrash(t, env, "crash-1.txt")
	w.Tick(ctx, env)
	if r.ran("git checkout -b autofix/") != 1 || r.ran("claude -p /fix-koolo") != 1 {
		t.Fatalf("expected a branch and a Claude run, got %v", r.runs)
	}
	claude := ""
	for _, c := range r.runs {
		if strings.HasPrefix(c, "claude") {
			claude = c
		}
	}
	if !strings.Contains(claude, "nil map") || !strings.Contains(claude, "Edit,Write") || !strings.Contains(claude, "Do not push") {
		t.Fatalf("claude call lacks context or permissions: %s", claude)
	}
	if w.verify == nil {
		t.Fatal("should be verifying the fix")
	}

	// The same kind of problem comes back during verification -> roll back to main and rebuild.
	c.add(5 * time.Minute)
	writeCrash(t, env, "crash-2.txt")
	w.Tick(ctx, env)
	if r.ran("git checkout main") != 1 || r.ran("powershell -ExecutionPolicy Bypass -File scripts") != 1 {
		t.Fatalf("expected rollback to main + rebuild, got %v", r.runs)
	}
	fixes, _ := ReadAll[FixRecord](env.Store, "fixes.jsonl")
	if last := fixes[len(fixes)-1]; last.Outcome != "rolled_back" {
		t.Fatalf("last fix record %+v", last)
	}
}

func TestWatchdogFixHoldsAfterVerifyWindow(t *testing.T) {
	env, k, _, c := newEnv(t)
	ctx := context.Background()
	k.set(func(f *fakeKoolo) { f.status.Characters["Sorcaa"] = CharStatus{Status: "In game"} })
	w := newWatchdog(env)
	writeCrash(t, env, "crash-1.txt")
	w.Tick(ctx, env)
	c.add(31 * time.Minute)
	// The repaired bot plays normally again.
	k.set(func(f *fakeKoolo) { f.status.Characters["Sorcaa"] = CharStatus{Status: "In game", Games: 6} })
	w.Tick(ctx, env)
	if w.verify != nil {
		t.Fatal("verification should have ended")
	}
	fixes, _ := ReadAll[FixRecord](env.Store, "fixes.jsonl")
	if fixes[len(fixes)-1].Outcome != "holds" {
		t.Fatalf("want holds, got %+v", fixes[len(fixes)-1])
	}
}

func TestWatchdogLimitsAndOffMode(t *testing.T) {
	env, k, r, c := newEnv(t)
	ctx := context.Background()
	k.set(func(f *fakeKoolo) { f.status.Characters["Sorcaa"] = CharStatus{Status: "In game"} })
	env.Cfg.Watchdog.MaxFixesPerDay = 1
	w := newWatchdog(env)
	w.fix(ctx, env, "errors", "x")
	w.verify = nil
	c.add(2 * time.Hour)
	w.fix(ctx, env, "errors", "y") // over the daily limit
	if n := r.ran("claude"); n != 1 {
		t.Fatalf("daily limit not enforced: %d claude runs", n)
	}
	env.Cfg.Watchdog.Autofix = "off"
	c.add(48 * time.Hour)
	w.fix(ctx, env, "errors", "z")
	if n := r.ran("claude"); n != 1 {
		t.Fatalf("off mode still ran claude")
	}
}

func TestWatchdogSettingsModeHasNoEditTools(t *testing.T) {
	args := claudeArgs("settings", fixPrompt("settings", "stall", "stuck"))
	tools := args[len(args)-1]
	if strings.Contains(tools, "Edit") || strings.Contains(tools, "Bash") || !strings.Contains(args[1], "SETTINGS ONLY") {
		t.Fatalf("settings mode args wrong: tools=%s", tools)
	}
	if code := claudeArgs("code", "x"); !strings.Contains(code[len(code)-1], "Edit") {
		t.Fatalf("code mode should allow edits: %v", code)
	}
}

func TestWatchdogStallErrorsAndRestart(t *testing.T) {
	env, k, r, c := newEnv(t)
	ctx := context.Background()
	env.Cfg.Watchdog.Autofix = "off" // only check detection here
	alerts := env.Bus.Subscribe(EvAlert)
	k.set(func(f *fakeKoolo) { f.status.Characters["Sorcaa"] = CharStatus{Status: "In game", Games: 4} })
	w := newWatchdog(env)
	w.Tick(ctx, env)
	c.add(26 * time.Minute)
	w.Tick(ctx, env)
	if a := drain(alerts); len(a) != 1 || !strings.Contains(a[0].Text, "stall") {
		t.Fatalf("want stall alert, got %v", a)
	}

	// error burst: 20 new lines
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "time=12:00 level=ERROR msg=\"pathing failed "+string(rune('a'+i))+"\"")
	}
	k.set(func(f *fakeKoolo) { f.logs = strings.Join(lines, "\n") })
	w.Tick(ctx, env)
	if a := drain(alerts); len(a) != 1 || !strings.Contains(a[0].Text, "errors") {
		t.Fatalf("want error-burst alert, got %v", a)
	}
	w.Tick(ctx, env) // same lines again: nothing new
	if a := drain(alerts); len(a) != 0 {
		t.Fatalf("old error lines counted twice: %v", a)
	}

	// Koolo gone for 3 ticks -> restarted
	k.set(func(f *fakeKoolo) { f.down = true })
	for i := 0; i < 3; i++ {
		w.Tick(ctx, env)
	}
	if len(r.starts) != 1 || !strings.HasSuffix(r.starts[0], "koolo.exe") {
		t.Fatalf("koolo not restarted: %v", r.starts)
	}
}

// ---- patch watch ----------------------------------------------------------------

func TestPatchWatchRequestsFixWhenReaderBreaks(t *testing.T) {
	env, k, _, _ := newEnv(t)
	ctx := context.Background()
	d2r := filepath.Join(env.Cfg.InstallDir, "d2r")
	_ = os.MkdirAll(filepath.Join(env.Cfg.InstallDir, "config"), 0o755)
	_ = os.MkdirAll(d2r, 0o755)
	_ = os.WriteFile(filepath.Join(env.Cfg.InstallDir, "config", "koolo.yaml"), []byte("D2RPath: "+d2r+"\n"), 0o644)
	exe := filepath.Join(d2r, "D2R.exe")
	_ = os.WriteFile(exe, []byte("v1"), 0o644)
	fixes, alerts := env.Bus.Subscribe(EvFixRequest), env.Bus.Subscribe(EvAlert)

	p := &PatchWatch{}
	p.Check(ctx, env) // remembers v1
	if len(drain(alerts)) != 0 {
		t.Fatal("first check should be silent")
	}
	_ = os.WriteFile(exe, []byte("version two"), 0o644) // "patch"
	k.set(func(f *fakeKoolo) { f.doctor = "[FAIL] Patterns essential patterns missing: UnitTable" })
	p.Check(ctx, env)
	if len(drain(fixes)) != 1 || len(drain(alerts)) != 1 {
		t.Fatal("patch with broken reader should alert and request a fix")
	}
	p.Check(ctx, env) // unchanged
	if len(drain(alerts)) != 0 {
		t.Fatal("no change: no alert")
	}
}

// ---- session export ---------------------------------------------------------------

func TestSessionExportsVideoPipelineFolder(t *testing.T) {
	env, k, _, c := newEnv(t)
	ctx := context.Background()
	k.set(func(f *fakeKoolo) {
		f.status.Characters["Sorcaa"] = CharStatus{Status: "In game"}
		f.events = `[{"id":3,"time":"` + c.now().Add(10*time.Second).Format(time.RFC3339) + `","kind":"death","text":"death during pit"}]`
	})
	s := &Session{}
	s.Tick(ctx, env)
	if !s.active {
		t.Fatal("session should start when a character is in game")
	}
	dir := s.dir
	c.add(2 * time.Minute)
	k.set(func(f *fakeKoolo) { f.events = "[]" })
	s.Tick(ctx, env) // still playing at 2:00
	k.set(func(f *fakeKoolo) { f.status.Characters["Sorcaa"] = CharStatus{Status: "Not Started"} })
	c.add(1 * time.Minute)
	s.Tick(ctx, env) // stopped, but not idle long enough yet
	if !s.active {
		t.Fatal("session ended too early")
	}
	c.add(6 * time.Minute)
	s.Tick(ctx, env)
	if s.active {
		t.Fatal("session should end after idle minutes")
	}
	var meta map[string]any
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil || json.Unmarshal(raw, &meta) != nil {
		t.Fatalf("meta.json: %v", err)
	}
	if meta["video_seconds"].(float64) != 125 || len(meta["keyframes"].([]any)) != 2 {
		t.Fatalf("meta %v", meta)
	}
	log, _ := os.ReadFile(filepath.Join(dir, "log.txt"))
	if !strings.Contains(string(log), "DEATH: death during pit") || !strings.Contains(string(log), "SESSION END") {
		t.Fatalf("log.txt:\n%s", log)
	}
	for _, f := range []string{"PROMPT.md", "overlay.schema.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("%s missing", f)
		}
	}
}

// ---- dashboard ----------------------------------------------------------------------

func TestDashboardStateAndLocalOnly(t *testing.T) {
	env, _, _, _ := newEnv(t)
	env.Cfg.Challenge.StartDate = "2026-11-26"
	_ = env.Store.Append("alerts.jsonl", Event{Time: env.Now(), Kind: EvAlert, Text: "hello"})
	d := &Dashboard{Sup: NewSupervisor(env)}
	h := d.Handler(env)

	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var st DashboardState
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || st.Challenge.Day != 3 || len(st.Alerts) != 1 {
		t.Fatalf("state %+v err %v", st, err)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.20:5555"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("LAN request got %d", rec.Code)
	}
}
