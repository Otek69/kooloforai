// Package brain lets Claude make the high-level decisions for Koolo and
// narrate them for viewers. Koolo keeps doing all real-time play (movement,
// combat, potions, looting); the brain only:
//
//   - picks which of your enabled runs to do, and in what order, before each game
//   - comments on what happened (run finished, item found, death, chicken)
//   - looks at the death screenshot and explains what went wrong
//
// Every thought is written to an overlay text file for OBS and to a JSONL log
// for editing. Spending is tracked and capped per session.
package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"gopkg.in/yaml.v3"
)

// Config is loaded from config/brain.yaml.
type Config struct {
	Enabled bool `yaml:"enabled"`
	// APIKey is optional; when empty the ANTHROPIC_API_KEY environment variable is used.
	APIKey string `yaml:"apiKey"`
	// FastModel handles frequent, cheap commentary.
	FastModel string `yaml:"fastModel"`
	// SmartModel handles run planning and death analysis.
	SmartModel string `yaml:"smartModel"`
	// Persona is added to every prompt: the voice the AI uses on stream.
	Persona string `yaml:"persona"`
	// MaxSessionUSD stops all API calls once this much has been spent since Koolo started.
	MaxSessionUSD float64 `yaml:"maxSessionUSD"`
	// MaxRunsPerGame caps how many runs the brain may plan per game (0 = no cap).
	MaxRunsPerGame int `yaml:"maxRunsPerGame"`
	// CommentOnItems makes the brain react to every stashed item.
	CommentOnItems bool `yaml:"commentOnItems"`
	// OverlayFile is read by OBS (Text source, "Read from file").
	OverlayFile string `yaml:"overlayFile"`
	// OverlayLines is how many recent thoughts stay on screen.
	OverlayLines int `yaml:"overlayLines"`
	// LogFile receives one JSON line per decision, with cost, for editing later.
	LogFile string `yaml:"logFile"`
	// MemoryFile keeps run statistics, lessons and goals between sessions.
	MemoryFile string `yaml:"memoryFile"`
	// TuneSurvival lets the AI adjust potion/chicken thresholds between games (within safe bounds).
	TuneSurvival bool `yaml:"tuneSurvival"`
	// UsePlaybook gives the model the speedrun route knowledge (MrLlamaSC tutorials + speedrun.com) for planning.
	UsePlaybook bool `yaml:"usePlaybook"`
	// Provider is "anthropic" (Claude API) or "local" (any OpenAI-compatible server: Ollama, LM Studio, llama.cpp, vLLM).
	Provider string `yaml:"provider"`
	// LocalURL is the OpenAI-compatible base URL for provider "local".
	LocalURL string `yaml:"localURL"`
}

func defaults() Config {
	return Config{
		FastModel:      "claude-haiku-5-5",
		SmartModel:     "claude-opus-5-5",
		Persona:        "You are an AI playing Diablo II: Resurrected live on a YouTube channel. Be entertaining, confident and a bit dramatic, but honest about mistakes.",
		MaxSessionUSD:  5,
		MaxRunsPerGame: 0,
		CommentOnItems: true,
		OverlayFile:    "brain/overlay.txt",
		OverlayLines:   6,
		LogFile:        "brain/thoughts.jsonl",
		MemoryFile:     "brain/memory.json",
		TuneSurvival:   true,
		UsePlaybook:    true,
		Provider:       "anthropic",
		LocalURL:       "http://localhost:11434/v1",
	}
}

// Default is the process-wide brain. It is never nil; when disabled every
// method is a cheap no-op so Koolo behaves exactly as before.
var Default = &Brain{}

// ErrBudget is returned once MaxSessionUSD has been reached.
var ErrBudget = errors.New("brain: session budget reached")

type Brain struct {
	cfg    Config
	client anthropic.Client
	local  *localClient
	logger *slog.Logger

	mu       sync.Mutex
	spentUSD float64
	calls    int
	overlay  []string
	history  []string // short memory of recent happenings, fed back into prompts
	last     Snapshot
	mem      *Memory
	curRun   string

	queue chan func()
}

// Load reads the config file and, if enabled, sets Default. A missing file
// leaves the brain disabled.
func Load(path string, logger *slog.Logger) (*Brain, error) {
	cfg := defaults()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Info("brain: no config file, AI brain disabled", slog.String("path", path))
			return Default, nil
		}
		return Default, fmt.Errorf("brain: reading %s: %w", path, err)
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Default, fmt.Errorf("brain: parsing %s: %w", path, err)
	}
	if !cfg.Enabled {
		logger.Info("brain: disabled in config")
		return Default, nil
	}

	b := &Brain{
		cfg:    cfg,
		logger: logger,
		queue:  make(chan func(), 32),
		mem:    loadMemory(cfg.MemoryFile),
	}
	switch cfg.Provider {
	case "local":
		if strings.HasPrefix(cfg.FastModel, "claude-") {
			cfg.FastModel = "qwen2.5vl:7b"
		}
		if strings.HasPrefix(cfg.SmartModel, "claude-") {
			cfg.SmartModel = cfg.FastModel
		}
		b.cfg = cfg
		b.local = &localClient{baseURL: strings.TrimRight(cfg.LocalURL, "/"), apiKey: cfg.APIKey}
	case "", "anthropic":
		opts := []option.RequestOption{option.WithMaxRetries(2)}
		if cfg.APIKey != "" {
			opts = append(opts, option.WithAPIKey(cfg.APIKey))
		} else if os.Getenv("ANTHROPIC_API_KEY") == "" {
			return Default, errors.New("brain: enabled but no apiKey in config and ANTHROPIC_API_KEY is not set")
		}
		b.client = anthropic.NewClient(opts...)
	default:
		return Default, fmt.Errorf("brain: unknown provider %q (use anthropic or local)", cfg.Provider)
	}
	go b.worker()
	Default = b
	b.say("system", "AI brain online ("+cfg.Provider+"). Smart model: "+cfg.SmartModel+", fast model: "+cfg.FastModel)
	logger.Info("brain: enabled", slog.String("smartModel", cfg.SmartModel), slog.String("fastModel", cfg.FastModel))
	return b, nil
}

