// Package app is Nina in one process, as a library: her voice (fsttm-live's pkg/dialogue) and the core
// (core/: her log, the world rebuilt from it, the home's rules composed under the director, her System 2
// on pi), assembled from her config and the backends a deployment gives her (Backends: acasa's Home
// Assistant at home, Google's APIs and the tablet's own player elsewhere). The persona depends on no
// deployment; a deployment's cmd/nina builds the backends and calls Run (the redesign, s-b478fe59).
//
// Gemini Live (System 1) has the home's cards and, with tasks on, delegate(goal): an open-ended goal
// becomes a pi task (System 2) whose result she says as a NOTE.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ibnHatab/fsttm-live/pkg/dialogue"
	"github.com/ibnHatab/nina-live/agenda"
	"github.com/ibnHatab/nina-live/core"
	"github.com/ibnHatab/nina-live/glow"
	"github.com/ibnHatab/nina-live/hatools"
	nlog "github.com/ibnHatab/nina-live/log"
	"github.com/ibnHatab/nina-live/mcp"
	"github.com/ibnHatab/nina-live/music"
	"github.com/ibnHatab/nina-live/tasks"
	"github.com/ibnHatab/nina-live/websearch"
	"github.com/ibnHatab/nina-live/whatsapp"
	"github.com/ibnHatab/nina-live/youtube"
	"github.com/metis-lang-dev/metis-agent/go/pkg/metishome"
	semhome "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/home"
	"google.golang.org/genai"
	"gopkg.in/yaml.v3"
)

// Config is nina.yaml (the persona's part: a deployment's own sections beside it, as acasa's hass).
type Config struct {
	Log  string `yaml:"log"`
	Home struct {
		Root            string        `yaml:"root"`
		Metisc          string        `yaml:"metisc"`
		Packages        []string      `yaml:"packages"`
		Beat            time.Duration `yaml:"beat"`
		CheckpointEvery int           `yaml:"checkpoint_every"`
		CallTimeout     time.Duration `yaml:"call_timeout"`
		Composed        bool          `yaml:"composed"`
		Route           string        `yaml:"route"` // director: every card's intent through the director (the redesign's step 4); "" straight to the card
	} `yaml:"home"`
	Dialogue string `yaml:"dialogue"`
	// Persona is Nina's character, put before the voice's protocol and the task runner's charter
	// (prompts/nina.md); Voice, the voice's protocol (prompts/voice.md): together, the dialogue's charter
	Persona string            `yaml:"persona"`
	Voice   string            `yaml:"voice"`
	Levels  map[string]string `yaml:"levels"`
	// Agenda: a target handle (catalog/domains/agenda) → its id in the agenda's backend (default
	// agenda.Default: Home Assistant's entities for Nina's Google calendar, Google Tasks' list, the local pair)
	Agenda map[string]string `yaml:"agenda"`
	Tasks  struct {
		Enabled   bool          `yaml:"enabled"`
		Provider  string        `yaml:"provider"`
		Model     string        `yaml:"model"`
		Key       string        `yaml:"key"`
		Charter   string        `yaml:"charter"`
		MaxTurns  int           `yaml:"max_turns"`
		Timeout   time.Duration `yaml:"timeout"`
		Transform *bool         `yaml:"transform"`
		Steer     *bool         `yaml:"steer"`
		FollowUp  *bool         `yaml:"follow_up"`
		Fallback  []struct {
			Provider string `yaml:"provider"`
			Model    string `yaml:"model"`
			Key      string `yaml:"key"`
		} `yaml:"fallback"` // the one-shot asks' (narration) next models when the first fails
	} `yaml:"tasks"`
	Search struct { // web_search (websearch/): Google Search grounding through Gemini Flash
		Enabled bool     `yaml:"enabled"`
		SearXNG string   `yaml:"searxng"` // its URL: tried first
		Models  []string `yaml:"models"`  // Gemini grounding, tried in order after it
		Key     string   `yaml:"key"`
		Daily   int      `yaml:"daily"` // web queries a day at most
	} `yaml:"search"`
	Music struct { // one knob for the music and her voice (music.Link; fsttm-live scripts/calib)
		Top       float64  `yaml:"top"`       // the stream's level at 100 %, dBFS RMS on the speakers' feed (0: the music's own volume)
		Under     float64  `yaml:"under"`     // dB under her voice while anyone talks (default 10)
		Curve     string   `yaml:"curve"`     // the player's volume law: mpd (software mixer) | pulse (MPD's mixer_type hardware on PulseAudio)
		Languages []string `yaml:"languages"` // the stations' languages this place listens to (Radio Browser's: english, romanian…; empty: any)
	} `yaml:"music"`
	// YouTube (youtube/): her screen's videos found by search (an API key), her own playlists by name
	YouTube struct {
		Key       string            `yaml:"key"`       // the API key's file
		Playlists map[string]string `yaml:"playlists"` // her own: name → id (unlisted ones the key cannot list)
	} `yaml:"youtube"`
	// WhatsApp (whatsapp/): her own number's channel, a linked device (nina-whatsapp link PHONE first)
	WhatsApp struct {
		Enabled bool          `yaml:"enabled"`
		Store   string        `yaml:"store"`    // its SQLite file (whatsmeow's keys, her messages)
		Names   []string      `yaml:"names"`    // words naming her in a group (nina)
		Show    []string      `yaml:"show"`     // groups on her screen's card, by name
		MinGap  time.Duration `yaml:"min_gap"`  // between two answers in one chat (10 s)
		PerHour int           `yaml:"per_hour"` // answers an hour in all (30)
		Answer  string        `yaml:"answer"`   // named (direct and named in groups) | self (only the number's own chat) | none
		Keep    []string      `yaml:"keep"`     // chats kept: self, names, all (none: all)
	} `yaml:"whatsapp"`
	Radar *bool `yaml:"radar"` // the radar's distance aims her voice (default true); false where her speakers are not where it is
	// Cards: the voice's home tools, "" one per package, "two" director and attended (cards.go, the redesign's step 5)
	Cards string       `yaml:"cards"`
	MCP   []mcp.Server `yaml:"mcp"` // MCP servers whose tools her tasks use (mcp/): Home Assistant's first
}

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

