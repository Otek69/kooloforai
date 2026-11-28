package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/hectorgimenez/d2go/pkg/data"
	"github.com/hectorgimenez/d2go/pkg/data/item"
	"github.com/hectorgimenez/d2go/pkg/data/stat"
	"github.com/hectorgimenez/koolo/internal/config"
	"github.com/hectorgimenez/koolo/internal/health"
)

// The assistant powers the "AI Assistant" page in Koolo's control panel: you
// ask in plain words, Claude answers using your character's real data and can
// propose changes as cards. Nothing changes until you press Apply.

type ChatTurn struct {
	Role string `json:"role"` // "user" or "assistant"
	Text string `json:"text"`
}

// Proposal is one change the assistant suggests. Kind decides which field is used.
type Proposal struct {
	Kind       string   `json:"kind"` // pickit_rule | runs | chicken_on_curses | goal
	Title      string   `json:"title"`
	Why        string   `json:"why"`
	PickitRule string   `json:"pickitRule"`
	Runs       []string `json:"runs"`
	Curses     []string `json:"curses"`
	Goal       string   `json:"goal"`
}

type AssistReply struct {
	Reply     string     `json:"reply"`
	Proposals []Proposal `json:"proposals"`
	SpentUSD  float64    `json:"spentUSD"`
	// QuestionID is set in desktop mode: the answer comes later from Claude Desktop (poll /api/assistant/answer).
	QuestionID int `json:"questionId,omitempty"`
}

var ErrDisabled = errors.New("the AI brain is disabled: set enabled: true in config/brain.yaml and add an API key")

const nipGuide = `Pickit rules use the NIP format, one rule per line:
  [name] == ItemName && [quality] == unique # [stat] >= value
Examples:
  [name] == shako && [quality] == unique
  [type] == ring && [quality] == rare # [fcr] >= 10 && [tohit] >= 100
  [name] == berrune
  [name] == broadsword && [quality] == normal && [flag] != ethereal # [sockets] == 4
  [type] == smallcharm && [quality] == magic # [maxhp] >= 20
Item names are lowercase without spaces (e.g. berrune, monarch, archonplate, smallcharm).
Use [type] for item classes (ring, amulet, helm, armor, shield, smallcharm, grandcharm, jewel).
Only propose rules you are confident are valid NIP syntax.`

// Assist answers one message from the assistant page.
func (b *Brain) Assist(ctx context.Context, charName string, cfg *config.CharacterCfg, d *data.Data, history []ChatTurn, question string) (AssistReply, error) {
	if !b.Enabled() {
		return AssistReply{}, ErrDisabled
	}
	if b.IsDesktop() {
		return b.askDesktop(charName, question), nil
	}

	allRuns := make([]string, 0, len(config.AvailableRuns))
	for r := range config.AvailableRuns {
		allRuns = append(allRuns, string(r))
	}
	sort.Strings(allRuns)
	enabled := make([]string, 0, len(cfg.Game.Runs))
	for _, r := range cfg.Game.Runs {
		enabled = append(enabled, string(r))
	}

	var state string
	if d != nil && d.PlayerUnit.Address != 0 {
		state = TakeSnapshot(*d, charName, cfg.Character.Class).String() + "\n" + ItemReport(*d)
	} else {
		state = fmt.Sprintf("Character %s (%s). The bot is not running, so live game data (gear, stash) is not available.\n", charName, cfg.Character.Class)
	}

	settings := fmt.Sprintf("Enabled runs (in order): %s\nAll runs Koolo supports: %s\nSurvival: %s\nChicken on curses/auras: %v (possible: %s)\n",
		strings.Join(enabled, ", "), strings.Join(allRuns, ", "), currentSurvival(cfg).String(),
		cfg.Health.ChickenOnCurses, strings.Join(curseNames(), ", "))

	var conv strings.Builder
	for _, t := range lastTurns(history, 8) {
		fmt.Fprintf(&conv, "%s: %s\n", t.Role, t.Text)
	}

	task := `You are now the setup assistant in the bot's control panel, talking to the channel owner (not to viewers).
Answer their message helpfully and concretely, using the character data below. Plain text, short paragraphs.
You can also suggest runeword plans: use the runes and socketed bases listed in the stash/inventory; only claim a runeword
is makeable if every rune and a base with the right socket count are present.
When a settings change would help, add it to "proposals" (the owner applies each one with a button):
- kind "pickit_rule": one NIP line in pickitRule. ` + "\n" + nipGuide + `
- kind "runs": the complete new ordered run list in runs (names from "All runs Koolo supports").
- kind "chicken_on_curses": the complete new list in curses.
- kind "goal": a new long-term goal for the AI in goal.
Give each proposal a short title and a one-sentence why. Leave unused fields empty. Use no proposals when none are needed.`

	prompt := fmt.Sprintf("Character data:\n%s\nCurrent settings:\n%s\nAI memory:\n%s\nConversation so far:\n%s\nOwner's new message: %s",
		state, settings, b.mem.Summary(), conv.String(), question)

	schema, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reply": map[string]any{"type": "string"},
			"proposals": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"kind":       map[string]any{"type": "string", "enum": []string{"pickit_rule", "runs", "chicken_on_curses", "goal"}},
						"title":      map[string]any{"type": "string"},
						"why":        map[string]any{"type": "string"},
						"pickitRule": map[string]any{"type": "string"},
						"runs":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"curses":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"goal":       map[string]any{"type": "string"},
					},
					"required":             []string{"kind", "title", "why", "pickitRule", "runs", "curses", "goal"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []string{"reply", "proposals"},
		"additionalProperties": false,
	})

	text, err := b.call(ctx, callOpts{
		model:     b.cfg.SmartModel,
		system:    b.systemPromptWithRoute(task),
		text:      prompt,
		maxTokens: 16000,
		effort:    anthropic.BetaOutputConfigEffortMedium,
		schema:    schema,
	})
	if err != nil {
		return AssistReply{}, err
	}
	var reply AssistReply
	if err := json.Unmarshal([]byte(text), &reply); err != nil {
		reply = AssistReply{Reply: text}
	}
	reply.SpentUSD, _ = b.Spent()
	return reply, nil
}

