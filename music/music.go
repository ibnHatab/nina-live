// Package music is the music package's host (catalog/domains/music): internet radio on MPD through
// Home Assistant, and what the rules cannot hold about the person's taste in atoms: which station
// stands for a genre, which of the person's own words stand for a station, when a station began
// (so the host can tell a kept station from a skipped one), the volume.
//
// The taste itself is the rules': liked(G, P) tokens, one per time a genre was kept on at a time
// of day P. The host reads their multiplicity and asserts the favourite and the runner-up of the
// hour, or a genre never kept at this hour to explore, and says when a station was kept
// (kept(G), after KeepAfter) or skipped (skipped(G), changed within SkipWithin).
//
// Everything is a log event (stream "music": tuned, learned, forgot, word), so a restart rebuilds
// the host and re-asserts the rules' tokens and last genres: the taste outlives the process.
package music

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Genres and Parts are music.llp's types (gx: the person's own words).
var (
	Genres   = []string{"jazz", "classical", "ambient", "lofi", "rock", "pop", "electronic", "news", "world"}
	Parts    = []string{"morning", "afternoon", "evening", "night"}
	Outcomes = []string{"done", "refused"}
)

// Station is a radio station.
type Station struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Event is one of the host's log events.
type Event struct {
	Kind    string  `json:"kind"` // tuned | learned | forgot | word | stopped
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
	Save       func(Event)
	Now        func() time.Time
	KeepAfter  time.Duration // a station on this long is kept (default 10 min)
	SkipWithin time.Duration // a station changed within this is skipped (default 90 s)

	mu       sync.Mutex
	liked    map[string]int     // "genre|part" → tokens (the rules' multiset, mirrored from the log)
	last     map[string]string  // part → genre
	best     map[string]Station // genre → the station kept for it
	words    map[string]Station // the person's words → the station they meant
	bad      map[string]bool    // station URLs skipped
	restored bool
	asserted map[string]bool // what the host asserts now (to retract on change)
	talking  bool
	vol      float64
	cur      *current
	pendSkip string
}

type current struct {
	genre, words string
	station      Station
	at           time.Time
	counted      bool
}

// New is the host rebuilt from its log events.
func New(past []Event) *Host {
	h := &Host{liked: map[string]int{}, last: map[string]string{}, best: map[string]Station{}, words: map[string]Station{},
		bad: map[string]bool{}, asserted: map[string]bool{}, vol: 0.3}
	for _, e := range past {
		h.apply(e)
	}
	return h
}

func (h *Host) apply(e Event) {
	switch e.Kind {
	case "tuned":
		h.last[e.Part] = e.Genre
	case "learned":
		h.liked[e.Genre+"|"+e.Part]++
		if e.Station.URL != "" {
			h.best[e.Genre] = e.Station
			delete(h.bad, e.Station.URL)
		}
	case "forgot":
		if h.liked[e.Genre+"|"+e.Part] > 0 {
			h.liked[e.Genre+"|"+e.Part]--
		}
		if e.Station.URL != "" {
			h.bad[e.Station.URL] = true
			if h.best[e.Genre].URL == e.Station.URL {
				delete(h.best, e.Genre)
			}
		}
	case "word":
		h.words[norm(e.Words)] = e.Station
	}
}

