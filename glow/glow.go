// Package glow is the glow package's host (catalog/domains/glow, the taste's lifted rules over
// colours): the bedroom's LED Geiling lights, in Alexa, driven through Home Assistant's script
// alexa_quiet (Alexa commands through the kitchen Echo, silenced while Alexa confirms).
//
// The colours are items of the semantic world's taste (kind "colour", tagged), registered at the
// start; the person's kept (the light left in a colour for KeepAfter) and skipped (changed within
// SkipWithin) are evidence for that colour at this time of day; the home's character leans before
// (warm after dark, bright in the morning). Like the music's host, it asserts favourite(O) or
// untasted, runner_up(O) or explore(O), last_here(O) from the taste, and acts when the rules engage.
package glow

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ibnHatab/nina-live/taste"
	sw "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/semworld"
)

// Outcomes are the package's outcome atoms.
var Outcomes = []string{"ok", "no"}

// Preset is a colour the light can take: its offer (glow.llp's handle), what Alexa calls the colour,
// its brightness, its tags.
type Preset struct {
	Offer      string
	Colour     string
	Brightness int
	Tags       []string
}

// Presets are the bedroom light's colours.
var Presets = []Preset{
	{"warm", "warm white", 60, []string{"warm", "calm", "cozy", "cosy", "soft"}},
	{"amber", "amber", 35, []string{"warm", "amber", "dim", "calm", "cozy", "cosy", "golden"}},
	{"sunset", "orange", 40, []string{"sunset", "warm", "dim", "orange"}},
	{"ember", "dark orange", 15, []string{"sunset", "ember", "sleep", "dim", "night", "red"}},
	{"white", "white", 80, []string{"white", "bright", "wake", "neutral"}},
	{"daylight", "daylight white", 100, []string{"daylight", "bright", "focus", "reading"}},
	{"cool", "cool white", 70, []string{"cool", "focus", "bright"}},
}

func (p Preset) id() string { return "colour:bedroom:" + p.Offer }

// Host is the glow's host.
type Host struct {
	Light string // what Alexa calls the light (default "LED Geiling lights")
	// Say sends Alexa commands, quietly (Home Assistant's script.alexa_quiet); nil: the light is not reached
	Say        func(ctx context.Context, commands []string) error
	Taste      taste.Client
	Now        func() time.Time
	KeepAfter  time.Duration // a colour left on this long is kept (default 30 min)
	SkipWithin time.Duration // changed within this, skipped (default 2 min)
	Mood       string

	mu         sync.Mutex
	registered bool
	asserted   map[string]bool
	fav, alt   string // the offers favourite(O) and runner_up(O)/explore(O) stand for
	last       map[string]string
	cur        *current
	left       *current
	pend       []string
	bright     int
}

type current struct {
	p       Preset
	at      time.Time
	counted bool
}

func (h *Host) taste() taste.Client {
	if h.Taste == nil {
		h.Taste = taste.NewMemory()
	}
	return h.Taste
}

func (h *Host) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Host) light() string {
	if h.Light != "" {
		return h.Light
	}
	return "LED Geiling lights"
}

func (h *Host) context() sw.Context { return sw.Context{Part: taste.Part(h.now()), Mood: h.Mood} }

func preset(offer string) (Preset, bool) {
	for _, p := range Presets {
		if p.Offer == offer {
			return p, true
		}
	}
	return Preset{}, false
}

func offerOf(id string) string { return strings.TrimPrefix(id, "colour:bedroom:") }

