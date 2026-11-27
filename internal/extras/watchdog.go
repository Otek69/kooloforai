package extras

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Watchdog keeps Koolo alive and starts Claude Code to repair it when something breaks.
//
// Triggers: Koolo process gone (restart it), a new crash report, a burst of error lines, a character
// making no progress, or a fix request from another extra (e.g. patch detected).
// Safety: a daily limit, one fix at a time with a cooldown, fixes on their own git branch, never
// pushed, and an automatic rollback if the same problem comes back during the verification window.

type FixRecord struct {
	Time    time.Time `json:"time"`
	Reason  string    `json:"reason"`
	Mode    string    `json:"mode"`
	Branch  string    `json:"branch,omitempty"`
	Base    string    `json:"base,omitempty"`
	Outcome string    `json:"outcome"` // started | done | holds | rolled_back | skipped | error
	Detail  string    `json:"detail,omitempty"`
}

type Watchdog struct {
	downTicks   int
	seenCrashes map[string]bool
	lastErrLine string
	progress    map[string]progressMark
	lastFix     time.Time
	fixesToday  int
	fixDay      string
	verify      *verifying
}

type progressMark struct {
	games int
	since time.Time
}

type verifying struct {
	trigger string
	base    string
	branch  string
	until   time.Time
}

func (w *Watchdog) Name() string { return "watchdog" }

func (w *Watchdog) Run(ctx context.Context, env *Env) error {
	w.seenCrashes = map[string]bool{}
	w.progress = map[string]progressMark{}
	for _, f := range w.crashFiles(env) { // only crashes from now on count
		w.seenCrashes[f] = true
	}
	requests := env.Bus.Subscribe(EvFixRequest)
	every := time.Duration(max(5, env.Cfg.Watchdog.IntervalSeconds)) * time.Second
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case req := <-requests:
			w.fix(ctx, env, "request", req.Text)
		case <-ticker.C:
			w.Tick(ctx, env)
		}
	}
}

func (w *Watchdog) installDir(env *Env) string {
	if env.Cfg.InstallDir != "" {
		return env.Cfg.InstallDir
	}
	return "."
}

func (w *Watchdog) crashFiles(env *Env) []string {
	files, _ := filepath.Glob(filepath.Join(w.installDir(env), "crash-reports", "*.txt"))
	return files
}

// Tick runs one round of checks (exported for tests).
func (w *Watchdog) Tick(ctx context.Context, env *Env) {
	now := env.Now()
	if w.verify != nil && now.After(w.verify.until) {
		env.Bus.Publish(Event{Kind: EvAlert, Text: fmt.Sprintf("✅ The auto-fix on branch %s held for %d minutes. Review and merge it when you like.",
			w.verify.branch, env.Cfg.Watchdog.VerifyMinutes)})
		_ = env.Store.Append("fixes.jsonl", FixRecord{Time: now, Reason: w.verify.trigger, Branch: w.verify.branch, Outcome: "holds"})
		w.verify = nil
	}

	// 1. New crash report?
	for _, f := range w.crashFiles(env) {
		if !w.seenCrashes[f] {
			w.seenCrashes[f] = true
			report, _ := os.ReadFile(f)
			w.trigger(ctx, env, "crash", "Koolo crashed. Crash report "+f+":\n"+truncate(string(report), 6000))
		}
	}

	// 2. Koolo reachable?
	st, err := GetStatus(ctx, env.Koolo)
	if err != nil {
		if errors.Is(err, ErrKooloDown) {
			w.downTicks++
			if w.downTicks == 3 {
				w.kooloDown(ctx, env)
			}
		}
		return
	}
	w.downTicks = 0

	// 3. Error burst in the log?
	if logs, err := env.Koolo.Call(ctx, "koolo_logs", map[string]any{"errorsOnly": true, "lines": 400}); err == nil {
		fresh := w.newErrorLines(logs)
		if env.Cfg.Watchdog.ErrorBurst > 0 && len(fresh) >= env.Cfg.Watchdog.ErrorBurst {
			w.trigger(ctx, env, "errors", fmt.Sprintf("%d new error lines in Koolo's log:\n%s", len(fresh), truncate(strings.Join(fresh, "\n"), 6000)))
		}
	}

	// 4. A running character making no progress?
	stall := time.Duration(env.Cfg.Watchdog.StallMinutes) * time.Minute
	for name, cs := range st.Characters {
		running := cs.Status == "In game" || cs.Status == "Starting"
		pm, ok := w.progress[name]
		if !ok || cs.Games != pm.games || !running {
			w.progress[name] = progressMark{games: cs.Games, since: now}
			continue
		}
		if stall > 0 && now.Sub(pm.since) >= stall {
			w.progress[name] = progressMark{games: cs.Games, since: now}
			w.trigger(ctx, env, "stall", fmt.Sprintf("%s has been running for %d minutes without finishing a game (status %q, details %q). It is probably stuck.",
				name, env.Cfg.Watchdog.StallMinutes, cs.Status, cs.Details))
		}
	}
}

