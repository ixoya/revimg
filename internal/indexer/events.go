package indexer

import (
	"sync"
	"time"
)

// EventType categorises a file-system change observed by the watcher or scanner.
type EventType string

const (
	EventCreated       EventType = "created"
	EventDeleted       EventType = "deleted"
	EventModified      EventType = "modified"
	EventRenamedOld    EventType = "renamed_old"
	EventRenamedNew    EventType = "renamed_new"
	EventError         EventType = "error"
	EventScanStarted   EventType = "scan_started"
	EventScanCompleted EventType = "scan_completed"
	EventRepairStarted EventType = "repair_started"
	EventRepairCompleted EventType = "repair_completed"

)

// Event describes a single file-system change.
type Event struct {
	Time    time.Time `json:"time"`
	Type    EventType `json:"type"`
	Path    string    `json:"path"`
	Message string    `json:"message,omitempty"`
}

// EventLog holds a ring buffer of recent events.
type EventLog struct {
	mu   sync.RWMutex
	buf  []Event
	cap  int
	pos  int // next write position
	full bool
}

// NewEventLog creates a ring buffer holding up to cap events.
func NewEventLog(capacity int) *EventLog {
	return &EventLog{cap: capacity, buf: make([]Event, capacity)}
}

// Push appends an event to the ring buffer.
func (el *EventLog) Push(t EventType, path, msg string) {
	el.mu.Lock()
	el.buf[el.pos] = Event{Time: time.Now(), Type: t, Path: path, Message: msg}
	el.pos++
	if el.pos >= el.cap {
		el.pos = 0
		el.full = true
	}
	el.mu.Unlock()
}

// Recent returns the most recent n events in reverse chronological order
// (newest first). If n <= 0 or n > cap, all events are returned.
func (el *EventLog) Recent(n int) []Event {
	el.mu.RLock()
	defer el.mu.RUnlock()

	count := el.pos
	if el.full {
		count = el.cap
	}
	if n > 0 && n < count {
		count = n
	}
	out := make([]Event, count)
	if el.full {
		// Wrap: walk back count steps from the newest event (pos-1).
		// The old code read forward from pos, returning the OLDEST
		// window and hiding the newest events once n < cap.
		for i := 0; i < count; i++ {
			idx := (el.pos-1-i)%el.cap
			if idx < 0 {
				idx += el.cap
			}
			out[i] = el.buf[idx]
		}
	} else {
		start := el.pos - count
		if start < 0 {
			start = 0
		}
		for i := el.pos - 1; i >= start; i-- {
			out[el.pos-1-i] = el.buf[i]
		}
	}
	return out
}
