package persona

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

func load(t *testing.T) *Config {
	c, err := Load("../catalog/persona.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Deterministic: the same moment gives the same Nina.
func TestDeterministic(t *testing.T) {
	c := load(t)
	at := time.Date(2027, 3, 14, 10, 30, 0, 0, time.Local)
	a, b := (&Selector{C: c}).Eval(at, ""), (&Selector{C: c}).Eval(at, "")
	if a.Mood != b.Mood || a.Prompt(c) != b.Prompt(c) {
		t.Fatalf("%v != %v", a, b)
	}
	if n := len(strings.Fields(a.Prompt(c))); n > 75 {
		t.Fatalf("the prompt block is %d words: %s", n, a.Prompt(c))
	}
	t.Log(a.Prompt(c))
}

// A forced mood is immediate; it ends when it ends.
func TestOverride(t *testing.T) {
	c := load(t)
	s := &Selector{C: c}
	at := time.Date(2027, 3, 14, 13, 0, 0, 0, time.Local)
	s.Eval(at, "")
	s.Override("calm", at.Add(30*time.Minute))
	if st := s.Eval(at.Add(time.Minute), ""); st.Mood != "calm" || st.Override != "calm" {
		t.Fatalf("override: %+v", st)
	}
	if st := s.Eval(at.Add(45*time.Minute), ""); st.Override != "" {
		t.Fatalf("override not over: %+v", st)
	}
}

// TestYear simulates a year in steps of the evaluation interval: every mood appears, a mood holds
// MinStay, and the day has a few moods, not dozens. -v prints the distribution and two weeks.
func TestYear(t *testing.T) {
	c := load(t)
	s := &Selector{C: c}
	start := time.Date(2026, 11, 25, 0, 0, 0, 0, time.Local)
	share, runs, switches := map[string]time.Duration{}, map[string][]time.Duration{}, 0
	cur, since := "", start
	days := map[string]map[string]bool{}
	for at := start; at.Before(start.AddDate(1, 0, 0)); at = at.Add(c.Every) {
		act := ""
		if at.Hour() == 19 || at.Hour() == 8 { // someone talking now and then
			act = "talking"
		}
		st := s.Eval(at, act)
		share[st.Mood] += c.Every
		d := at.Format("2006-01-02")
		if days[d] == nil {
			days[d] = map[string]bool{}
		}
		days[d][st.Mood] = true
		if st.Mood != cur {
			if cur != "" {
				switches++
				if at.Sub(since) < c.MinStay {
					t.Fatalf("%s held %v < %v at %v", cur, at.Sub(since), c.MinStay, at)
				}
				runs[cur] = append(runs[cur], at.Sub(since))
			}
			cur, since = st.Mood, at
		}
	}
	total := 365 * 24 * time.Hour
	var names []string
	for _, m := range c.MoodNames() {
		names = append(names, m)
		if share[m] == 0 {
			t.Errorf("%s never appears in a year", m)
		}
	}
	sort.Slice(names, func(i, j int) bool { return share[names[i]] > share[names[j]] })
	for _, m := range names {
		var sum time.Duration
		for _, r := range runs[m] {
			sum += r
		}
		mean := time.Duration(0)
		if len(runs[m]) > 0 {
			mean = sum / time.Duration(len(runs[m]))
		}
		t.Logf("%-8s %5.1f%%  runs %4d  mean %v", m, 100*float64(share[m])/float64(total), len(runs[m]), mean.Round(time.Minute))
	}
	perDay := float64(switches) / 365
	t.Logf("switches a day: %.1f", perDay)
	if perDay < 1 || perDay > 10 {
		t.Errorf("switches a day %.1f: want between 1 and 10", perDay)
	}
	// two weeks, as the design's table: the mood at 11:00 and at 21:00
	s2 := &Selector{C: c}
	for d := 0; d < 14; d++ {
		day := start.AddDate(0, 0, d)
		var line []string
		for h := 0; h < 24; h++ {
			for m := 0; m < 60; m += 15 {
				st := s2.Eval(day.Add(time.Duration(h)*time.Hour+time.Duration(m)*time.Minute), "")
				if (h == 11 || h == 21) && m == 0 {
					line = append(line, fmt.Sprintf("%02d:00 %s", h, st.Artwork))
				}
			}
		}
		tp := c.Temperament(day)
		t.Logf("%s  %s   E%.2f W%.2f C%.2f P%.2f I%.2f S%.2f  %s", day.Format("Mon 02 Jan"), strings.Join(line, "  "),
			tp["energy"], tp["warmth"], tp["curiosity"], tp["playfulness"], tp["introspection"], tp["sociability"], c.MoonPhase(day))
	}
}
