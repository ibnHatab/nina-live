// nina: Nina in one process (milestone 1, ARCHITECTURE.md §7): her voice (fsttm-live's
// pkg/dialogue), the home's rules (metis-agent's pkg/metishome) on the semantic world, the world
// rebuilt from her event log (world), Home Assistant as her tool (acasa's hass).
//
//	nina -config ~/repo/acasa/live/nina.yaml
//
// Every change to the world is an event in the log first; the dialogue's events are logged too,
// and what was said and heard is indexed for recall. pi (System 2) comes in milestone 3.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	stdlog "log"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ibnHatab/acasa/hass"
	"github.com/ibnHatab/fsttm-live/pkg/dialogue"
	nlog "github.com/ibnHatab/nina-live/log"
	"github.com/ibnHatab/nina-live/world"
	"github.com/metis-lang-dev/metis-agent/go/pkg/metishome"
	semhome "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/home"
	sw "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/semworld"
	"google.golang.org/genai"
	"gopkg.in/yaml.v3"
)

// Config is nina.yaml: where her memory is, the home's rules, Home Assistant, the dialogue's own
// config (fsttm-live's YAML: sound, models, attention, preferences, plugins).
type Config struct {
	Log  string `yaml:"log"`
	Home struct {
		Root            string        `yaml:"root"`
		Metisc          string        `yaml:"metisc"`
		Packages        []string      `yaml:"packages"`
		Beat            time.Duration `yaml:"beat"`
		CheckpointEvery int           `yaml:"checkpoint_every"`
		CallTimeout     time.Duration `yaml:"call_timeout"`
		Composed        bool          `yaml:"composed"` // the packages as one composition under the director, on the tick
	} `yaml:"home"`
	Hass struct {
		URL   string `yaml:"url"`
		Token string `yaml:"token"`
	} `yaml:"hass"`
	Dialogue string            `yaml:"dialogue"`
	Levels   map[string]string `yaml:"levels"` // a reasoning kind → context | info | important | urgent | drop
}

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[2:])
	}
	return p
}

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
	if c.Home.Beat == 0 {
		c.Home.Beat = time.Second
	}
	if c.Home.CallTimeout == 0 {
		c.Home.CallTimeout = 6 * time.Second
	}
	if len(c.Home.Packages) == 0 {
		c.Home.Packages = metishome.Packages
	}
	if c.Levels == nil {
		c.Levels = map[string]string{"result": "context", "event": "info", "surprise": "info", "error": "context"}
	}
	root := expand(c.Home.Root)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// her memory, and the world rebuilt from it
	if err := os.MkdirAll(filepath.Dir(expand(c.Log)), 0o755); err != nil {
		stdlog.Fatal(err)
	}
	L, err := nlog.Open(expand(c.Log))
	if err != nil {
		stdlog.Fatal(err)
	}
	defer L.Close()
	var members []world.Member
	for _, p := range c.Home.Packages {
		members = append(members, world.Member{Name: p, Manifest: filepath.Join(root, "domains", p, "domain.yaml")})
	}
	t0 := time.Now()
	W, rep, err := world.Open(ctx, L, world.Config{Map: filepath.Join(root, "world", "home.map.json"),
		Members: members, Code: code(), Every: c.Home.CheckpointEvery, Now: now})
	if err != nil {
		stdlog.Fatalf("the world: %v", err)
	}
	stdlog.Printf("the world rebuilt from %d events in %.2fs (%d checkpoints checked, %d missed, genesis %v)",
		rep.Events, time.Since(t0).Seconds(), rep.Checked, len(rep.Mismatch), rep.Genesis)
	if len(rep.Mismatch) > 0 {
		L.Append(now(), "sys", "replay_mismatch", "", "", rep, 1)
	}

	// Home Assistant: the feed and the device
	m, err := semhome.LoadMap(filepath.Join(root, "world", "home.map.json"))
	if err != nil {
		stdlog.Fatal(err)
	}
	ha, err := hass.New(c.Hass.URL, c.Hass.Token)
	if err != nil {
		stdlog.Fatal(err)
	}
	dev := hass.NewDevice(ha, m)

	// her voice: the dialogue, its preferences from the log
	dcfg, err := dialogue.LoadConfig(expand(c.Dialogue))
	if err != nil {
		stdlog.Fatal(err)
	}
	L.Read(ctx, 0, func(e nlog.Event) error {
		var p dialogue.Prefs
		if e.Kind == "set" && json.Unmarshal(e.Data, &p) == nil {
			dcfg.Prefs = p
		}
		return nil
	}, "prefs")
	ev := dialogue.NewEvents(io.Discard)

	// the home's rules, on the world through the log
	var d *dialogue.Dialogue
	var dmu sync.Mutex
	voice := func() *dialogue.Dialogue { dmu.Lock(); defer dmu.Unlock(); return d }
	home, err := metishome.Open(metishome.Options{Root: root, Packages: c.Home.Packages, Metisc: expand(c.Home.Metisc),
		Sem: W.Home, Apply: W.Apply, Device: dev, Composed: c.Home.Composed,
		OnTick: func(kind string, f map[string]any) {
			if kind == "tick-director" || kind == "tick-relay" || kind == "tick-card" || kind == "tick-said" {
				L.Append(now(), "tick", kind, "", "", f, 1)
			}
		},
		OnReasoning: func(r metishome.Reasoning) {
			L.Append(now(), "reasoning", r.Kind, r.Domain, "", map[string]string{"line": r.Line, "status": r.Status, "text": r.Text}, 1)
			if v := voice(); v != nil {
				route(v, c.Levels, r)
			}
		}})
	if err != nil {
		stdlog.Fatal(err)
	}
	stdlog.Printf("%d packages served (home: %s backend, composed %v)", len(c.Home.Packages), home.Backend(), c.Home.Composed)

	dd, err := dialogue.New(ctx, dcfg, ev, homeTools(home, c.Home.CallTimeout))
	if err != nil {
		stdlog.Fatal(err)
	}
	dmu.Lock()
	d = dd
	dmu.Unlock()
	d.SavePrefs = func(p dialogue.Prefs) error {
		_, err := L.Append(now(), "prefs", "set", "", "", p, 1)
		return err
	}
	go logDialogue(ctx, d, L)
	proj := &projector{said: map[string]string{}}
	W.OnOut = func(e semhome.Event, out semhome.Out) {
		if e.Kind == "settle" {
			proj.step(d, out.Projected)
		}
	}
	if err := d.StartPlugins(ctx); err != nil {
		stdlog.Fatal(err)
	}
	go func() {
		if err := ha.Follow(ctx, W.Apply, now); err != nil {
			stdlog.Printf("[hass] %v: the world gets no readings", err)
		}
	}()
	go func() {
		tk := time.NewTicker(c.Home.Beat)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				home.Beat()
			}
		}
	}()
	L.Append(now(), "sys", "start", "", "", map[string]any{"code": code(), "replay": rep}, 1)
	if err := d.Run(ctx); err != nil {
		stdlog.Printf("the dialogue: %v", err)
	}
	W.Checkpoint(now())
	L.Append(now(), "sys", "stop", "", "", nil, 1)
}

