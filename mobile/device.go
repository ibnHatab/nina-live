package nina

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	fmobile "github.com/ibnHatab/fsttm-live/pkg/mobile"
	"github.com/ibnHatab/nina-live/music"
	"github.com/ibnHatab/nina-live/tasks"
)

// The device's own tools on the tablet (2026-11-27): the radio in the app's player (stations from
// radio-browser, as the music package finds them at home), alarms and timers in the Clock app. Each
// names one of the app's fixed actions (fsttm-live pkg/mobile Device), never an intent. Interim: the
// standalone composition (no Home Assistant) makes these the music and timers packages' Android
// backends, so the rules see them (docs/design/google-services.md).

func deviceTools() []tasks.Tool {
	obj := func(props map[string]any, req ...string) map[string]any {
		o := map[string]any{"type": "object", "properties": props}
		if len(req) > 0 {
			r := make([]any, len(req))
			for i, s := range req {
				r[i] = s
			}
			o["required"] = r
		}
		return o
	}
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	return []tasks.Tool{
		{Name: "radio", Domain: "music",
			Description: "The radio on this tablet: play a station (a genre such as jazz, classical, news, ambient, or words of a station's name), stop it, or set its volume.",
			Parameters: obj(map[string]any{
				"action": map[string]any{"type": "string", "enum": []any{"play", "stop", "volume"}},
				"genre":  str("for play: a genre (jazz, classical, lounge, news, ambient, rock, …)"),
				"words":  str("for play: words of the station's name, when the person named one"),
				"level":  map[string]any{"type": "number", "description": "for volume: 0 to 1"}}, "action"),
			Call: radio},
		{Name: "alarm", Domain: "timers",
			Description: "Set an alarm in this tablet's Clock app: it rings even if Nina is not running.",
			Parameters:  obj(map[string]any{"time": str("HH:MM, 24-hour, local"), "label": str("what it is for")}, "time"),
			Call:        alarm},
		{Name: "timer", Domain: "timers",
			Description: "Start a countdown timer in this tablet's Clock app.",
			Parameters: obj(map[string]any{"minutes": map[string]any{"type": "number", "description": "how long, in minutes (0.5 for 30 seconds)"},
				"label": str("what it is for")}, "minutes"),
			Call: timer},
	}
}

func radio(ctx context.Context, a map[string]any) (string, string) {
	switch act, _ := a["action"].(string); act {
	case "stop":
		if _, err := fmobile.Act("radio.stop", nil); err != nil {
			return "error", err.Error()
		}
		return "done", "the radio is off"
	case "volume":
		lv, _ := a["level"].(float64)
		if _, err := fmobile.Act("radio.volume", map[string]any{"volume": lv}); err != nil {
			return "error", err.Error()
		}
		return "done", fmt.Sprintf("the radio's volume is %.0f%%", lv*100)
	case "play":
		genre, _ := a["genre"].(string)
		words, _ := a["words"].(string)
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		sts, err := music.RadioBrowser(c, strings.ToLower(strings.TrimSpace(genre)), strings.TrimSpace(words))
		if err != nil {
			return "error", "the station list did not answer: " + err.Error()
		}
		if st, ok := bestStation(sts, strings.ToLower(strings.TrimSpace(genre))); ok {
			if _, err := fmobile.Act("radio.play", map[string]any{"url": st.URL}); err != nil {
				return "error", err.Error()
			}
			return "done", "playing " + st.Name
		}
		return "refused", "no station found for " + strings.TrimSpace(genre+" "+words)
	}
	return "refused", "action: play, stop or volume"
}

func alarm(_ context.Context, a map[string]any) (string, string) {
	t, _ := a["time"].(string)
	h, m, ok := strings.Cut(strings.TrimSpace(t), ":")
	hh, e1 := strconv.Atoi(h)
	mm, e2 := strconv.Atoi(m)
	if !ok || e1 != nil || e2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return "refused", "a time as HH:MM, 24-hour"
	}
	label, _ := a["label"].(string)
	if _, err := fmobile.Act("alarm.set", map[string]any{"hour": hh, "minute": mm, "label": label}); err != nil {
		return "error", err.Error()
	}
	return "done", fmt.Sprintf("alarm set for %02d:%02d in the Clock app", hh, mm)
}

func timer(_ context.Context, a map[string]any) (string, string) {
	min, _ := a["minutes"].(float64)
	secs := int(min * 60)
	if secs <= 0 || secs > 24*3600 {
		return "refused", "minutes: more than 0, at most a day"
	}
	label, _ := a["label"].(string)
	if _, err := fmobile.Act("timer.set", map[string]any{"seconds": secs, "label": label}); err != nil {
		return "error", err.Error()
	}
	return "done", fmt.Sprintf("a %s timer is running in the Clock app", (time.Duration(secs) * time.Second).String())
}

// bestStation: radio-browser orders by popularity, and a popular station may carry the genre among
// many tags ("jazz" gave "Christmas FM Non-stop!", 2026-11-28): first a station with the genre in its
// name, then one tagged with it exactly, then the most popular.
func bestStation(sts []music.Station, genre string) (music.Station, bool) {
	var tagged, any *music.Station
	for i := range sts {
		st := &sts[i]
		if st.URL == "" {
			continue
		}
		if any == nil {
			any = st
		}
		if genre == "" {
			break
		}
		if strings.Contains(strings.ToLower(st.Name), genre) {
			return *st, true
		}
		if tagged == nil {
			for _, tg := range st.Tags {
				if strings.EqualFold(strings.TrimSpace(tg), genre) {
					tagged = st
					break
				}
			}
		}
	}
	switch {
	case tagged != nil:
		return *tagged, true
	case any != nil:
		return *any, true
	}
	return music.Station{}, false
}
