// Package persona is Nina's temperament (the user's design, 2026-11-24, the "fortune cookies"): a
// deterministic synthetic temperament that changes how she speaks, never what she knows or whether
// she helps. Slow cycles (fictional biorhythms and the moon, used for their shape) make a daily
// temperament of six dimensions; the hour's preferences and what the home is doing pick one of eight
// moods close to it; each mood has its persona (the dashboard's artwork) and a short prompt block.
//
// No randomness: the same day and hour give the same Nina. A mood holds for a while (MinStay) and a
// new one must win clearly (Advantage), so she never changes in the middle of a conversation.
// The numbers live in catalog/persona.yaml.
package persona

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Dimensions are the temperament's, in order.
var Dimensions = []string{"energy", "warmth", "curiosity", "playfulness", "introspection", "sociability"}

// Oscillators are the cycles a dimension weighs, in order.
var Oscillators = []string{"physical", "emotional", "intellectual", "persona", "moon", "h1", "h2"}

// Temperament is the six dimensions, each in [0,1].
type Temperament map[string]float64

type Cycle struct {
	Period float64 `yaml:"period"`
	Phase  float64 `yaml:"phase"`
}

type Mood struct {
	Artwork string             `yaml:"artwork"`
	Ideal   map[string]float64 `yaml:"ideal"`
	Prompt  string             `yaml:"prompt"`
}

// Config is catalog/persona.yaml.
type Config struct {
	Epoch  string           `yaml:"epoch"`
	Cycles map[string]Cycle `yaml:"cycles"`
	Moon   struct {
		New    string  `yaml:"new"`
		Period float64 `yaml:"period"`
	} `yaml:"moon"`
	Weights6 map[string]map[string]float64 `yaml:"temperament"`
	Moods    map[string]Mood               `yaml:"moods"`
	Hours    [][]string                    `yaml:"circadian"`
	Activity map[string]string             `yaml:"activity"`
	Weights  struct {
		Temperament float64 `yaml:"temperament"`
		Circadian   float64 `yaml:"circadian"`
		Activity    float64 `yaml:"activity"`
	} `yaml:"weights"`
	Every     time.Duration `yaml:"every"`
	MinStay   time.Duration `yaml:"min_stay"`
	Advantage float64       `yaml:"advantage"`
	Relative  bool          `yaml:"relative"` // the temperament's similarity rescaled across the moods each time

	epoch, moonNew time.Time
	windows        []window
}

type window struct {
	mu, sigma          float64 // hours
	primary, secondary string
}

// Load reads and checks the config.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, c.init()
}

func (c *Config) init() error {
	var err error
	if c.epoch, err = time.Parse("2006-01-02", c.Epoch); err != nil {
		return fmt.Errorf("persona: epoch: %w", err)
	}
	if c.moonNew, err = time.Parse(time.RFC3339, c.Moon.New); err != nil {
		return fmt.Errorf("persona: moon: %w", err)
	}
	for _, o := range Oscillators {
		if _, ok := c.Cycles[o]; !ok && o != "moon" {
			return fmt.Errorf("persona: no cycle %q", o)
		}
	}
	for _, d := range Dimensions {
		if _, ok := c.Weights6[d]; !ok {
			return fmt.Errorf("persona: no weights for %q", d)
		}
	}
	if len(c.Moods) == 0 {
		return fmt.Errorf("persona: no moods")
	}
	for name, m := range c.Moods {
		for _, d := range Dimensions {
			if _, ok := m.Ideal[d]; !ok {
				return fmt.Errorf("persona: mood %s has no ideal %s", name, d)
			}
		}
	}
	hours := func(s string) (float64, error) {
		var h, m int
		if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil {
			return 0, fmt.Errorf("persona: circadian %q: %w", s, err)
		}
		return float64(h) + float64(m)/60, nil
	}
	for _, w := range c.Hours {
		if len(w) != 4 {
			return fmt.Errorf("persona: circadian %v: [from, to, primary, secondary]", w)
		}
		a, err := hours(w[0])
		if err != nil {
			return err
		}
		b, err := hours(w[1])
		if err != nil {
			return err
		}
		for _, m := range w[2:] {
			if _, ok := c.Moods[m]; !ok {
				return fmt.Errorf("persona: circadian names no mood %q", m)
			}
		}
		c.windows = append(c.windows, window{mu: (a + b) / 2, sigma: math.Max(1, (b-a)/2), primary: w[2], secondary: w[3]})
	}
	if c.Every <= 0 {
		c.Every = 15 * time.Minute
	}
	return nil
}