// homeTools are the home's packages as the voice session's tools.
func homeTools(home *metishome.Service, timeout time.Duration) dialogue.Tools {
	var decls []*genai.FunctionDeclaration
	funcs := map[string]dialogue.ToolFunc{}
	for _, t := range home.Tools() {
		name := t.Function.Name
		decls = append(decls, &genai.FunctionDeclaration{Name: name, Description: t.Function.Description,
			ParametersJsonSchema: t.Function.Parameters})
		funcs[name] = func(ctx context.Context, args map[string]any) map[string]any {
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			o := home.Call(cctx, name, args)
			return map[string]any{"status": o.Status, "text": o.Text}
		}
	}
	return dialogue.Tools{Decls: []*genai.Tool{{FunctionDeclarations: decls}}, Funcs: funcs}
}

// route: a reasoning line by its kind's level: context (she knows it, says nothing), a NOTE
// (info, important, urgent: the floor and the knob decide), or dropped.
func route(d *dialogue.Dialogue, levels map[string]string, r metishome.Reasoning) {
	lv := levels[r.Kind]
	switch lv {
	case "drop":
		return
	case "", "context":
		d.Context("", r.Line)
	default:
		if d.Note(lv, r.Line) != nil {
			d.Context("", r.Line)
		}
	}
}

// logDialogue writes the dialogue's events to the log (stream dialog) and indexes what was
// heard and said for recall.
func logDialogue(ctx context.Context, d *dialogue.Dialogue, L *nlog.Log) {
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

// projector gives the voice session the world's lines when their words change (the age coarse,
// so a line is said again a few times an hour, not every minute).
type projector struct {
	mu   sync.Mutex
	said map[string]string
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

func (p *projector) step(d *dialogue.Dialogue, lines []sw.Line) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, l := range lines {
		text := ages.ReplaceAllString(l.Text, ageWord(l.Age))
		k := l.Stream + "\x00" + l.Key
		if p.said[k] == text {
			continue
		}
		p.said[k] = text
		key := l.Key
		if l.Stream == "env" && key != "" {
			text = key + ": " + text
		}
		d.Context(key, text)
	}
}
