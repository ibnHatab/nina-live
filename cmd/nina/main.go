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
	"github.com/ibnHatab/nina-live/core"
	nlog "github.com/ibnHatab/nina-live/log"
	"github.com/ibnHatab/nina-live/tasks"
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
	Dialogue string            `yaml:"dialogue"`
	Levels   map[string]string `yaml:"levels"`
	Tasks    struct {
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
	} `yaml:"tasks"`
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

	cc := core.Config{Log: L, Root: root, Metisc: expand(c.Home.Metisc), Packages: c.Home.Packages, Composed: c.Home.Composed,
		Device: hass.NewDevice(ha, m), Code: code(), CheckpointEvery: c.Home.CheckpointEvery, CallTimeout: c.Home.CallTimeout,
		Levels: c.Levels, Now: now}
	if c.Tasks.Enabled {
		charter := taskCharter
		if c.Tasks.Charter != "" {
			if b, err := os.ReadFile(expand(c.Tasks.Charter)); err == nil {
				charter = string(b)
			}
		}
		cc.Tasks = &tasks.Config{Provider: c.Tasks.Provider, Model: c.Tasks.Model, KeyFile: c.Tasks.Key, System: charter,
			MaxTurns: c.Tasks.MaxTurns, Timeout: c.Tasks.Timeout,
			Mech: tasks.Mechanisms{Transform: on(c.Tasks.Transform), Steer: on(c.Tasks.Steer), FollowUp: on(c.Tasks.FollowUp)}}
	}
	t0 := time.Now()
	k, err := core.Open(ctx, cc)
	if err != nil {
		stdlog.Fatal(err)
	}
	if k.Tasks != nil {
		k.Tasks.AddTool(k.Recall())
	}
	stdlog.Printf("the world rebuilt from %d events (%d checkpoints checked, %d missed, genesis %v); %s backend, composed %v, tasks %v; %.2fs",
		k.Report.Events, k.Report.Checked, len(k.Report.Mismatch), k.Report.Genesis, k.Home.Backend(), c.Home.Composed,
		k.Tasks != nil, time.Since(t0).Seconds())

	// her voice: the dialogue, its preferences from the log, the core's tools (and delegate)
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
	d, err := dialogue.New(ctx, dcfg, dialogue.NewEvents(io.Discard), voiceTools(k))
	if err != nil {
		stdlog.Fatal(err)
	}
	d.SavePrefs = func(p dialogue.Prefs) error {
		_, err := L.Append(now(), "prefs", "set", "", "", p, 1)
		return err
	}
	k.SetVoice(d)
	go logDialogue(ctx, d, L)
	if err := d.StartPlugins(ctx); err != nil {
		stdlog.Fatal(err)
	}
	go func() {
		if err := ha.Follow(ctx, k.World.Apply, now); err != nil {
			stdlog.Printf("[hass] %v: the world gets no readings", err)
		}
	}()
	go k.Run(ctx, c.Home.Beat)
	L.Append(now(), "sys", "start", "", "", map[string]any{"code": code(), "replay": k.Report}, 1)
	if err := d.Run(ctx); err != nil {
		stdlog.Printf("the dialogue: %v", err)
	}
	L.Append(now(), "sys", "stop", "", "", nil, 1)
}

// voiceTools are Gemini Live's: the home's cards and, with tasks, delegate.
func voiceTools(k *core.Core) dialogue.Tools {
	var decls []*genai.FunctionDeclaration
	funcs := map[string]dialogue.ToolFunc{}
	for _, t := range k.HomeTools() {
		t := t
		decls = append(decls, &genai.FunctionDeclaration{Name: t.Name, Description: t.Description, ParametersJsonSchema: t.Parameters})
		funcs[t.Name] = func(ctx context.Context, args map[string]any) map[string]any {
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
	return dialogue.Tools{Decls: []*genai.Tool{{FunctionDeclarations: decls}}, Funcs: funcs}
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
