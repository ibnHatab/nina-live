package timers

import (
	"testing"
	"time"
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
