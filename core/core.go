// Package core is Nina without her voice and without the home's devices: her log, the world
// rebuilt from it, the home's rules (composed under the director, on the tick), her System 2
// (tasks on pi) and the routing between them. cmd/nina adds the voice (fsttm-live's dialogue) and
// Home Assistant (acasa's hass); the integration tests drive this with a simulated device and
// pi's faux provider.
//
// The routing (ARCHITECTURE.md §3.2, §4):
//   - a rule's line (an outcome, a thing noticed) → the log (stream reasoning); to the voice by its
//     kind's level (Voice: silent context, or a NOTE); into the running tasks that used its package
//     (Steer, or FollowUp for a late outcome);
//   - the director's forward → the log (stream tick); the target joins the tasks that used its source;
//   - a beat's projected lines → the voice's silent context when their words change;
//   - a task's result → the voice as a NOTE.
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ibnHatab/nina-live/agenda"
	"github.com/ibnHatab/nina-live/escalate"
	"github.com/ibnHatab/nina-live/explain"
	"github.com/ibnHatab/nina-live/glow"
	"github.com/ibnHatab/nina-live/habits"
	nlog "github.com/ibnHatab/nina-live/log"
	"github.com/ibnHatab/nina-live/music"
	"github.com/ibnHatab/nina-live/persona"
	"github.com/ibnHatab/nina-live/port"
	"github.com/ibnHatab/nina-live/tasks"
	"github.com/ibnHatab/nina-live/taste"
	"github.com/ibnHatab/nina-live/timers"
	"github.com/ibnHatab/nina-live/world"
	"github.com/metis-lang-dev/metis-agent/go/pkg/metishome"
	semhome "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/home"
	sw "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/semworld"
)

// Voice is where Nina speaks (the dialogue; a test's recorder).
type Voice interface {
	Context(key, line string)
	Note(level, text string) error
}

type Config struct {
	Log      *nlog.Log
	Root     string // the catalog (nina-live/catalog)
	Metisc   string
	Packages []string // default metishome.Packages
	Composed bool
	// Escalate: a goal for the task runner goes through the escalate package's rules (the redesign's step 6:
	// its card replaces the voice's delegate); off, its card is hidden and it starts no task
	Escalate bool
	// RouteIntents: every card's intent enters through the composition's director (the redesign's channel A,
	// s-b478fe59: home.route director); off, straight into the card as before
	RouteIntents bool
	// WorldBeat: the semantic world settles on its own clock, this often (the redesign's step 3: its updates
	// never wait for the rules or the LLMs in the loop); 0: on the rules' beat, as before
	WorldBeat       time.Duration
	Device          metishome.Device
	Code            string // the build, for the world's checkpoints
	CheckpointEvery int
	CallTimeout     time.Duration
	Levels          map[string]string // a rule's reply kind → context | info | important | urgent | drop
	Tasks           *tasks.Config     // nil: no System 2 (its Tools, World, Log, Note and Now are filled here)
	Timers          bool              // the timers package (catalog/domains/timers), its host rebuilt from the log (always when Composed: the composition has it)
	Agenda          *agenda.Host      // the agenda package's host (writes through Home Assistant); nil when Composed: one that writes nothing
	Persona         string            // Nina's character (prompts/nina.md's text): the explainer's voice
	Character       string            // the home's character (default Root/character.yaml): the taste's priors, loaded when it changes
	Glow            *glow.Host        // the glow package's host (the bedroom light through Alexa); nil when Composed: one that reaches nothing
	Music           *music.Host       // the music package's host (MPD through Home Assistant), its taste rebuilt from the log; nil when Composed: one that plays nothing
	Workspace       string            // the Metis loop's workspace .metis dir (default Root/domains/.metis): surprises dropped there for the pi loop
	Records         string            // the Metis runtime's surprise records ("catalog", the default: each domain's records/ in the catalog, the formalization loop's input; "off")
	Temperament     string            // Nina's temperament (default Root/persona.yaml; "-": none): her mood, its prompt and persona
	// Publish puts Nina's persona where the dashboard sees it (Home Assistant's sensor.nina_persona); nil: nowhere
	Publish func(state string, attrs map[string]any)
	Now     func() float64
}

type Core struct {
	strainState
	Cfg    Config
	Log    *nlog.Log
	World  *world.World
	Home   *metishome.Service
	Tasks  *tasks.Runner
	Timers *timers.Host
	Agenda *agenda.Host
	Music  *music.Host
	Habits *habits.Host
	Glow   *glow.Host
	Taste  *taste.World // the semantic world's taste, as the hosts use it
	// Explain narrates the inference trace ("why did you do that?", a surprise); nil without tasks
	Explain *explain.Explainer

	lastSurprise float64
	unseen       int               // surprises since the person last asked about them
	names        map[string]string // a map id (u2, l1) → the home's words for it ("living room ventilation")
	presence     Presence          // the radar's last reading
	settings     Settings          // her screens' settings (core/settings.go)
	base         *Settings         // the temperament's numbers from its file
	lastMinute   string
	mood         *persona.Selector // Nina's temperament; nil without one
	moodAt       time.Time
	moodAct      string
	moodState    persona.State
	Character    *taste.Character // the home's character (its moods, its routines)
	routineDay   map[string]string
	Report       world.Report

	mu    sync.Mutex
	voice Voice
	port  *port.Port // the world's lines to the voice and the tasks: on a new value or confidence level
	// Observed: the integration layer's lines now (Home Assistant's weather, lists, calendars: acasa's
	// hass.Observer), with the world's before each task request
	Observed func() []string
	// WhatsApp: her WhatsApp channel (UseWhatsApp), for her settings' groups
	WhatsApp WhatsAppShows
}

