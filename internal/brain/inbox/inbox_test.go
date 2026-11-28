package inbox

import "testing"

func TestInbox(t *testing.T) {
	var b Inbox[string]
	id := b.Ask("Sorcaa", " Leveling script not working ")
	if p := b.Pending(); len(p) != 1 || p[0].Text != "Leveling script not working" {
		t.Fatalf("pending %+v", p)
	}
	if err := b.Answer(99, "x", nil); err == nil {
		t.Fatal("unknown id accepted")
	}
	if err := b.Answer(id, " ", nil); err == nil {
		t.Fatal("empty answer accepted")
	}
	if err := b.Answer(id, "Check the log", []string{"runs"}); err != nil {
		t.Fatal(err)
	}
	if err := b.Answer(id, "again", nil); err == nil {
		t.Fatal("answered twice")
	}
	q, ok := b.Get(id)
	if !ok || !q.Answered || q.Answer != "Check the log" || len(q.Proposals) != 1 || len(b.Pending()) != 0 {
		t.Fatalf("%+v", q)
	}
	for i := 0; i < keep+5; i++ {
		b.Ask("x", "y")
	}
	if _, ok := b.Get(id); ok {
		t.Fatal("old questions not dropped")
	}
}
