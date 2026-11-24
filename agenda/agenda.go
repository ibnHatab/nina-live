// Package agenda is the agenda package's host (catalog/domains/agenda): it writes Nina's events and
// to-dos through Home Assistant's calendar and todo classes, whatever integration is behind them
// (Google Calendar and Google Tasks: the phone sees them; Local Calendar and Local To-do for tests).
// The rules decide where Nina may write; the host turns an accepted command and its payload (the
// summary, the start, the duration, the due date) into calendar.create_event or todo.add_item, and
// asserts failed(T) when Home Assistant refuses it.
//
// Every write is recorded (stream "agenda", through Save), so an item that comes back from the
// phone is known as Nina's own; each also carries a mark in its description.
package agenda

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Mark ends the description of what Nina writes: how she knows her own items when they come back.
const Mark = "(set by Nina)"

// Outcomes are the package's outcome atoms.
var Outcomes = []string{"done", "refused"}

// Write is one item written (the log's record).
type Write struct {
	Kind    string `json:"kind"` // event | todo
	Target  string `json:"target"`
	Entity  string `json:"entity"`
	Summary string `json:"summary"`
	Start   string `json:"start,omitempty"`
	End     string `json:"end,omitempty"`
	Due     string `json:"due,omitempty"`
	Err     string `json:"err,omitempty"`
}

// Host is the agenda's host.
type Host struct {
	// Entities: a target handle (agenda.llp's type target) → the Home Assistant entity
	Entities map[string]string
	// Names: a target handle → its name in Nina's lines
	Names map[string]string
	// Call is a Home Assistant service call (nil: none: every write fails)
	Call func(ctx context.Context, domain, service string, data map[string]any) error
	Save func(Write)
	Now  func() time.Time

	mu     sync.Mutex
	failed []string
}

// Default are Pascal's (2026-11-24): Nina's Google calendar, the local test pair, the person's
// own Google calendar (read only, by the rules), Google Tasks' default list.
var Default = map[string]string{
	"nina_cal": "calendar.nina", "nina_local": "calendar.nina_local", "person_cal": "calendar.aaosdeveloper_gmail_com",
	"my_tasks": "todo.my_tasks", "nina_todo": "todo.nina_local",
}

var names = map[string]string{"nina_cal": "my calendar", "nina_local": "the local test calendar",
	"person_cal": "your calendar", "my_tasks": "your tasks", "nina_todo": "my local list"}

func (h *Host) Tick(map[string]int) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, t := range h.failed {
		out = append(out, "+failed("+t+")")
	}
	h.failed = nil
	return out
}

func (h *Host) Name(handle string) (string, bool) {
	if n, ok := h.Names[handle]; ok {
		return n, true
	}
	n, ok := names[handle]
	return n, ok
}

// OnEvent takes an accepted write (an event is named after its rule: event[C=nina_cal]).
func (h *Host) OnEvent(name string, args []string, payload map[string]any) {
	rule, _, _ := strings.Cut(name, "[")
	if (rule != "event" && rule != "todo") || len(args) == 0 {
		return
	}
	target := args[0]
	go h.write(rule, target, payload) // a call may take a while: never inside the beat
}

func (h *Host) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Host) write(kind, target string, p map[string]any) {
	w := Write{Kind: kind, Target: target, Entity: h.Entities[target], Summary: str(p["summary"])}
	if w.Summary == "" {
		w.Summary = "Reminder"
	}
	var err error
	switch {
	case w.Entity == "":
		err = fmt.Errorf("no entity for %s", target)
	case h.Call == nil:
		err = fmt.Errorf("no Home Assistant")
	case kind == "event":
		var start time.Time
		if start, err = parseTime(str(p["start"]), h.now()); err == nil {
			mins := num(p["minutes"])
			if mins <= 0 {
				mins = 30
			}
			end := start.Add(time.Duration(mins) * time.Minute)
			w.Start, w.End = start.Format("2006-01-02 15:04:05"), end.Format("2006-01-02 15:04:05")
			err = h.call("calendar", "create_event", map[string]any{"entity_id": w.Entity, "summary": w.Summary,
				"start_date_time": w.Start, "end_date_time": w.End, "description": Mark})
		}
	case kind == "todo":
		data := map[string]any{"entity_id": w.Entity, "item": w.Summary, "description": Mark}
		if d := str(p["due"]); d != "" {
			if t, e := time.ParseInLocation("2006-01-02", d, time.Local); e == nil {
				w.Due = t.Format("2006-01-02")
				data["due_date"] = w.Due
			}
		}
		err = h.call("todo", "add_item", data)
	}
	if err != nil {
		w.Err = err.Error()
		h.mu.Lock()
		h.failed = append(h.failed, target)
		h.mu.Unlock()
	}
	if h.Save != nil {
		h.Save(w)
	}
}

func (h *Host) call(domain, service string, data map[string]any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return h.Call(ctx, domain, service, data)
}

// parseTime reads a local date and time: YYYY-MM-DDTHH:MM, with seconds or a zone, or HH:MM
// (today, or tomorrow when it has passed), or "in N minutes".
func parseTime(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return now.Add(time.Hour).Truncate(time.Hour), nil
	}
	for _, f := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return t, nil
		}
	}
	if t, err := time.ParseInLocation("15:04", s, time.Local); err == nil {
		d := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
		if d.Before(now) {
			d = d.AddDate(0, 0, 1)
		}
		return d, nil
	}
	if f := strings.Fields(s); len(f) >= 2 && f[0] == "in" {
		if n, err := strconv.ParseFloat(f[1], 64); err == nil {
			return now.Add(time.Duration(n * float64(time.Minute))), nil
		}
	}
	return time.Time{}, fmt.Errorf("a time I can't read: %q", s)
}

func str(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}
