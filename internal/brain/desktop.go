package brain

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hectorgimenez/koolo/internal/brain/inbox"
	"github.com/hectorgimenez/koolo/internal/config"
)

// Questions from the assistant page wait here for Claude Desktop (it reads them via koolo_owner_questions).
var desktopInbox inbox.Inbox[Proposal]

// askDesktop queues the owner's question for Claude Desktop instead of calling an API.
func (b *Brain) askDesktop(charName, question string) AssistReply {
	id := desktopInbox.Ask(charName, question)
	b.addEvent("question", fmt.Sprintf("Owner asked (question %d, %s): %s", id, charName, question))
	return AssistReply{
		QuestionID: id,
		Reply: "Sent to Claude Desktop (desktop mode makes no API calls). Open Claude Desktop and say: " +
			"\"answer my Koolo questions\". Its answer appears here.",
	}
}

// PendingQuestions returns the owner's unanswered questions (for Claude Desktop).
func PendingQuestions() []inbox.Question[Proposal] { return desktopInbox.Pending() }

// AnswerQuestion stores Claude Desktop's answer; the assistant page shows it with Apply buttons.
func (b *Brain) AnswerQuestion(id int, text string, proposals []Proposal) error {
	if err := desktopInbox.Answer(id, text, proposals); err != nil {
		return err
	}
	b.addEvent("answer", fmt.Sprintf("Claude Desktop answered question %d", id))
	return nil
}

// Question returns one question with its answer, if any.
func Question(id int) (inbox.Question[Proposal], bool) { return desktopInbox.Get(id) }

// Desktop mode (provider: desktop): the Claude Desktop app is the brain. It cannot be called by Koolo; instead it
// connects to Koolo's MCP server when the user talks to it, reads what happened (events, stats, game state) and
// sets the plan that Koolo follows from the next game on. No API key and no per-call cost: it uses the Claude plan.

var errDesktopMode = errors.New("brain: desktop mode makes no API calls")

// DesktopPlan is what Claude Desktop decides; Koolo applies it at the start of every game until it changes.
type DesktopPlan struct {
	Runs     []string          `json:"runs"`
	Goal     string            `json:"goal,omitempty"`
	Survival *SurvivalSettings `json:"survival,omitempty"`
	SetAt    time.Time         `json:"setAt"`
}

// Event is something that happened in the game, kept for Claude Desktop to read via MCP.
type Event struct {
	ID   int       `json:"id"`
	Time time.Time `json:"time"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
}

const maxEvents = 300

func (b *Brain) IsDesktop() bool { return b.Enabled() && b.desktop }

func (b *Brain) addEvent(kind, text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.eventID++
	b.events = append(b.events, Event{ID: b.eventID, Time: time.Now(), Kind: kind, Text: text})
	if len(b.events) > maxEvents {
		b.events = b.events[len(b.events)-maxEvents:]
	}
}

// Events returns events with an ID greater than sinceID (0 = all kept events).
func (b *Brain) Events(sinceID int) []Event {
	if !b.Enabled() {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Event
	for _, e := range b.events {
		if e.ID > sinceID {
			out = append(out, e)
		}
	}
	return out
}

// SetDesktopPlan validates and stores Claude Desktop's plan. Runs must be among the character's enabled runs.
func (b *Brain) SetDesktopPlan(cfg *config.CharacterCfg, runs []string, goal string, survival *SurvivalSettings) (string, error) {
	if !b.Enabled() {
		return "", ErrDisabled
	}
	enabled := map[string]bool{}
	for _, r := range cfg.Game.Runs {
		enabled[string(r)] = true
	}
	var clean []string
	for _, r := range runs {
		r = strings.TrimSpace(r)
		if !enabled[r] {
			return "", fmt.Errorf("run %q is not enabled for this character (enabled: %v); enable it in Koolo's settings or with koolo_apply_change first", r, cfg.Game.Runs)
		}
		clean = append(clean, r)
	}
	plan := &DesktopPlan{Runs: clean, Goal: strings.TrimSpace(goal), SetAt: time.Now()}
	msg := fmt.Sprintf("Plan set: runs %v", clean)
	if survival != nil {
		s := survival.clamped()
		plan.Survival = &s
		msg += "; survival " + s.String()
	}
	b.mu.Lock()
	b.plan = plan
	b.mu.Unlock()
	if plan.Goal != "" {
		b.mem.mu.Lock()
		b.mem.Goal = plan.Goal
		b.mem.mu.Unlock()
		msg += "; goal: " + plan.Goal
	}
	b.saveMemory()
	b.addEvent("plan", msg)
	return msg + ". Koolo applies it from the next game.", nil
}

// Say shows a line on the stream overlay (used by Claude Desktop for commentary).
func (b *Brain) Say(text string) error {
	if !b.Enabled() {
		return ErrDisabled
	}
	b.say("desktop", text)
	return nil
}

// AddLesson stores a lesson in the AI's long-term memory.
func (b *Brain) AddLesson(text string) error {
	if !b.Enabled() {
		return ErrDisabled
	}
	b.mu.Lock()
	level := b.last.Level
	b.mu.Unlock()
	b.mem.addLesson(text, level)
	b.saveMemory()
	return nil
}

// LastFailure returns the screenshot (JPEG) and description of the most recent death or chicken.
func (b *Brain) LastFailure() ([]byte, string) {
	if !b.Enabled() {
		return nil, ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failureJPEG, b.failureText
}

// applyDesktopPlan is PlanGame for desktop mode: follow Claude Desktop's latest plan, else the normal run list.
func (b *Brain) applyDesktopPlan(cfg *config.CharacterCfg) []config.Run {
	b.mu.Lock()
	plan := b.plan
	b.mu.Unlock()
	allowed := cfg.Game.Runs
	if plan == nil || len(plan.Runs) == 0 {
		return allowed
	}
	if b.cfg.TuneSurvival && plan.Survival != nil && currentSurvival(cfg) != *plan.Survival {
		applySurvival(cfg, *plan.Survival)
	}
	enabled := map[string]config.Run{}
	for _, r := range allowed {
		enabled[string(r)] = r
	}
	var out []config.Run
	for _, name := range plan.Runs {
		if r, ok := enabled[name]; ok {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return allowed
	}
	return out
}
