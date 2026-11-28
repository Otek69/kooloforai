// Package inbox holds the owner's questions from the assistant page while Claude Desktop is the brain
// (provider: desktop). Koolo cannot call Claude Desktop, so questions wait here until Claude Desktop reads
// them over MCP and answers; the page polls for the answer.
package inbox

import (
	"errors"
	"strings"
	"sync"
	"time"
)

type Question[P any] struct {
	ID         int       `json:"id"`
	Time       time.Time `json:"time"`
	Character  string    `json:"character"`
	Text       string    `json:"text"`
	Answered   bool      `json:"answered"`
	Answer     string    `json:"answer,omitempty"`
	Proposals  []P       `json:"proposals,omitempty"`
	AnsweredAt time.Time `json:"answeredAt,omitempty"`
}

const keep = 50

type Inbox[P any] struct {
	mu     sync.Mutex
	nextID int
	qs     []Question[P]
}

// Ask stores a question and returns its id.
func (b *Inbox[P]) Ask(character, text string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	b.qs = append(b.qs, Question[P]{ID: b.nextID, Time: time.Now(), Character: character, Text: strings.TrimSpace(text)})
	if len(b.qs) > keep {
		b.qs = b.qs[len(b.qs)-keep:]
	}
	return b.nextID
}

// Pending returns the unanswered questions, oldest first.
func (b *Inbox[P]) Pending() []Question[P] {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Question[P]
	for _, q := range b.qs {
		if !q.Answered {
			out = append(out, q)
		}
	}
	return out
}

// Get returns one question by id.
func (b *Inbox[P]) Get(id int) (Question[P], bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, q := range b.qs {
		if q.ID == id {
			return q, true
		}
	}
	return Question[P]{}, false
}

// Answer stores the answer to a question.
func (b *Inbox[P]) Answer(id int, text string, proposals []P) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("empty answer")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.qs {
		if b.qs[i].ID == id {
			if b.qs[i].Answered {
				return errors.New("question already answered")
			}
			b.qs[i].Answered, b.qs[i].Answer, b.qs[i].Proposals, b.qs[i].AnsweredAt = true, strings.TrimSpace(text), proposals, time.Now()
			return nil
		}
	}
	return errors.New("no such question (ids come from koolo_owner_questions)")
}