// Open builds the core: the world rebuilt from the log, the home's rules on it, the tasks.
func Open(ctx context.Context, c Config) (*Core, error) {
	if c.Now == nil {
		c.Now = func() float64 { return float64(time.Now().UnixNano()) / 1e9 }
	}
	if len(c.Packages) == 0 {
		c.Packages = metishome.Packages
	}
	if c.Records == "" {
		c.Records = "catalog"
	}
	if c.CallTimeout == 0 {
		c.CallTimeout = 6 * time.Second
	}
	if c.Levels == nil {
		c.Levels = map[string]string{"result": "context", "event": "info", "surprise": "info", "error": "context"}
	}
	k := &Core{Cfg: c, Log: c.Log, port: port.New(port.Default)}
	k.Taste = &taste.World{Now: c.Now} // its world is filled in once the world is open
	var hosts map[string]metishome.HostSpec
	if c.Timers || c.Composed {
		var past []timers.Event
		c.Log.Read(ctx, 0, func(e nlog.Event) error {
			var te timers.Event
			if json.Unmarshal(e.Data, &te) == nil {
				past = append(past, te)
			}
			return nil
		}, "timers")
		k.Timers = timers.New(c.Now, func(e timers.Event) {
			c.Log.Append(c.Now(), "timers", e.Kind, "", "", e, 1)
			go k.timersContext() // the host's lock is held here
		}, past)
		hosts = map[string]metishome.HostSpec{"timers": {New: func() metishome.Host { return k.Timers }, Outcomes: timers.Outcomes}}
		if !slices.Contains(c.Packages, "timers") {
			c.Packages = append(slices.Clone(c.Packages), "timers")
		}
	}
	if c.Composed {
		if _, err := os.Stat(filepath.Join(c.Root, "domains", "escalate", "domain.yaml")); err == nil {
			eh := &escalate.Host{}
			if c.Escalate { // its start rule starts the task; the load is the runner's
				eh.Start = func(goal string) {
					if k.Tasks != nil {
						k.Tasks.Delegate(goal, "important")
					}
				}
				eh.Running = func() int {
					if k.Tasks == nil {
						return 0
					}
					return k.Tasks.Running()
				}
			}
			if hosts == nil {
				hosts = map[string]metishome.HostSpec{}
			}
			hosts["escalate"] = metishome.HostSpec{New: func() metishome.Host { return eh }, Outcomes: escalate.Outcomes}
			if !slices.Contains(c.Packages, "escalate") {
				c.Packages = append(slices.Clone(c.Packages), "escalate")
			}
		}
	}
	if c.Composed || c.Agenda != nil {
		ag := c.Agenda
		if ag == nil {
			ag = &agenda.Host{Entities: agenda.Default}
		}
		if ag.Save == nil {
			ag.Save = func(w agenda.Write) { c.Log.Append(c.Now(), "agenda", w.Kind, w.Entity, "", w, 1) }
		}
		k.Agenda = ag
		if hosts == nil {
			hosts = map[string]metishome.HostSpec{}
		}
		hosts["agenda"] = metishome.HostSpec{New: func() metishome.Host { return ag }, Outcomes: agenda.Outcomes}
		if !slices.Contains(c.Packages, "agenda") {
			c.Packages = append(slices.Clone(c.Packages), "agenda")
		}
	}
	if c.Composed || c.Music != nil {
		var past []music.Event
		c.Log.Read(ctx, 0, func(e nlog.Event) error {
			var me music.Event
			if json.Unmarshal(e.Data, &me) == nil {
				past = append(past, me)
			}
			return nil
		}, "music")
		mh := c.Music
		if mh == nil {
			mh = &music.Host{}
		}
		fresh := music.New(past) // the stations played last at each time of day: from the log
		fresh.Backend, fresh.Search, fresh.Now, fresh.KeepAfter, fresh.SkipWithin = mh.Backend, mh.Search, mh.Now, mh.KeepAfter, mh.SkipWithin
		fresh.Languages = mh.Languages
		fresh.Taste = k.Taste // the person's taste: the semantic world's
		fresh.Save = func(e music.Event) { c.Log.Append(c.Now(), "music", e.Kind, e.Genre, "", e, 1) }
		k.Music = fresh
		if hosts == nil {
			hosts = map[string]metishome.HostSpec{}
		}
		hosts["music"] = metishome.HostSpec{New: func() metishome.Host { return fresh }, Outcomes: music.Outcomes}
		if !slices.Contains(c.Packages, "music") {
			c.Packages = append(slices.Clone(c.Packages), "music")
		}
	}
	if c.Composed || c.Glow != nil {
		gh := c.Glow
		if gh == nil {
			gh = &glow.Host{}
		}
		gh.Taste = k.Taste
		k.Glow = gh
		if hosts == nil {
			hosts = map[string]metishome.HostSpec{}
		}
		hosts["glow"] = metishome.HostSpec{New: func() metishome.Host { return gh }, Outcomes: glow.Outcomes}
		if !slices.Contains(c.Packages, "glow") {
			c.Packages = append(slices.Clone(c.Packages), "glow")
		}
	}
	if c.Composed {
		var past []habits.Call
		c.Log.Read(ctx, 0, func(e nlog.Event) error {
			var hc habits.Call
			if json.Unmarshal(e.Data, &hc) == nil {
				past = append(past, hc)
			}
			return nil
		}, "call")
		hb := habits.New(past)
		hb.Replay = func(ctx context.Context, tool string, args map[string]any) (string, string) {
			o := k.Home.Call(ctx, tool, args)
			k.Log.Append(k.Cfg.Now(), "habits", "replayed", tool, "", map[string]any{"args": args, "status": o.Status, "text": o.Text}, 1)
			return o.Status, o.Text
		}
		k.Habits = hb
		if hosts == nil {
			hosts = map[string]metishome.HostSpec{}
		}
		hosts["habits"] = metishome.HostSpec{New: func() metishome.Host { return hb }, Outcomes: habits.Outcomes}
		if !slices.Contains(c.Packages, "habits") {
			c.Packages = append(slices.Clone(c.Packages), "habits")
		}
	}
	// her temperament (catalog/persona.yaml) and the persona package's host: the moments that set it aside
	if tp := c.Temperament; tp != "-" {
		if tp == "" {
			tp = filepath.Join(c.Root, "persona.yaml")
		}
		pc, err := persona.Load(tp)
		if err != nil {
			return nil, fmt.Errorf("the temperament: %w", err)
		}
		k.mood = &persona.Selector{C: pc}
		ph := &persona.Host{Sel: k.mood, Now: func() time.Time { return time.Unix(int64(c.Now()), 0) },
			OnChange: func() { k.mu.Lock(); k.moodAt = time.Time{}; k.mu.Unlock() }} // evaluated at the next beat
		if hosts == nil {
			hosts = map[string]metishome.HostSpec{}
		}
		// the temperament package (was persona: renamed for the narratives' persona member, the redesign's step 3)
		hosts["temperament"] = metishome.HostSpec{New: func() metishome.Host { return ph }, Outcomes: persona.Outcomes}
		if c.Composed && !slices.Contains(c.Packages, "temperament") {
			c.Packages = append(slices.Clone(c.Packages), "temperament")
		}
	}
	if hosts != nil {
		k.Cfg.Packages = c.Packages
	}
	var members []world.Member
	for _, p := range c.Packages {
		members = append(members, world.Member{Name: p, Manifest: filepath.Join(c.Root, "domains", p, "domain.yaml")})
	}
	k.names = mapNames(filepath.Join(c.Root, "world", "home.map.json"))
	var err error
	k.World, k.Report, err = world.Open(ctx, c.Log, world.Config{Map: filepath.Join(c.Root, "world", "home.map.json"),
		Members: members, Code: c.Code, Every: c.CheckpointEvery, Now: c.Now})
	if err != nil {
		return nil, fmt.Errorf("the world: %w", err)
	}
	if len(k.Report.Mismatch) > 0 {
		c.Log.Append(c.Now(), "sys", "replay_mismatch", "", "", k.Report, 1)
	}
	k.Taste.Home, k.Taste.Apply = k.World.Home, k.World.Apply
	k.Home, err = metishome.Open(metishome.Options{Root: c.Root, Packages: c.Packages, Metisc: c.Metisc,
		Sem: k.World.Home, Apply: k.World.Apply, Device: c.Device, Composed: c.Composed, RouteIntents: c.RouteIntents, OwnClock: c.WorldBeat > 0, Records: c.Records, Hosts: hosts,
		OnSurprise:  func(domain string, r map[string]any) { k.surprise(domain, r) },
		OnReasoning: k.reasoning, OnTick: k.tick})
	if err != nil {
		return nil, err
	}
	if err := k.loadCharacter(ctx); err != nil {
		return nil, err
	}
	k.World.OnOut = func(e semhome.Event, out semhome.Out) {
		for _, s := range out.Surprises {
			k.surprise("world", map[string]any{"kind": s.Kind, "key": s.Key, "why": s.Why})
		}
		if e.Kind == "settle" {
			k.project(out.Projected)
		}
	}
	if c.Tasks != nil {
		tc := *c.Tasks
		tc.Tools = append(k.HomeTools(), tc.Tools...)
		tc.World, tc.Log, tc.Now = k.WorldLines, c.Log, c.Now
		tc.Note = func(level, text string) {
			if v := k.Voice(); v != nil {
				v.Note(level, text)
			}
		}
		if k.Tasks, err = tasks.New(tc); err != nil {
			return nil, err
		}
		ws := c.Workspace
		if ws == "" {
			ws = filepath.Join(c.Root, "domains", ".metis")
		}
		k.Explain = &explain.Explainer{Log: c.Log, Root: c.Root, Ask: k.Tasks.Ask, Persona: c.Persona, Now: c.Now,
			WS: &explain.Workspace{Dir: expandHome(ws)}}
	}
	return k, nil
}

