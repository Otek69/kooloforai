package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hectorgimenez/koolo/internal/brain"
	"github.com/hectorgimenez/koolo/internal/config"
	"github.com/hectorgimenez/koolo/internal/event"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Koolo's MCP server: lets Claude Code (or any MCP client) on THIS PC see and steer the running bot,
// so it can diagnose problems, change settings and verify fixes. Served at http://localhost:8087/mcp.
// Only loopback connections are accepted; the SDK also blocks DNS-rebinding attacks.

func (s *HttpServer) mcpHandler() http.Handler {
	srv := mcp.NewServer(&mcp.Implementation{Name: "koolo", Version: config.Version}, &mcp.ServerOptions{
		Instructions: "Tools for the Koolo AI Diablo II: Resurrected bot running on this PC (offline characters). " +
			"As the bot's brain: read koolo_events (since the last id you saw) and koolo_status, look at koolo_last_failure after deaths, " +
			"then set the next plan with koolo_brain_plan, save lessons with koolo_add_lesson and talk to viewers with koolo_say. " +
			"To debug: koolo_doctor and koolo_logs first, then koolo_status / koolo_game_state / koolo_screenshot. " +
			"Settings changes go through koolo_apply_change (validated). Code fixes: edit the Go source in this repo, " +
			"rebuild with scripts/dev-rebuild.ps1, then verify with these tools.",
	})
	s.addMCPTools(srv)
	s.addBrainTools(srv)
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{})
	return loopbackOnly(h)
}

func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || !net.ParseIP(host).IsLoopback() {
			http.Error(w, "the Koolo MCP server only accepts connections from this PC", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func textResult(format string, a ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, a...)}}}
}

func jsonResult(v any) *mcp.CallToolResult {
	b, _ := json.MarshalIndent(v, "", "  ")
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
}

type charIn struct {
	Character string `json:"character" jsonschema:"character (supervisor) name as in config/<name>; empty = the only/first one"`
}

func (s *HttpServer) pickCharacter(name string) (string, error) {
	if name != "" {
		if _, ok := config.Characters[name]; !ok {
			return "", fmt.Errorf("unknown character %q", name)
		}
		return name, nil
	}
	names := s.manager.AvailableSupervisors()
	if len(names) == 0 {
		return "", errors.New("no characters configured")
	}
	sort.Strings(names)
	return names[0], nil
}

