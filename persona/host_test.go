package persona

import (
	"testing"
	"time"
)

// The rules' events hold a mood on the temperament, for the moment's time; it expires.
func TestHost(t *testing.T) {
	c := load(t)
	now := time.Date(2027, 3, 14, 13, 0, 0, 0, time.Local)
	s := &Selector{C: c}
	changed := 0
	h := &Host{Sel: s, Now: func() time.Time { return now }, OnChange: func() { changed++ }}
	s.Eval(now, "")
	h.OnEvent("steady[K=distress,T=cozy]", nil, nil)
	if st := s.Eval(now, ""); st.Mood != "cozy" || changed != 1 {
		t.Fatalf("distress: %s (changed %d)", st.Mood, changed)
	}
	now = now.Add(Hold["distress"] + time.Minute)
	if out := h.Tick(nil); len(out) != 1 || out[0] != "+expired" {
		t.Fatalf("no expiry: %v", out)
	}
	if st := s.Eval(now, ""); st.Override != "" {
		t.Fatalf("held past its time: %+v", st)
	}
	h.OnEvent("wish[T=playful]", nil, nil)
	if st := s.Eval(now, ""); st.Mood != "playful" {
		t.Fatalf("wish: %s", st.Mood)
	}
	h.OnEvent("natural_wished[A=playful]", nil, nil)
	if st := s.Eval(now, ""); st.Override != "" {
		t.Fatalf("not released: %+v", st)
	}
}
