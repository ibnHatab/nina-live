// Package timers is the timers package's host (catalog/domains/timers): what the rules cannot
// hold, each slot's minutes, subject, deadline and the mode a reminder waits for. The rules take a
// slot and keep its lifecycle; the host learns the slot from the event that took it (started,
// armed), with the call's payload, asserts due(T) when a countdown's time has come and full when
// no slot is free, and names a slot by what it is for.
//
// Its state is a projection of the log (stream "timers": set, armed, rang, cancelled), so a
// restart rebuilds it: the slots still taken are re-asserted as held(T) / held_for(T, S), and a
// countdown that ended while Nina was down rings at the first beat.
package timers

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Slots are the package's timer handles (timers.llp's type timer).
var Slots = []string{"k1", "k2", "k3", "k4", "k5", "k6"}

// Outcomes are the package's outcome atoms.
var Outcomes = []string{"done", "refused", "said"}

// Event is one of the host's log events (stream "timers", its kind and data).
type Event struct {
	Kind string `json:"kind"` // set | armed | rang | cancelled
	Slot string `json:"slot"`
	// set: the countdown; armed: the mode it waits for
	Subject  string  `json:"subject,omitempty"`
	Minutes  float64 `json:"minutes,omitempty"`
	Deadline float64 `json:"deadline,omitempty"`
	Scene    string  `json:"scene,omitempty"`
}

type slot struct {
	state    string // "" free | counting | waiting
	subject  string
	minutes  float64
	deadline float64
	scene    string
	rung     bool // due(T) asserted
	held     bool // to re-assert after a restart
}

// Host is the timers' host.
type Host struct {
	Now  func() float64
	Save func(Event) // each change, to the log (nil: kept in memory only)

	mu       sync.Mutex
	slots    map[string]*slot
	full     bool
	clearing bool            // a cancel's marker to retract
	signals  map[string]bool // a scene's wish seen at the last beat: retracted at this one
}

// New is the host, its slots rebuilt from the log's events (in order).
func New(now func() float64, save func(Event), past []Event) *Host {
	h := &Host{Now: now, Save: save, slots: map[string]*slot{}, signals: map[string]bool{}}
	for _, s := range Slots {
		h.slots[s] = &slot{}
	}
	for _, e := range past {
		h.apply(e)
	}
	for _, s := range h.slots {
		s.held = s.state != ""
	}
	return h
}

func (h *Host) apply(e Event) {
	s := h.slots[e.Slot]
	if s == nil {
		return
	}
	switch e.Kind {
	case "set":
		*s = slot{state: "counting", subject: e.Subject, minutes: e.Minutes, deadline: e.Deadline}
	case "armed":
		*s = slot{state: "waiting", subject: e.Subject, scene: e.Scene}
	case "rang", "cancelled":
		subject := s.subject // the name stays for the line being said
		*s = slot{subject: subject}
	}
}

func (h *Host) record(e Event) {
	h.apply(e)
	if h.Save != nil {
		h.Save(e)
	}
}

// Tick is one beat's world lines.
func (h *Host) Tick(counts map[string]int) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.Now()
	var out []string
	// what lasts a beat: a cancel's marker, a scene's wish (the reminders waiting for it have rung)
	if h.clearing {
		h.clearing = false
		out = append(out, "-clearing")
	}
	for _, sc := range []string{"home", "night", "away"} {
		a := "scene_timers(" + sc + ")"
		if counts[a] > 0 {
			if h.signals[a] {
				out = append(out, "-"+a)
				delete(h.signals, a)
			} else {
				h.signals[a] = true
			}
		} else {
			delete(h.signals, a)
		}
	}
	used := 0
	for _, name := range Slots {
		s := h.slots[name]
		if s.held {
			s.held = false
			switch s.state {
			case "counting":
				out = append(out, "+held("+name+")")
			case "waiting":
				out = append(out, fmt.Sprintf("+held_for(%s,%s)", name, s.scene))
			}
		}
		if s.state != "" {
			used++
		}
		if s.state == "counting" && !s.rung && now >= s.deadline {
			s.rung = true
			out = append(out, "+due("+name+")")
		}
	}
	if full := used == len(Slots); full != h.full {
		h.full = full
		if full {
			out = append(out, "+full")
		} else {
			out = append(out, "-full")
		}
	}
	return out
}