// Moods are the moods' names, sorted.
func (c *Config) MoodNames() []string {
	var out []string
	for m := range c.Moods {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// day is t: whole local calendar days since the epoch (a DST change moves nothing).
func (c *Config) day(now time.Time) float64 {
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return math.Round(today.Sub(c.epoch).Hours() / 24)
}

// Oscillate is each oscillator's value today, in [-1,1].
func (c *Config) Oscillate(now time.Time) map[string]float64 {
	t := c.day(now)
	out := map[string]float64{}
	for name, cy := range c.Cycles {
		out[name] = math.Sin(2*math.Pi*t/cy.Period + cy.Phase)
	}
	y, m, d := now.Date()
	noon := time.Date(y, m, d, 12, 0, 0, 0, now.Location())
	out["moon"] = -math.Cos(2 * math.Pi * noon.Sub(c.moonNew).Hours() / 24 / c.Moon.Period)
	return out
}

// Temperament is today's.
func (c *Config) Temperament(now time.Time) Temperament {
	o := c.Oscillate(now)
	t := Temperament{}
	for _, d := range Dimensions {
		x := 0.0
		for k, w := range c.Weights6[d] {
			x += w * o[k]
		}
		t[d] = math.Max(0, math.Min(1, (x+1)/2))
	}
	return t
}

// MoonPhase names the moon's phase (for the prompt's atmosphere and the dashboard).
func (c *Config) MoonPhase(now time.Time) string {
	age := math.Mod(now.Sub(c.moonNew).Hours()/24, c.Moon.Period)
	if age < 0 {
		age += c.Moon.Period
	}
	names := []string{"new moon", "waxing crescent", "first quarter", "waxing gibbous", "full moon", "waning gibbous", "last quarter", "waning crescent"}
	return names[int(math.Floor(age/c.Moon.Period*8+0.5))%8]
}

// Circadian is each mood's preference at this hour, in [0,1].
func (c *Config) Circadian(now time.Time) map[string]float64 {
	h := float64(now.Hour()) + float64(now.Minute())/60
	out := map[string]float64{}
	g := func(mu, sigma float64) float64 {
		d := math.Abs(h - mu)
		d = math.Min(d, 24-d)
		return math.Exp(-d * d / (2 * sigma * sigma))
	}
	for _, w := range c.windows {
		out[w.primary] = math.Max(out[w.primary], g(w.mu, w.sigma))
		out[w.secondary] = math.Max(out[w.secondary], .5*g(w.mu, w.sigma))
	}
	return out
}

// Scores are each mood's Q: temperament, the hour, the activity ("" for none).
func (c *Config) Scores(now time.Time, activity string) map[string]float64 {
	t := c.Temperament(now)
	circ := c.Circadian(now)
	fav := c.Activity[activity]
	sim := map[string]float64{}
	lo, hi := math.Inf(1), math.Inf(-1)
	for name, m := range c.Moods {
		dist := 0.0
		for _, d := range Dimensions {
			dist += (t[d] - m.Ideal[d]) * (t[d] - m.Ideal[d])
		}
		sim[name] = 1 - math.Sqrt(dist)/math.Sqrt(float64(len(Dimensions)))
		lo, hi = math.Min(lo, sim[name]), math.Max(hi, sim[name])
	}
	out := map[string]float64{}
	for name := range c.Moods {
		s := sim[name]
		if c.Relative && hi > lo { // the closest mood 1, the farthest 0: as wide as the hour's preferences
			s = (s - lo) / (hi - lo)
		}
		a := 0.5
		if fav == name {
			a = 1
		}
		out[name] = c.Weights.Temperament*s + c.Weights.Circadian*circ[name] + c.Weights.Activity*a
	}
	return out
}

// State is Nina's mood now, and why.
type State struct {
	Mood        string
	Artwork     string
	Since       time.Time
	Temperament Temperament
	Moon        string
	Scores      map[string]float64
	Activity    string
	Override    string // a mood forced for a while ("" when none)
}

// Selector keeps the mood from one evaluation to the next (its hysteresis).
type Selector struct {
	C        *Config
	cur      State
	override struct {
		mood  string
		until time.Time
	}
}

// Override forces a mood until a time (an emergency's calm, a person's wish): immediate.
func (s *Selector) Override(mood string, until time.Time) {
	s.override.mood, s.override.until = mood, until
}

// Eval is the mood now: the best by score, unless the current one has held less than MinStay or
// the best does not win by Advantage. The first call takes the best.
func (s *Selector) Eval(now time.Time, activity string) State {
	sc := s.C.Scores(now, activity)
	best := ""
	for _, m := range s.C.MoodNames() {
		if best == "" || sc[m] > sc[best] {
			best = m
		}
	}
	next := s.cur.Mood
	switch {
	case s.override.mood != "" && now.Before(s.override.until):
		next = s.override.mood
	case next == "" || s.cur.Override != "":
		next = best
	case best != next && now.Sub(s.cur.Since) >= s.C.MinStay && sc[best] >= sc[next]+s.C.Advantage:
		next = best
	}
	since := s.cur.Since
	if next != s.cur.Mood {
		since = now
	}
	ov := ""
	if s.override.mood != "" && now.Before(s.override.until) {
		ov = s.override.mood
	}
	s.cur = State{Mood: next, Artwork: s.C.Moods[next].Artwork, Since: since, Temperament: s.C.Temperament(now),
		Moon: s.C.MoonPhase(now), Scores: sc, Activity: activity, Override: ov}
	return s.cur
}

// Prompt is the state's block for the voice (keyed "persona"): the mood's instructions, the day's
// strongest leanings in words, under a hundred tokens.
func (st State) Prompt(c *Config) string {
	var lean []string
	words := map[string][2]string{
		"energy": {"low-key", "energetic"}, "warmth": {"reserved", "warm"}, "curiosity": {"settled", "curious"},
		"playfulness": {"serious", "playful"}, "introspection": {"outward", "reflective"}, "sociability": {"quiet", "sociable"},
	}
	type kv struct {
		k string
		v float64
	}
	var ds []kv
	for _, d := range Dimensions {
		ds = append(ds, kv{d, st.Temperament[d]})
	}
	sort.Slice(ds, func(i, j int) bool { return math.Abs(ds[i].v-.5) > math.Abs(ds[j].v-.5) })
	for _, d := range ds[:2] {
		w := words[d.k][1]
		if d.v < .5 {
			w = words[d.k][0]
		}
		lean = append(lean, w)
	}
	return fmt.Sprintf("PERSONA_STATE: mood %s. %s Today you lean %s, under a %s. Same precision and helpfulness as always.",
		st.Mood, c.Moods[st.Mood].Prompt, strings.Join(lean, " and "), st.Moon)
}
