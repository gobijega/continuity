// Package events records the agent's decisions and link-state changes as a
// bounded, thread-safe log (spec §19). The timeline is both an audit trail and
// the feed behind the dashboard.
package events

import (
	"sync"
	"time"
)

// Event is a single logged occurrence.
type Event struct {
	At        time.Time `json:"at"`
	Node      string    `json:"node,omitempty"`
	Interface string    `json:"interface,omitempty"`
	Type      string    `json:"type"`
	From      string    `json:"from,omitempty"`
	To        string    `json:"to,omitempty"`
	Reason    string    `json:"reason,omitempty"`
}

// Log is a fixed-capacity ring of recent events, safe for concurrent use.
type Log struct {
	mu  sync.Mutex
	buf []Event
	max int
}

// NewLog returns a Log holding up to max recent events.
func NewLog(max int) *Log {
	if max <= 0 {
		max = 200
	}
	return &Log{max: max}
}

// Add appends an event, evicting the oldest once at capacity.
func (l *Log) Add(e Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, e)
	if len(l.buf) > l.max {
		l.buf = l.buf[len(l.buf)-l.max:]
	}
}

// Recent returns up to n of the most-recent events, newest first. n<=0 returns
// every stored event.
func (l *Log) Recent(n int) []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n <= 0 || n > len(l.buf) {
		n = len(l.buf)
	}
	out := make([]Event, n)
	for i := 0; i < n; i++ {
		out[i] = l.buf[len(l.buf)-1-i]
	}
	return out
}

// Len returns the number of stored events.
func (l *Log) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buf)
}
