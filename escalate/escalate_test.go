package escalate

import (
	"testing"
	"time"
)

// TestTheLoad: free until two run, full when the hour's budget is spent or there is no task runner; the
// start rule's event starts the task with the call's goal.
func TestTheLoad(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	running := 0
	var started []string
	h := &Host{Start: func(g string) { started = append(started, g) }, Running: func() int { return running }, PerHour: 2,
		Now: func() time.Time { return now }}
	if got := h.Tick(map[string]int{}); len(got) != 1 || got[0] != "+load(free)" {
		t.Fatalf("free: %v", got)
	}
	running = 2
	if got := h.Tick(map[string]int{"load(free)": 1}); len(got) != 2 || got[0] != "-load(free)" || got[1] != "+load(busy)" {
		t.Fatalf("busy: %v", got)
	}
	running = 0
	h.OnEvent("start[dyn]", nil, map[string]any{"goal": "check the fans"})
	h.OnEvent("start[dyn]", nil, map[string]any{"goal": "read the list"})
	time.Sleep(50 * time.Millisecond)
	if h.Load() != "full" || len(started) != 2 {
		t.Fatalf("after two this hour: %s %v", h.Load(), started)
	}
	now = now.Add(61 * time.Minute)
	if h.Load() != "free" {
		t.Fatalf("an hour later: %s", h.Load())
	}
	if (&Host{}).Load() != "full" {
		t.Fatal("no task runner: full")
	}
}
