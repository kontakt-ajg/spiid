package app

import (
	"fmt"
	"testing"
)

func TestMessagesIndexesAndLimit(t *testing.T) {
	e := newEnv(t, sftpOpts())
	s := e.service()
	if ms, next := s.Messages(0); len(ms) != 0 || next != 0 {
		t.Fatalf("empty: %v %d", ms, next)
	}
	s.addMessage("a")
	s.addMessage("b")
	ms, next := s.Messages(0)
	if len(ms) != 2 || next != 2 || ms[1].Text != "b" || ms[0].Time.IsZero() {
		t.Fatalf("%v %d", ms, next)
	}
	if ms, next = s.Messages(1); len(ms) != 1 || ms[0].Text != "b" || next != 2 {
		t.Fatalf("since 1: %v %d", ms, next)
	}
	if ms, next = s.Messages(next); len(ms) != 0 || next != 2 {
		t.Fatalf("since next: %v %d", ms, next)
	}
	if ms, next = s.Messages(99); len(ms) != 0 || next != 2 {
		t.Fatalf("since past the end: %v %d", ms, next)
	}
	for i := 2; i < 700; i++ {
		s.addMessage(fmt.Sprint(i))
	}
	ms, next = s.Messages(0)
	if next != 700 || len(ms) != maxMessages || ms[0].Text != "200" || ms[len(ms)-1].Text != "699" {
		t.Fatalf("after overflow: %d messages, next %d, first %q", len(ms), next, ms[0].Text)
	}
	if ms, _ = s.Messages(650); len(ms) != 50 || ms[0].Text != "650" {
		t.Fatalf("since 650: %d, first %q", len(ms), ms[0].Text)
	}
}
