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
	"errors"
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
	// Entities: a target handle (agenda.llp's type target) → its id in the backend (a Home Assistant
	// entity, a Google calendar or task list)
	Entities map[string]string
	// Names: a target handle → its name in Nina's lines
	Names map[string]string
	// Backend keeps the calendars and lists (Home Assistant's integrations: HA; Google's APIs; nil: none,
	// every write fails)
	Backend Backend
	Save    func(Write)
	Now     func() time.Time

	mu     sync.Mutex
	failed []string
}

// Default are Pascal's (2026-11-24): Nina's Google calendar, the local test pair, the person's
// own Google calendar (read only, by the rules), Google Tasks' default list.
var Default = map[string]string{
	"nina_cal": "calendar.nina", "nina_local": "calendar.nina_local", "person_cal": "calendar.aaosdeveloper_gmail_com",
	"my_tasks": "todo.my_tasks", "nina_todo": "todo.nina_local", "shopping": "todo.shopping_list",
}

// GoogleDefault are the handles on Google's own services (the agenda on Google by the person's choice, no
// agenda: map in her config): her calendar and the person's are the account's primary calendar, the tasks
// Google Tasks' default list, the shopping list a list titled so (made by the person; none: refused).
var GoogleDefault = map[string]string{
	"nina_cal": "primary", "person_cal": "primary", "my_tasks": "@default", "shopping": "Shopping list",
}

var names = map[string]string{"nina_cal": "my calendar", "nina_local": "the local test calendar",
	"person_cal": "your calendar", "my_tasks": "your tasks", "nina_todo": "my local list", "shopping": "the shopping list"}

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
	if (rule != "event" && rule != "todo" && rule != "remove") || len(args) == 0 {
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
	if w.Summary == "" && kind == "event" {
		w.Summary = "Reminder"
	}
	var err error
	switch {
	case w.Summary == "": // a to-do or a removal of nothing (2026-11-26: "what alarms do I have" had added "Reminder")
		err = fmt.Errorf("no item named")
	case w.Entity == "":
		err = fmt.Errorf("no entity for %s", target)
	case h.Backend == nil:
		err = fmt.Errorf("no calendar or list service")
	case kind == "event":
		var start time.Time
		at := str(p["start"])
		if at == "" {
			at = str(p["due"]) // the voice put the time there (pero, 2026-11-26: the event had gone to the next hour)
		}
		if start, err = parseTime(at, h.now()); err == nil {
			mins := num(p["minutes"])
			if mins <= 0 {
				mins = 30
			}
			end := start.Add(time.Duration(mins) * time.Minute)
			w.Start, w.End = start.Format("2006-01-02 15:04:05"), end.Format("2006-01-02 15:04:05")
			err = h.do(func(ctx context.Context) error {
				return h.Backend.CreateEvent(ctx, w.Entity, w.Summary, w.Start, w.End, Mark)
			})
		}
	case kind == "todo":
		if d := str(p["due"]); d != "" {
			if t, e := time.ParseInLocation("2006-01-02", d, time.Local); e == nil {
				w.Due = t.Format("2006-01-02")
			}
		}
		err = h.do(func(ctx context.Context) error { return h.Backend.AddItem(ctx, w.Entity, w.Summary, w.Due, Mark) })
	case kind == "remove":
		item := w.Summary
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		items, e := h.Backend.Items(ctx, w.Entity)
		cancel()
		if e != nil && !errors.Is(e, ErrNoItems) {
			err = e
			break
		}
		if e == nil { // the list's own item, by its name as said (else: removed by the name as said)
			if item = Match(items, w.Summary); item == "" {
				err = fmt.Errorf("no %q on it", w.Summary)
				break
			}
		}
		w.Summary = item
		err = h.do(func(ctx context.Context) error { return h.Backend.RemoveItem(ctx, w.Entity, item) })
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

// Match is the list's own item for one said ("call the plumber" → "Call the plumber"): the same
// words, case and punctuation aside; else the one item containing them; else "".
func Match(items []string, said string) string {
	key := func(s string) string {
		return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
			if r == '\'' || r == '"' || r == '.' || r == ',' || r == '!' || r == '?' {
				return -1
			}
			return r
		}, strings.ToLower(s))), " ")
	}
	k := key(said)
	for _, it := range items {
		if key(it) == k {
			return it
		}
	}
	var one string
	for _, it := range items {
		if k != "" && strings.Contains(key(it), k) {
			if one != "" {
				return "" // more than one: not this way
			}
			one = it
		}
	}
	return one
}

func (h *Host) do(f func(ctx context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return f(ctx)
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
	if day, hm, ok := strings.Cut(strings.ToLower(s), " "); ok && (day == "today" || day == "tomorrow") { // the voice's "tomorrow 13:00"
		if t, err := time.ParseInLocation("15:04", strings.TrimSpace(hm), time.Local); err == nil {
			d := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
			if day == "tomorrow" {
				d = d.AddDate(0, 0, 1)
			}
			return d, nil
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