// Expand is a path with ~/ at the home directory.
func Expand(p string) string { return expand(p) }

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[2:])
	}
	return p
}

func on(b *bool) bool { return b == nil || *b }

// code is what computed the world's checkpoints: this build and the semantic world's version.
func code() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	rev := bi.Main.Version
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" {
			rev = s.Value
		}
	}
	for _, d := range bi.Deps {
		if strings.HasSuffix(d.Path, "semantic-world/go") {
			rev += " " + d.Version
		}
	}
	return rev
}

const taskCharter = `You are Nina's task runner: a goal the person gave Nina, carried out with the home's tools and her memory, then answered in two or three short sentences the voice will say.
- The home's tools ask the home's rules; a refusal says why: believe it, and say it.
- A message beginning "HOME" is what the home's systems report (their current beliefs, an outcome after your call): use it, never read it out as such.
- If something you did has not been confirmed yet, say so plainly.
- Say only what the tools and HOME tell you: what you did and how things are now.`

// Backends are the far ends Nina's hosts and tools reach; nil leaves that part out.
type Backends struct {
	Device   metishome.Device                                   // the home's devices (lights, air, media): the world's commands
	Agenda   agenda.Backend                                     // her calendars and lists
	Music    music.Backend                                      // the music's player
	Glow     func(ctx context.Context, commands []string) error // the bedroom light's mood, said to the Echo
	Publish  func(state string, attrs map[string]any)           // her persona where the home's screens see it
	Reader   Reader                                             // the lists, calendars and weather to read
	Observed func() []string                                    // the integrations' lines now (her silent context)
	// Watch runs until ctx ends: the house's states folded into her world (apply), the integrations'
	// lines as they change (line)
	Watch func(ctx context.Context, apply func(semhome.Event) (semhome.Out, error), line func(key, text string))
	// Presence runs until ctx ends: the radar's presence and distance (her voice's aim)
	Presence func(ctx context.Context, set func(core.Presence))
	Albums   func(ctx context.Context) ([]string, error) // the photo albums her screen can show
}

