// Package port is the semantic world's quantized port: a world line's confidence P quantized into
// levels (the manifests' levels: at [0.5, 0.9] → unsure, likely, sure), with hysteresis, and a
// line passed on only when what it says (its value) or its level changes. The voice's context and
// the tasks' steering read the world through it, so a belief wavering at a threshold, its age or
// its wording does not tell Nina, or a task, the same thing again (the user's advice, 2026-11-24:
// quantized ports with probability from the semantic port, to avoid repeated dialogues).
package port

import (
	"strings"
	"sync"

	sw "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/semworld"
)

// Levels quantize a confidence.
type Levels struct {
	At     []float64 // ascending thresholds: len(Names)-1 of them
	Names  []string
	Margin float64 // a level is left only Margin past its threshold
}

// Default is the manifests' (comfort's chill, heat, damp): unsure < 0.5 ≤ likely < 0.9 ≤ sure.
var Default = Levels{At: []float64{0.5, 0.9}, Names: []string{"unsure", "likely", "sure"}, Margin: 0.05}

// Port passes on the lines whose value or level changed.
type Port struct {
	L    Levels
	mu   sync.Mutex
	last map[string]state // by stream and key
}

type state struct {
	value string
	level int
}

func New(l Levels) *Port { return &Port{L: l, last: map[string]state{}} }

// Value is what a line says, without its confidence, source and age ("bedroom ventilation:
// running (sure, sensor, just now)" → "bedroom ventilation: running").
func Value(text string) string {
	if i := strings.LastIndex(text, " ("); i > 0 && strings.HasSuffix(text, ")") {
		return text[:i]
	}
	return text
}

// level is p's level from prev (-1: none yet), with hysteresis.
func (l Levels) level(p float64, prev int) int {
	n := 0
	for n < len(l.At) && p >= l.At[n] {
		n++
	}
	if prev < 0 || n == prev {
		return n
	}
	if n > prev && p < l.At[prev]+l.Margin { // up: past the threshold by the margin
		return prev
	}
	if n < prev && p >= l.At[prev-1]-l.Margin { // down: below it by the margin
		return prev
	}
	return n
}

// Name is a level's name.
func (l Levels) Name(i int) string {
	if i >= 0 && i < len(l.Names) {
		return l.Names[i]
	}
	return ""
}

// Step is one line through the port: changed when its value or its level is new; level is its
// level's name.
func (p *Port) Step(l sw.Line) (changed bool, level string) {
	k := l.Stream + "\x00" + l.Key
	v := Value(l.Text)
	p.mu.Lock()
	defer p.mu.Unlock()
	prev, seen := p.last[k]
	pl := -1
	if seen {
		pl = prev.level
	}
	lv := p.L.level(l.P, pl)
	p.last[k] = state{v, lv}
	return !seen || prev.value != v || prev.level != lv, p.L.Name(lv)
}