// timersContext gives the voice what is set (its silent context, keyed "timers").
func (k *Core) timersContext() {
	if v := k.Voice(); v != nil && k.Timers != nil {
		v.Context("timers", k.Timers.Summary())
	}
}

// surprise is a surprise of the Metis runtime (a domain's) or of the world: in the log (stream
// surprise), and, at most once every three minutes, narrated with the trace that led to it, as a note.
func (k *Core) surprise(domain string, r map[string]any) {
	now := k.Cfg.Now()
	k.Log.Append(now, "surprise", "surprise", domain, "", r, 1)
	if k.Explain != nil && k.Explain.WS != nil { // to the Metis loop's workspace, for the pi loop to take and rule
		if id, err := k.Explain.WS.Drop(domain, r, "", time.Unix(int64(now), 0)); err == nil {
			k.Log.Append(now, "surprise", "dropped", domain, "", map[string]string{"id": id}, 1)
		}
	}
	k.mu.Lock()
	k.unseen++
	mention := now-k.lastSurprise >= 1800 // a word about it at most every half hour; the rest when asked
	if mention {
		k.lastSurprise = now
	}
	k.mu.Unlock()
	k.surprisesNow()
	what := k.surpriseWords(r)
	if v := k.Voice(); v != nil && (mention || what != "") {
		if what != "" { // a device that did not do what it was told (B01-C02): she must not say it did
			v.Note("info", fmt.Sprintf("Something in the %s surprised you just now: %s. Do not say it did what was asked; "+
				"say so in a few words when the moment is quiet, and if asked.", domain, what))
		} else {
			v.Note("info", fmt.Sprintf("Something in the %s surprised you just now (its rules did not expect it). Mention it in a few words when the moment is quiet; the person can ask what surprised you.", domain))
		}
	}
}

