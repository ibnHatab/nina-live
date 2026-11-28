package timers

import (
	"strings"
	"testing"
	"time"

	sw "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/semworld"
)

// an alarm's clock time: today's when it is still to come, else tomorrow's; a date and time as given
func TestClockTime(t *testing.T) {
	now := time.Date(2026, 11, 26, 21, 50, 0, 0, time.Local)
	for _, c := range []struct {
		in   string
		want time.Time
	}{
		{"07:30", time.Date(2026, 11, 27, 7, 30, 0, 0, time.Local)},
		{"22:15", time.Date(2026, 11, 26, 22, 15, 0, 0, time.Local)},
		{"7:30pm", time.Date(2026, 11, 27, 19, 30, 0, 0, time.Local)},
		{"2026-11-28T09:00", time.Date(2026, 11, 28, 9, 0, 0, 0, time.Local)},
	} {
		if got, err := clockTime(c.in, now); err != nil || !got.Equal(c.want) {
			t.Errorf("%s: %v %v, want %v", c.in, got, err, c.want)
		}
	}
	if _, err := clockTime("soonish", now); err == nil {
		t.Error("soonish read")
	}
}

// ALARM's payload (at): the countdown runs to that time, named "the alarm"
func TestAlarmSetsTheDeadline(t *testing.T) {
	now := float64(time.Date(2026, 11, 26, 21, 50, 0, 0, time.Local).Unix())
	var got []Event
	h := New(func() float64 { return now }, func(e Event) { got = append(got, e) }, nil)
	h.OnEvent("set[T=k1]", []string{"k1"}, map[string]any{"at": "07:30"})
	want := float64(time.Date(2026, 11, 27, 7, 30, 0, 0, time.Local).Unix())
	if len(got) != 1 || got[0].Deadline != want || got[0].Subject != "the alarm" {
		t.Fatalf("alarm: %+v", got)
	}
}

// mem is the world's schedule in memory (the host's owner's part), fired by settle.
type mem struct {
	s   *sw.Schedule
	now func() float64
}

func (m *mem) Set(e sw.Sched) error     { e.Owner = Owner; return m.s.Set(e, m.now()) }
func (m *mem) Cancel(slot string) error { m.s.Cancel(Owner, slot); return nil }
func (m *mem) Entries() []sw.Sched      { return m.s.Of(Owner) }
func (m *mem) settle(h *Host) {
	for _, d := range m.s.Settle(m.now()) {
		h.Due(d.Slot)
	}
}

func has(lines []string, l string) bool {
	for _, x := range lines {
		if x == l {
			return true
		}
	}
	return false
}

// a countdown is the world's entry: due(T) once when the world fires it, not before
func TestACountdownIsTheWorldsEntry(t *testing.T) {
	now := float64(time.Date(2026, 11, 26, 21, 50, 0, 0, time.Local).Unix())
	clock := func() float64 { return now }
	h := New(clock, nil, nil)
	m := &mem{sw.NewSchedule(), clock}
	if err := h.Attach(m); err != nil {
		t.Fatal(err)
	}
	h.OnEvent("set[T=k2]", []string{"k2"}, map[string]any{"minutes": 5.0, "subject": "the tea"})
	if e := m.Entries(); len(e) != 1 || e[0].Slot != "k2" || e[0].At != now+300 || e[0].Periodic() {
		t.Fatalf("entries: %+v", e)
	}
	now += 299
	m.settle(h)
	if h.Pending() || has(h.Tick(nil), "+due(k2)") {
		t.Fatal("due early")
	}
	now += 1
	m.settle(h)
	if !h.Pending() || !has(h.Tick(nil), "+due(k2)") {
		t.Fatal("not due at its time")
	}
	m.settle(h)
	if has(h.Tick(nil), "+due(k2)") {
		t.Fatal("due twice")
	}
	h.OnEvent("ring[T=k2]", []string{"k2"}, nil)
	if len(h.Set()) != 0 {
		t.Fatalf("set after it rang: %v", h.Set())
	}
}

