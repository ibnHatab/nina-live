// Package music is the music package's host (catalog/domains/music, an instance of the taste's
// lifted rules over genres): internet radio on MPD through Home Assistant.
//
// The person's taste is the semantic world's (taste.Client): every station played is an item of
// kind "station" with free tags (its genre, the directory's tags, the person's own words once they
// kept what the words found); kept (on for KeepAfter) and skipped (left within SkipWithin) are
// evidence for that very station at this time of day. The host reads the taste each beat and
// asserts what the lifted rules need: favourite(O) or untasted, runner_up(O) or explore(O),
// last_here(O); it binds each to its station, and when the rules engage an offer it plays that
// station (or, for a genre asked by name, the world's select for the genre, the directory when
// the world knows no station yet).
//
// What is the host's alone (the stations played last at each time of day, the volume) is its log
// (stream "music"); the taste outlives restarts in the world's snapshots.
package music

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ibnHatab/nina-live/taste"
	sw "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/semworld"
)

// Genres are music.llp's offers (gx: the person's own words).
var (
	Genres   = []string{"jazz", "classical", "ambient", "lofi", "rock", "pop", "electronic", "news", "world"}
	Outcomes = []string{"ok", "no"}
)

// Station is a radio station.
type Station struct {
	Name string   `json:"name"`
	URL  string   `json:"url"`
	Tags []string `json:"tags,omitempty"`
}

// ID is a station's item id in the taste.
func (s Station) ID() string {
	h := sha256.Sum256([]byte(s.URL))
	return "station:" + hex.EncodeToString(h[:6])
}

// Event is one of the host's log events.
type Event struct {
	Kind    string  `json:"kind"` // tuned
	Genre   string  `json:"genre,omitempty"`
	Part    string  `json:"part,omitempty"`
	Words   string  `json:"words,omitempty"`
	Station Station `json:"station,omitempty"`
	At      float64 `json:"at"`
}

// Host is the music's host.
type Host struct {
	Player string // the media_player (default media_player.music_player_daemon)
	// Call is a Home Assistant service call (nil: nothing plays)
	Call func(ctx context.Context, domain, service string, data map[string]any) error
	// Search finds stations for a genre's tag or the person's words (default: Radio Browser)
	Search     func(ctx context.Context, tag, words string) ([]Station, error)
	Taste      taste.Client // the semantic world's taste (nil: a memory of its own)
	Save       func(Event)
	Now        func() time.Time
	KeepAfter  time.Duration // a station on this long is kept (default 10 min)
	SkipWithin time.Duration // a station left within this is skipped (default 90 s)
	Mood       string        // the mood now ("" none: step 3's director sets it)

	mu       sync.Mutex
	last     map[string]Station // part → the station played last at that time of day
	asserted map[string]bool
	bindFav  Station // what favourite(O) stands for
	bindAlt  Station // what runner_up(O) / explore(O) stands for
	favID    string  // their items' ids
	altID    string
	talking  bool
	vol      float64
	cur      *current
	pend     []string // lines for the next beat (skipped)
	left     *current // the station just left within SkipWithin
	stations map[string]Station
}

type current struct {
	genre, words string
	station      Station
	at           time.Time
	counted      bool
}

// New is the host rebuilt from its log events.
func New(past []Event) *Host {
	h := &Host{last: map[string]Station{}, asserted: map[string]bool{}, vol: 0.3, stations: map[string]Station{}}
	for _, e := range past {
		if e.Kind == "tuned" && e.Station.URL != "" {
			h.last[e.Part] = e.Station
			h.stations[e.Station.ID()] = e.Station
		}
	}
	return h
}

func (h *Host) record(e Event) {
	e.At = float64(h.now().UnixNano()) / 1e9
	if e.Kind == "tuned" {
		h.last[e.Part] = e.Station
	}
	if h.Save != nil {
		h.Save(e)
	}
}