// surpriseWords: a world surprise on a device's variable in the home's words ("the living room
// ventilation (power): commanded on, no reading after 15 s"), or "" (no device of the map).
func (k *Core) surpriseWords(r map[string]any) string {
	key, _ := r["key"].(string)
	id, v, ok := strings.Cut(key, ".")
	name := k.names[id]
	if !ok || name == "" {
		return ""
	}
	why, _ := r["why"].(string)
	if why == "" {
		why = "not what its rules expected"
	}
	return fmt.Sprintf("the %s (%s): %s", name, v, why)
}

// mapNames reads the home map's words: each device's or room's first word, by its id.
func mapNames(path string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return out
	}
	for _, sec := range m {
		var items map[string]struct {
			Words []string `json:"words"`
		}
		if json.Unmarshal(sec, &items) != nil {
			continue
		}
		for id, it := range items {
			if len(it.Words) > 0 {
				out[id] = it.Words[0]
			}
		}
	}
	return out
}

// surprisesNow tells Nina's screen how many surprises the person has not heard about, and how
// many of the domains' records are still open (her card's ⚑).
func (k *Core) surprisesNow() {
	k.mu.Lock()
	unseen := k.unseen
	k.mu.Unlock()
	open := 0
	if k.Explain != nil && k.Explain.WS != nil {
		open = explain.Counts(k.Explain.WS.List())["open"]
	}
	if l, ok := k.Voice().(interface{ Log(string, map[string]any) }); ok {
		l.Log("surprises", map[string]any{"unseen": unseen, "open": open})
	}
}

// clock gives the voice the time, once a minute: its session knows no clock of its own (it said
// 18:17 at 17:17), and the person asks.
func (k *Core) clock() {
	now := time.Unix(int64(k.Cfg.Now()), 0)
	m := now.Format("Mon 2 Jan 2006 15:04")
	k.mu.Lock()
	same := m == k.lastMinute
	k.lastMinute = m
	k.mu.Unlock()
	if same {
		return
	}
	if v := k.Voice(); v != nil {
		zone, _ := now.Zone()
		v.Context("now", fmt.Sprintf("now: %s (%s, local time)", m, zone))
	}
}

