package brain

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"log/slog"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/hectorgimenez/koolo/internal/config"
	"github.com/hectorgimenez/koolo/internal/event"
)

// systemPromptWithRoute adds the speedrun route knowledge for the big decisions.
func (b *Brain) systemPromptWithRoute(task string) string {
	if !b.cfg.UsePlaybook {
		return b.systemPrompt(task)
	}
	return b.systemPrompt(task) + "\n\nGAME KNOWLEDGE (from top speedrunners; Koolo already handles map navigation, use this for strategy):\n" + playbook
}

func (b *Brain) systemPrompt(task string) string {
	return b.cfg.Persona + `

You control the strategy of a Diablo II: Resurrected character in offline single player.
A scripted bot carries out your decisions in real time (moving, fighting, potions, looting);
you decide what to do and explain it to the viewers.

How to play well:
- Treat your run history as evidence. A run with repeated deaths or chickens at the current level is too
  dangerous right now: skip it or move it later, and come back after leveling or better gear.
- Runs that succeed reliably and drop items are worth repeating. Mix in variety so the stream stays fun.
- Follow the lessons you wrote down earlier unless the situation has clearly changed.
- Low resistances (below about 0 in Nightmare or 30 in Hell after the penalty) make elemental bosses deadly.

Rules for what you write:
- "thought" is shown on screen next to the gameplay. Keep it to 1-3 short sentences.
- Talk like a player thinking out loud, in first person. No markdown, no emojis, no lists.
- Use only facts from the information you are given; never invent items or numbers.

` + task
}

type gamePlan struct {
	Thought  string            `json:"thought"`
	Goal     string            `json:"goal"`
	Runs     []string          `json:"runs"`
	Survival *SurvivalSettings `json:"survival,omitempty"`
}

// PlanGame runs before every game. The smart model chooses which of the
// enabled runs to do and in what order, updates its long-term goal and, if
// allowed, tunes potion/chicken thresholds. It only ever returns runs from the
// user's enabled list; on any error it returns that list unchanged.
func (b *Brain) PlanGame(ctx context.Context, snap Snapshot, cfg *config.CharacterCfg) []config.Run {
	allowed := cfg.Game.Runs
	if !b.Enabled() || len(allowed) == 0 {
		return allowed
	}
	b.mu.Lock()
	b.last = snap
	b.mu.Unlock()
	b.mem.mu.Lock()
	b.mem.Games++
	b.mem.mu.Unlock()

	names := make([]string, 0, len(allowed))
	allowedSet := map[string]config.Run{}
	for _, r := range allowed {
		if _, dup := allowedSet[string(r)]; !dup {
			names = append(names, string(r))
			allowedSet[string(r)] = r
		}
	}

	props := map[string]any{
		"thought": map[string]any{"type": "string"},
		"goal":    map[string]any{"type": "string"},
		"runs": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "string", "enum": names},
		},
	}
	required := []string{"thought", "goal", "runs"}
	survivalTask := ""
	if b.cfg.TuneSurvival {
		intProp := map[string]any{"type": "integer"}
		props["survival"] = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"healingPotionAt": intProp, "rejuvPotionAtLife": intProp, "chickenAt": intProp,
				"mercHealingPotionAt": intProp, "mercChickenAt": intProp,
			},
			"required":             []string{"healingPotionAt", "rejuvPotionAtLife", "chickenAt", "mercHealingPotionAt", "mercChickenAt"},
			"additionalProperties": false,
		}
		required = append(required, "survival")
		survivalTask = " Also set \"survival\": the life percentages at which the bot drinks potions and leaves the game (chicken)." +
			" Play safer after deaths, and more aggressively when chickens are wasting runs that would have been fine. " + boundsText() +
			" Current settings: " + currentSurvival(cfg).String() + "."
	}
	schema, _ := json.Marshal(map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             required,
		"additionalProperties": false,
	})

	limit := ""
	if b.cfg.MaxRunsPerGame > 0 {
		limit = fmt.Sprintf(" Choose at most %d runs.", b.cfg.MaxRunsPerGame)
	}
	task := "A new game is starting. Choose which runs to do this game and in what order." + limit +
		" \"goal\" is your long-term objective in one sentence (keep it, or update it when you achieve it or it stops making sense)." +
		survivalTask +
		" \"thought\" explains your plan to the viewers."

	prompt := fmt.Sprintf("Game state:\n%s\n%s\nThis session so far:\n%s\n\nRuns you may choose from: %s",
		snap, b.mem.Summary(), b.recentHistory(), strings.Join(names, ", "))

	text, err := b.call(ctx, callOpts{
		model:     b.cfg.SmartModel,
		system:    b.systemPromptWithRoute(task),
		text:      prompt,
		maxTokens: 8000,
		effort:    anthropic.BetaOutputConfigEffortMedium,
		schema:    schema,
	})
	if err != nil {
		b.fallbackNotice(err)
		b.saveMemory()
		return allowed
	}

	var plan gamePlan
	if err := json.Unmarshal([]byte(text), &plan); err != nil {
		b.logger.Warn("brain: could not parse game plan", slog.String("text", text), slog.Any("error", err))
		b.saveMemory()
		return allowed
	}

	var out []config.Run
	seen := map[string]bool{}
	for _, name := range plan.Runs {
		r, ok := allowedSet[name]
		if !ok || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, r)
		if b.cfg.MaxRunsPerGame > 0 && len(out) >= b.cfg.MaxRunsPerGame {
			break
		}
	}
	if len(out) == 0 {
		out = allowed
	}

	b.mem.mu.Lock()
	goalChanged := plan.Goal != "" && plan.Goal != b.mem.Goal
	if plan.Goal != "" {
		b.mem.Goal = strings.TrimSpace(plan.Goal)
	}
	b.mem.mu.Unlock()

	b.say("plan", plan.Thought)
	if goalChanged {
		b.say("goal", "New goal: "+plan.Goal)
	}

	if b.cfg.TuneSurvival && plan.Survival != nil {
		before := currentSurvival(cfg)
		after := plan.Survival.clamped()
		if after != before {
			applySurvival(cfg, after)
			b.mem.mu.Lock()
			b.mem.Survival = &after
			b.mem.mu.Unlock()
			b.say("survival", fmt.Sprintf("Adjusting survival: potion at %d%%, chicken at %d%%.", after.HealingPotionAt, after.ChickenAt))
		}
	}

	b.saveMemory()
	return out
}

