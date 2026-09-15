package indexer

import (
	"fmt"
	"testing"
)

func pushSeq(el *EventLog, from, to int) {
	for i := from; i < to; i++ {
		el.Push(EventModified, fmt.Sprintf("/tmp/f%d.mp4", i), "")
	}
}

func paths(evs []Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Path
	}
	return out
}

func expectPaths(t *testing.T, evs []Event, want ...string) {
	t.Helper()
	got := paths(evs)
	if len(got) != len(want) {
		t.Fatalf("got %d events %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d: got %s, want %s (full list %v)", i, got[i], want[i], got)
		}
	}
}

func TestRecentNonFull(t *testing.T) {
	el := NewEventLog(10)
	pushSeq(el, 0, 5)
	expectPaths(t, el.Recent(3), "/tmp/f4.mp4", "/tmp/f3.mp4", "/tmp/f2.mp4")
	expectPaths(t, el.Recent(0),
		"/tmp/f4.mp4", "/tmp/f3.mp4", "/tmp/f2.mp4", "/tmp/f1.mp4", "/tmp/f0.mp4")
}

func TestRecentFullExact(t *testing.T) {
	el := NewEventLog(5)
	pushSeq(el, 0, 7) // wraps: pos=2, buffer holds 2..6
	expectPaths(t, el.Recent(5),
		"/tmp/f6.mp4", "/tmp/f5.mp4", "/tmp/f4.mp4", "/tmp/f3.mp4", "/tmp/f2.mp4")
}

// Regression: full ring + n < cap used to return the OLDEST window,
// hiding the newest events from the Activity feed.
func TestRecentFullPartial(t *testing.T) {
	el := NewEventLog(5)
	pushSeq(el, 0, 7)
	expectPaths(t, el.Recent(3), "/tmp/f6.mp4", "/tmp/f5.mp4", "/tmp/f4.mp4")
}

func TestRecentFullPartialLateWrap(t *testing.T) {
	el := NewEventLog(5)
	pushSeq(el, 0, 12) // pos=2 again, buffer holds 7..11
	expectPaths(t, el.Recent(3), "/tmp/f11.mp4", "/tmp/f10.mp4", "/tmp/f9.mp4")
}

func TestRecentOverCap(t *testing.T) {
	el := NewEventLog(5)
	pushSeq(el, 0, 7)
	expectPaths(t, el.Recent(99),
		"/tmp/f6.mp4", "/tmp/f5.mp4", "/tmp/f4.mp4", "/tmp/f3.mp4", "/tmp/f2.mp4")
}
