package port

import (
	"testing"

	sw "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/semworld"
)

// TestAWaveringBeliefIsToldOnce: a belief wavering about a threshold, its age and its wording
// changing, is passed on once per value and level; a real change of value or level is passed on.
func TestAWaveringBeliefIsToldOnce(t *testing.T) {
	p := New(Default)
	line := func(text string, pr float64) sw.Line {
		return sw.Line{Stream: "env", Key: "u1.power", Text: text, P: pr}
	}
	steps := []struct {
		text string
		p    float64
		want bool
		lv   string
	}{
		{"bedroom ventilation: running (likely, command, just now)", 0.7, true, "likely"},
		{"bedroom ventilation: running (likely, command, 1 min ago)", 0.72, false, "likely"}, // the age
		{"bedroom ventilation: running (sure, sensor, just now)", 0.92, false, "likely"},     // within the margin
		{"bedroom ventilation: running (sure, sensor, just now)", 0.97, true, "sure"},
		{"bedroom ventilation: running (likely, sensor, 3 min ago)", 0.88, false, "sure"}, // wavering down
		{"bedroom ventilation: running (likely, sensor, 9 min ago)", 0.8, true, "likely"},
		{"bedroom ventilation: stopped (sure, sensor, just now)", 0.8, true, "likely"}, // the value
	}
	for i, s := range steps {
		got, lv := p.Step(line(s.text, s.p))
		if got != s.want || lv != s.lv {
			t.Fatalf("step %d (%s, %.2f): changed %v level %s, want %v %s", i, s.text, s.p, got, lv, s.want, s.lv)
		}
	}
}