// HandleEvent is registered on Koolo's event listener. It never blocks: API
// calls happen on the brain's own goroutine.
func (b *Brain) HandleEvent(_ context.Context, e event.Event) error {
	if !b.Enabled() {
		return nil
	}
	switch ev := e.(type) {
	case event.RunStartedEvent:
		b.mu.Lock()
		b.curRun = ev.RunName
		b.mu.Unlock()
		b.say("run", "Starting "+prettyRun(ev.RunName)+".")

	case event.RunFinishedEvent:
		b.mu.Lock()
		level := b.last.Level
		b.mu.Unlock()
		b.mem.recordRun(ev.RunName, string(ev.Reason), level)
		b.saveMemory()
		if ev.Reason == event.FinishedOK {
			b.async(func() {
				b.comment("run_finished", fmt.Sprintf("You just finished the %s run successfully.", prettyRun(ev.RunName)))
			})
		}

	case event.ItemStashedEvent:
		b.mu.Lock()
		run := b.curRun
		b.mu.Unlock()
		b.mem.recordItem(run)
		if b.cfg.CommentOnItems {
			desc := describeItem(ev.Item.Item)
			b.async(func() {
				b.comment("item", "You just found and stashed this item: "+desc+". React to it, and say whether it could be an upgrade for you.")
			})
		}

	case event.GameFinishedEvent:
		switch ev.Reason {
		case event.FinishedDied, event.FinishedChicken, event.FinishedMercChicken:
			img := ev.Image()
			msg := ev.Message()
			reason := string(ev.Reason)
			b.mu.Lock()
			run := b.curRun
			b.mu.Unlock()
			b.async(func() { b.analyseFailure(reason, msg, run, img) })
		}
	}
	return nil
}

