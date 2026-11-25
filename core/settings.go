package core

// Nina's settings from her screens (the user, 2026-11-25: a Nina card in Home Assistant's full mode):
// the name she goes by, her persona (automatic, from her temperament, or one pinned), her temperament's
// tuning (the weights of temperament, hour and activity, how long a mood holds, the margin a new one
// needs). The panel's actions (dialogue.OnAction): name, persona, temperament; each is kept in the log
// (stream settings), applied again at the start, and told to her screens (the settings event).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	nlog "github.com/ibnHatab/nina-live/log"
	"github.com/ibnHatab/nina-live/persona"
)

type Settings struct {
	Name    string `json:"name"`    // "" Nina
	Persona string `json:"persona"` // "auto", or an artwork pinned
	// the temperament's tuning ("" / 0: as catalog/persona.yaml says)
	Weights   []float64 `json:"weights,omitempty"` // temperament, circadian, activity
	MinStay   string    `json:"min_stay,omitempty"`
	Advantage float64   `json:"advantage,omitempty"`
}

// Action is a panel action of Nina's own (false: not hers).
func (k *Core) Action(action, arg string) (bool, error) {
	k.mu.Lock()
	st := k.settings
	k.mu.Unlock()
	switch action {
	case "name":
		st.Name = strings.TrimSpace(arg)
	case "persona":
		st.Persona = strings.TrimSpace(arg)
	case "temperament":
		var t Settings
		if err := json.Unmarshal([]byte(arg), &t); err != nil {
			return true, fmt.Errorf("temperament: %w", err)
		}
		if len(t.Weights) != 0 && len(t.Weights) != 3 {
			return true, fmt.Errorf("temperament: three weights (temperament, circadian, activity)")
		}
		if t.MinStay != "" {
			if _, err := time.ParseDuration(t.MinStay); err != nil {
				return true, fmt.Errorf("temperament: min_stay: %w", err)
			}
		}
		st.Weights, st.MinStay, st.Advantage = t.Weights, t.MinStay, t.Advantage
	default:
		return false, nil
	}
	if err := k.applySettings(st); err != nil {
		return true, err
	}
	k.Log.Append(k.Cfg.Now(), "settings", "set", action, "", st, 1)
	return true, nil
}

// applySettings puts the settings to work and tells her screens.
func (k *Core) applySettings(st Settings) error {
	if k.mood != nil {
		c := k.mood.C
		if st.Persona != "" && st.Persona != "auto" {
			mood := ""
			for m, d := range c.Moods {
				if d.Artwork == st.Persona {
					mood = m
				}
			}
			if mood == "" {
				return fmt.Errorf("persona: no %q (one of the moods' artworks, or auto)", st.Persona)
			}
			k.mood.Override(mood, time.Now().AddDate(100, 0, 0)) // pinned
		} else {
			k.mood.Override("", time.Now())
		}
		k.mood.Tune(func(c *persona.Config) {
			if k.base == nil { // the file's numbers, to go back to
				k.base = &Settings{Weights: []float64{c.Weights.Temperament, c.Weights.Circadian, c.Weights.Activity},
					MinStay: c.MinStay.String(), Advantage: c.Advantage}
			}
			w, ms, adv := st.Weights, st.MinStay, st.Advantage
			if len(w) != 3 {
				w = k.base.Weights
			}
			if ms == "" {
				ms = k.base.MinStay
			}
			if adv == 0 {
				adv = k.base.Advantage
			}
			c.Weights.Temperament, c.Weights.Circadian, c.Weights.Activity = w[0], w[1], w[2]
			c.MinStay, _ = time.ParseDuration(ms)
			c.Advantage = adv
		})
	}
	k.mu.Lock()
	k.settings = st
	k.moodAt = time.Time{} // her mood again at the next beat
	k.mu.Unlock()
	if v := k.Voice(); v != nil {
		if st.Name != "" && !strings.EqualFold(st.Name, "nina") {
			v.Context("name", fmt.Sprintf("name: the people here call you %s now; answer to it and use it for yourself (the wake word is still \"Nina\")", st.Name))
		} else {
			v.Context("name", "")
		}
	}
	k.settingsNow()
	return nil
}

// settingsNow tells her screens the settings, with what they can choose from.
func (k *Core) settingsNow() {
	l, ok := k.Voice().(interface{ Log(string, map[string]any) })
	if !ok {
		return
	}
	k.mu.Lock()
	st := k.settings
	k.mu.Unlock()
	f := map[string]any{"name": st.Name, "persona": st.Persona}
	if k.mood != nil {
		c := k.mood.C
		var arts []string
		for _, m := range c.MoodNames() {
			arts = append(arts, c.Moods[m].Artwork+":"+m)
		}
		f["personas"] = arts
		f["weights"] = []float64{c.Weights.Temperament, c.Weights.Circadian, c.Weights.Activity}
		f["min_stay"] = c.MinStay.String()
		f["advantage"] = c.Advantage
	}
	l.Log("settings", f)
}

// loadSettings applies the last settings from the log (at the start).
func (k *Core) loadSettings(ctx context.Context) {
	var last *Settings
	k.Log.Read(ctx, 0, func(e nlog.Event) error {
		var st Settings
		if json.Unmarshal(e.Data, &st) == nil {
			last = &st
		}
		return nil
	}, "settings")
	if last != nil {
		k.applySettings(*last)
	} else {
		k.settingsNow()
	}
}
