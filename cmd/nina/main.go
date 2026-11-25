// nina: Nina in one process: her voice (fsttm-live's pkg/dialogue), and the core (core/: her log,
// the world rebuilt from it, the home's rules composed under the director, her System 2 on pi),
// with Home Assistant as her tool (acasa's hass).
//
//	nina -config ~/repo/acasa/live/nina.yaml
//
// Gemini Live (System 1) has the home's five cards and, with tasks on, delegate(goal): an
// open-ended goal becomes a pi task (System 2) whose result she says as a NOTE.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	stdlog "log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/ibnHatab/acasa/hass"
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
	semhome "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/home"
	"google.golang.org/genai"
	"gopkg.in/yaml.v3"
)

// Config is nina.yaml.
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
	} `yaml:"home"`
	Hass struct {
		URL   string `yaml:"url"`
		Token string `yaml:"token"`
	} `yaml:"hass"`
	Dialogue string `yaml:"dialogue"`
	// Persona is Nina's character, put before the voice's protocol and the task runner's charter
	// (prompts/nina.md); Voice, the voice's protocol (prompts/voice.md): together, the dialogue's charter
	Persona string            `yaml:"persona"`
	Voice   string            `yaml:"voice"`
	Levels  map[string]string `yaml:"levels"`
	// Agenda: a target handle (catalog/domains/agenda) → its Home Assistant entity (default
	// agenda.Default: Nina's Google calendar, Google Tasks' list, the local test pair)
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
		Top   float64 `yaml:"top"`   // the stream's level at 100 %, dBFS RMS on the speakers' feed (0: the music's own volume)
		Under float64 `yaml:"under"` // dB under her voice while anyone talks (default 10)
	} `yaml:"music"`
	MCP []mcp.Server `yaml:"mcp"` // MCP servers whose tools her tasks use (mcp/): Home Assistant's first
}

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

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

