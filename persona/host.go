package persona

import (
	"strings"
	"sync"
	"time"
)

// Outcomes are the persona package's outcome atoms (catalog/domains/persona).
var Outcomes = []string{"done", "refused"}

// Hold is how long a moment's mood holds (the person's wish: Wish).
var Hold = map[string]time.Duration{"emergency": 30 * time.Minute, "warning": 30 * time.Minute,
	"distress": time.Hour, "technical": 45 * time.Minute, "debate": 45 * time.Minute}

// Wish is how long a mood the person asked for holds.
const Wish = 2 * time.Hour

// Host is the persona package's host: the rules decide what a moment of the conversation (or the
// person's wish) calls for; the host holds that mood on her temperament (the Selector's override)
// and, when its time is up, tells the rules (+expired).
type Host struct {
	Sel      *Selector
	Now      func() time.Time
	OnChange func() // the mood changed: evaluate and publish now

	mu    sync.Mutex
	until time.Time
	pend  []string
}

func (h *Host) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Host) Tick(map[string]int) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.until.IsZero() && h.now().After(h.until) {
		h.until = time.Time{}
		h.pend = append(h.pend, "+expired")
	}
	out := h.pend
	h.pend = nil
	return out
}

// OnEvent: "steady[K=technical,T=focus]" holds focus for the moment; "wish[T=playful]" for Wish;
// relax, natural, lapse let it go.
func (h *Host) OnEvent(name string, args []string, payload map[string]any) {
	rule, rest, _ := strings.Cut(name, "[")
	kv := map[string]string{}
	for _, p := range strings.Split(strings.TrimSuffix(rest, "]"), ",") {
		if k, v, ok := strings.Cut(p, "="); ok {
			kv[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	now := h.now()
	h.mu.Lock()
	switch rule {
	case "steady", "steady_again", "steady_wished":
		d := Hold[kv["K"]]
		if d == 0 {
			d = 30 * time.Minute
		}
		h.until = now.Add(d)
		h.Sel.Override(kv["T"], h.until)
	case "wish", "wish_held", "wish_again":
		h.until = now.Add(Wish)
		h.Sel.Override(kv["T"], h.until)
	case "relax", "natural", "natural_wished", "lapse", "lapse_wished":
		h.until = time.Time{}
		h.Sel.Override("", now)
	default:
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()
	if h.OnChange != nil {
		h.OnChange()
	}
}
