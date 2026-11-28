// Package escalate is the escalate package's host (catalog escalate/, the redesign's step 6, s-b478fe59):
// a goal the voice cannot do with one card becomes a task of Nina's task runner when the rules allow it.
// The rules read the load (free, busy, full) this host gives each beat; their start rule's event starts
// the task with the call's goal (the payload). A rule of another package may later ask for a task the same
// way (F4: the rules' own tasks).
package escalate

import (
	"strings"
	"sync"
	"time"
)

// Outcomes are the outcome atoms the agent narrates, then retracts.
var Outcomes = []string{"done", "refused"}

// Host is the escalate package's host.
type Host struct {
	Start   func(goal string) // the task runner's start (nil: no task runner: the load is full)
	Running func() int        // tasks running now
	Max     int               // tasks at once (default 2)
	PerHour int               // tasks an hour (default 12)
	Now     func() time.Time

	mu      sync.Mutex
	started []time.Time
	load    string
}

func (h *Host) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// Load is free, busy (Max running) or full (no task runner, or the hour's budget spent).
func (h *Host) Load() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.loadLocked()
}

func (h *Host) loadLocked() string {
	if h.Start == nil {
		return "full"
	}
	max, per := h.Max, h.PerHour
	if max <= 0 {
		max = 2
	}
	if per <= 0 {
		per = 12
	}
	cut := h.now().Add(-time.Hour)
	n := 0
	for _, t := range h.started {
		if t.After(cut) {
			n++
		}
	}
	if n >= per {
		return "full"
	}
	if h.Running != nil && h.Running() >= max {
		return "busy"
	}
	return "free"
}

// Tick gives the rules the load, as one level atom.
func (h *Host) Tick(counts map[string]int) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	want := h.loadLocked()
	var out []string
	for _, l := range []string{"free", "busy", "full"} {
		a := "load(" + l + ")"
		if n := counts[a]; n > 0 && l != want {
			out = append(out, "-"+a)
		}
	}
	if counts["load("+want+")"] == 0 {
		out = append(out, "+load("+want+")")
	}
	h.load = want
	return out
}

// OnEvent: the start rule fired: the task, with the call's goal.
func (h *Host) OnEvent(name string, args []string, payload map[string]any) {
	if rule, _, _ := strings.Cut(name, "["); rule != "start" {
		return
	}
	goal, _ := payload["goal"].(string)
	h.mu.Lock()
	h.started = append(h.started, h.now())
	start := h.Start
	h.mu.Unlock()
	if start != nil && goal != "" {
		go start(goal)
	}
}