func (h *Host) record(e Event) {
	e.At = float64(h.now().UnixNano()) / 1e9
	h.apply(e)
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

// Talking is the voice's floor: someone holds it (the person or Nina).
func (h *Host) Talking(on bool) {
	h.mu.Lock()
	h.talking = on
	h.mu.Unlock()
}

func key(atom string) string { return strings.ReplaceAll(atom, " ", "") }

// Tick is one beat's world lines.
func (h *Host) Tick(counts map[string]int) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := map[string]int{}
	for k, v := range counts {
		c[key(k)] = v
	}
	var out []string
	if !h.restored { // the taste and the last genres, back into the rules
		h.restored = true
		for k, n := range h.liked {
			g, p, _ := strings.Cut(k, "|")
			for i := 0; i < n; i++ {
				out = append(out, fmt.Sprintf("+liked(%s,%s)", g, p))
			}
		}
		for p, g := range h.last {
			if g != "jazz" {
				out = append(out, fmt.Sprintf("-last(jazz,%s)", p), fmt.Sprintf("+last(%s,%s)", g, p))
			}
		}
	}
	now := h.now()
	p := Part(now)
	want := map[string]bool{"part(" + p + ")": true}
	if h.talking {
		want["talking"] = true
	} else {
		want["floor_free"] = true
	}
	// the hour's favourite and runner-up from the multiset; else a genre to explore
	type gc struct {
		g string
		n int
	}
	var ranked []gc
	for _, g := range Genres {
		n := c[key(fmt.Sprintf("liked(%s,%s)", g, p))]
		if n == 0 {
			n = h.liked[g+"|"+p] // before the restore's lines are in
		}
		if n > 0 {
			ranked = append(ranked, gc{g, n})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].n > ranked[j].n })
	playing := ""
	if h.cur != nil {
		playing = h.cur.genre
	}
	if len(ranked) == 0 {
		want["untasted("+p+")"] = true
	} else {
		want[fmt.Sprintf("favourite(%s,%s)", ranked[0].g, p)] = true
	}
	ru := ""
	for _, r := range ranked {
		if r.g != playing {
			ru = r.g
			if playing != "" || r.g != ranked[0].g {
				break
			}
		}
	}
	if ru != "" && ru != playing && (len(ranked) > 1 || playing != "") {
		want[fmt.Sprintf("runner_up(%s,%s)", ru, p)] = true
	} else {
		for i := range Genres { // explore: the next genre never kept at this hour, turning with the clock
			g := Genres[(now.YearDay()+now.Hour()+i)%len(Genres)]
			if h.liked[g+"|"+p] == 0 && c[key(fmt.Sprintf("liked(%s,%s)", g, p))] == 0 && g != playing {
				want[fmt.Sprintf("explore(%s,%s)", g, p)] = true
				break
			}
		}
	}
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
	// kept and skipped
	if h.cur != nil && !h.cur.counted && now.Sub(h.cur.at) >= h.keepAfter() {
		h.cur.counted = true
		out = append(out, "+kept("+h.cur.genre+")")
	}
	if h.pendSkip != "" {
		g := h.pendSkip
		h.pendSkip = ""
		if c[key(fmt.Sprintf("liked(%s,%s)", g, p))] > 0 {
			out = append(out, "+skipped("+g+")")
		}
	}
	return out
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
	case "play", "switch", "something", "something_new", "something_else", "something_new_genre", "again":
		words, _ := payload["words"].(string)
		if words == "" {
			words, _ = payload["genre"].(string)
		}
		h.mu.Lock()
		h.leaving()
		h.mu.Unlock()
		go h.tune(b["G"], strings.TrimSpace(words))
	case "stop", "away":
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
		e := Event{Kind: "learned", Genre: b["G"], Part: b["P"]}
		if h.cur != nil && h.cur.genre == b["G"] {
			e.Station = h.cur.station
			if h.cur.words != "" { // the person's words, from now on theirs for this station
				h.record(Event{Kind: "word", Words: h.cur.words, Station: h.cur.station, Genre: b["G"]})
			}
		}
		h.record(e)
		h.mu.Unlock()
	case "unlearn":
		h.mu.Lock()
		h.record(Event{Kind: "forgot", Genre: b["G"], Part: b["P"], Station: h.lastSkipped(b["G"])})
		h.mu.Unlock()
	}
}

// leaving: the station playing is left; within SkipWithin, it was skipped. Holds h.mu.
func (h *Host) leaving() {
	if h.cur != nil && h.now().Sub(h.cur.at) < h.skipWithin() {
		h.pendSkip = h.cur.genre
		h.bad[h.cur.station.URL] = true
	}
}