func (s *HttpServer) addMCPTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{Name: "koolo_status", Description: "Status of every character: running/paused, games, deaths, chickens, errors, drops, plus the AI brain's goal, lessons and spend."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			type charStatus struct {
				Status   string `json:"status"`
				Details  string `json:"details,omitempty"`
				Running  string `json:"runningFor,omitempty"`
				Games    int    `json:"games"`
				Deaths   int    `json:"deaths"`
				Chickens int    `json:"chickens"`
				Errors   int    `json:"errors"`
				Drops    int    `json:"drops"`
				LastEnd  string `json:"lastGameResult,omitempty"`
			}
			out := map[string]any{"version": config.Version}
			chars := map[string]charStatus{}
			for _, name := range s.manager.AvailableSupervisors() {
				st := s.manager.Status(name)
				cs := charStatus{Status: string(st.SupervisorStatus), Details: st.Details, Games: len(st.Games), Drops: len(st.Drops)}
				if !st.StartedAt.IsZero() {
					cs.Running = time.Since(st.StartedAt).Round(time.Second).String()
				}
				for _, g := range st.Games {
					switch g.Reason {
					case event.FinishedDied:
						cs.Deaths++
					case event.FinishedChicken, event.FinishedMercChicken:
						cs.Chickens++
					case event.FinishedError:
						cs.Errors++
					}
				}
				if n := len(st.Games); n > 0 {
					cs.LastEnd = string(st.Games[n-1].Reason)
				}
				chars[name] = cs
			}
			out["characters"] = chars
			out["brain"] = brain.Default.View()
			return jsonResult(out), nil, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "koolo_game_state", Description: "Live game state of a running character read from memory: level, area, life, gear, inventory, stash, resistances."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in charIn) (*mcp.CallToolResult, any, error) {
			name, err := s.pickCharacter(in.Character)
			if err != nil {
				return nil, nil, err
			}
			d := s.liveData(name)
			if d == nil {
				return textResult("%s is not running or not in a game.", name), nil, nil
			}
			cfg := config.Characters[name]
			return textResult("%s\n%s", brain.TakeSnapshot(*d, name, cfg.Character.Class), brain.ItemReport(*d)), nil, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "koolo_screenshot", Description: "Screenshot of a running character's game window right now."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in charIn) (*mcp.CallToolResult, any, error) {
			name, err := s.pickCharacter(in.Character)
			if err != nil {
				return nil, nil, err
			}
			c := s.manager.GetContext(name)
			if c == nil || c.GameReader == nil {
				return textResult("%s is not running.", name), nil, nil
			}
			img := c.GameReader.Screenshot()
			if img == nil {
				return textResult("Could not capture the game window."), nil, nil
			}
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
				return nil, nil, err
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: buf.Bytes(), MIMEType: "image/jpeg"}}}, nil, nil
		})

	type logsIn struct {
		Lines      int    `json:"lines,omitempty" jsonschema:"how many lines from the end (default 150, max 2000)"`
		ErrorsOnly bool   `json:"errorsOnly,omitempty" jsonschema:"only WARN/ERROR lines"`
		Character  string `json:"character,omitempty" jsonschema:"read this character's supervisor log instead of the main Koolo log"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "koolo_logs", Description: "Tail of the newest Koolo log (or a character's supervisor log)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in logsIn) (*mcp.CallToolResult, any, error) {
			dir := config.Koolo.LogSaveDirectory
			if dir == "" {
				dir = "logs"
			}
			pattern := "Koolo-log-*.txt"
			if in.Character != "" {
				pattern = "Supervisor-log-" + in.Character + "-*.txt"
			}
			file := newestFile(filepath.Join(dir, pattern))
			if file == "" {
				return textResult("No log files matching %s in %s.", pattern, dir), nil, nil
			}
			raw, err := os.ReadFile(file)
			if err != nil {
				return nil, nil, err
			}
			lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
			if in.ErrorsOnly {
				var keep []string
				for _, l := range lines {
					if strings.Contains(l, "level=ERROR") || strings.Contains(l, "level=WARN") {
						keep = append(keep, l)
					}
				}
				lines = keep
			}
			n := in.Lines
			if n <= 0 {
				n = 150
			}
			n = min(n, 2000)
			if len(lines) > n {
				lines = lines[len(lines)-n:]
			}
			return textResult("%s (last %d lines)\n%s", file, len(lines), strings.Join(lines, "\n")), nil, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "koolo_crash_reports", Description: "List crash reports and show the newest one."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			files, _ := filepath.Glob(filepath.Join("crash-reports", "*.txt"))
			if len(files) == 0 {
				return textResult("No crash reports."), nil, nil
			}
			newest := newestFile(filepath.Join("crash-reports", "*.txt"))
			raw, _ := os.ReadFile(newest)
			text := string(raw)
			if len(text) > 60000 {
				text = text[:60000] + "\n... (truncated)"
			}
			return textResult("%d crash report(s). Newest: %s\n\n%s", len(files), newest, text), nil, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "koolo_doctor", Description: "Run Koolo Doctor: checks settings, characters, pickit, AI brain, the D2R window and a step-by-step memory-reading test. Takes up to a minute."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			exe, _ := os.Executable()
			doctor := filepath.Join(filepath.Dir(exe), "koolo-doctor.exe")
			if _, err := os.Stat(doctor); err != nil {
				return textResult("koolo-doctor.exe not found next to koolo.exe."), nil, nil
			}
			cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			out, err := exec.CommandContext(cctx, doctor, "--no-elevate", "--no-wait").CombinedOutput()
			if err != nil && len(out) == 0 {
				return nil, nil, fmt.Errorf("running the doctor failed: %w", err)
			}
			return textResult("%s", out), nil, nil
		})

	type controlIn struct {
		Character string `json:"character" jsonschema:"character name"`
		Action    string `json:"action" jsonschema:"start, stop or pause (pause toggles pause/resume)"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "koolo_control", Description: "Start, stop or pause/resume a character's bot. Use only with offline characters."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in controlIn) (*mcp.CallToolResult, any, error) {
			name, err := s.pickCharacter(in.Character)
			if err != nil {
				return nil, nil, err
			}
			switch in.Action {
			case "start":
				if err := s.manager.Start(name, false); err != nil {
					return nil, nil, err
				}
			case "stop":
				s.manager.Stop(name)
			case "pause":
				s.manager.TogglePause(name)
			default:
				return nil, nil, fmt.Errorf("unknown action %q (use start, stop or pause)", in.Action)
			}
			return textResult("%s: %s done.", name, in.Action), nil, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "koolo_reload_config", Description: "Reload all config files from disk and apply them to running characters."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			if err := s.manager.ReloadConfig(); err != nil {
				return nil, nil, err
			}
			return textResult("Config reloaded."), nil, nil
		})

	type applyIn struct {
		Character string         `json:"character" jsonschema:"character name"`
		Proposal  brain.Proposal `json:"proposal" jsonschema:"kind pickit_rule (pickitRule), runs (runs), chicken_on_curses (curses) or goal (goal)"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "koolo_apply_change", Description: "Apply a validated settings change: a NIP pickit rule, a new run list, the curse-chicken list or the AI goal."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in applyIn) (*mcp.CallToolResult, any, error) {
			name, err := s.pickCharacter(in.Character)
			if err != nil {
				return nil, nil, err
			}
			msg, err := s.applyProposal(name, config.Characters[name], in.Proposal)
			if err != nil {
				return nil, nil, err
			}
			s.logger.Info("MCP change applied: " + msg)
			return textResult("%s", msg), nil, nil
		})
}

// Brain tools: with provider "desktop" in config/brain.yaml, Claude Desktop is the brain and uses these.
func (s *HttpServer) addBrainTools(srv *mcp.Server) {
	type eventsIn struct {
		SinceID int `json:"sinceId,omitempty" jsonschema:"only events with a higher id (use the last id you saw); 0 = all recent"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "koolo_events", Description: "What happened in the game since you last looked: new games, runs started/finished, items stashed, deaths, chickens, plan changes. Each event has an id."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in eventsIn) (*mcp.CallToolResult, any, error) {
			ev := brain.Default.Events(in.SinceID)
			if ev == nil {
				return textResult("No events (the AI brain is disabled in config/brain.yaml, or nothing happened yet)."), nil, nil
			}
			return jsonResult(ev), nil, nil
		})

	type planIn struct {
		Character string                  `json:"character" jsonschema:"character name"`
		Runs      []string                `json:"runs" jsonschema:"ordered runs for the next games, chosen from the character's enabled runs"`
		Goal      string                  `json:"goal,omitempty" jsonschema:"long-term goal in one sentence"`
		Thought   string                  `json:"thought,omitempty" jsonschema:"1-2 sentences for the stream overlay explaining the plan"`
		Survival  *brain.SurvivalSettings `json:"survival,omitempty" jsonschema:"optional potion/chicken thresholds in percent of life (clamped to safe ranges)"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "koolo_brain_plan", Description: "Set the plan Koolo follows from the next game: run order, goal, optional survival thresholds, and a thought for the overlay."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in planIn) (*mcp.CallToolResult, any, error) {
			name, err := s.pickCharacter(in.Character)
			if err != nil {
				return nil, nil, err
			}
			msg, err := brain.Default.SetDesktopPlan(config.Characters[name], in.Runs, in.Goal, in.Survival)
			if err != nil {
				return nil, nil, err
			}
			if in.Thought != "" {
				_ = brain.Default.Say(in.Thought)
			}
			return textResult("%s", msg), nil, nil
		})

	type sayIn struct {
		Text string `json:"text" jsonschema:"1-2 sentences of commentary, first person, for the stream overlay"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "koolo_say", Description: "Show a thought on the stream overlay (brain/overlay.txt)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in sayIn) (*mcp.CallToolResult, any, error) {
			if err := brain.Default.Say(in.Text); err != nil {
				return nil, nil, err
			}
			return textResult("Shown on the overlay."), nil, nil
		})

	type lessonIn struct {
		Text string `json:"text" jsonschema:"one short, concrete rule for the future (e.g. which run to avoid until which level)"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "koolo_add_lesson", Description: "Save a lesson in the AI's long-term memory (shown in koolo_status and used in future plans)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in lessonIn) (*mcp.CallToolResult, any, error) {
			if err := brain.Default.AddLesson(in.Text); err != nil {
				return nil, nil, err
			}
			return textResult("Lesson saved."), nil, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "koolo_last_failure", Description: "Screenshot and description of the most recent death or chicken."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			jpg, text := brain.Default.LastFailure()
			if text == "" {
				return textResult("No death or chicken recorded this session."), nil, nil
			}
			content := []mcp.Content{&mcp.TextContent{Text: text}}
			if len(jpg) > 0 {
				content = append(content, &mcp.ImageContent{Data: jpg, MIMEType: "image/jpeg"})
			}
			return &mcp.CallToolResult{Content: content}, nil, nil
		})
}

func newestFile(pattern string) string {
	files, _ := filepath.Glob(pattern)
	var best string
	var bestT time.Time
	for _, f := range files {
		if st, err := os.Stat(f); err == nil && st.ModTime().After(bestT) {
			best, bestT = f, st.ModTime()
		}
	}
	return best
}