// newErrorLines returns error lines after the last one we saw.
func (w *Watchdog) newErrorLines(logs string) []string {
	var lines []string
	for _, l := range strings.Split(logs, "\n") {
		if strings.Contains(l, "level=ERROR") {
			lines = append(lines, l)
		}
	}
	start := 0
	if w.lastErrLine != "" {
		start = len(lines) // if the marker is gone (new log file), count nothing until next time
		for i := len(lines) - 1; i >= 0; i-- {
			if lines[i] == w.lastErrLine {
				start = i + 1
				break
			}
		}
	}
	if len(lines) > 0 {
		w.lastErrLine = lines[len(lines)-1]
	}
	return lines[start:]
}

func (w *Watchdog) kooloDown(ctx context.Context, env *Env) {
	if !env.Cfg.Watchdog.RestartKoolo {
		env.Bus.Publish(Event{Kind: EvAlert, Text: "Koolo is not reachable."})
		return
	}
	out, _ := env.Runner.Run(ctx, "", "tasklist", "/FI", "IMAGENAME eq koolo.exe", "/NH")
	if strings.Contains(strings.ToLower(out), "koolo.exe") {
		env.Bus.Publish(Event{Kind: EvAlert, Text: "Koolo is running but its MCP server doesn't answer. Check that it isn't frozen."})
		return
	}
	dir := w.installDir(env)
	if err := env.Runner.Start(dir, filepath.Join(dir, "koolo.exe")); err != nil {
		env.Bus.Publish(Event{Kind: EvAlert, Text: "Koolo stopped and could not be restarted: " + err.Error()})
		return
	}
	env.Bus.Publish(Event{Kind: EvAlert, Text: "Koolo had stopped; the watchdog restarted it. Start your character in Koolo (or let /fix-koolo look at why)."})
	if w.verify != nil {
		w.rollback(ctx, env, "Koolo stopped again")
	}
}

// trigger handles a detected problem: rollback if we're verifying a fix for it, otherwise start a fix.
func (w *Watchdog) trigger(ctx context.Context, env *Env, kind, details string) {
	if w.verify != nil {
		w.rollback(ctx, env, kind+": "+firstLine(details))
		return
	}
	w.fix(ctx, env, kind, details)
}