// OnEvent takes the rules' fired rules (an event is named after its rule; its args are the rule's
// bindings): a slot taken (with the call's payload), rung, cancelled.
func (h *Host) OnEvent(name string, args []string, payload map[string]any) {
	name, _, _ = strings.Cut(name, "[") // set[T=k4]: the rule and its bindings
	if name == "cancel_all" {
		h.cancelAll()
		return
	}
	slot, scene := "", ""
	for _, a := range args {
		if _, ok := h.slots[a]; ok {
			slot = a
		} else if a == "home" || a == "night" || a == "away" {
			scene = a
		}
	}
	if slot == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	switch name {
	case "set":
		m := number(payload["minutes"])
		if m <= 0 {
			m = 1
		}
		h.record(Event{Kind: "set", Slot: slot, Subject: subject(payload), Minutes: m, Deadline: h.Now() + m*60})
	case "remind_set":
		h.record(Event{Kind: "armed", Slot: slot, Subject: subject(payload), Scene: scene})
	case "ring", "remind":
		h.record(Event{Kind: "rang", Slot: slot})
	case "cancel", "cancel_reminder":
		h.record(Event{Kind: "cancelled", Slot: slot})
	}
}

func (h *Host) cancelAll() {
	h.mu.Lock()
	h.clearing = true
	h.mu.Unlock()
}

// Name is a slot's name in the rules' lines: what it is for, and a countdown's time left.
func (h *Host) Name(handle string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.slots[handle]
	if s == nil || s.subject == "" {
		return "", false
	}
	if s.state == "counting" {
		if left := s.deadline - h.Now(); left > 30 {
			return fmt.Sprintf("%s (in %s)", s.subject, minutes(left)), true
		}
	}
	return s.subject, true
}

var occasion = map[string]string{"away": "you leave", "night": "you go to bed", "home": "you are back"}

// Summary is what is set, as one line for the voice's context.
func (h *Host) Summary() string {
	set := h.Set()
	if len(set) == 0 {
		return "timers: none set"
	}
	return "timers set: " + strings.Join(set, "; ")
}

// Set are the slots taken now, for a status line.
func (h *Host) Set() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, name := range Slots {
		s := h.slots[name]
		switch s.state {
		case "counting":
			// the time left with it: she reckoned it wrong from the clock time alone (2026-11-26: "about
			// two minutes" for 1.5), and read the line out as it was
			out = append(out, fmt.Sprintf("%s at %s, %s from now", s.subject, time.Unix(int64(s.deadline), 0).Format("15:04"), left(s.deadline-h.Now())))
		case "waiting":
			out = append(out, fmt.Sprintf("%s when %s", s.subject, occasion[s.scene]))
		}
	}
	return out
}

// left: a countdown's time left, to the 10 s under three minutes.
func left(sec float64) string {
	if sec >= 180 {
		return minutes(sec)
	}
	t := int(math.Round(math.Max(sec, 0)/10) * 10)
	switch m, s := t/60, t%60; {
	case m == 0:
		return fmt.Sprintf("%d s", s)
	case s == 0:
		return fmt.Sprintf("%d min", m)
	default:
		return fmt.Sprintf("%d min %d s", m, s)
	}
}

func minutes(sec float64) string {
	m := math.Round(sec / 60)
	switch {
	case m < 1:
		return "under a minute"
	case m == 1:
		return "1 min"
	case m < 120:
		return fmt.Sprintf("%.0f min", m)
	}
	return fmt.Sprintf("%.1f h", m/60)
}

func number(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f
	}
	return 0
}

func subject(p map[string]any) string {
	if s, ok := p["subject"].(string); ok && strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	return "the timer"
}
