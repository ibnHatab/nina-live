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