// activity is what the home is doing, for the temperament: its sleep or wake mood, someone
// talking, music playing ("" for nothing in particular).
func (k *Core) activity() string {
	if k.Glow != nil {
		if m := k.Glow.MoodNow(); m == "sleep" || m == "wake" {
			return m
		}
	}
	if k.Music != nil {
		playing, talking := k.Music.Busy()
		if talking {
			return "talking"
		}
		if playing {
			return "music"
		}
	}
	return ""
}

// temperament evaluates Nina's mood every so often (and when what the home does changes): on a
// new mood, her voice gets its prompt block (keyed "persona", between turns), the log a line and
// the dashboard her persona. Deterministic: catalog/persona.yaml and the clock decide.
func (k *Core) temperament() {
	if k.mood == nil {
		return
	}
	now := time.Unix(int64(k.Cfg.Now()), 0)
	act := k.activity()
	k.mu.Lock()
	due := k.moodAt.IsZero() || now.Sub(k.moodAt) >= k.mood.C.Every || act != k.moodAct
	if !due {
		k.mu.Unlock()
		return
	}
	first := k.moodAt.IsZero() && k.moodState.Mood == ""
	k.moodAt, k.moodAct = now, act
	if first { // once more in 30 s: the panel's pages may not have been listening yet
		k.moodAt = now.Add(30*time.Second - k.mood.C.Every)
	}
	prev := k.moodState.Mood
	st := k.mood.Eval(now, act)
	k.moodState = st
	k.mu.Unlock()
	// to Nina's own screen, every evaluation (her panel keeps the last for a page loading now)
	if l, ok := k.Voice().(interface{ Log(string, map[string]any) }); ok {
		l.Log("persona", map[string]any{"artwork": st.Artwork, "mood": st.Mood, "since": st.Since.Format(time.RFC3339),
			"moon": st.Moon, "override": st.Override})
	}
	// to Home Assistant at every evaluation, unchanged or not: a state set through its API is gone
	// after a restart until it is set again (her dashboard's card would guess by the date meanwhile)
	if k.Cfg.Publish != nil {
		go k.Cfg.Publish(st.Artwork, map[string]any{"friendly_name": "Nina's persona", "mood": st.Mood, "since": st.Since.Format(time.RFC3339),
			"moon": st.Moon, "activity": act, "temperament": st.Temperament, "icon": "mdi:drama-masks"})
	}
	if st.Mood == prev {
		return
	}
	if v := k.Voice(); v != nil {
		v.Context("persona", st.Prompt(k.mood.C))
	}
	k.herLight(st.Mood, now)
	if st.Mood != prev {
		k.Log.Append(k.Cfg.Now(), "persona", st.Mood, st.Artwork, "", map[string]any{"from": prev, "activity": act,
			"temperament": st.Temperament, "moon": st.Moon, "override": st.Override}, 1)
	}
}

// herLight tells the light's rules Nina's new mood (glow's her_glow decides), as one of the home's
// moods (the character's her_moods), between the morning's and the night's routines only: her
// mood does not wake the bedroom at night.
func (k *Core) herLight(mood string, now time.Time) {
	if k.Glow == nil || k.Character == nil {
		return
	}
	if wake, sleep := k.Character.Routine["wake"], k.Character.Routine["sleep"]; wake != "" && sleep != "" {
		if hm := now.Format("15:04"); hm < wake || hm >= sleep {
			return
		}
	}
	m := mood
	if h, ok := k.Character.Her[mood]; ok {
		m = h
	}
	if _, ok := k.Character.Moods[m]; !ok {
		return // not a mood of the home's
	}
	k.Glow.HerMood(m)
}

// Mood is Nina's temperament now (the dashboard's and the tools' view).
func (k *Core) Mood() persona.State {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.moodState
}

// routines turn the home to a mood at the character's times (bedtime: sleep; morning: wake), once a
// day each, as a person would ask the scenes; logged as a routine (stream routine), never as the
// person's habit.
func (k *Core) routines() {
	if k.Character == nil || len(k.Character.Routine) == 0 {
		return
	}
	now := time.Unix(int64(k.Cfg.Now()), 0)
	hm, day := now.Format("15:04"), now.Format("2006-01-02")
	k.mu.Lock()
	if k.routineDay == nil {
		k.routineDay = map[string]string{}
	}
	var due []string
	for m, at := range k.Character.Routine {
		if at == hm && k.routineDay[m] != day {
			k.routineDay[m] = day
			due = append(due, m)
		}
	}
	k.mu.Unlock()
	for _, m := range due {
		m := m
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), k.Cfg.CallTimeout)
			defer cancel()
			o := k.Home.Call(ctx, "home_scenes", map[string]any{"intent": "MOOD", "mood": m})
			k.Log.Append(k.Cfg.Now(), "routine", m, "", "", map[string]string{"status": o.Status, "text": o.Text}, 1)
		}()
	}
}