// Named is a list, a calendar or a weather source by its id and its name.
type Named struct{ ID, Name string }

// Reader reads what the read tools show (reading only: what changes them is a Metis package's command).
type Reader interface {
	Lists() []Named
	Items(ctx context.Context, id, status string) ([]string, error)
	Calendars() []Named
	Events(ctx context.Context, id string, window time.Duration, n int) ([]string, error)
	Weathers() []Named
	Forecast(ctx context.Context, id, kind string, n int) (string, error)
}

// App is Nina assembled: New opens her, Start sets her running, Dialogue is her voice.
type App struct {
	c     Config
	b     Backends
	L     *nlog.Log
	K     *core.Core
	D     *dialogue.Dialogue
	wa    *whatsapp.Client
	waLog func(string, map[string]any)
}

// LoadConfig reads nina.yaml into the persona's Config (yaml: its own sections; a deployment reads its own).
func LoadConfig(b []byte, c *Config) error { return yaml.Unmarshal(b, c) }

// Run is New, Start and her dialogue until ctx ends.
func Run(ctx context.Context, c Config, b Backends) error {
	a, err := New(ctx, c, b)
	if err != nil {
		return err
	}
	defer a.L.Close()
	if err := a.Start(ctx); err != nil {
		return err
	}
	a.L.Append(now(), "sys", "start", "", "", map[string]any{"code": code(), "replay": a.K.Report}, 1)
	if err := a.D.Run(ctx); err != nil {
		stdlog.Printf("the dialogue: %v", err)
	}
	a.L.Append(now(), "sys", "stop", "", "", nil, 1)
	return nil
}

