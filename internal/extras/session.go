package extras

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Session turns each play session into a folder the d2live video pipeline understands
// (d2live-edit write / render): log.txt, meta.json, keyframes/, PROMPT.md, overlay.schema.json.
// A session starts when a character starts playing and ends after IdleMinutesToEnd without playing.
// With OBS enabled it also starts/stops the recording so video and log line up.

type Recorder interface {
	Start() error
	Stop() (outputPath string, err error)
}

type Session struct {
	OBS Recorder // optional

	active      bool
	start       time.Time
	lastActive  time.Time
	dir         string
	lines       []string
	keyframes   []map[string]any
	lastShot    time.Time
	lastEventID int
	videoPath   string
}

func (s *Session) Name() string { return "session" }

func (s *Session) Run(ctx context.Context, env *Env) error {
	subs := []<-chan Event{env.Bus.Subscribe(EvNewGame), env.Bus.Subscribe(EvMilestone), env.Bus.Subscribe(EvAlert)}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer s.end(env) // close the session if extras stop
	for {
		select {
		case <-ctx.Done():
			return nil
		case e := <-subs[0]:
			s.log(env, "GAME FINISHED: "+e.Text)
		case e := <-subs[1]:
			s.log(env, "MILESTONE: "+e.Text)
		case e := <-subs[2]:
			s.log(env, "ALERT: "+firstLine(e.Text))
		case <-ticker.C:
			s.Tick(ctx, env)
		}
	}
}

// Tick checks whether a session starts/ends and collects brain events and screenshots (exported for tests).
func (s *Session) Tick(ctx context.Context, env *Env) {
	st, err := GetStatus(ctx, env.Koolo)
	playing := false
	if err == nil {
		for _, cs := range st.Characters {
			if cs.Status == "In game" {
				playing = true
			}
		}
	}
	now := env.Now()
	if playing {
		s.lastActive = now
		if !s.active {
			s.begin(env)
		}
	} else if s.active && now.Sub(s.lastActive) >= time.Duration(max(1, env.Cfg.Session.IdleMinutesToEnd))*time.Minute {
		s.end(env)
		return
	}
	if !s.active || err != nil {
		return
	}
	// Brain events (deaths, items, plans, thoughts) since the last look.
	if txt, err := env.Koolo.Call(ctx, "koolo_events", map[string]any{"sinceId": s.lastEventID}); err == nil {
		var evs []struct {
			ID   int       `json:"id"`
			Time time.Time `json:"time"`
			Kind string    `json:"kind"`
			Text string    `json:"text"`
		}
		if json.Unmarshal([]byte(txt), &evs) == nil {
			for _, e := range evs {
				s.lastEventID = max(s.lastEventID, e.ID)
				s.lines = append(s.lines, fmt.Sprintf("[%s] %s: %s", ts(e.Time.Sub(s.start)), strings.ToUpper(e.Kind), e.Text))
			}
		}
	}
	every := time.Duration(max(15, env.Cfg.Session.ScreenshotEverySeconds)) * time.Second
	if now.Sub(s.lastShot) >= every {
		s.lastShot = now
		if img, err := env.Koolo.Image(ctx, "koolo_screenshot", nil); err == nil && len(img) > 0 {
			t := now.Sub(s.start).Seconds()
			name := fmt.Sprintf("%07.1fs.jpg", t)
			if os.WriteFile(filepath.Join(s.dir, "keyframes", name), img, 0o644) == nil {
				s.keyframes = append(s.keyframes, map[string]any{"t": round1(t), "file": "keyframes/" + name})
			}
		}
	}
	s.flush(env)
}

func (s *Session) begin(env *Env) {
	now := env.Now()
	s.active, s.start, s.lastActive = true, now, now
	s.lines, s.keyframes, s.videoPath = nil, nil, ""
	s.dir = filepath.Join(filepath.Dir(env.Store.Dir), "sessions", now.Format("2006-01-02_15-04-05"))
	_ = os.MkdirAll(filepath.Join(s.dir, "keyframes"), 0o755)
	if s.OBS != nil {
		if err := s.OBS.Start(); err != nil {
			env.Bus.Publish(Event{Kind: EvAlert, Text: "Session started, but OBS recording could not be started: " + err.Error()})
		}
	}
	s.log(env, "SESSION START")
}