// ExplainTool is "explain yourself" as a tool: the voice's and the tasks'.
func (k *Core) ExplainTool() tasks.Tool {
	return tasks.Tool{Name: "narrate", Domain: "explain",
		Description: "Narrate yourself from the home's trace and its rules' comments (Metis's narrate). intent NARRATE: \"narrate\", \"explain what you did\" (focus recent: the last minutes in order), \"why did you do that?\" (focus last: only the last thing the home did and what led to it). intent SURPRISES: \"what surprised you?\": where the rules did not expect what happened, and where the loop is with it. The narration is spoken to the person by itself, word for word: when the result says so, say nothing.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"intent":   map[string]any{"type": "string", "enum": []string{"NARRATE", "SURPRISES"}},
			"focus":    map[string]any{"type": "string", "enum": []string{"last", "recent"}, "description": "NARRATE: last for \"why did you do that?\", recent for the last minutes"},
			"question": map[string]any{"type": "string", "description": "the person's question, in their words"},
			"minutes":  map[string]any{"type": "number", "description": "NARRATE recent: how far back (default 10)"}},
			"required": []string{"intent"}},
		Call: func(ctx context.Context, args map[string]any) (string, string) {
			if k.Explain == nil {
				return "error", "no model to narrate with"
			}
			q, _ := args["question"].(string)
			m, _ := args["minutes"].(float64)
			c, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			var text string
			var err error
			switch intent, _ := args["intent"].(string); {
			case intent == "SURPRISES":
				text, err = k.Explain.Surprises(c, q)
				k.mu.Lock()
				k.unseen = 0 // the person has heard them
				k.mu.Unlock()
				k.surprisesNow()
			case args["focus"] == "last":
				text, err = k.Explain.Why(c, q)
			default:
				if q == "" {
					q = "Narrate what you did, in order, and why."
				}
				text, err = k.Explain.Explain(c, q, m)
			}
			if err != nil {
				return "error", err.Error()
			}
			// in full, as written (the user, 2026-11-24): a tool result the voice retells in its own
			// shorter words, so the narration goes to it as a SAY message (prompts/voice.md: spoken
			// exactly), right after this call; the result asks it to say nothing meanwhile
			text = strings.TrimSpace(text)
			if d, ok := k.Voice().(interface{ Do(string, string) error }); ok && text != "" {
				go func() { time.Sleep(300 * time.Millisecond); d.Do("say", text) }()
				return "done", "The narration is being spoken to the person word for word right after this. Say nothing now: no introduction, no summary."
			}
			return "done", text
		}}
}

// loadCharacter puts the home's character into the world (its priors, its curiosity) when the file
// differs from the one last loaded (the log's stream character).
func (k *Core) loadCharacter(ctx context.Context) error {
	path := k.Cfg.Character
	if path == "" {
		path = filepath.Join(k.Cfg.Root, "character.yaml")
	}
	ch, err := taste.LoadCharacter(path)
	if err != nil {
		if os.IsNotExist(err) && k.Cfg.Character == "" {
			return nil
		}
		return fmt.Errorf("the character: %w", err)
	}
	last := ""
	if e, ok, err := k.Log.Latest(ctx, "character", "loaded"); err == nil && ok {
		var x struct{ Hash string }
		if json.Unmarshal(e.Data, &x) == nil {
			last = x.Hash
		}
	}
	k.World.Home.W.Lock()
	priors := len(k.World.Home.W.Taste.Priors)
	k.World.Home.W.Unlock()
	k.Character = ch
	if k.Music != nil {
		k.Music.Moods = ch.MoodTags("station")
	}
	if k.Glow != nil {
		k.Glow.Moods = ch.MoodTags("colour")
	}
	if last == ch.Hash && (priors > 0 || len(ch.Priors) == 0) {
		return nil // loaded, and still in the world
	}
	for _, ev := range ch.Events(k.Cfg.Now()) {
		if _, err := k.World.Apply(ev); err != nil {
			return fmt.Errorf("the character: %w", err)
		}
	}
	k.Log.Append(k.Cfg.Now(), "character", "loaded", "", "", map[string]any{"hash": ch.Hash, "path": path, "priors": len(ch.Priors)}, 1)
	return nil
}

// Floor is the voice's floor machine's state (fsttm: USER, SYSTEM, FREEu, FREEs): while anyone
// holds it, the music's rules duck the music.
func (k *Core) Floor(to string) {
	if k.Music != nil {
		k.Music.Talking(to == "USER" || to == "SYSTEM")
	}
}

// SetVoice gives the core its voice (the dialogue is made after the core: its tools are the core's).
// Presence is the radar's reading (the Pi's LD2410C, fsttm-live cmd/radar: sensor.nina_viewer).
type Presence struct {
	Present, Near, InReach bool
	Distance               float64 // metres
	Trend                  string  // approaching | departing | still
}