// Tick is one beat's world lines: the taste now, as the lifted rules need it.
func (h *Host) Tick(map[string]int) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.asserted == nil {
		h.asserted, h.last = map[string]bool{}, map[string]string{}
	}
	if !h.registered {
		h.registered = true
		for _, p := range Presets {
			h.taste().Register(sw.Item{ID: p.id(), Kind: "colour", Name: fmt.Sprintf("%s, %d%%", p.Colour, p.Brightness),
				Ref: p.Colour, Tags: p.Tags})
		}
	}
	c := h.context()
	want := map[string]bool{}
	if fav, ok := h.taste().Select("colour", nil, c, nil); ok && !fav.Explored {
		h.fav = offerOf(fav.ID)
		want["favourite("+h.fav+")"] = true
	} else {
		want["untasted"] = true
	}
	var ex []string
	if h.cur != nil {
		ex = append(ex, h.cur.p.id())
	}
	if alt, ok := h.taste().Select("colour", nil, c, ex); ok {
		h.alt = offerOf(alt.ID)
		if alt.Explored {
			want["explore("+h.alt+")"] = true
		} else {
			want["runner_up("+h.alt+")"] = true
		}
	}
	l := h.last[c.Part]
	if l == "" {
		l = "warm"
	}
	want["last_here("+l+")"] = true
	var out []string
	for a := range h.asserted {
		if !want[a] {
			out = append(out, "-"+a)
			delete(h.asserted, a)
		}
	}
	for a := range want {
		if !h.asserted[a] {
			out = append(out, "+"+a)
			h.asserted[a] = true
		}
	}
	sort.Strings(out)
	keep := h.KeepAfter
	if keep <= 0 {
		keep = 30 * time.Minute
	}
	if h.cur != nil && !h.cur.counted && h.now().Sub(h.cur.at) >= keep {
		h.cur.counted = true
		out = append(out, "+kept("+h.cur.p.Offer+")")
	}
	out = append(out, h.pend...)
	h.pend = nil
	return out
}

func (h *Host) OnEvent(name string, args []string, payload map[string]any) {
	rule, rest, _ := strings.Cut(name, "[")
	o := ""
	for _, kv := range strings.Split(strings.TrimSuffix(rest, "]"), ",") {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.TrimSpace(k) == "O" {
			o = strings.TrimSpace(v)
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.asserted == nil {
		h.asserted, h.last = map[string]bool{}, map[string]string{}
	}
	switch rule {
	case "glow", "reglow", "something", "something_new", "something_else", "something_new_one", "again":
		if o == "gx_colour" { // the person's own words: the taste's select over the colours' tags
			words, _ := payload["words"].(string)
			if words == "" {
				words, _ = payload["colour"].(string)
			}
			if c, ok := h.taste().Select("colour", []string{words}, h.context(), nil); ok {
				o = offerOf(c.ID)
			} else if c, ok := h.taste().Select("colour", nil, h.context(), nil); ok { // words it can't place: its own choice for now
				o = offerOf(c.ID)
			}
		}
		p, ok := preset(o)
		if !ok {
			return
		}
		h.leaving()
		h.cur = &current{p: p, at: h.now()}
		h.last[taste.Part(h.now())] = p.Offer
		h.bright = p.Brightness
		h.say(fmt.Sprintf("turn on %s", h.light()), fmt.Sprintf("set %s to %s", h.light(), p.Colour),
			fmt.Sprintf("set %s to %d percent", h.light(), p.Brightness))
	case "off":
		h.leaving()
		h.cur = nil
		h.say(fmt.Sprintf("turn off %s", h.light()))
	case "up", "down":
		if rule == "up" {
			h.bright = min(100, h.bright+20)
		} else {
			h.bright = max(5, h.bright-20)
		}
		h.say(fmt.Sprintf("set %s to %d percent", h.light(), h.bright))
	case "learn":
		if h.cur != nil {
			h.taste().Prefer(h.cur.p.id(), h.context(), 1)
		}
	case "unlearn":
		if h.left != nil {
			h.taste().Prefer(h.left.p.id(), h.context(), -1)
			h.left = nil
		}
	}
}

// leaving: the colour on is left; within SkipWithin, skipped. Holds h.mu.
func (h *Host) leaving() {
	skip := h.SkipWithin
	if skip <= 0 {
		skip = 2 * time.Minute
	}
	if h.cur != nil && h.now().Sub(h.cur.at) < skip {
		h.left = h.cur
		h.pend = append(h.pend, "+skipped("+h.cur.p.Offer+")")
	}
}

func (h *Host) say(cmds ...string) {
	if h.Say == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		h.Say(ctx, cmds)
	}()
}

// Name names a colour in the lines: "amber, 35%".
func (h *Host) Name(handle string) (string, bool) {
	if p, ok := preset(handle); ok {
		return fmt.Sprintf("%s, %d%%", p.Colour, p.Brightness), true
	}
	return "", false
}