// one that comes back: a cron entry, due at each of its times; the slot stays taken until cancelled
func TestOneThatComesBack(t *testing.T) {
	now := float64(time.Date(2026, 11, 27, 8, 0, 0, 0, time.Local).Unix()) // a Friday
	clock := func() float64 { return now }
	var got []Event
	h := New(clock, func(e Event) { got = append(got, e) }, nil)
	m := &mem{sw.NewSchedule(), clock}
	h.Attach(m)
	h.OnEvent("every_set[T=k1]", []string{"k1"}, map[string]any{"at": "07:00", "every": "weekdays", "subject": "wake up"})
	if len(got) != 1 || got[0].Kind != "every" || got[0].Cron != "0 7 * * 1-5" {
		t.Fatalf("events: %+v", got)
	}
	if s := h.Set(); len(s) != 1 || !strings.Contains(s[0], "every weekday at 07:00, next Mon 07:00") {
		t.Fatalf("set: %v", s)
	}
	for day := 0; day < 2; day++ {
		now = float64(time.Date(2026, 11, 30+day, 7, 0, 0, 0, time.Local).Unix())
		m.settle(h)
		if !has(h.Tick(nil), "+due(k1)") {
			t.Fatalf("day %d: not due", day)
		}
		h.OnEvent("ring_every[T=k1]", []string{"k1"}, nil)
	}
	if e := m.Entries(); len(e) != 1 || e[0].Fired != 2 {
		t.Fatalf("entries: %+v", e)
	}
	h.OnEvent("every_set[T=k3]", []string{"k3"}, map[string]any{"minutes": 30.0, "subject": "stretch"})
	if e, _ := m.s.Get(Owner, "k3"); e.Every != 1800 || e.At != now+1800 {
		t.Fatalf("a period: %+v", e)
	}
	h.OnEvent("cancel_every[T=k1]", []string{"k1"}, nil)
	if _, ok := m.s.Get(Owner, "k1"); ok {
		t.Fatal("cancelled, still in the world")
	}
}

// a restart: a countdown the world does not hold is set again at its time (past: due at the next settle);
// one that comes back is set again; an entry of the timers' for a free slot goes
func TestARestartAgreesWithTheWorld(t *testing.T) {
	now := float64(time.Date(2026, 11, 26, 22, 0, 0, 0, time.Local).Unix())
	clock := func() float64 { return now }
	past := []Event{{Kind: "set", Slot: "k1", Subject: "the tea", Minutes: 5, Deadline: now - 60},
		{Kind: "every", Slot: "k2", Subject: "pills", Cron: "0 22 * * *"},
		{Kind: "set", Slot: "k3", Deadline: now - 600}, {Kind: "rang", Slot: "k3"}}
	h := New(clock, nil, past)
	m := &mem{sw.NewSchedule(), clock}
	m.Set(sw.Sched{Slot: "k3", At: now + 60})
	if err := h.Attach(m); err != nil {
		t.Fatal(err)
	}
	if e := m.Entries(); len(e) != 2 || e[0].Slot != "k1" || e[1].Slot != "k2" || e[1].Cron != "0 22 * * *" {
		t.Fatalf("entries: %+v", e)
	}
	l := h.Tick(nil)
	if !has(l, "+held(k1)") || !has(l, "+held_every(k2)") {
		t.Fatalf("held: %v", l)
	}
	m.settle(h)
	if !has(h.Tick(nil), "+due(k1)") {
		t.Fatal("the countdown that ended while down did not ring")
	}
}

func TestCronOf(t *testing.T) {
	for _, c := range []struct{ at, every, want string }{
		{"07:00", "weekdays", "0 7 * * 1-5"}, {"22:30", "daily", "30 22 * * *"}, {"8:00", "", "0 8 * * *"},
		{"09:15", "weekends", "15 9 * * 0,6"}, {"08:00", "mon,thu", "0 8 * * 1,4"},
		{"08:00", "Mondays and Thursdays", "0 8 * * 1,4"}, {"7pm", "sunday", "0 19 * * 0"},
	} {
		if got, err := CronOf(c.at, c.every); err != nil || got != c.want {
			t.Errorf("%s %s: %q %v, want %q", c.at, c.every, got, err, c.want)
		}
	}
	for _, bad := range [][2]string{{"soon", "daily"}, {"08:00", "fortnightly"}, {"08:00", "s"}, {"2026-11-28T08:00", "daily"}} {
		if _, err := CronOf(bad[0], bad[1]); err == nil {
			t.Errorf("%v read", bad)
		}
	}
}