func (h *Host) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Host) taste() taste.Client {
	if h.Taste == nil {
		h.Taste = taste.NewMemory()
	}
	return h.Taste
}

// Talking is the voice's floor: someone holds it (the person or Nina).
func (h *Host) Talking(on bool) {
	h.mu.Lock()
	h.talking = on
	h.mu.Unlock()
}

func (h *Host) context() sw.Context {
	return sw.Context{Part: taste.Part(h.now()), Mood: h.Mood}
}

// genreOf is a station item's genre: the first of its tags that is a genre ("gx" when none).
func (h *Host) genreOf(id string) string {
	it, ok := h.taste().Get(id)
	if !ok {
		return "gx"
	}
	for _, t := range it.Tags {
		for _, g := range Genres {
			if t == g {
				return g
			}
		}
	}
	return "gx"
}

// station is a station item: the host's own, or the world's (its Ref the URL). Holds h.mu.
func (h *Host) station(id string) (Station, bool) {
	if s, ok := h.stations[id]; ok {
		return s, true
	}
	if it, ok := h.taste().Get(id); ok && it.Ref != "" {
		s := Station{Name: it.Name, URL: it.Ref, Tags: it.Tags}
		h.stations[id] = s
		return s, true
	}
	return Station{}, false
}

// Tick is one beat's world lines: the taste now, as the lifted rules need it.
func (h *Host) Tick(map[string]int) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.context()
	want := map[string]bool{}
	if h.talking {
		want["talking"] = true
	} else {
		want["floor_free"] = true
	}
	playing := ""
	if h.cur != nil {
		playing = h.cur.station.ID()
	}
	if fav, ok := h.taste().Select("station", nil, c, nil); ok && !fav.Explored {
		if s, ok := h.station(fav.ID); ok {
			h.bindFav, h.favID = s, fav.ID
			want["favourite("+h.genreOf(fav.ID)+")"] = true
		}
	}
	if !anyPrefix(want, "favourite(") {
		want["untasted"] = true
	}
	ex := []string{}
	if playing != "" {
		ex = append(ex, playing)
	}
	if alt, ok := h.taste().Select("station", nil, c, ex); ok {
		if s, ok := h.station(alt.ID); ok {
			h.bindAlt, h.altID = s, alt.ID
			if alt.Explored {
				want["explore("+h.genreOf(alt.ID)+")"] = true
			} else {
				want["runner_up("+h.genreOf(alt.ID)+")"] = true
			}
		}
	}
	if !anyPrefix(want, "runner_up(") && !anyPrefix(want, "explore(") {
		// the world knows no other station: a genre none of its stations carries, turning with the clock
		known := map[string]bool{}
		for id := range h.stations {
			known[h.genreOf(id)] = true
		}
		now := h.now()
		for i := range Genres {
			g := Genres[(now.YearDay()+now.Hour()+i)%len(Genres)]
			if !known[g] {
				h.bindAlt, h.altID = Station{Tags: []string{g}}, ""
				want["explore("+g+")"] = true
				break
			}
		}
	}
	lastGenre := "jazz"
	if s, ok := h.last[c.Part]; ok {
		lastGenre = h.genreOf(s.ID())
	}
	want["last_here("+lastGenre+")"] = true

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
	if h.cur != nil && !h.cur.counted && h.now().Sub(h.cur.at) >= h.keepAfter() {
		h.cur.counted = true
		out = append(out, "+kept("+h.cur.genre+")")
	}
	out = append(out, h.pend...)
	h.pend = nil
	return out
}