// New opens her log, her core (the world rebuilt from the log), her tools and her dialogue.
func New(ctx context.Context, c Config, b Backends) (*App, error) {
	root := expand(c.Home.Root)
	if err := os.MkdirAll(filepath.Dir(expand(c.Log)), 0o755); err != nil {
		return nil, err
	}
	L, err := nlog.Open(expand(c.Log))
	if err != nil {
		return nil, err
	}
	a := &App{c: c, b: b, L: L}
	fail := func(err error) (*App, error) { L.Close(); return nil, err }

	ents := agenda.Default
	if len(c.Agenda) > 0 {
		ents = c.Agenda
	}
	var ag *agenda.Host
	if b.Agenda != nil {
		ag = &agenda.Host{Entities: ents, Backend: b.Agenda}
	}
	var mu *music.Host
	if b.Music != nil {
		mu = &music.Host{Backend: b.Music}
		// stations in the place's languages first (2026-11-26)
		mu.Languages = c.Music.Languages
	}
	var gl *glow.Host
	if b.Glow != nil {
		gl = &glow.Host{Say: b.Glow} // script.alexa_quiet at home: the Echo silenced
	}
	persona := ""
	if b, err := os.ReadFile(expand(c.Persona)); err == nil && c.Persona != "" {
		persona = string(b)
	}
	cc := core.Config{Persona: persona, Agenda: ag, Music: mu, Glow: gl, Publish: b.Publish, Log: L, Root: root, Metisc: expand(c.Home.Metisc), Packages: c.Home.Packages, Composed: c.Home.Composed, RouteIntents: c.Home.Route == "director",
		Device: b.Device, Code: code(), CheckpointEvery: c.Home.CheckpointEvery, CallTimeout: c.Home.CallTimeout,
		Levels: c.Levels, Now: now}
	if c.Tasks.Enabled {
		charter := taskCharter
		if c.Tasks.Charter != "" {
			if b, err := os.ReadFile(expand(c.Tasks.Charter)); err == nil {
				charter = string(b)
			}
		}
		if b, err := os.ReadFile(expand(c.Persona)); err == nil && c.Persona != "" {
			charter = string(b) + "\n\n# The task runner\n\n" + charter
		}
		var fbs []tasks.Fallback
		for _, f := range c.Tasks.Fallback {
			fbs = append(fbs, tasks.Fallback{Provider: f.Provider, Model: f.Model, KeyFile: expand(f.Key)})
		}
		cc.Tasks = &tasks.Config{Provider: c.Tasks.Provider, Model: c.Tasks.Model, KeyFile: c.Tasks.Key, System: charter, Fallbacks: fbs,
			MaxTurns: c.Tasks.MaxTurns, Timeout: c.Tasks.Timeout,
			Mech:   tasks.Mechanisms{Transform: on(c.Tasks.Transform), Steer: on(c.Tasks.Steer), FollowUp: on(c.Tasks.FollowUp)},
			Strain: func(err error) { strainOf("mind", err) }}
	}
	t0 := time.Now()
	k, err := core.Open(ctx, cc)
	if err != nil {
		return fail(err)
	}
	strainCore.Store(k)
	a.K = k
	k.Observed = b.Observed // the integrations' lines (at home: Home Assistant's weather, lists, calendars, timers, people, players)
	var reads []tasks.Tool
	if b.Reader != nil {
		reads = readTools(b.Reader)
	}
	if c.Search.Enabled { // web_search: Google Search grounding through Gemini Flash, the voice's and the tasks'
		key := ""
		if len(c.Search.Models) > 0 {
			k, err := tasks.Key("google", expand(c.Search.Key))
			if err != nil {
				return fail(fmt.Errorf("search: %w", err))
			}
			key = k
		}
		ws, err := websearch.New(ctx, c.Search.SearXNG, key, c.Search.Models, c.Search.Daily, L, now)
		if err != nil {
			return fail(fmt.Errorf("search: %w", err))
		}
		ws.Strain = func(err error) { strainOf("search", err) }
		reads = append(reads, ws.Tool())
	}
	var stageLog func(string, map[string]any) // her screens, once her voice is (the stage's "stage" events)
	yt, err := youtube.Load(expand(firstOf(c.YouTube.Key, "~/.config/nina/youtube.key")), c.YouTube.Playlists)
	if err != nil {
		stdlog.Printf("youtube: no API key (%v): her own playlists only, no search", err)
	}
	reads = append(reads, screenTool(yt, b.Albums, func(d map[string]any) {
		if stageLog != nil {
			stageLog("stage", d)
		}
	}))
	if c.WhatsApp.Enabled && k.Tasks != nil {
		w := c.WhatsApp
		wc, err := whatsapp.Open(ctx, whatsapp.Config{Store: expand(firstOf(w.Store, "~/.local/share/nina/whatsapp.db")), Names: w.Names,
			Show: w.Show, MinGap: w.MinGap, PerHour: w.PerHour, Answer: waAnswer(k), Mode: w.Answer, Keep: w.Keep,
			Log: func(kind string, d map[string]any) {
				if a.waLog != nil {
					a.waLog(kind, d)
				}
			},
			Changed: k.WhatsAppChanged,
			Strain:  func(err error) { strainOf("whatsapp", err) }})
		if err != nil {
			stdlog.Printf("whatsapp: %v", err)
		} else {
			a.wa = wc // linked or not: her screens can link it (the WhatsApp card)
			reads = append(reads, waReadTool(wc))
			if !wc.LoggedIn() {
				stdlog.Printf("whatsapp: not linked yet (her WhatsApp card: Link a device; or nina-whatsapp link-qr)")
			}
		}
	}
	if k.Tasks != nil {
		k.Tasks.AddTool(k.Recall())
		reads = append(reads, k.ExplainTool()) // the voice's and the tasks' (the loop below)
		for _, t := range reads {
			k.Tasks.AddTool(t)
		}
		// Home Assistant through its MCP servers: Nina's own three tools (hatools), the tasks' only. Each call is
		// what no card covered: logged (stream mcp, uncovered) for the Metis workspace and the SITL's coverage
		// metric (the redesign's channel C, s-b478fe59)
		for _, t := range hatools.Dial(ctx, c.MCP, stdlog.Printf) {
			k.Tasks.AddTool(uncovered(L, t))
		}
	}
	stdlog.Printf("the world rebuilt from snapshot %d (%s) and %d events (%d checkpoints checked, %d missed, genesis %v; %d pruned); %s backend, composed %v, tasks %v; %.2fs",
		k.Report.Snapshot, k.Report.Refused, k.Report.Events, k.Report.Checked, len(k.Report.Mismatch), k.Report.Genesis, k.Report.Pruned, k.Home.Backend(), c.Home.Composed,
		k.Tasks != nil, time.Since(t0).Seconds())

	// her voice: the dialogue, its preferences from the log, the core's tools (and delegate)
	dcfg, err := dialogue.LoadConfig(expand(c.Dialogue))
	if err != nil {
		return fail(err)
	}
	if c.Persona != "" && c.Voice != "" { // the charter: Nina's character, then the voice's protocol
		p, err1 := os.ReadFile(expand(c.Persona))
		v, err2 := os.ReadFile(expand(c.Voice))
		if err1 == nil && err2 == nil {
			path := filepath.Join(filepath.Dir(expand(c.Log)), "charter.md")
			if os.WriteFile(path, append(append(p, "\n\n"...), v...), 0o644) == nil {
				dcfg.Charter = path
			}
		}
	}
	L.Read(ctx, 0, func(e nlog.Event) error {
		var p dialogue.Prefs
		if e.Kind == "set" && json.Unmarshal(e.Data, &p) == nil {
			dcfg.Prefs = p
		}
		return nil
	}, "prefs")
	d, err := dialogue.New(ctx, dcfg, dialogue.NewEvents(io.Discard), voiceTools(k, reads, c.Cards))
	if err != nil {
		return fail(err)
	}
	a.D = d
	d.SavePrefs = func(p dialogue.Prefs) error {
		_, err := L.Append(now(), "prefs", "set", "", "", p, 1)
		return err
	}
	k.SetVoice(d)
	if c.Music.Top != 0 && d.Calibrated() && k.Music != nil { // one knob: the music in dB from her voice's level
		// on the core's host (rebuilt from the log at start), not the one given to it
		k.Music.Link = &music.Link{Top: c.Music.Top, Under: c.Music.Under, Voice: d.Volume, Now: d.Loudness, Knob: d.Knob, Curve: c.Music.Curve}
		d.OnDuck = func(bool) { k.Music.Follow() } // a person over her: the music down with her at once
		stdlog.Printf("music: one knob with her voice (top %.0f dBFS, %.0f dB under her while anyone talks)", c.Music.Top, c.Music.Under)
	}
	d.OnAction = k.Action // her settings from her screens: name, persona, temperament
	stageLog = func(kind string, data map[string]any) { d.Log(kind, data) }
	return a, nil
}

