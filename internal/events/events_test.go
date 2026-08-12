package events

import (
	"testing"
	"time"
)

func TestLogRingAndOrder(t *testing.T) {
	l := NewLog(3)
	base := time.Unix(1700000000, 0)
	for i := 0; i < 5; i++ {
		l.Add(Event{At: base.Add(time.Duration(i) * time.Second), Type: "t", Reason: string(rune('a' + i))})
	}
	if l.Len() != 3 {
		t.Fatalf("Len = %d, want 3 (ring must evict oldest)", l.Len())
	}
	r := l.Recent(0)
	if len(r) != 3 || r[0].Reason != "e" || r[2].Reason != "c" {
		t.Fatalf("Recent order wrong (want newest-first e,d,c): %+v", r)
	}
}

func TestRecentN(t *testing.T) {
	l := NewLog(10)
	for i := 0; i < 4; i++ {
		l.Add(Event{Type: "x"})
	}
	if got := l.Recent(2); len(got) != 2 {
		t.Errorf("Recent(2) len = %d, want 2", len(got))
	}
}
