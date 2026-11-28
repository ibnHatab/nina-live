// Package timers is the timers package's host (catalog/domains/timers): what the rules cannot
// hold, each slot's subject and time and the mode a reminder waits for. The rules take a slot and
// keep its lifecycle; the host learns the slot from the event that took it (started, armed), with
// the call's payload, and names a slot by what it is for.
//
// The time is the semantic world's (the redesign's W5): a countdown, an alarm and one that comes
// back are entries of the world's schedule (owner "timers", the slot its handle), which the world
// fires on its own clock; the host asserts due(T) once for each firing (Due), and full when no slot
// is free.
//
// Its slots are a projection of the log (stream "timers": set, every, armed, rang, cancelled), and
// the schedule the world's own, so a restart rebuilds both: the slots still taken are re-asserted
// as held(T) / held_for(T, S) / held_every(T), and a countdown whose entry the world no longer has
// (it fired while Nina was down, or was set before the schedule) is set again at its time, so it
// rings at once when that has passed.
package timers

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sw "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/semworld"
)

// Slots are the package's timer handles (timers.llp's type timer).
var Slots = []string{"k1", "k2", "k3", "k4", "k5", "k6"}

// Outcomes are the package's outcome atoms.
var Outcomes = []string{"done", "refused", "said"}

// Owner is the timers' name in the world's schedule.
const Owner = "timers"

// Schedule is the world's schedule as the host writes it (its own entries: Owner's).
type Schedule interface {
	Set(e sw.Sched) error
	Cancel(slot string) error
	Entries() []sw.Sched
}

// Event is one of the host's log events (stream "timers", its kind and data).
type Event struct {
	Kind string `json:"kind"` // set | every | armed | rang | rang_every | cancelled
	Slot string `json:"slot"`
	// set: the countdown; every: what comes back (a cron spec or a period, from Deadline); armed: the mode it waits for
	Subject  string  `json:"subject,omitempty"`
	Minutes  float64 `json:"minutes,omitempty"`
	Deadline float64 `json:"deadline,omitempty"`
	Cron     string  `json:"cron,omitempty"`
	Period   float64 `json:"period,omitempty"`
	Scene    string  `json:"scene,omitempty"`
}

type slot struct {
	state    string // "" free | counting | repeating | waiting
	subject  string
	minutes  float64
	deadline float64 // counting: when; repeating with a period: its first time
	cron     string
	period   float64
	scene    string
	held     bool // to re-assert after a restart
}

// Host is the timers' host.
type Host struct {
	Now  func() float64
	Save func(Event) // each change, to the log (nil: kept in memory only)

	mu       sync.Mutex
	sched    Schedule
	slots    map[string]*slot
	due      map[string]bool // the world's firings, for the next beat
	full     bool
	clearing bool            // a cancel's marker to retract
	signals  map[string]bool // a scene's wish seen at the last beat: retracted at this one
}

// New is the host, its slots rebuilt from the log's events (in order); Attach gives it the world's schedule.
func New(now func() float64, save func(Event), past []Event) *Host {
	h := &Host{Now: now, Save: save, slots: map[string]*slot{}, due: map[string]bool{}, signals: map[string]bool{}}
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

// Attach gives the host the world's schedule and makes it agree with the slots: a countdown or one
// that comes back the world does not hold is set again (a countdown at its time: past, it fires at
// the world's next settle); an entry of the timers' for a free slot goes.
func (h *Host) Attach(s Schedule) error {
	h.mu.Lock()
	h.sched = s
	have := map[string]bool{}
	for _, e := range s.Entries() {
		have[e.Slot] = true
	}
	var set []sw.Sched
	var drop []string
	for _, name := range Slots {
		x := h.slots[name]
		switch {
		case (x.state == "counting" || x.state == "repeating") && !have[name]:
			set = append(set, x.entry(name))
		case x.state != "counting" && x.state != "repeating" && have[name]:
			drop = append(drop, name)
		}
	}
	h.mu.Unlock()
	for _, e := range set {
		if err := s.Set(e); err != nil {
			return err
		}
	}
	for _, name := range drop {
		if err := s.Cancel(name); err != nil {
			return err
		}
	}
	return nil
}

// entry is the slot's schedule entry.
func (x *slot) entry(name string) sw.Sched {
	e := sw.Sched{Owner: Owner, Slot: name, Subject: x.subject}
	switch {
	case x.cron != "":
		e.Cron = x.cron
	case x.period > 0:
		e.Every, e.At = x.period, x.deadline
	default:
		e.At = x.deadline
	}
	return e
}

// Due is the world's firing of the slot's entry: due(T) at the next beat.
func (h *Host) Due(slotName string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.slots[slotName]; ok {
		h.due[slotName] = true
	}
}

// Pending: the host has lines for the next beat (a firing, a slot to re-assert).
func (h *Host) Pending() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.due) > 0 || h.clearing {
		return true
	}
	for _, s := range h.slots {
		if s.held {
			return true
		}
	}
	return false
}