func (h *Host) lastSkipped(g string) Station {
	for u := range h.bad {
		if h.best[g].URL == u {
			return h.best[g]
		}
	}
	return Station{}
}

func (h *Host) tune(g, words string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	h.mu.Lock()
	st, ok := Station{}, false
	if words != "" { // the person's own words first: fuzzy, over what they kept before
		st, ok = h.match(words)
	}
	if !ok && g != "gx" {
		st, ok = h.best[g], h.best[g].URL != ""
	}
	h.mu.Unlock()
	if !ok {
		tag, q := g, ""
		if g == "gx" {
			tag, q = "", words
		}
		search := h.Search
		if search == nil {
			search = RadioBrowser
		}
		sts, err := search(ctx, tag, q)
		h.mu.Lock()
		for _, s := range sts { // a skipped station is demoted, never banned: the best of them when all were
			if !h.bad[s.URL] {
				st, ok = s, true
				break
			}
		}
		if !ok && len(sts) > 0 {
			st, ok = sts[0], true
		}
		h.mu.Unlock()
		if err != nil || !ok {
			return
		}
	}
	h.mu.Lock()
	h.cur = &current{genre: g, words: words, station: st, at: h.now()}
	p := Part(h.now())
	h.record(Event{Kind: "tuned", Genre: g, Part: p, Words: words, Station: st})
	v := h.vol
	h.mu.Unlock()
	h.call("media_player", "play_media", map[string]any{"media_content_type": "music", "media_content_id": st.URL})
	h.call("media_player", "volume_set", map[string]any{"volume_level": v})
}

// match is the person's words against the words they used for a station they kept: the most
// words in common, at least half of the shorter. Holds h.mu.
func (h *Host) match(words string) (Station, bool) {
	ws := strings.Fields(norm(words))
	var best Station
	score := 0.0
	for k, st := range h.words {
		ks := strings.Fields(k)
		common := 0
		for _, w := range ws {
			for _, x := range ks {
				if w == x && len(w) > 2 {
					common++
					break
				}
			}
		}
		n := min(len(ws), len(ks))
		if n == 0 {
			continue
		}
		if s := float64(common) / float64(n); s >= 0.5 && s > score && !h.bad[st.URL] {
			best, score = st, s
		}
	}
	return best, score > 0
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

// Name names a genre in the rules' lines: the person's own words for gx; a time of day as said.
func (h *Host) Name(handle string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if handle == "gx" {
		if h.cur != nil && h.cur.words != "" {
			if h.cur.station.Name != "" {
				return fmt.Sprintf("%q (%s)", h.cur.words, h.cur.station.Name), true
			}
			return fmt.Sprintf("%q", h.cur.words), true
		}
		return "what you asked for", true
	}
	if h.cur != nil && h.cur.genre == handle && h.cur.station.Name != "" {
		return fmt.Sprintf("%s (%s)", handle, h.cur.station.Name), true
	}
	return "", false
}

// RadioBrowser searches radio-browser.info: by tag, or by the person's words in the name and the
// tags. Only the genre or the words are sent.
func RadioBrowser(ctx context.Context, tag, words string) ([]Station, error) {
	q := url.Values{"order": {"clickcount"}, "reverse": {"true"}, "hidebroken": {"true"}, "limit": {"10"}}
	path := "/json/stations/search"
	if tag != "" {
		q.Set("tag", tag)
	} else {
		q.Set("name", words)
	}
	get := func(q url.Values) ([]Station, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://de1.api.radio-browser.info"+path+"?"+q.Encode(), nil)
		req.Header.Set("User-Agent", "nina-live/0")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var xs []struct {
			Name string `json:"name"`
			URL  string `json:"url_resolved"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&xs); err != nil {
			return nil, err
		}
		var out []Station
		for _, x := range xs {
			if x.URL != "" {
				out = append(out, Station{strings.TrimSpace(x.Name), x.URL})
			}
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
