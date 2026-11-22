package link

// A collision on a valid 63-byte label returns a valid free name (#674 review, F1).

import (
	"strings"
	"testing"
	"time"
)

func TestFreeNameKeepsLabelLimit(t *testing.T) {
	name := strings.Repeat("a", 63)
	s := State{Pairs: []Pair{{ID: "one", Effective: name}}}
	done := make(chan string, 1)
	go func() { done <- s.FreeName(name, "") }()
	select {
	case got := <-done:
		if ValidateName(got) != nil || got == name || got != strings.Repeat("a", 61)+"-2" {
			t.Fatalf("invalid collision result %q", got)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("valid 63-byte label collision never returns; suffix is appended without shortening the label")
	}
	// A long name of several labels keeps both limits.
	long := strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 63) + "." + strings.Repeat("e", 61)
	s = State{Pairs: []Pair{{ID: "one", Effective: long}}}
	if got := s.FreeName(long, ""); ValidateName(got) != nil || got == long || len(got) > MaxNameBytes {
		t.Fatalf("long collision %q", got)
	}
}