func (h *Host) apply(e Event) {
	s := h.slots[e.Slot]
	if s == nil {
		return
	}
	switch e.Kind {
	case "set":
		*s = slot{state: "counting", subject: e.Subject, minutes: e.Minutes, deadline: e.Deadline}
	case "every":
		*s = slot{state: "repeating", subject: e.Subject, cron: e.Cron, period: e.Period, deadline: e.Deadline}
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
			case "repeating":
				out = append(out, "+held_every("+name+")")
			case "waiting":
				out = append(out, fmt.Sprintf("+held_for(%s,%s)", name, s.scene))
			}
		}
		if s.state != "" {
			used++
		}
		if h.due[name] {
			delete(h.due, name)
			if s.state == "counting" || s.state == "repeating" {
				out = append(out, "+due("+name+")")
			}
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
// bindings): a slot taken (with the call's payload), rung, cancelled; its schedule entry set or
// cancelled in the world (after the host's lock: the world's settle hands firings to Due under its own).
func (h *Host) OnEvent(name string, args []string, payload map[string]any) {
	name, _, _ = strings.Cut(name, "[") // set[T=k4]: the rule and its bindings
	if name == "cancel_all" {
		h.cancelAll()
		return
	}
	slotName, scene := "", ""
	for _, a := range args {
		if _, ok := h.slots[a]; ok {
			slotName = a
		} else if a == "home" || a == "night" || a == "away" {
			scene = a
		}
	}
	if slotName == "" {
		return
	}
	h.mu.Lock()
	var set *sw.Sched
	cancel := false
	switch name {
	case "set":
		m := number(payload["minutes"])
		if at, ok := payload["at"].(string); ok && strings.TrimSpace(at) != "" { // an alarm at a clock time
			if t, err := clockTime(at, time.Unix(int64(h.Now()), 0)); err == nil {
				m = t.Sub(time.Unix(int64(h.Now()), 0)).Minutes()
			}
			if s, _ := payload["subject"].(string); strings.TrimSpace(s) == "" {
				payload["subject"] = "the alarm"
			}
		}
		if m <= 0 {
			m = 1
		}
		h.record(Event{Kind: "set", Slot: slotName, Subject: subject(payload), Minutes: m, Deadline: h.Now() + m*60})
		e := h.slots[slotName].entry(slotName)
		set = &e
	case "every_set":
		e := Event{Kind: "every", Slot: slotName, Subject: subject(payload)}
		at, _ := payload["at"].(string)
		every, _ := payload["every"].(string)
		if m := number(payload["minutes"]); strings.TrimSpace(at) == "" && m > 0 {
			e.Period, e.Deadline = m*60, h.Now()+m*60
		} else if c, err := CronOf(at, every); err == nil {
			e.Cron = c
		} else {
			e.Cron = "0 8 * * *" // the rules took the slot: it rings daily at eight until said otherwise
		}
		h.record(e)
		x := h.slots[slotName].entry(slotName)
		set = &x
	case "remind_set":
		h.record(Event{Kind: "armed", Slot: slotName, Subject: subject(payload), Scene: scene})
	case "ring", "remind": // with what it was for (and the occasion a reminder waited for): what she says
		x := h.slots[slotName]
		h.record(Event{Kind: "rang", Slot: slotName, Subject: x.subject, Scene: x.scene})
	case "ring_every":
		h.record(Event{Kind: "rang_every", Slot: slotName, Subject: h.slots[slotName].subject})
	case "cancel", "cancel_every":
		h.record(Event{Kind: "cancelled", Slot: slotName})
		cancel = true
	case "cancel_reminder", "cancel_ready":
		h.record(Event{Kind: "cancelled", Slot: slotName})
	}
	s := h.sched
	h.mu.Unlock()
	if s == nil {
		return
	}
	if set != nil {
		s.Set(*set)
	} else if cancel {
		s.Cancel(slotName)
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
	switch s.state {
	case "counting":
		if left := s.deadline - h.Now(); left > 30 {
			return fmt.Sprintf("%s (in %s)", s.subject, minutes(left)), true
		}
	case "repeating":
		return fmt.Sprintf("%s (%s)", s.subject, s.when()), true
	}
	return s.subject, true
}

// Said is what she tells the person when a timer, an alarm or a reminder rings ("": the event is
// not a ring): the voice's NOTE, said whatever she is doing, the person having asked for it.
func (e Event) Said() string {
	subject := e.Subject
	if subject == "" {
		subject = "the timer"
	}
	switch {
	case e.Kind == "rang" && e.Scene != "":
		return fmt.Sprintf("A reminder the person asked for, for when %s, has come: %q. Tell them now, in a few words.", occasion[e.Scene], subject)
	case e.Kind == "rang", e.Kind == "rang_every":
		return fmt.Sprintf("A timer or an alarm the person set has rung: %q. Tell them now, in a few words.", subject)
	}
	return ""
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
		case "repeating":
			next := ""
			if n := s.next(h.Now()); n > 0 {
				next = fmt.Sprintf(", next %s, %s from now", time.Unix(int64(n), 0).Format("Mon 15:04"), left(n-h.Now()))
			}
			out = append(out, fmt.Sprintf("%s %s%s", s.subject, s.when(), next))
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

// clockTime: an alarm's time, HH:MM (today, or tomorrow when it has passed) or YYYY-MM-DDTHH:MM, local.
func clockTime(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, f := range []string{"2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return t, nil
		}
	}
	for _, f := range []string{"15:04", "15.04", "3:04pm", "3:04 pm", "3pm", "3 pm"} {
		if t, err := time.ParseInLocation(f, strings.ToLower(s), time.Local); err == nil {
			d := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
			if !d.After(now) {
				d = d.AddDate(0, 0, 1)
			}
			return d, nil
		}
	}
	return time.Time{}, fmt.Errorf("a time I can't read: %q", s)
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

// when: what comes back, in words ("every weekday at 07:00", "every 30 min").
func (s *slot) when() string {
	if s.period > 0 {
		return "every " + minutes(s.period)
	}
	f := strings.Fields(s.cron)
	if len(f) != 5 {
		return "again"
	}
	days := map[string]string{"*": "every day", "1-5": "every weekday", "0,6": "at weekends"}[f[4]]
	if days == "" {
		var names []string
		for _, d := range strings.Split(f[4], ",") {
			if n, err := strconv.Atoi(d); err == nil && n >= 0 && n <= 7 {
				names = append(names, time.Weekday(n%7).String())
			}
		}
		days = "on " + strings.Join(names, ", ")
	}
	hh, _ := strconv.Atoi(f[1])
	mm, _ := strconv.Atoi(f[0])
	return fmt.Sprintf("%s at %02d:%02d", days, hh, mm)
}

// next: when it comes back next, after NOW (0: not known).
func (s *slot) next(now float64) float64 {
	if s.period > 0 {
		n := s.deadline
		for n <= now {
			n += s.period
		}
		return n
	}
	c, err := sw.ParseCron(s.cron)
	if err != nil {
		return 0
	}
	t := c.Next(time.Unix(int64(now), 0))
	if t.IsZero() {
		return 0
	}
	return float64(t.Unix())
}

var dayNames = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

// CronOf is a clock time (HH:MM, as an alarm's) on some days (daily, weekdays, weekends, or the days
// named: "mon,thu", "monday and thursday") as a cron spec, local time.
func CronOf(at, every string) (string, error) {
	t, err := clockTime(at, time.Now())
	if err != nil {
		return "", err
	}
	if strings.Contains(at, "-") {
		return "", fmt.Errorf("a time that comes back is a clock time, not a date: %q", at)
	}
	e := strings.ToLower(strings.TrimSpace(every))
	var dow string
	switch e {
	case "", "daily", "every day", "day", "days", "each day":
		dow = "*"
	case "weekdays", "weekday", "workdays", "work days", "monday to friday":
		dow = "1-5"
	case "weekends", "weekend", "saturday and sunday":
		dow = "0,6"
	default:
		seen := map[int]bool{}
		for _, w := range strings.FieldsFunc(e, func(r rune) bool { return r == ',' || r == ' ' || r == '/' || r == ';' }) {
			if w == "and" || w == "on" || w == "every" {
				continue
			}
			d := strings.TrimSuffix(w, "s")
			if len(d) > 3 {
				d = d[:3]
			}
			n, ok := dayNames[d]
			if !ok {
				return "", fmt.Errorf("days I can't read: %q", every)
			}
			seen[n] = true
		}
		if len(seen) == 0 {
			return "", fmt.Errorf("days I can't read: %q", every)
		}
		var ds []int
		for d := range seen {
			ds = append(ds, d)
		}
		sort.Ints(ds)
		var parts []string
		for _, d := range ds {
			parts = append(parts, strconv.Itoa(d))
		}
		dow = strings.Join(parts, ",")
	}
	return fmt.Sprintf("%d %d * * %s", t.Minute(), t.Hour(), dow), nil
}