func anyPrefix(m map[string]bool, p string) bool {
	for k := range m {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

func (h *Host) keepAfter() time.Duration {
	if h.KeepAfter > 0 {
		return h.KeepAfter
	}
	return 10 * time.Minute
}

func (h *Host) skipWithin() time.Duration {
	if h.SkipWithin > 0 {
		return h.SkipWithin
	}
	return 90 * time.Second
}

// bindings reads an event's name: rule[K=V,...] → the rule and its bindings.
func bindings(name string) (string, map[string]string) {
	rule, rest, _ := strings.Cut(name, "[")
	b := map[string]string{}
	for _, kv := range strings.Split(strings.TrimSuffix(rest, "]"), ",") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			b[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return rule, b
}

// OnEvent takes the rules' fired rules.
func (h *Host) OnEvent(name string, args []string, payload map[string]any) {
	rule, b := bindings(name)
	switch rule {
	case "play", "switch", "something", "something_new", "something_else", "something_new_one", "again":
		words, _ := payload["words"].(string)
		if words == "" {
			words, _ = payload["genre"].(string)
		}
		h.mu.Lock()
		h.leaving()
		var st Station
		switch rule {
		case "something":
			st = h.bindFav
		case "something_else", "something_new_one":
			st = h.bindAlt
		case "again", "something_new":
			st = h.last[taste.Part(h.now())]
		}
		h.mu.Unlock()
		go h.tune(b["O"], strings.TrimSpace(words), st)
	case "off", "away":
		h.mu.Lock()
		h.leaving()
		h.cur = nil
		h.mu.Unlock()
		go h.call("media_player", "media_stop", nil)
	case "up", "down":
		h.mu.Lock()
		if rule == "up" {
			h.vol = min(1, h.vol+0.1)
		} else {
			h.vol = max(0.05, h.vol-0.1)
		}
		v := h.vol
		h.mu.Unlock()
		go h.call("media_player", "volume_set", map[string]any{"volume_level": v})
	case "duck", "unduck":
		h.mu.Lock()
		v := h.vol
		h.mu.Unlock()
		if rule == "duck" {
			v *= 0.3
		}
		go h.call("media_player", "volume_set", map[string]any{"volume_level": v})
	case "night":
		go h.call("media_player", "volume_set", map[string]any{"volume_level": 0.08})
	case "learn":
		h.mu.Lock()
		if h.cur != nil {
			id := h.cur.station.ID()
			if h.cur.words != "" { // the person's words: from now on tags of this station
				h.taste().Register(sw.Item{ID: id, Kind: "station", Tags: []string{h.cur.words}})
			}
			h.taste().Prefer(id, h.context(), 1)
		}
		h.mu.Unlock()
	case "unlearn":
		h.mu.Lock()
		if h.left != nil {
			h.taste().Prefer(h.left.station.ID(), h.context(), -1)
			h.left = nil
		}
		h.mu.Unlock()
	}
}

// leaving: the station playing is left; within SkipWithin, it was skipped. Holds h.mu.
func (h *Host) leaving() {
	if h.cur != nil && h.now().Sub(h.cur.at) < h.skipWithin() {
		h.left = h.cur
		h.pend = append(h.pend, "+skipped("+h.cur.genre+")")
	}
}

// register puts stations into the taste as items (their genre, their tags). Holds h.mu.
func (h *Host) register(sts []Station, genre string) {
	for _, s := range sts {
		h.stations[s.ID()] = s
		tags := append([]string{}, s.Tags...)
		if genre != "" && genre != "gx" {
			tags = append(tags, genre)
		}
		h.taste().Register(sw.Item{ID: s.ID(), Kind: "station", Name: s.Name, Ref: s.URL, Tags: tags})
	}
}

func (h *Host) tune(g, words string, st Station) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if st.URL == "" { // asked by name, or a genre the world has no station for yet
		if len(st.Tags) > 0 {
			g = st.Tags[0]
		}
		var tags []string
		switch {
		case g == "gx":
			tags = []string{words}
		case g != "":
			tags = []string{g}
		}
		h.mu.Lock()
		if c, ok := h.taste().Select("station", tags, h.context(), nil); ok {
			st, _ = h.station(c.ID)
		}
		h.mu.Unlock()
		if st.URL == "" {
			tag, q := g, ""
			if g == "gx" || g == "" {
				tag, q = "", words
			}
			search := h.Search
			if search == nil {
				search = RadioBrowser
			}
			sts, err := search(ctx, tag, q)
			if err != nil || len(sts) == 0 {
				return
			}
			if len(sts) > 5 {
				sts = sts[:5]
			}
			h.mu.Lock()
			h.register(sts, g)
			h.mu.Unlock()
			st = sts[0]
		}
	}
	h.mu.Lock()
	if g == "" {
		g = h.genreOf(st.ID())
	}
	h.cur = &current{genre: g, words: words, station: st, at: h.now()}
	h.record(Event{Kind: "tuned", Genre: g, Part: taste.Part(h.now()), Words: words, Station: st})
	v := h.vol
	h.mu.Unlock()
	h.call("media_player", "play_media", map[string]any{"media_content_type": "music", "media_content_id": st.URL})
	h.call("media_player", "volume_set", map[string]any{"volume_level": v})
}

func norm(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.Trim(s, " .,!?"))), " ")
}