// comment gets a short reaction from the fast model.
func (b *Brain) comment(kind, what string) {
	b.mu.Lock()
	snap := b.last
	b.mu.Unlock()

	prompt := fmt.Sprintf("Game state at the start of this game:\n%s\nThis session so far:\n%s\n\nWhat just happened: %s",
		snap, b.recentHistory(), what)
	text, err := b.call(context.Background(), callOpts{
		model:     b.cfg.FastModel,
		system:    b.systemPrompt("React to what just happened in one or two short sentences."),
		text:      prompt,
		maxTokens: 2000,
		effort:    anthropic.BetaOutputConfigEffortLow,
		timeout:   30 * time.Second,
	})
	if err != nil {
		b.fallbackNotice(err)
		return
	}
	b.say(kind, text)
}

type failureReview struct {
	Thought string `json:"thought"`
	Lesson  string `json:"lesson"`
}

// analyseFailure asks the smart model what went wrong (with the screenshot if
// there is one) and stores a lesson that future planning will read.
func (b *Brain) analyseFailure(reason, message, run string, img image.Image) {
	b.mu.Lock()
	snap := b.last
	b.mu.Unlock()

	what := map[string]string{
		string(event.FinishedDied):        "The character DIED.",
		string(event.FinishedChicken):     "The character's life got dangerously low, so the bot left the game to survive (a 'chicken').",
		string(event.FinishedMercChicken): "The mercenary's life got dangerously low, so the bot left the game.",
	}[reason]
	if run != "" {
		what += " It happened during the " + prettyRun(run) + " run."
	}

	jpg, _ := encodeJPEG(img)
	prompt := fmt.Sprintf(
		"%s Bot message: %q\nGame state at the start of this game:\n%s\n%s\nThis session so far:\n%s\n"+
			"The screenshot (if any) is the moment it happened.",
		what, message, snap, b.mem.Summary(), b.recentHistory())

	schema, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"thought": map[string]any{"type": "string"},
			"lesson":  map[string]any{"type": "string"},
		},
		"required":             []string{"thought", "lesson"},
		"additionalProperties": false,
	})

	text, err := b.call(context.Background(), callOpts{
		model: b.cfg.SmartModel,
		system: b.systemPromptWithRoute("\"thought\": explain to viewers, in two or three sentences, what most likely went wrong and what you will change. Be honest and a little dramatic." +
			" \"lesson\": one short, concrete rule for your future self (for example which run to avoid until which level, or what resistance to fix). Leave it empty if there is nothing new to learn."),
		text:      prompt,
		imageJPEG: jpg,
		maxTokens: 8000,
		effort:    anthropic.BetaOutputConfigEffortMedium,
		schema:    schema,
	})
	if err != nil {
		b.fallbackNotice(err)
		return
	}
	var review failureReview
	if err := json.Unmarshal([]byte(text), &review); err != nil {
		b.say("failure", text)
		return
	}
	b.say("failure", review.Thought)
	if review.Lesson != "" {
		b.mem.addLesson(review.Lesson, snap.Level)
		b.saveMemory()
		b.say("lesson", "Lesson learned: "+review.Lesson)
	}
}

func (b *Brain) saveMemory() {
	if b.mem == nil || b.mem.path == "" {
		return
	}
	if err := b.mem.save(); err != nil {
		b.logger.Warn("brain: saving memory", slog.Any("error", err))
	}
}

func (b *Brain) fallbackNotice(err error) {
	if errors.Is(err, ErrBudget) {
		b.mu.Lock()
		already := len(b.history) > 0 && strings.HasPrefix(b.history[len(b.history)-1], "system: Budget")
		b.mu.Unlock()
		if !already {
			b.say("system", fmt.Sprintf("Budget of $%.2f reached. The brain is resting; the bot keeps playing your normal run list.", b.cfg.MaxSessionUSD))
		}
		return
	}
	b.logger.Warn("brain: API call failed, bot continues without it", slog.Any("error", err))
}

func encodeJPEG(img image.Image) (string, error) {
	if img == nil {
		return "", nil
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 75}); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func prettyRun(name string) string {
	return strings.ReplaceAll(name, "_", " ")
}