func main() {
	cfgPath := flag.String("config", "~/.config/nina/nina.yaml", "nina.yaml")
	flag.Parse()
	var c Config
	b, err := os.ReadFile(expand(*cfgPath))
	if err != nil {
		stdlog.Fatal(err)
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		stdlog.Fatalf("%s: %v", *cfgPath, err)
	}
	root := expand(c.Home.Root)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(filepath.Dir(expand(c.Log)), 0o755); err != nil {
		stdlog.Fatal(err)
	}
	L, err := nlog.Open(expand(c.Log))
	if err != nil {
		stdlog.Fatal(err)
	}
	defer L.Close()
	m, err := semhome.LoadMap(filepath.Join(root, "world", "home.map.json"))
	if err != nil {
		stdlog.Fatal(err)
	}
	ha, err := hass.New(c.Hass.URL, c.Hass.Token)
	if err != nil {
		stdlog.Fatal(err)
	}

	ents := agenda.Default
	if len(c.Agenda) > 0 {
		ents = c.Agenda
	}
	ag := &agenda.Host{Entities: ents, Call: func(ctx context.Context, domain, service string, data map[string]any) error {
		_, err := ha.Service(ctx, domain, service, data, false)
		return err
	}}
	mu := &music.Host{Call: func(ctx context.Context, domain, service string, data map[string]any) error {
		_, err := ha.Service(ctx, domain, service, data, false)
		return err
	}}
	gl := &glow.Host{Say: func(ctx context.Context, commands []string) error { // script.alexa_quiet: the Echo silenced
		_, err := ha.Service(ctx, "script", "alexa_quiet", map[string]any{"commands": commands}, false)
		return err
	}}
	persona := ""
	if b, err := os.ReadFile(expand(c.Persona)); err == nil && c.Persona != "" {
		persona = string(b)
	}
	publish := func(state string, attrs map[string]any) { // Nina's persona, for the dashboard (sensor.nina_persona)
		if _, err := ha.Call("POST", "/api/states/sensor.nina_persona", map[string]any{"state": state, "attributes": attrs}); err != nil {
			stdlog.Printf("[persona] %v", err)
		}
	}
	cc := core.Config{Persona: persona, Agenda: ag, Music: mu, Glow: gl, Publish: publish, Log: L, Root: root, Metisc: expand(c.Home.Metisc), Packages: c.Home.Packages, Composed: c.Home.Composed,
		Device: hass.NewDevice(ha, m), Code: code(), CheckpointEvery: c.Home.CheckpointEvery, CallTimeout: c.Home.CallTimeout,
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
			Mech: tasks.Mechanisms{Transform: on(c.Tasks.Transform), Steer: on(c.Tasks.Steer), FollowUp: on(c.Tasks.FollowUp)}}
	}
	t0 := time.Now()
	k, err := core.Open(ctx, cc)
	if err != nil {
		stdlog.Fatal(err)
	}
	obs := hass.NewObserver(ha) // Home Assistant's standard classes: weather, lists, calendars, timers, people, players
	k.Observed = obs.Lines
	obs.OnLine = k.Observe
	reads := readTools(obs)
	if c.Search.Enabled { // web_search: Google Search grounding through Gemini Flash, the voice's and the tasks'
		key := ""
		if len(c.Search.Models) > 0 {
			k, err := tasks.Key("google", expand(c.Search.Key))
			if err != nil {
				stdlog.Fatalf("search: %v", err)
			}
			key = k
		}
		ws, err := websearch.New(ctx, c.Search.SearXNG, key, c.Search.Models, c.Search.Daily, L, now)
		if err != nil {
			stdlog.Fatalf("search: %v", err)
		}
		reads = append(reads, ws.Tool())
	}
	if k.Tasks != nil {
		k.Tasks.AddTool(k.Recall())
		reads = append(reads, k.ExplainTool()) // the voice's and the tasks' (the loop below)
		for _, t := range reads {
			k.Tasks.AddTool(t)
		}
		// Home Assistant through its MCP servers: Nina's own three tools (hatools), the tasks' only
		for _, t := range hatools.Dial(ctx, c.MCP, stdlog.Printf) {
			k.Tasks.AddTool(t)
		}
	}
	stdlog.Printf("the world rebuilt from snapshot %d (%s) and %d events (%d checkpoints checked, %d missed, genesis %v; %d pruned); %s backend, composed %v, tasks %v; %.2fs",
		k.Report.Snapshot, k.Report.Refused, k.Report.Events, k.Report.Checked, len(k.Report.Mismatch), k.Report.Genesis, k.Report.Pruned, k.Home.Backend(), c.Home.Composed,
		k.Tasks != nil, time.Since(t0).Seconds())

	// her voice: the dialogue, its preferences from the log, the core's tools (and delegate)
	dcfg, err := dialogue.LoadConfig(expand(c.Dialogue))
	if err != nil {
		stdlog.Fatal(err)
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
	d, err := dialogue.New(ctx, dcfg, dialogue.NewEvents(io.Discard), voiceTools(k, reads))
	if err != nil {
		stdlog.Fatal(err)
	}
	d.SavePrefs = func(p dialogue.Prefs) error {
		_, err := L.Append(now(), "prefs", "set", "", "", p, 1)
		return err
	}
	k.SetVoice(d)
	if c.Music.Top != 0 && d.Calibrated() && k.Music != nil { // one knob: the music in dB from her voice's level
		// on the core's host (rebuilt from the log at start), not the one given to it
		k.Music.Link = &music.Link{Top: c.Music.Top, Under: c.Music.Under, Voice: d.Volume, Now: d.Loudness, Knob: d.Knob}
		stdlog.Printf("music: one knob with her voice (top %.0f dBFS, %.0f dB under her while anyone talks)", c.Music.Top, c.Music.Under)
	}
	d.OnAction = k.Action // her settings from her screens: name, persona, temperament
	go func() {           // the radar (sensor.nina_viewer, the Pi's LD2410C): presence and distance → core.Presence
		last := ""
		for range time.Tick(2 * time.Second) {
			raw, err := ha.Call("GET", "/api/states/sensor.nina_viewer", nil)
			if err != nil {
				continue // no radar yet
			}
			var st struct {
				State      string         `json:"state"`
				Updated    string         `json:"last_updated"`
				Attributes map[string]any `json:"attributes"`
			}
			if json.Unmarshal(raw, &st) != nil || st.Updated == last {
				continue
			}
			last = st.Updated
			p := core.Presence{Present: st.State == "present"}
			p.Distance, _ = st.Attributes["distance"].(float64)
			p.Near, _ = st.Attributes["near"].(bool)
			p.InReach, _ = st.Attributes["in_reach"].(bool)
			p.Trend, _ = st.Attributes["trend"].(string)
			k.Presence(p)
		}
	}()
	go logDialogue(ctx, d, L, k.Floor)
	if err := d.StartPlugins(ctx); err != nil {
		stdlog.Fatal(err)
	}
	go func() {
		if err := ha.Follow(ctx, obs.Feed(k.World.Apply), now); err != nil {
			stdlog.Printf("[hass] %v: the world gets no readings", err)
		}
	}()
	go k.Run(ctx, c.Home.Beat)
	go obs.Run(ctx, 10*time.Minute)
	L.Append(now(), "sys", "start", "", "", map[string]any{"code": code(), "replay": k.Report}, 1)
	if err := d.Run(ctx); err != nil {
		stdlog.Printf("the dialogue: %v", err)
	}
	L.Append(now(), "sys", "stop", "", "", nil, 1)
}

