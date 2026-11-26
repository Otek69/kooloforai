package brain

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Memory persists between Koolo sessions (brain/memory.json), so the AI
// remembers which runs go well, where it keeps dying, and what it learned.
type Memory struct {
	path string
	mu   sync.Mutex

	Games    int                 `json:"games"`
	Goal     string              `json:"goal"`
	Runs     map[string]*RunStat `json:"runs"`
	Lessons  []Lesson            `json:"lessons"`
	Survival *SurvivalSettings   `json:"survival,omitempty"` // last thresholds the AI chose
}

type RunStat struct {
	Attempts  int       `json:"attempts"`
	OK        int       `json:"ok"`
	Deaths    int       `json:"deaths"`
	Chickens  int       `json:"chickens"`
	Errors    int       `json:"errors"`
	Items     int       `json:"items"`
	LastLevel int       `json:"lastLevel"`
	LastTry   time.Time `json:"lastTry"`
}

type Lesson struct {
	Time  time.Time `json:"time"`
	Level int       `json:"level"`
	Text  string    `json:"text"`
}

const maxLessons = 15

func loadMemory(path string) *Memory {
	m := &Memory{path: path, Runs: map[string]*RunStat{}}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, m)
		if m.Runs == nil {
			m.Runs = map[string]*RunStat{}
		}
	}
	return m
}

func (m *Memory) save() error {
	m.mu.Lock()
	raw, err := json.MarshalIndent(m, "", "  ")
	m.mu.Unlock()
	if err != nil {
		return err
	}
	return writeAtomic(m.path, string(raw))
}

func (m *Memory) stat(run string) *RunStat {
	s, ok := m.Runs[run]
	if !ok {
		s = &RunStat{}
		m.Runs[run] = s
	}
	return s
}

func (m *Memory) recordRun(run, reason string, level int) {
	m.mu.Lock()
	s := m.stat(run)
	s.Attempts++
	s.LastLevel = level
	s.LastTry = time.Now()
	switch reason {
	case "ok":
		s.OK++
	case "death":
		s.Deaths++
	case "chicken", "merc chicken":
		s.Chickens++
	default:
		s.Errors++
	}
	m.mu.Unlock()
}

func (m *Memory) recordItem(run string) {
	if run == "" {
		return
	}
	m.mu.Lock()
	m.stat(run).Items++
	m.mu.Unlock()
}

func (m *Memory) addLesson(text string, level int) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	m.mu.Lock()
	m.Lessons = append(m.Lessons, Lesson{Time: time.Now(), Level: level, Text: text})
	if len(m.Lessons) > maxLessons {
		m.Lessons = m.Lessons[len(m.Lessons)-maxLessons:]
	}
	m.mu.Unlock()
}

// Summary is the text the model sees: per-run record and lessons learned.
func (m *Memory) Summary() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	var b strings.Builder
	fmt.Fprintf(&b, "Games played so far (all sessions): %d\n", m.Games)
	if m.Goal != "" {
		fmt.Fprintf(&b, "Your current long-term goal: %s\n", m.Goal)
	}
	if len(m.Runs) == 0 {
		b.WriteString("Run history: none yet.\n")
	} else {
		names := make([]string, 0, len(m.Runs))
		for n := range m.Runs {
			names = append(names, n)
		}
		sort.Strings(names)
		b.WriteString("Run history (attempts / success / deaths / chickens / errors / items stashed, last tried at char level):\n")
		for _, n := range names {
			s := m.Runs[n]
			fmt.Fprintf(&b, "- %s: %d / %d / %d / %d / %d / %d, level %d\n",
				n, s.Attempts, s.OK, s.Deaths, s.Chickens, s.Errors, s.Items, s.LastLevel)
		}
	}
	if len(m.Lessons) > 0 {
		b.WriteString("Lessons you wrote down after earlier failures (oldest first):\n")
		for _, l := range m.Lessons {
			fmt.Fprintf(&b, "- (at level %d) %s\n", l.Level, l.Text)
		}
	}
	return b.String()
}
