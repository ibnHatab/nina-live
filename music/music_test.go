package music

import (
	"math"
	"testing"
)

// One knob: the music at her voice's level (lively), under it by 10 dB while anyone talks, on a
// stream at -19 dBFS at 100 % and her voice at -36 dBFS; the knob up moves both.
func TestLink(t *testing.T) {
	her := -36.0
	h := New(nil)
	h.Link = &Link{Top: -19, Voice: func() float64 { return her }}
	db := func() float64 { return math.Round(MPDLevel(h.volume()) - 19) }
	if d := db(); d != -36 {
		t.Fatalf("music at %v dBFS, want her -36", d)
	}
	h.off = -6
	if d := db(); d != -42 {
		t.Fatalf("calm: %v, want -42", d)
	}
	h.ducked = true
	if d := db(); d != -46 {
		t.Fatalf("ducked: %v, want -46", d)
	}
	h.ducked, h.off = false, 0
	her = -30 // the knob at 2 m... 4 m
	if d := db(); d != -30 {
		t.Fatalf("knob up: %v, want -30", d)
	}
	if v := MPDLevel(MPDVolume(-17)); math.Abs(v+17) > 1e-9 {
		t.Fatalf("round trip: %v", v)
	}
	h.Link = nil
	h.vol, h.ducked = 0.5, true
	if v := h.volume(); v != 0.15 {
		t.Fatalf("unlinked duck: %v", v)
	}
}

// PulseAudio's cubic volume: -12 dB is 0.63, -30 dB 0.32.
func TestPulseCurve(t *testing.T) {
	l := &Link{Curve: "pulse"}
	if v := l.volume(-12); math.Abs(v-0.631) > 0.001 {
		t.Fatalf("-12 dB: %v", v)
	}
	if v := l.volume(-30); math.Abs(v-0.316) > 0.001 {
		t.Fatalf("-30 dB: %v", v)
	}
}

// A place's languages: its stations, any when none is set; Radio Browser's lists ("french,english").
func TestSpeaks(t *testing.T) {
	h := &Host{Languages: []string{"english"}}
	for _, c := range []struct {
		s    Station
		want bool
	}{{Station{Name: "RMC", Language: "french"}, false}, {Station{Name: "BBC", Language: "english"}, true},
		{Station{Name: "both", Language: "French,English"}, true}, {Station{Name: "old"}, false}} {
		if got := h.speaks(c.s); got != c.want {
			t.Errorf("%s: %v, want %v", c.s.Name, got, c.want)
		}
	}
	if !(&Host{}).speaks(Station{Name: "any"}) {
		t.Error("no languages set: any station")
	}
}
