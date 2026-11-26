package agenda

import (
	"testing"
	"time"
)

// the times the voice sends: a date and time, a clock time, "tomorrow 13:00", "in 20 minutes"
func TestParseTime(t *testing.T) {
	now := time.Date(2026, 11, 26, 22, 40, 0, 0, time.Local)
	for _, c := range []struct {
		in   string
		want time.Time
	}{
		{"2026-11-27T10:00:00", time.Date(2026, 11, 27, 10, 0, 0, 0, time.Local)},
		{"tomorrow 13:00", time.Date(2026, 11, 27, 13, 0, 0, 0, time.Local)},
		{"Today 23:15", time.Date(2026, 11, 26, 23, 15, 0, 0, time.Local)},
		{"09:00", time.Date(2026, 11, 27, 9, 0, 0, 0, time.Local)},
		{"in 20 minutes", now.Add(20 * time.Minute)},
	} {
		if got, err := parseTime(c.in, now); err != nil || !got.Equal(c.want) {
			t.Errorf("%q: %v %v, want %v", c.in, got, err, c.want)
		}
	}
}
