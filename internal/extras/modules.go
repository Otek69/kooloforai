package extras

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Autopilot: the "check" you'd type in Claude Desktop, run automatically every N games by Claude Code
// (headless, on your Claude plan). It reviews events, deaths and gear, then sets the plan.
// ---------------------------------------------------------------------------

type Autopilot struct {
	games    int
	lastRun  time.Time
	runsDay  string
	runsDone int
}

func (a *Autopilot) Name() string { return "autopilot" }

const autopilotPrompt = `You are the brain of Koolo AI, a bot playing Diablo II: Resurrected with an OFFLINE character for a
YouTube channel. Koolo plays in real time; you decide. Use the koolo MCP tools:
1. koolo_events (sinceId from brain/autopilot-last-event.txt in the Koolo folder if you can read it, else 0) and koolo_status.
2. If there were deaths or chickens: koolo_last_failure, then save one concrete lesson with koolo_add_lesson.
3. Gear advisor: koolo_game_state. If a stashed item is a clear upgrade over what is equipped, say which and why in your report
   and with koolo_say (you can't equip it yourself).
4. Decide the plan for the next games from the run record, lessons, level, gear and the route playbook
   (internal/brain/playbook.md). Set it with koolo_brain_plan, with a short "thought" for the viewers.
5. React to big moments since the last check with koolo_say (1-2 entertaining sentences).
Do not start/stop characters or change other settings. Do not edit code. End with a 3-5 line report.`

func (a *Autopilot) Run(ctx context.Context, env *Env) error {
	games := env.Bus.Subscribe(EvNewGame)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-games:
			a.games++
			a.maybeRun(ctx, env)
		}
	}
}

func (a *Autopilot) maybeRun(ctx context.Context, env *Env) {
	cfg := env.Cfg.Autopilot
	now := env.Now()
	if day := now.Format("2006-01-02"); day != a.runsDay {
		a.runsDay, a.runsDone = day, 0
	}
	if a.games < max(1, cfg.EveryGames) || now.Sub(a.lastRun) < time.Duration(cfg.MinMinutes)*time.Minute || a.runsDone >= cfg.MaxPerDay {
		return
	}
	if env.Cfg.RepoDir == "" {
		env.Bus.Publish(Event{Kind: EvAlert, Text: "Autopilot is on but repoDir is not set in extras.yaml."})
		a.games = 0
		return
	}
	a.games, a.lastRun = 0, now
	a.runsDone++
	cctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	out, err := env.Runner.Run(cctx, env.Cfg.RepoDir, env.Cfg.ClaudeCommand,
		"-p", autopilotPrompt, "--allowedTools", "mcp__koolo__*,Read")
	rec := map[string]any{"time": now, "ok": err == nil, "report": truncate(lastLines(out, 12), 2000)}
	_ = env.Store.Append("autopilot.jsonl", rec)
	if err != nil {
		env.Log.Warn("autopilot: claude failed", "error", err, "output", truncate(out, 500))
	}
}

// ---------------------------------------------------------------------------
// PatchWatch: notices a D2R update (D2R.exe changed) and checks whether Koolo can still read the game.
// ---------------------------------------------------------------------------

type PatchWatch struct{ last string }

func (p *PatchWatch) Name() string { return "patchwatch" }

func (p *PatchWatch) d2rExe(env *Env) string {
	var k struct {
		D2RPath string `yaml:"D2RPath"`
	}
	dir := env.Cfg.InstallDir
	if dir == "" {
		dir = "."
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config", "koolo.yaml"))
	if err != nil || yaml.Unmarshal(raw, &k) != nil || k.D2RPath == "" {
		return ""
	}
	return filepath.Join(k.D2RPath, "D2R.exe")
}

func (p *PatchWatch) Run(ctx context.Context, env *Env) error {
	every := time.Duration(max(1, env.Cfg.PatchWatch.IntervalMinute)) * time.Minute
	for {
		p.Check(ctx, env)
		if !sleep(ctx, every) {
			return nil
		}
	}
}

// Check compares D2R.exe's size and date with the last look (exported for tests).
func (p *PatchWatch) Check(ctx context.Context, env *Env) {
	exe := p.d2rExe(env)
	if exe == "" {
		return
	}
	st, err := os.Stat(exe)
	if err != nil {
		return
	}
	sig := fmt.Sprintf("%d-%d", st.Size(), st.ModTime().Unix())
	stateFile := filepath.Join(env.Store.Dir, "d2r-version.txt")
	if p.last == "" {
		if b, err := os.ReadFile(stateFile); err == nil {
			p.last = strings.TrimSpace(string(b))
		}
	}
	if sig == p.last {
		return
	}
	first := p.last == ""
	p.last = sig
	_ = os.MkdirAll(env.Store.Dir, 0o755)
	_ = os.WriteFile(stateFile, []byte(sig), 0o644)
	if first {
		return // first run: just remember the current version
	}
	env.Bus.Publish(Event{Kind: EvPatch, Text: "D2R was updated"})
	report, err := env.Koolo.Call(ctx, "koolo_doctor", nil)
	if err != nil {
		env.Bus.Publish(Event{Kind: EvAlert, Text: "D2R was updated. Koolo Doctor could not run (" + err.Error() + "); check that Koolo can still read the game."})
		return
	}
	if strings.Contains(report, "essential patterns missing") || strings.Contains(report, "game data looks wrong") {
		env.Bus.Publish(Event{Kind: EvAlert, Text: "⚠️ D2R was updated and Koolo can't read the new version."})
		env.Bus.Publish(Event{Kind: EvFixRequest, Text: "D2R was updated and the memory reader no longer matches. Koolo Doctor report:\n" + truncate(report, 6000)})
		return
	}
	env.Bus.Publish(Event{Kind: EvAlert, Text: "D2R was updated; Koolo Doctor says Koolo can still read the game. 👍"})
}

// ---------------------------------------------------------------------------
// Alerts: Discord webhook + a log the dashboard shows.
// ---------------------------------------------------------------------------

type Alerts struct {
	Client *http.Client
	sent   []time.Time
}

func (a *Alerts) Name() string { return "alerts" }

func (a *Alerts) Run(ctx context.Context, env *Env) error {
	ch := env.Bus.Subscribe(EvAlert)
	if a.Client == nil {
		a.Client = &http.Client{Timeout: 10 * time.Second}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case e := <-ch:
			_ = env.Store.Append("alerts.jsonl", e)
			env.Log.Info("alert", "text", e.Text)
			a.discord(ctx, env, e.Text)
		}
	}
}

func (a *Alerts) discord(ctx context.Context, env *Env, text string) {
	hook := env.Cfg.Alerts.DiscordWebhook
	if hook == "" {
		return
	}
	now := env.Now()
	var recent []time.Time
	for _, t := range a.sent {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	a.sent = recent
	if len(a.sent) >= 5 { // Discord rate limits; the dashboard still has every alert
		return
	}
	a.sent = append(a.sent, now)
	body, _ := json.Marshal(map[string]string{"content": truncate("**Koolo AI** "+text, 1900)})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, hook, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if resp, err := a.Client.Do(req); err == nil {
		resp.Body.Close()
	} else {
		env.Log.Warn("alerts: discord", "error", err)
	}
}