func (w *Watchdog) fix(ctx context.Context, env *Env, kind, details string) {
	cfg := env.Cfg.Watchdog
	now := env.Now()
	day := now.Format("2006-01-02")
	if w.fixDay != day {
		w.fixDay, w.fixesToday = day, 0
	}
	skip := func(why string) {
		_ = env.Store.Append("fixes.jsonl", FixRecord{Time: now, Reason: kind, Outcome: "skipped", Detail: why})
		env.Bus.Publish(Event{Kind: EvAlert, Text: fmt.Sprintf("Problem detected (%s): %s\nNo auto-fix: %s", kind, firstLine(details), why)})
	}
	switch {
	case cfg.Autofix == "off" || cfg.Autofix == "":
		skip("auto-fix is off in extras.yaml")
		return
	case env.Cfg.RepoDir == "":
		skip("set repoDir in extras.yaml to your kooloforai folder")
		return
	case w.fixesToday >= cfg.MaxFixesPerDay:
		skip(fmt.Sprintf("daily limit of %d fixes reached", cfg.MaxFixesPerDay))
		return
	case now.Sub(w.lastFix) < 30*time.Minute:
		skip("a fix ran less than 30 minutes ago")
		return
	}
	w.fixesToday++
	w.lastFix = now
	w.progress = map[string]progressMark{} // the stall timer restarts after a fix

	// Stop the bot while Claude works (ignore errors: Koolo may be down).
	if st, err := GetStatus(ctx, env.Koolo); err == nil {
		for name, cs := range st.Characters {
			if cs.Status == "In game" || cs.Status == "Starting" || cs.Status == "Paused" {
				_, _ = env.Koolo.Call(ctx, "koolo_control", map[string]any{"character": name, "action": "stop"})
			}
		}
	}

	repo := env.Cfg.RepoDir
	base, err := env.Runner.Run(ctx, repo, "git", "rev-parse", "--abbrev-ref", "HEAD")
	base = strings.TrimSpace(base)
	if err != nil || base == "" {
		skip("the repoDir is not a git checkout: " + strings.TrimSpace(base))
		return
	}
	branch := "autofix/" + now.Format("20060102-150405")
	if out, err := env.Runner.Run(ctx, repo, "git", "checkout", "-b", branch); err != nil {
		skip("could not create branch " + branch + ": " + strings.TrimSpace(out))
		return
	}
	env.Bus.Publish(Event{Kind: EvAlert, Text: fmt.Sprintf("🔧 Problem detected (%s): %s\nClaude Code is fixing it on branch %s.", kind, firstLine(details), branch)})
	_ = env.Store.Append("fixes.jsonl", FixRecord{Time: now, Reason: kind, Mode: cfg.Autofix, Branch: branch, Base: base, Outcome: "started"})

	args := claudeArgs(cfg.Autofix, fixPrompt(cfg.Autofix, kind, details))
	cctx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	out, err := env.Runner.Run(cctx, repo, env.Cfg.ClaudeCommand, args...)
	cancel()
	rec := FixRecord{Time: env.Now(), Reason: kind, Mode: cfg.Autofix, Branch: branch, Base: base, Outcome: "done", Detail: truncate(lastLines(out, 15), 3000)}
	if err != nil {
		rec.Outcome = "error"
		rec.Detail = err.Error() + "\n" + rec.Detail
		_ = env.Store.Append("fixes.jsonl", rec)
		w.verify = &verifying{trigger: kind, base: base, branch: branch, until: env.Now()}
		w.rollback(ctx, env, "Claude Code failed: "+err.Error())
		return
	}
	_ = env.Store.Append("fixes.jsonl", rec)
	w.progress = map[string]progressMark{}
	env.Bus.Publish(Event{Kind: EvAlert, Text: "Claude's report:\n" + truncate(lastLines(out, 8), 1500)})
	w.verify = &verifying{trigger: kind, base: base, branch: branch,
		until: env.Now().Add(time.Duration(max(5, cfg.VerifyMinutes)) * time.Minute)}
}

// rollback returns to the last good branch and rebuilds, because the fix didn't hold.
func (w *Watchdog) rollback(ctx context.Context, env *Env, why string) {
	v := w.verify
	w.verify = nil
	if v == nil {
		return
	}
	repo := env.Cfg.RepoDir
	out, err := env.Runner.Run(ctx, repo, "git", "checkout", v.base)
	if err == nil {
		out, err = env.Runner.Run(ctx, repo, "powershell", "-ExecutionPolicy", "Bypass", "-File",
			filepath.Join("scripts", "dev-rebuild.ps1"), "-InstallDir", w.installDir(env))
	}
	rec := FixRecord{Time: env.Now(), Reason: v.trigger, Branch: v.branch, Base: v.base, Outcome: "rolled_back", Detail: why}
	if err != nil {
		rec.Outcome, rec.Detail = "error", why+"; rollback failed: "+err.Error()+" "+truncate(out, 500)
	}
	_ = env.Store.Append("fixes.jsonl", rec)
	env.Bus.Publish(Event{Kind: EvAlert, Text: fmt.Sprintf("↩️ The auto-fix on %s did not hold (%s). Rolled back to %s and waiting for you. The branch is kept for review.",
		v.branch, why, v.base)})
}

func claudeArgs(mode, prompt string) []string {
	tools := "mcp__koolo__*,Read,Grep,Glob"
	if mode == "code" {
		tools += ",Edit,Write,Bash(go:*),Bash(git:*),Bash(powershell:*)"
	}
	return []string{"-p", prompt, "--permission-mode", "acceptEdits", "--allowedTools", tools}
}

func fixPrompt(mode, kind, details string) string {
	scope := "You may change settings AND code. Follow the Fix loop in CLAUDE.md: diagnose, fix (smallest change, test where possible), " +
		"rebuild and restart with scripts/dev-rebuild.ps1, start the character with koolo_control, verify with koolo_logs and koolo_status, " +
		"and commit on the current branch (already created for you). Do not push. Do not switch branches."
	if mode == "settings" {
		scope = "SETTINGS ONLY: do not edit code files. Diagnose with the koolo tools and fix with koolo_apply_change or koolo_reload_config, " +
			"then start the character with koolo_control and verify. If only a code change would fix it, say so and stop."
	}
	return "/fix-koolo The koolo-extras watchdog detected a problem (" + kind + "). Nobody is watching: work autonomously, then end with " +
		"a short report: cause, change, verification result.\n" + scope + "\nOffline characters only.\n\nDetails:\n" + details
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…(truncated)"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