func (h *Host) call(domain, service string, data map[string]any) {
	if h.Call == nil {
		return
	}
	d := map[string]any{"entity_id": h.player()}
	for k, v := range data {
		d[k] = v
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	h.Call(ctx, domain, service, d)
}

func (h *Host) player() string {
	if h.Player != "" {
		return h.Player
	}
	return "media_player.music_player_daemon"
}

// Name names an offer in the rules' lines: its station (the one playing, or the one it stands for
// now), the person's own words for gx.
func (h *Host) Name(handle string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cur != nil && h.cur.genre == handle {
		if handle == "gx" && h.cur.words != "" {
			return fmt.Sprintf("%q (%s)", h.cur.words, h.cur.station.Name), true
		}
		if h.cur.station.Name != "" {
			return fmt.Sprintf("%s (%s)", handle, h.cur.station.Name), true
		}
	}
	for i, s := range []Station{h.bindFav, h.bindAlt} {
		id := []string{h.favID, h.altID}[i]
		if s.Name != "" && h.genreOf(id) == handle {
			if handle == "gx" {
				return s.Name, true
			}
			return fmt.Sprintf("%s (%s)", handle, s.Name), true
		}
	}
	if handle == "gx" {
		return "what you asked for", true
	}
	return "", false
}

// RadioBrowser searches radio-browser.info: by tag, or by the person's words in the name and the
// tags. Only the genre or the words are sent.
func RadioBrowser(ctx context.Context, tag, words string) ([]Station, error) {
	q := url.Values{"order": {"clickcount"}, "reverse": {"true"}, "hidebroken": {"true"}, "limit": {"10"}}
	if tag != "" {
		q.Set("tag", tag)
	} else {
		q.Set("name", words)
	}
	get := func(q url.Values) ([]Station, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://de1.api.radio-browser.info/json/stations/search?"+q.Encode(), nil)
		req.Header.Set("User-Agent", "nina-live/0")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var xs []struct {
			Name string `json:"name"`
			URL  string `json:"url_resolved"`
			Tags string `json:"tags"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&xs); err != nil {
			return nil, err
		}
		var out []Station
		for _, x := range xs {
			if x.URL == "" {
				continue
			}
			var tags []string
			for i, t := range strings.Split(x.Tags, ",") {
				if t = norm(t); t != "" && i < 6 {
					tags = append(tags, t)
				}
			}
			out = append(out, Station{strings.TrimSpace(x.Name), x.URL, tags})
		}
		return out, nil
	}
	out, err := get(q)
	if err == nil && len(out) == 0 && words != "" { // the words as a tag, word by word
		for _, w := range strings.Fields(norm(words)) {
			if len(w) < 4 {
				continue
			}
			q.Del("name")
			q.Set("tag", w)
			if out, err = get(q); err == nil && len(out) > 0 {
				break
			}
		}
	}
	return out, err
}