// Presence takes the radar's reading (the user, 2026-11-25: presence and distance): her loudness
// follows the distance; on a change (someone arrives, comes near, leaves) the voice and the tasks
// get a line of context and the log a line; someone stepping up within reach of the screen and
// near (1.5 m) wakes her for the follow-up, so they can simply talk (anyone farther, passing
// through the room, does not: awake, the microphone's audio goes to the cloud).
func (k *Core) Presence(p Presence) {
	if p.Present {
		k.Viewer(p.Distance)
	} else if r, ok := k.Voice().(interface{ SetReach(float64) }); ok {
		r.SetReach(0) // nobody: her voice carries to the bubble's edge, as the person set it
	}
	k.mu.Lock()
	prev := k.presence
	k.presence = p
	k.mu.Unlock()
	if p.Present == prev.Present && p.Near == prev.Near {
		return
	}
	line := "presence: nobody is in front of the screen"
	kind := "left"
	switch {
	case p.Present && p.Near:
		line, kind = fmt.Sprintf("presence: someone is at the screen, %.1f m away (%s)", p.Distance, p.Trend), "near"
	case p.Present:
		line, kind = fmt.Sprintf("presence: someone is in the room, %.1f m from the screen (%s)", p.Distance, p.Trend), "arrived"
	}
	k.Observe("presence", line)
	k.Log.Append(k.Cfg.Now(), "presence", kind, "", "", map[string]any{"distance": p.Distance, "trend": p.Trend, "near": p.Near}, 1)
	if l, ok := k.Voice().(interface{ Log(string, map[string]any) }); ok { // to her screens (the activity card)
		l.Log("presence", map[string]any{"kind": kind, "distance": p.Distance, "trend": p.Trend})
	}
	if p.Near && !prev.Near && p.Trend != "departing" {
		if d, ok := k.Voice().(interface{ Do(string, string) error }); ok {
			d.Do("wake", "")
		}
	}
}

// Viewer is the person's distance: her voice's loudness follows it, closer quieter. With the
// room calibrated (fsttm-live scripts/calib, audio.level) she is aimed at them, within the
// bubble the person set; otherwise about 0.6 at 0.9 m, 1 at 1.5 m, 1.6 from 2.4 m.
func (k *Core) Viewer(distance float64) {
	if distance <= 0 {
		return
	}
	if r, ok := k.Voice().(interface {
		SetReach(float64)
		Calibrated() bool
	}); ok && r.Calibrated() {
		r.SetReach(distance)
		return
	}
	g, ok := k.Voice().(interface{ SetGain(float64) })
	if !ok {
		return
	}
	g.SetGain(math.Max(.6, math.Min(1.6, distance/1.5)))
}

func (k *Core) SetVoice(v Voice) {
	k.mu.Lock()
	k.voice = v
	k.mu.Unlock()
	k.timersContext()
	go func() { time.Sleep(5 * time.Second); k.surprisesNow(); k.loadSettings(context.Background()) }() // to her screens
}

func (k *Core) Voice() Voice {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.voice
}

// Run beats the home's rules until ctx ends.
func (k *Core) Run(ctx context.Context, beat time.Duration) {
	if beat <= 0 {
		beat = time.Second
	}
	tk := time.NewTicker(beat)
	defer tk.Stop()
	if wb := k.Cfg.WorldBeat; wb > 0 { // the world's own clock: its settle, whatever the rules are doing
		go func() {
			wt := time.NewTicker(wb)
			defer wt.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-wt.C:
					k.Home.Settle()
				}
			}
		}()
	}
	for {
		select {
		case <-ctx.Done():
			k.World.Checkpoint(k.Cfg.Now())
			k.World.Snapshot(k.Cfg.Now()) // the next start restores it; the world events before it go
			return
		case <-tk.C:
			k.Home.Beat()
			k.routines()
			k.clock()
			k.temperament()
		}
	}
}

func (k *Core) reasoning(r metishome.Reasoning) {
	k.Log.Append(k.Cfg.Now(), "reasoning", r.Kind, r.Domain, "", map[string]string{"line": r.Line, "status": r.Status, "text": r.Text}, 1)
	if k.Tasks != nil {
		k.Tasks.Reasoning(r.Domain, r.Kind, r.Line)
	}
	v := k.Voice()
	if v == nil {
		return
	}
	switch lv := k.Cfg.Levels[r.Kind]; lv {
	case "drop":
	case "", "context":
		v.Context("", r.Line)
	default:
		if v.Note(lv, r.Line) != nil {
			v.Context("", r.Line)
		}
	}
}

func (k *Core) tick(kind string, f map[string]any) {
	switch kind {
	case "tick-director", "tick-relay", "tick-card", "tick-said", "tick-intent-bypass":
		k.Log.Append(k.Cfg.Now(), "tick", kind, "", "", f, 1)
	}
	if kind == "tick-relay" && f["what"] == "goal" && k.Tasks != nil {
		// a scene's wish routed to a package: a task that used the planner has reached it too
		for _, src := range []string{"scenes"} {
			k.Tasks.Routed(src, fmt.Sprint(f["domain"]))
		}
	}
}

var ages = regexp.MustCompile(`just now|\d+ (min|h|d) ago`)