// readTools read Home Assistant's classes for the voice and the tasks: every to-do list, every
// calendar, the weather (whatever integration is behind each). Reading only: what changes them is
// a Metis domain's command, like the home's.
func readTools(o *hass.Observer) []tasks.Tool {
	obj := func(props map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": props}
	}
	return []tasks.Tool{
		{Name: "lists", Domain: "lists", Description: "Read the to-do and shopping lists (Home Assistant's: the shopping list, local and Google lists): their open items.",
			Parameters: obj(map[string]any{"list": map[string]any{"type": "string", "description": "a list's name; empty: every list"}}),
			Call: func(ctx context.Context, a map[string]any) (string, string) {
				want, _ := a["list"].(string)
				var out []string
				for _, st := range o.Entities("todo") {
					n, _ := st.Attributes["friendly_name"].(string)
					if want != "" && !strings.Contains(strings.ToLower(n+" "+st.EntityID), strings.ToLower(want)) {
						continue
					}
					items, err := o.Items(ctx, st.EntityID, "needs_action")
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
				for _, st := range o.Entities("calendar") {
					evs, err := o.Events(ctx, st.EntityID, time.Duration(days*24)*time.Hour, 20)
					if err != nil {
						return "error", err.Error()
					}
					n, _ := st.Attributes["friendly_name"].(string)
					if len(evs) == 0 {
						out = append(out, n+": nothing")
					} else {
						out = append(out, n+": "+strings.Join(evs, "; "))
					}
				}
				if len(out) == 0 {
					return "done", "no calendar is connected to Home Assistant yet"
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
				for _, st := range o.Entities("weather") {
					f, err := o.Forecast(ctx, st.EntityID, kind, n)
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
func voiceTools(k *core.Core, reads []tasks.Tool) dialogue.Tools {
	var decls []*genai.FunctionDeclaration
	funcs := map[string]dialogue.ToolFunc{}
	for _, t := range append(k.HomeTools(), reads...) {
		t := t
		decls = append(decls, &genai.FunctionDeclaration{Name: t.Name, Description: t.Description, ParametersJsonSchema: t.Parameters})
		funcs[t.Name] = func(ctx context.Context, args map[string]any) map[string]any {
			if g, _ := args["genre"].(string); t.Name == "home_music" && args["intent"] == "PLAY" && g == "" {
				// the voice gives the person's words alone ("calm"): they are the kind asked for
				// (intent.yaml: genre is a known kind or what the person said); the mapper wants one
				if w, _ := args["words"].(string); w != "" {
					args["genre"] = w
				}
			}
			status, text := t.Call(ctx, args)
			return map[string]any{"status": status, "text": text}
		}
	}
	if k.Tasks != nil {
		decls = append(decls, &genai.FunctionDeclaration{Name: "delegate",
			Description: "Hand an open-ended goal to Nina's task runner: something that takes several steps, " +
				"checking things or doing them in order, or remembering what was said before. It answers at once; " +
				"the result comes later as a note. Not for a single command a home tool does by itself.",
			ParametersJsonSchema: map[string]any{"type": "object", "properties": map[string]any{
				"goal": map[string]any{"type": "string", "description": "the goal, in the person's terms, complete"}},
				"required": []any{"goal"}}})
		funcs["delegate"] = func(_ context.Context, args map[string]any) map[string]any {
			goal, _ := args["goal"].(string)
			if strings.TrimSpace(goal) == "" {
				return map[string]any{"status": "error", "text": "a goal is needed"}
			}
			t := k.Tasks.Delegate(goal, "important")
			return map[string]any{"status": "accepted", "task": t.ID, "text": "I'm on it; the result will come as a note."}
		}
	}
	// (the Live API's own google_search beside them was tried, 2026-11-25: on a key without billing the
	// session's setup itself is refused, "exceeded your current quota": her voice would not start)
	return dialogue.Tools{Decls: []*genai.Tool{{FunctionDeclarations: decls}}, Funcs: funcs}
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
			if kind == "floor" && floor != nil {
				to, _ := e["to"].(string)
				floor(to)
			}
			if kind == "interim" || kind == "said" || kind == "" {
				continue
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