// Start sets her running: her WhatsApp, the radar, the dialogue's log and plugins, the house's states into
// her world, her beat.
func (a *App) Start(ctx context.Context) error {
	c, b, k, d, L := a.c, a.b, a.K, a.D, a.L
	if b.Presence != nil && (c.Radar == nil || *c.Radar) { // the radar's distance aims her voice (radar: false where her speakers are not where it is)
		go b.Presence(ctx, k.Presence)
	}
	wa := a.wa
	if wa != nil { // her screens hear of WhatsApp now; then it connects
		a.waLog = func(kind string, data map[string]any) { d.Log(kind, data) }
		k.UseWhatsApp(wa)  // her settings' groups on her screen
		if wa.LoggedIn() { // connected by its link otherwise (StartLink, from her screen)
			// in the background, again after a failure (a DNS hiccup at the start had left her offline, and
			// its 10 s dial had held her panel's start: her settings were said before it listened, 2026-11-27)
			go func() {
				wait := 10 * time.Second
				for {
					err := wa.Connect(ctx)
					if err == nil {
						stdlog.Printf("whatsapp: connected (answers: %s; keeps %v; shows %v)", firstOf(c.WhatsApp.Answer, "named"), c.WhatsApp.Keep, c.WhatsApp.Show)
						return
					}
					stdlog.Printf("whatsapp: %v (again in %s)", err, wait)
					select {
					case <-ctx.Done():
						return
					case <-time.After(wait):
					}
					wait = min(2*wait, 5*time.Minute)
				}
			}()
		}
	}
	go logDialogue(ctx, d, L, k.Floor)
	if err := d.StartPlugins(ctx); err != nil {
		return err
	}
	if b.Watch != nil {
		go b.Watch(ctx, k.World.Apply, k.Observe)
	}
	go k.Run(ctx, c.Home.Beat)
	return nil
}