func (s *Session) end(env *Env) {
	if !s.active {
		return
	}
	s.log(env, "SESSION END")
	if s.OBS != nil {
		if p, err := s.OBS.Stop(); err == nil {
			s.videoPath = p
		}
	}
	s.flush(env)
	s.active = false
	env.Bus.Publish(Event{Kind: EvAlert, Text: fmt.Sprintf("🎬 Session saved: %s (%s). Make the video with: d2live-edit write --session \"%s\" then d2live-edit render --session \"%s\"",
		s.dir, ts(s.lastActive.Sub(s.start)), s.dir, s.dir)})
}

func (s *Session) log(env *Env, text string) {
	if !s.active {
		return
	}
	s.lines = append(s.lines, fmt.Sprintf("[%s] %s", ts(env.Now().Sub(s.start)), text))
}

func (s *Session) flush(env *Env) {
	if s.dir == "" {
		return
	}
	secs := s.lastActive.Sub(s.start).Seconds() + 5
	meta := map[string]any{"video": s.videoPath, "video_seconds": round1(secs), "video_start": float64(s.start.UnixMilli()) / 1000,
		"turns": len(s.lines), "keyframes": s.keyframes, "model": "koolo"}
	b, _ := json.MarshalIndent(meta, "", "  ")
	_ = os.WriteFile(filepath.Join(s.dir, "meta.json"), b, 0o644)
	_ = os.WriteFile(filepath.Join(s.dir, "log.txt"), []byte(strings.Join(s.lines, "\n")), 0o644)
	_ = os.WriteFile(filepath.Join(s.dir, "overlay.schema.json"), []byte(overlaySchemaJSON), 0o644)
	_ = os.WriteFile(filepath.Join(s.dir, "PROMPT.md"), []byte(fmt.Sprintf(sessionPrompt, secs)), 0o644)
}

func ts(d time.Duration) string {
	sec := int(max(0, d.Seconds()))
	if sec >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", sec/3600, sec%3600/60, sec%60)
	}
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

const sessionPrompt = `You are the editor of a YouTube channel where an AI-driven bot (Koolo AI, with a Claude brain) plays
Diablo II: Resurrected by itself. log.txt is the timestamped log of one recording: games, deaths, items, plans,
milestones and the AI's thoughts. keyframes/ has screenshots named by video time.

Write overlay.json for this video, matching overlay.schema.json exactly:
- "title": a catchy episode title. "character": class/name if known.
- "commentary": the AI's inner voice on screen, first person, 1-2 sentences (max ~140 characters), funny but faithful.
  "t" = video second it appears, "d" = seconds it stays (4-10). One line every ~20-60 s while something happens.
- "objectives": when the goal/plan changes. "highlights": deaths, level ups, bosses, great items, fails, wins.
- "chapters": YouTube chapters, first at t=0.
The video is %.0f seconds long; all times must be inside it. Never invent events the log doesn't show.
`

const overlaySchemaJSON = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["title", "character", "chapters", "commentary", "objectives", "highlights"],
  "properties": {
    "title": {"type": "string"},
    "character": {"type": "string"},
    "chapters": {"type": "array", "items": {"type": "object", "additionalProperties": false, "required": ["t", "title"],
      "properties": {"t": {"type": "number"}, "title": {"type": "string"}}}},
    "commentary": {"type": "array", "items": {"type": "object", "additionalProperties": false, "required": ["t", "d", "text"],
      "properties": {"t": {"type": "number"}, "d": {"type": "number"}, "text": {"type": "string"}}}},
    "objectives": {"type": "array", "items": {"type": "object", "additionalProperties": false, "required": ["t", "text"],
      "properties": {"t": {"type": "number"}, "text": {"type": "string"}}}},
    "highlights": {"type": "array", "items": {"type": "object", "additionalProperties": false, "required": ["t", "kind", "title", "subtitle"],
      "properties": {"t": {"type": "number"}, "kind": {"type": "string", "enum": ["death","levelup","boss","item","fail","win","funny","close_call"]},
        "title": {"type": "string"}, "subtitle": {"type": "string"}}}}
  }
}`
