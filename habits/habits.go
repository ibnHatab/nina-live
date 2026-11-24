// Package habits is the habits package's host (catalog/domains/habits): "again" and "the usual",
// in any domain. It keeps the person's card calls (the log's stream "call": every call the rules
// answered done) and, for a request, finds the call meant:
//   - "again" (repeat): the most recent call;
//   - "the usual" (usual): the call made most often at this time of day (any time of day when
//     there is none at this one), the most recent breaking a tie;
//   - the person's words narrow either: a fuzzy match over the calls' words (the card, the intent,
//     the arguments), so "the music again" finds the last music call.
//
// The host asserts found(D) for the call's domain, or nothing; the rules decide whether D may be
// repeated, and the host replays the call through the same cards the voice uses.
package habits

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var Outcomes = []string{"done", "refused"}

// Call is one card call the rules answered done.
type Call struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
	At   float64        `json:"at"`
}

// Domain is the call's package (home_music → music).
func (c Call) Domain() string { return strings.TrimPrefix(c.Tool, "home_") }

// words are the call's words: its domain, its intent, its arguments' values.
func (c Call) words() []string {
	var ws []string
	ws = append(ws, strings.Fields(strings.ToLower(c.Domain()))...)
	keys := make([]string, 0, len(c.Args))
	for k := range c.Args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ws = append(ws, strings.Fields(strings.ToLower(fmt.Sprint(c.Args[k])))...)
	}
	return ws
}

// signature: the same request (the card and its arguments).
func (c Call) signature() string { return c.Tool + " " + strings.Join(c.words(), " ") }

// Say is how a call is named in a line: "music: play jazz".
func (c Call) Say() string {
	var parts []string
	if v, ok := c.Args["intent"].(string); ok {
		parts = append(parts, strings.ToLower(v))
	}
	keys := make([]string, 0, len(c.Args))
	for k := range c.Args {
		if k != "intent" && k != "words" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, fmt.Sprint(c.Args[k]))
	}
	return c.Domain() + ": " + strings.Join(parts, " ")
}

// Host is the habits' host.
type Host struct {
	// Replay makes a call again (the core's: the same cards as the voice)
	Replay func(ctx context.Context, tool string, args map[string]any) (status, text string)
	Now    func() time.Time

	mu      sync.Mutex
	calls   []Call
	pending []string // lines for the next beat
	chosen  map[string]Call
}

// New is the host with the calls so far (the log's).
func New(past []Call) *Host {
	return &Host{calls: past, chosen: map[string]Call{}}
}

// Add records a call the rules answered done.
func (h *Host) Add(c Call) {
	if c.Domain() == "habits" {
		return
	}
	h.mu.Lock()
	h.calls = append(h.calls, c)
	if len(h.calls) > 2000 {
		h.calls = h.calls[len(h.calls)-2000:]
	}
	h.mu.Unlock()
}

func (h *Host) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func part(t time.Time) string {
	switch hr := t.Hour(); {
	case hr >= 5 && hr < 12:
		return "morning"
	case hr >= 12 && hr < 18:
		return "afternoon"
	case hr >= 18 && hr < 23:
		return "evening"
	}
	return "night"
}

func (h *Host) Tick(map[string]int) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.pending
	h.pending = nil
	return out
}

func (h *Host) OnEvent(name string, args []string, payload map[string]any) {
	rule, rest, _ := strings.Cut(name, "[")
	switch rule {
	case "repeat", "usual":
		words, _ := payload["words"].(string)
		h.mu.Lock()
		c, ok := h.find(rule == "usual", words)
		if ok {
			h.chosen[c.Domain()] = c
			h.pending = append(h.pending, "+found("+c.Domain()+")")
		} else {
			h.pending = append(h.pending, "+nothing")
		}
		h.mu.Unlock()
	case "replay_it":
		d := strings.TrimSuffix(strings.TrimPrefix(rest, "D="), "]")
		h.mu.Lock()
		c, ok := h.chosen[d]
		h.mu.Unlock()
		if ok && h.Replay != nil {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				h.Replay(ctx, c.Tool, c.Args)
			}()
		}
	}
}

// find is the call meant. Holds h.mu.
func (h *Host) find(usual bool, words string) (Call, bool) {
	ws := strings.Fields(strings.ToLower(words))
	var cands []Call
	for _, c := range h.calls {
		if len(ws) == 0 || overlap(ws, c.words()) > 0 {
			cands = append(cands, c)
		}
	}
	if len(cands) == 0 {
		return Call{}, false
	}
	if !usual {
		return cands[len(cands)-1], true
	}
	p := part(h.now())
	at := func(c Call) string { return part(time.Unix(int64(c.At), 0)) }
	for _, sameHour := range []bool{true, false} {
		count := map[string]int{}
		last := map[string]int{}
		for i, c := range cands {
			if sameHour && at(c) != p {
				continue
			}
			count[c.signature()]++
			last[c.signature()] = i
		}
		best, bi := "", -1
		for s, n := range count {
			if bi < 0 || n > count[best] || (n == count[best] && last[s] > last[best]) {
				best, bi = s, last[s]
			}
		}
		if bi >= 0 {
			return cands[bi], true
		}
	}
	return Call{}, false
}

// overlap: the person's words found among the call's (a word of four letters or more matches on
// its stem, its first four letters: "evening" finds "evenings", "music" finds "musical").
func overlap(ws, cs []string) int {
	n := 0
	for _, w := range ws {
		if len(w) < 3 || w == "the" || w == "again" {
			continue
		}
		for _, c := range cs {
			if c == w || (len(w) >= 4 && len(c) >= 4 && w[:4] == c[:4]) {
				n++
				break
			}
		}
	}
	return n
}

// Name names a domain in the lines: the call chosen for it ("music: play jazz").
func (h *Host) Name(handle string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.chosen[handle]; ok {
		return c.Say(), true
	}
	return "", false
}
