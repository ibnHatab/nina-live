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
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ibnHatab/nina-live/agenda"
	"github.com/ibnHatab/nina-live/glow"
	"github.com/ibnHatab/nina-live/habits"
	nlog "github.com/ibnHatab/nina-live/log"
	"github.com/ibnHatab/nina-live/music"
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
	Log             *nlog.Log
	Root            string // the catalog (nina-live/catalog)
	Metisc          string
	Packages        []string // default metishome.Packages
	Composed        bool
	Device          metishome.Device
	Code            string // the build, for the world's checkpoints
	CheckpointEvery int
	CallTimeout     time.Duration
	Levels          map[string]string // a rule's reply kind → context | info | important | urgent | drop
	Tasks           *tasks.Config     // nil: no System 2 (its Tools, World, Log, Note and Now are filled here)
	Timers          bool              // the timers package (catalog/domains/timers), its host rebuilt from the log (always when Composed: the composition has it)
	Agenda          *agenda.Host      // the agenda package's host (writes through Home Assistant); nil when Composed: one that writes nothing
	Character       string            // the home's character (default Root/character.yaml): the taste's priors, loaded when it changes
	Glow            *glow.Host        // the glow package's host (the bedroom light through Alexa); nil when Composed: one that reaches nothing
	Music           *music.Host       // the music package's host (MPD through Home Assistant), its taste rebuilt from the log; nil when Composed: one that plays nothing
	Now             func() float64
}

type Core struct {
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
	Report world.Report

	mu    sync.Mutex
	voice Voice
	port  *port.Port // the world's lines to the voice and the tasks: on a new value or confidence level
	// Observed: the integration layer's lines now (Home Assistant's weather, lists, calendars: acasa's
	// hass.Observer), with the world's before each task request
	Observed func() []string
}

// Open builds the core: the world rebuilt from the log, the home's rules on it, the tasks.
func Open(ctx context.Context, c Config) (*Core, error) {
	if c.Now == nil {
		c.Now = func() float64 { return float64(time.Now().UnixNano()) / 1e9 }
	}
	if len(c.Packages) == 0 {
		c.Packages = metishome.Packages
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
		fresh.Player, fresh.Call, fresh.Search, fresh.Now, fresh.KeepAfter, fresh.SkipWithin = mh.Player, mh.Call, mh.Search, mh.Now, mh.KeepAfter, mh.SkipWithin
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
	if hosts != nil {
		k.Cfg.Packages = c.Packages
	}
	var members []world.Member
	for _, p := range c.Packages {
		members = append(members, world.Member{Name: p, Manifest: filepath.Join(c.Root, "domains", p, "domain.yaml")})
	}
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
		Sem: k.World.Home, Apply: k.World.Apply, Device: c.Device, Composed: c.Composed, Records: "off", Hosts: hosts,
		OnReasoning: k.reasoning, OnTick: k.tick})
	if err != nil {
		return nil, err
	}
	if err := k.loadCharacter(ctx); err != nil {
		return nil, err
	}
	k.World.OnOut = func(e semhome.Event, out semhome.Out) {
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
	}
	return k, nil
}

// timersContext gives the voice what is set (its silent context, keyed "timers").
func (k *Core) timersContext() {
	if v := k.Voice(); v != nil && k.Timers != nil {
		v.Context("timers", k.Timers.Summary())
	}
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
func (k *Core) SetVoice(v Voice) {
	k.mu.Lock()
	k.voice = v
	k.mu.Unlock()
	k.timersContext()
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
	for {
		select {
		case <-ctx.Done():
			k.World.Checkpoint(k.Cfg.Now())
			k.World.Snapshot(k.Cfg.Now()) // the next start restores it; the world events before it go
			return
		case <-tk.C:
			k.Home.Beat()
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
	case "tick-director", "tick-relay", "tick-card", "tick-said":
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
	o := k.Home.Call(ctx, tool, args)
	if o.Status == "done" && tool != "home_habits" {
		hc := habits.Call{Tool: tool, Args: args, At: k.Cfg.Now()}
		k.Log.Append(hc.At, "call", strings.TrimPrefix(tool, "home_"), "", "", hc, 1)
		if k.Habits != nil {
			k.Habits.Add(hc)
		}
	}
	return o
}

// HomeTools are the home's cards as a task's tools.
func (k *Core) HomeTools() []tasks.Tool {
	var out []tasks.Tool
	for _, t := range k.Home.Tools() {
		name := t.Function.Name
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