func ageWord(s float64) string {
	switch {
	case s < 90:
		return "just now"
	case s < 600:
		return "a few min ago"
	case s < 3600:
		return "over 10 min ago"
	case s < 86400:
		return fmt.Sprintf("%.0f h ago", s/3600)
	}
	return fmt.Sprintf("%.0f d ago", s/86400)
}

// project gives the voice, and the tasks that named what they are about, the world's lines when
// what they say or their confidence's level changes (the quantized port).
func (k *Core) project(lines []sw.Line) {
	v := k.Voice()
	if v == nil {
		v = mute{}
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, l := range lines {
		if changed, _ := k.port.Step(l); !changed { // a new value or confidence level only
			continue
		}
		text := ages.ReplaceAllString(l.Text, ageWord(l.Age))
		if l.Stream == "env" && l.Key != "" {
			if k.Tasks != nil {
				k.Tasks.World(text)
			}
			text = l.Key + ": " + text
		}
		v.Context(l.Key, text)
	}
}

type mute struct{}

func (mute) Context(string, string)    {}
func (mute) Note(string, string) error { return nil }

// Observe is an integration's line that changed (its key: the entity): the voice's silent context
// and the tasks that named what it is about.
func (k *Core) Observe(key, line string) {
	if v := k.Voice(); v != nil {
		v.Context(key, line)
	}
	if k.Tasks != nil {
		k.Tasks.World(line)
	}
}

// WorldLines are the world's environment lines now (a task's TransformContext), then the
// integrations' and what the timers hold.
func (k *Core) WorldLines() []string {
	out := []string{"now: " + time.Unix(int64(k.Cfg.Now()), 0).Format("Mon 2 Jan 2006 15:04")}
	for _, l := range k.World.Home.Lines(k.Cfg.Now()) {
		if l.Stream == "env" {
			out = append(out, ages.ReplaceAllString(l.Text, ageWord(l.Age)))
		}
	}
	if k.Observed != nil {
		out = append(out, k.Observed()...)
	}
	if k.Timers != nil {
		out = append(out, k.Timers.Summary())
	}
	return out
}

// Call is a card call (the voice's, a task's): the rules' outcome; one they answered done is the
// person's habit, kept in the log (stream call) for "again" and "the usual".
func (k *Core) Call(ctx context.Context, tool string, args map[string]any) metishome.Outcome {
	from := k.Log.Last()
	o := k.Home.Call(ctx, tool, args)
	if o.Status == "done" && k.commanded(ctx, from) { // the house was told; a reading confirms it (B01-C02)
		o.Text += " (Sent to the device: not confirmed until it reports back. Say it was asked, not that it runs.)"
	}
	if o.Status == "done" && tool != "home_habits" {
		hc := habits.Call{Tool: tool, Args: args, At: k.Cfg.Now()}
		k.Log.Append(hc.At, "call", strings.TrimPrefix(tool, "home_"), "", "", hc, 1)
		if k.Habits != nil {
			k.Habits.Add(hc)
		}
	}
	return o
}

// commanded: whether the world took a device command since log row FROM (a call that switched something).
func (k *Core) commanded(ctx context.Context, from int64) bool {
	found := false
	_ = k.Log.Read(ctx, from+1, func(e nlog.Event) error {
		if e.Kind == "command" {
			found = true
		}
		return nil
	}, "world")
	return found
}

// HomeTools are the home's cards as a task's tools.
func (k *Core) HomeTools() []tasks.Tool {
	var out []tasks.Tool
	for _, t := range k.Home.Tools() {
		name := t.Function.Name
		if name == "home_escalate" && !k.Cfg.Escalate { // the redesign's step 6, off: the voice keeps delegate
			continue
		}
		out = append(out, tasks.Tool{Name: name, Description: t.Function.Description, Parameters: t.Function.Parameters,
			Domain: strings.TrimPrefix(name, "home_"),
			Call: func(ctx context.Context, args map[string]any) (string, string) {
				c, cancel := context.WithTimeout(ctx, k.Cfg.CallTimeout)
				defer cancel()
				o := k.Call(c, name, args)
				return o.Status, o.Text
			}})
	}
	return out
}

// Recall is the episodes' search as a task's tool (what was said and done before).
func (k *Core) Recall() tasks.Tool {
	return tasks.Tool{Name: "recall", Description: "Search what was said and done before (the conversation's and the tasks' history): words to find; the most recent first.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string", "description": "the words to look for"}},
			"required": []any{"query"}},
		Call: func(ctx context.Context, args map[string]any) (string, string) {
			q, _ := args["query"].(string)
			eps, err := k.Log.Recall(ctx, q, 5)
			if err != nil {
				return "error", err.Error()
			}
			if len(eps) == 0 {
				return "done", "nothing found"
			}
			var b strings.Builder
			for _, e := range eps {
				fmt.Fprintf(&b, "%s (%s): %s\n", time.Unix(int64(e.At), 0).Format("Mon 15:04"), e.Kind, e.Text)
			}
			return "done", strings.TrimSpace(b.String())
		}}
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[2:])
	}
	return p
}
