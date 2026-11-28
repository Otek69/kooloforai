package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hectorgimenez/d2go/pkg/data"
	"github.com/hectorgimenez/d2go/pkg/nip"
	"github.com/hectorgimenez/koolo/internal/brain"
	"github.com/hectorgimenez/koolo/internal/config"
	"github.com/hectorgimenez/koolo/internal/health"
)

// AI Assistant page: chat with Claude about your character, see the AI's
// memory, the armory (all items) and apply suggested changes with one click.

const aiPickitFile = "ai_rules.nip"

type assistantPage struct {
	Supervisor string
	Character  string
	Running    bool
	Memory     brain.MemoryView
	Armory     []brain.ArmoryItem
}

func (s *HttpServer) assistantPage(w http.ResponseWriter, r *http.Request) {
	sup := r.URL.Query().Get("supervisor")
	cfg, found := config.Characters[sup]
	if !found {
		http.Error(w, "Character configuration "+sup+" wasn't found", http.StatusNotFound)
		return
	}
	page := assistantPage{
		Supervisor: sup,
		Character:  cfg.CharacterName,
		Memory:     brain.Default.View(),
	}
	if d := s.liveData(sup); d != nil {
		page.Running = true
		page.Armory = brain.Armory(*d)
	}
	if err := s.templates.ExecuteTemplate(w, "assistant.gohtml", page); err != nil {
		s.logger.Error("rendering assistant page: " + err.Error())
	}
}

func (s *HttpServer) liveData(sup string) *data.Data {
	gd := s.manager.GetData(sup)
	if gd == nil || gd.PlayerUnit.Address == 0 {
		return nil
	}
	d := gd.Data
	return &d
}

type assistRequest struct {
	Supervisor string           `json:"supervisor"`
	Message    string           `json:"message"`
	History    []brain.ChatTurn `json:"history"`
}

func (s *HttpServer) assistantAsk(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var req assistRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	cfg, found := config.Characters[req.Supervisor]
	if !found {
		http.Error(w, "unknown character", http.StatusNotFound)
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		http.Error(w, "empty message", http.StatusBadRequest)
		return
	}

	reply, err := brain.Default.Assist(r.Context(), req.Supervisor, cfg, s.liveData(req.Supervisor), req.History, req.Message)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, brain.ErrDisabled) || errors.Is(err, brain.ErrBudget) {
			status = http.StatusServiceUnavailable
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, reply)
}

// assistantAnswer returns a desktop-mode question with Claude Desktop's answer once it has one.
func (s *HttpServer) assistantAnswer(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.URL.Query().Get("id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	q, ok := brain.Question(id)
	if !ok {
		http.Error(w, "unknown question", http.StatusNotFound)
		return
	}
	writeJSON(w, q)
}

type applyRequest struct {
	Supervisor string         `json:"supervisor"`
	Proposal   brain.Proposal `json:"proposal"`
}

func (s *HttpServer) assistantApply(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var req applyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	cfg, found := config.Characters[req.Supervisor]
	if !found {
		http.Error(w, "unknown character", http.StatusNotFound)
		return
	}

	msg, err := s.applyProposal(req.Supervisor, cfg, req.Proposal)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.logger.Info("AI assistant change applied: " + msg)
	writeJSON(w, map[string]string{"result": msg})
}

func (s *HttpServer) applyProposal(sup string, cfg *config.CharacterCfg, p brain.Proposal) (string, error) {
	switch p.Kind {
	case "pickit_rule":
		rule := strings.TrimSpace(p.PickitRule)
		if rule == "" || strings.ContainsAny(rule, "\r\n") {
			return "", errors.New("the rule must be a single line")
		}
		if _, err := nip.NewRule(rule, aiPickitFile, 1); err != nil {
			return "", fmt.Errorf("the rule is not valid NIP, nothing was changed: %w", err)
		}
		dir := pickitDir(sup, cfg)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		f, err := os.OpenFile(filepath.Join(dir, aiPickitFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return "", err
		}
		_, werr := fmt.Fprintf(f, "// %s\n%s\n", oneLine(p.Title), rule)
		f.Close()
		if werr != nil {
			return "", werr
		}
		if err := s.manager.ReloadConfig(); err != nil {
			return "", fmt.Errorf("rule saved to %s but reloading failed: %w", aiPickitFile, err)
		}
		return "Pickit rule added to " + aiPickitFile, nil

	case "runs":
		if len(p.Runs) == 0 {
			return "", errors.New("the run list is empty")
		}
		runs := make([]config.Run, 0, len(p.Runs))
		for _, name := range p.Runs {
			r := config.Run(strings.TrimSpace(name))
			if _, ok := config.AvailableRuns[r]; !ok {
				return "", fmt.Errorf("unknown run %q, nothing was changed", name)
			}
			runs = append(runs, r)
		}
		cfg.Game.Runs = runs
		return "Runs updated", s.saveAndReload(sup, cfg)

	case "chicken_on_curses":
		var curses []string
		for _, c := range p.Curses {
			c = strings.ToLower(strings.TrimSpace(c))
			if _, ok := health.CurseStates[c]; !ok {
				return "", fmt.Errorf("unknown curse %q, nothing was changed", c)
			}
			curses = append(curses, c)
		}
		cfg.Health.ChickenOnCurses = curses
		return "Curse chicken list updated", s.saveAndReload(sup, cfg)

	case "goal":
		if strings.TrimSpace(p.Goal) == "" {
			return "", errors.New("the goal is empty")
		}
		brain.Default.SetGoal(oneLine(p.Goal))
		return "Goal set", nil
	}
	return "", fmt.Errorf("unknown proposal kind %q", p.Kind)
}

func (s *HttpServer) saveAndReload(sup string, cfg *config.CharacterCfg) error {
	if err := config.SaveSupervisorConfig(sup, cfg); err != nil {
		return err
	}
	return s.manager.ReloadConfig()
}

// pickitDir mirrors how config.Load finds a character's pickit folder.
func pickitDir(sup string, cfg *config.CharacterCfg) string {
	if config.Koolo.CentralizedPickitPath != "" && cfg.UseCentralizedPickit {
		if _, err := os.Stat(config.Koolo.CentralizedPickitPath); err == nil {
			return config.Koolo.CentralizedPickitPath
		}
	}
	return filepath.Join("config", sup, "pickit")
}

func oneLine(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(s))
}

// requireJSON blocks cross-site form posts: a browser can't send
// application/json to another origin without a CORS preflight, which Koolo never approves.
func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return false
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
