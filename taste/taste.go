// Package taste is the semantic world's taste as the domains' hosts use it (step 2 of the user's
// plan, 2026-11-24: preferences general, in the semantic world, selected the same way in any
// domain). A host registers what its domain offers (items with free tags), gives evidence (kept,
// skipped) and selects; every change is a world event (world.Apply: the log, replays, snapshots),
// a selection only reads.
//
// The home's character (catalog/character.yaml) is loaded into the world as prior events when it
// changes: what the home leans to before it knows the person.
package taste

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sync"
	"time"

	semhome "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/home"
	sw "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/semworld"
	"gopkg.in/yaml.v3"
)

// Client is the taste a host uses.
type Client interface {
	Register(it sw.Item)
	Prefer(id string, c sw.Context, delta float64)
	Select(kind string, tags []string, c sw.Context, exclude []string) (sw.Choice, bool)
	Get(id string) (sw.Item, bool)
}

// Part is the time of day.
func Part(t time.Time) string {
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

// World is the taste of a world kept by the log: changes through apply (the world's one writer).
type World struct {
	Home  *semhome.Home
	Apply func(semhome.Event) (semhome.Out, error)
	Now   func() float64
}

func (w *World) now() float64 {
	if w.Now != nil {
		return w.Now()
	}
	return float64(time.Now().UnixNano()) / 1e9
}

func (w *World) Register(it sw.Item) {
	w.Apply(semhome.Event{At: w.now(), Kind: "item", Item: &it})
}

func (w *World) Prefer(id string, c sw.Context, delta float64) {
	w.Apply(semhome.Event{At: w.now(), Kind: "prefer", ID: id, Pref: &c, Delta: delta})
}

func (w *World) Select(kind string, tags []string, c sw.Context, exclude []string) (sw.Choice, bool) {
	w.Home.W.Lock()
	defer w.Home.W.Unlock()
	return w.Home.W.Taste.Select(kind, tags, c, exclude, w.now())
}

func (w *World) Get(id string) (sw.Item, bool) {
	w.Home.W.Lock()
	defer w.Home.W.Unlock()
	return w.Home.W.Taste.Get(id)
}

// Memory is a taste of its own (a test's, a host without a world).
type Memory struct {
	mu  sync.Mutex
	T   *sw.Taste
	Now func() float64
}

func NewMemory() *Memory { return &Memory{T: sw.NewTaste()} }

func (m *Memory) now() float64 {
	if m.Now != nil {
		return m.Now()
	}
	return float64(time.Now().UnixNano()) / 1e9
}

func (m *Memory) Register(it sw.Item) { m.mu.Lock(); m.T.Register(it); m.mu.Unlock() }
func (m *Memory) Prefer(id string, c sw.Context, d float64) {
	m.mu.Lock()
	m.T.Prefer(id, c, d, m.now())
	m.mu.Unlock()
}
func (m *Memory) Select(kind string, tags []string, c sw.Context, ex []string) (sw.Choice, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.T.Select(kind, tags, c, ex, m.now())
}
func (m *Memory) Get(id string) (sw.Item, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.T.Get(id)
}

// Character is catalog/character.yaml.
type Character struct {
	Curious float64                        `yaml:"curious"`
	Moods   map[string]map[string][]string `yaml:"moods"`     // mood → kind → tags
	Routine map[string]string              `yaml:"routines"`  // mood → HH:MM: the home turns to it each day
	Her     map[string]string              `yaml:"her_moods"` // Nina's temper → the home's mood her light takes (one of the same name if absent)
	Priors  []struct {
		Tag  string  `yaml:"tag"`
		Part string  `yaml:"part"`
		Mood string  `yaml:"mood"`
		L    float64 `yaml:"l"`
	} `yaml:"priors"`
	Hash string `yaml:"-"`
}

// LoadCharacter reads the character file.
func LoadCharacter(path string) (*Character, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Character
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	s := sha256.Sum256(b)
	c.Hash = hex.EncodeToString(s[:8])
	return &c, nil
}

// MoodTags are the moods' tags for one kind (station, colour): what a host looks for in a mood.
func (c *Character) MoodTags(kind string) map[string][]string {
	out := map[string][]string{}
	for m, kinds := range c.Moods {
		if tags := kinds[kind]; len(tags) > 0 {
			out[m] = tags
		}
	}
	return out
}

// Events are the character as world events (its priors and its curiosity).
func (c *Character) Events(at float64) []semhome.Event {
	var out []semhome.Event
	if c.Curious > 0 {
		out = append(out, semhome.Event{At: at, Kind: "curious", Delta: c.Curious})
	}
	for _, p := range c.Priors {
		out = append(out, semhome.Event{At: at, Kind: "prior", Value: p.Tag, Pref: &sw.Context{Part: p.Part, Mood: p.Mood}, Delta: p.L})
	}
	return out
}