func (b *Brain) Enabled() bool { return b != nil && b.queue != nil }

// worker runs background commentary so the bot is never blocked waiting on the API.
func (b *Brain) worker() {
	for job := range b.queue {
		job()
	}
}

// async queues a job; if the queue is full the job is dropped (commentary is optional).
func (b *Brain) async(job func()) {
	select {
	case b.queue <- job:
	default:
		b.logger.Debug("brain: commentary queue full, dropping")
	}
}

// ---- API calls ----------------------------------------------------------

type callOpts struct {
	model     string
	system    string
	text      string
	imageJPEG string // optional base64 JPEG (e.g. the death screenshot)
	maxTokens int64
	effort    anthropic.BetaOutputConfigEffort
	schema    json.RawMessage // optional structured output
	timeout   time.Duration
}

func (b *Brain) call(ctx context.Context, o callOpts) (string, error) {
	b.mu.Lock()
	if b.cfg.MaxSessionUSD > 0 && b.spentUSD >= b.cfg.MaxSessionUSD {
		b.mu.Unlock()
		return "", ErrBudget
	}
	b.mu.Unlock()

	if o.timeout == 0 {
		o.timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()

	if b.local != nil {
		if o.timeout < 180*time.Second {
			// local models on consumer GPUs can be slow on long prompts
			ctx2, cancel2 := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel2()
			ctx = ctx2
		}
		text, err := b.local.chat(ctx, o)
		if err == nil {
			b.mu.Lock()
			b.calls++
			b.mu.Unlock()
		}
		return text, err
	}

	content := []anthropic.BetaContentBlockParamUnion{}
	if o.imageJPEG != "" {
		content = append(content, anthropic.NewBetaImageBlock(anthropic.BetaBase64ImageSourceParam{
			Data:      o.imageJPEG,
			MediaType: "image/jpeg",
		}))
	}
	content = append(content, anthropic.NewBetaTextBlock(o.text))

	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(o.model),
		MaxTokens: o.maxTokens,
		System: []anthropic.BetaTextBlockParam{{
			Text:         o.system,
			CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
		}},
		Messages:     []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(content...)},
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: o.effort},
	}
	if o.schema != nil {
		params.OutputConfig.Format = anthropic.BetaJSONOutputFormatParam{Schema: o.schema}
	}
	// Server-side refusal fallback; Haiku has none, so only for the bigger models.
	if !strings.Contains(o.model, "haiku") {
		params.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
		params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
	}

	resp, err := b.client.Beta.Messages.New(ctx, params)
	if err != nil {
		return "", err
	}

	cost := estimateCost(string(resp.Model), resp.Usage.InputTokens, resp.Usage.OutputTokens,
		resp.Usage.CacheReadInputTokens, resp.Usage.CacheCreationInputTokens)
	b.mu.Lock()
	b.spentUSD += cost
	b.calls++
	b.mu.Unlock()

	if resp.StopReason == anthropic.BetaStopReasonRefusal {
		return "", errors.New("brain: model declined the request")
	}

	var sb strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			sb.WriteString(t.Text)
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

// Spent returns the dollars spent and number of calls this session.
func (b *Brain) Spent() (float64, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spentUSD, b.calls
}

// ---- overlay + log ------------------------------------------------------

type logLine struct {
	Time     time.Time `json:"time"`
	Kind     string    `json:"kind"`
	Thought  string    `json:"thought"`
	SpentUSD float64   `json:"spentUSD"`
	Calls    int       `json:"calls"`
}

// say shows a thought on the overlay, remembers it and logs it.
func (b *Brain) say(kind, thought string) {
	thought = strings.TrimSpace(strings.ReplaceAll(thought, "\n", " "))
	if thought == "" {
		return
	}
	b.mu.Lock()
	b.overlay = append(b.overlay, "> "+thought)
	if n := b.cfg.OverlayLines; n > 0 && len(b.overlay) > n {
		b.overlay = b.overlay[len(b.overlay)-n:]
	}
	b.history = append(b.history, kind+": "+thought)
	if len(b.history) > 12 {
		b.history = b.history[len(b.history)-12:]
	}
	spent, calls := b.spentUSD, b.calls
	text := strings.Join(b.overlay, "\n\n") + fmt.Sprintf("\n\nAPI cost this session: $%.2f (%d calls)", spent, calls)
	b.mu.Unlock()

	if err := writeAtomic(b.cfg.OverlayFile, text); err != nil {
		b.logger.Warn("brain: writing overlay", slog.Any("error", err))
	}
	if b.cfg.LogFile != "" {
		line, _ := json.Marshal(logLine{Time: time.Now(), Kind: kind, Thought: thought, SpentUSD: spent, Calls: calls})
		if err := appendLine(b.cfg.LogFile, line); err != nil {
			b.logger.Warn("brain: writing log", slog.Any("error", err))
		}
	}
	b.logger.Info("brain: "+thought, slog.String("kind", kind))
}

func (b *Brain) recentHistory() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.history) == 0 {
		return "(nothing yet)"
	}
	return strings.Join(b.history, "\n")
}

func writeAtomic(path, text string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func appendLine(path string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}