// readTools read the lists, the calendars and the weather for the voice and the tasks (whatever
// keeps each: Home Assistant's integrations, Google's APIs). Reading only: what changes them is a
// Metis package's command, like the home's.
func readTools(o Reader) []tasks.Tool {
	obj := func(props map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": props}
	}
	return []tasks.Tool{
		{Name: "lists", Domain: "lists", Description: "Read the to-do and shopping lists (Home Assistant's: the shopping list, local and Google lists): their open items.",
			Parameters: obj(map[string]any{"list": map[string]any{"type": "string", "description": "a list's name; empty: every list"}}),
			Call: func(ctx context.Context, a map[string]any) (string, string) {
				want, _ := a["list"].(string)
				var out []string
				for _, l := range o.Lists() {
					n := l.Name
					if want != "" && !strings.Contains(strings.ToLower(n+" "+l.ID), strings.ToLower(want)) {
						continue
					}
					items, err := o.Items(ctx, l.ID, "needs_action")
					if err != nil {
						return "error", err.Error()
					}
					if len(items) == 0 {
						out = append(out, n+": empty")
					} else {
						out = append(out, n+": "+strings.Join(items, ", "))
					}
				}
				if len(out) == 0 {
					return "done", "no such list"
				}
				return "done", strings.Join(out, "\n")
			}},
		{Name: "calendar", Domain: "calendar", Description: "Read the calendars' coming events (Home Assistant's calendars: Google, local).",
			Parameters: obj(map[string]any{"days": map[string]any{"type": "number", "description": "how many days ahead (default 7)"}}),
			Call: func(ctx context.Context, a map[string]any) (string, string) {
				days, _ := a["days"].(float64)
				if days <= 0 {
					days = 7
				}
				var out []string
				for _, cal := range o.Calendars() {
					evs, err := o.Events(ctx, cal.ID, time.Duration(days*24)*time.Hour, 20)
					if err != nil {
						return "error", err.Error()
					}
					n := cal.Name
					if len(evs) == 0 {
						out = append(out, n+": nothing")
					} else {
						out = append(out, n+": "+strings.Join(evs, "; "))
					}
				}
				if len(out) == 0 {
					return "done", "no calendar is connected yet"
				}
				return "done", strings.Join(out, "\n")
			}},
		{Name: "weather", Domain: "weather", Description: "The weather forecast: the next days, or the next hours.",
			Parameters: obj(map[string]any{"hourly": map[string]any{"type": "boolean", "description": "the next hours instead of the next days"}}),
			Call: func(ctx context.Context, a map[string]any) (string, string) {
				kind, n := "daily", 5
				if h, _ := a["hourly"].(bool); h {
					kind, n = "hourly", 12
				}
				var out []string
				for _, w := range o.Weathers() {
					f, err := o.Forecast(ctx, w.ID, kind, n)
					if err != nil {
						return "error", err.Error()
					}
					out = append(out, f)
				}
				if len(out) == 0 {
					return "done", "no weather integration"
				}
				return "done", strings.Join(out, "\n")
			}},
	}
}