// SetGoal lets the owner set the AI's long-term goal from the assistant page.
func (b *Brain) SetGoal(goal string) {
	if !b.Enabled() {
		return
	}
	goal = strings.TrimSpace(goal)
	b.mem.mu.Lock()
	b.mem.Goal = goal
	b.mem.mu.Unlock()
	b.saveMemory()
	b.say("goal", "New goal from the boss: "+goal)
}

// MemoryView is what the assistant page shows about the AI's memory.
type MemoryView struct {
	Enabled  bool
	Goal     string
	Games    int
	Runs     []RunView
	Lessons  []Lesson
	Thoughts []string
	SpentUSD float64
	Calls    int
}

type RunView struct {
	Name string
	RunStat
	SuccessPct int
}

func (b *Brain) View() MemoryView {
	if !b.Enabled() {
		return MemoryView{}
	}
	v := MemoryView{Enabled: true}
	b.mem.mu.Lock()
	v.Goal, v.Games = b.mem.Goal, b.mem.Games
	for name, s := range b.mem.Runs {
		pct := 0
		if s.Attempts > 0 {
			pct = s.OK * 100 / s.Attempts
		}
		v.Runs = append(v.Runs, RunView{Name: name, RunStat: *s, SuccessPct: pct})
	}
	v.Lessons = append(v.Lessons, b.mem.Lessons...)
	b.mem.mu.Unlock()
	sort.Slice(v.Runs, func(i, j int) bool { return v.Runs[i].Attempts > v.Runs[j].Attempts })
	b.mu.Lock()
	v.Thoughts = append(v.Thoughts, b.overlay...)
	v.SpentUSD, v.Calls = b.spentUSD, b.calls
	b.mu.Unlock()
	return v
}

// ArmoryItem is one row on the Armory panel.
type ArmoryItem struct {
	Location string
	Name     string
	Quality  string
	Sockets  string
	Ethereal bool
}

// Armory lists everything the character has, grouped by location.
func Armory(d data.Data) []ArmoryItem {
	var out []ArmoryItem
	locs := []item.LocationType{item.LocationEquipped, item.LocationMercenary, item.LocationInventory, item.LocationCube, item.LocationStash, item.LocationSharedStash}
	for _, loc := range locs {
		for _, it := range d.Inventory.ByLocation(loc) {
			out = append(out, ArmoryItem{
				Location: string(loc),
				Name:     itemName(it),
				Quality:  it.Quality.ToString(),
				Sockets:  socketText(it),
				Ethereal: it.Ethereal,
			})
		}
	}
	return out
}

// ItemReport is the full item list as text for the model (gear, stash, runes, socketed bases).
func ItemReport(d data.Data) string {
	var b strings.Builder
	group := map[string][]string{}
	order := []string{}
	for _, a := range Armory(d) {
		if _, ok := group[a.Location]; !ok {
			order = append(order, a.Location)
		}
		line := a.Name + " [" + a.Quality
		if a.Ethereal {
			line += ", ethereal"
		}
		if a.Sockets != "" {
			line += ", " + a.Sockets
		}
		group[a.Location] = append(group[a.Location], line+"]")
	}
	for _, loc := range order {
		fmt.Fprintf(&b, "%s (%d): %s\n", loc, len(group[loc]), strings.Join(group[loc], "; "))
	}
	return b.String()
}

func itemName(it data.Item) string {
	if it.IsRuneword && it.RunewordName != "" {
		return fmt.Sprintf("%s (%s)", it.RunewordName, it.Name)
	}
	if it.Identified && it.IdentifiedName != "" && it.IdentifiedName != string(it.Name) {
		return fmt.Sprintf("%s (%s)", it.IdentifiedName, it.Name)
	}
	return string(it.Name)
}

func socketText(it data.Item) string {
	total, ok := it.FindStat(stat.NumSockets, 0)
	if !ok || total.Value == 0 {
		return ""
	}
	return fmt.Sprintf("%d/%d sockets filled", len(it.Sockets), total.Value)
}

func lastTurns(h []ChatTurn, n int) []ChatTurn {
	if len(h) > n {
		return h[len(h)-n:]
	}
	return h
}

func curseNames() []string {
	names := make([]string, 0, len(health.CurseStates))
	for n := range health.CurseStates {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