// voiceTools are Gemini Live's: the home's cards, the read tools and, with tasks, delegate.
func voiceTools(k *core.Core, reads []tasks.Tool, cards string) dialogue.Tools {
	var del *genai.FunctionDeclaration
	var delFn dialogue.ToolFunc
	if k.Tasks != nil {
		del = &genai.FunctionDeclaration{Name: "delegate",
			Description: "Hand an open-ended goal to Nina's task runner: something that takes several steps, " +
				"checking things or doing them in order, or remembering what was said before. It answers at once; " +
				"the result comes later as a note. Not for a single command a home tool does by itself.",
			ParametersJsonSchema: map[string]any{"type": "object", "properties": map[string]any{
				"goal": map[string]any{"type": "string", "description": "the goal, in the person's terms, complete"}},
				"required": []any{"goal"}}}
		delFn = func(_ context.Context, args map[string]any) map[string]any {
			goal, _ := args["goal"].(string)
			if strings.TrimSpace(goal) == "" {
				return map[string]any{"status": "error", "text": "a goal is needed"}
			}
			t := k.Tasks.Delegate(goal, "important")
			return map[string]any{"status": "accepted", "task": t.ID, "text": "I'm on it; the result will come as a note."}
		}
	}
	if cards == "two" { // the redesign's step 5 (cards.go)
		return twoCards(k, reads, del, delFn)
	}
	var decls []*genai.FunctionDeclaration
	funcs := map[string]dialogue.ToolFunc{}
	for _, t := range append(k.HomeTools(), reads...) {
		t := t
		decls = append(decls, &genai.FunctionDeclaration{Name: t.Name, Description: t.Description, ParametersJsonSchema: t.Parameters})
		funcs[t.Name] = func(ctx context.Context, args map[string]any) map[string]any {
			musicWords(t.Name, args)
			status, text := t.Call(ctx, args)
			return map[string]any{"status": status, "text": text}
		}
	}
	if del != nil {
		decls = append(decls, del)
		funcs["delegate"] = delFn
	}
	// (the Live API's own google_search beside them was tried, 2026-11-25: on a key without billing the
	// session's setup itself is refused, "exceeded your current quota": her voice would not start)
	return dialogue.Tools{Decls: []*genai.Tool{{FunctionDeclarations: decls}}, Funcs: funcs}
}

// musicWords: the voice gives the person's words alone ("calm") to PLAY: they are the kind asked for
// (intent.yaml: genre is a known kind or what the person said); the mapper wants one.
func musicWords(tool string, args map[string]any) {
	if g, _ := args["genre"].(string); tool == "home_music" && args["intent"] == "PLAY" && g == "" {
		if w, _ := args["words"].(string); w != "" {
			args["genre"] = w
		}
	}
}

// strainCore is the core once open: her services' API errors (and successes) go to it as strain
// (core.Strain), whoever meets them first.
var strainCore atomic.Pointer[core.Core]

func strainOf(source string, err error) {
	if k := strainCore.Load(); k != nil {
		k.Strain(source, err)
	}
}

// voiceStrain: the voice's and the ears' events that say something about their service.
func voiceStrain(kind string, e map[string]any) {
	er, _ := e["err"].(string)
	switch kind {
	case "mind_error", "voice_closed", "voice_resume_failed":
		strainOf("voice", errors.New(er))
	case "voice_connected":
		strainOf("voice", nil)
	case "ears_error", "ears_closed":
		strainOf("ears", errors.New(er))
	case "ears_connected":
		strainOf("ears", nil)
	}
}

// logDialogue writes the dialogue's events to the log (stream dialog) and indexes what was
// heard and said for recall.
func logDialogue(ctx context.Context, d *dialogue.Dialogue, L *nlog.Log, floor func(string)) {
	ch, cancel := d.Subscribe()
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			kind, _ := e["ev"].(string)
			voiceStrain(kind, e)
			if kind == "floor" && floor != nil {
				to, _ := e["to"].(string)
				floor(to)
			}
			if kind == "interim" || kind == "said" || kind == "" {
				continue
			}
			if _, ok := e["png"]; ok && kind == "wa_status" { // a WhatsApp link's QR: on her screens, not in the log
				c := map[string]any{}
				for k, v := range e {
					if k != "png" {
						c[k] = v
					}
				}
				e = c
			}
			rec, err := L.Append(now(), "dialog", kind, "", "", e, 1)
			if err != nil {
				continue
			}
			var text string
			switch kind {
			case "heard", "turn", "typed":
				text, _ = e["text"].(string)
			case "reply_done":
				text, _ = e["said"].(string)
			}
			if strings.TrimSpace(text) != "" {
				L.Episode(rec, text)
			}
		}
	}
}

// uncovered wraps an MCP tool: every call is logged as an action no card covered (its name, arguments, status).
func uncovered(L *nlog.Log, t tasks.Tool) tasks.Tool {
	call := t.Call
	t.Call = func(ctx context.Context, args map[string]any) (string, string) {
		status, text := call(ctx, args)
		L.Append(now(), "mcp", "uncovered", t.Name, "", map[string]any{"tool": t.Name, "args": args, "status": status}, 1)
		return status, text
	}
	return t
}
