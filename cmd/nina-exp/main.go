// nina-exp: experiments with pi's Steer, FollowUp and TransformContext on Nina's composed home
// (milestone 3; the user: "experiment on Steer and FollowUp and TransformContext to make the
// system flexible and natural"). Never the real home: a semantic world fed a simulated Home
// Assistant (the units and the lights as states), a device that obeys a command after a delay
// (the Prana's), the home's composition on its tick, and a pi task per run.
//
//	./build.sh exp -scenarios all -conditions none,transform,signals,all
//	./build.sh exp -provider google -model gemini-3-flash-preview -key ~/.config/fsttm/gemini.key
//
// The model is any of pi-ai's (provider and id from its catalog); by default Gemini Flash through
// OpenRouter, paid per token: the free AI Studio tier's daily quota ends a grid after six runs.
//
// Each run is one scenario under one condition (which of the three mechanisms is on); its record
// (the result, the turns, the calls, what was steered and followed, the time) is a line of -out.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ibnHatab/nina-live/agenda"
	"github.com/ibnHatab/nina-live/music"
	"github.com/ibnHatab/nina-live/port"
	"github.com/ibnHatab/nina-live/tasks"
	"github.com/ibnHatab/nina-live/timers"
	"github.com/metis-lang-dev/metis-agent/go/pkg/metishome"
	semhome "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/home"
	sw "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/semworld"
)

const (
	defProvider = "openrouter"
	defModel    = "deepseek/deepseek-v4.1-flash" // the user's choice (2026-11-24): like Gemini 3.8 Flash, a fraction of the cost
	defKey      = "~/.config/metis/openrouter.env"
)

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

// home is one simulated home: its world, its device, its rules, a beat.
type home struct {
	mu     sync.Mutex
	w      *semhome.Home
	m      *semhome.Map
	svc    *metishome.Service
	delay  time.Duration
	state  map[string]string // entity → state (the simulated Home Assistant)
	preset map[string]string
	r      *tasks.Runner
	lines  []string   // the rules' lines, in order
	port   *port.Port // the world's lines to the task: core's quantized port
}

func (h *home) apply(ev semhome.Event) (semhome.Out, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.w.Apply(ev)
}

func (h *home) ha(entity, state, preset string) {
	attrs := map[string]any{}
	if preset != "" {
		attrs["preset_mode"] = preset
	}
	h.state[entity], h.preset[entity] = state, preset
	h.apply(semhome.Event{At: now(), Kind: "ha_state", HA: &semhome.HAState{EntityID: entity, State: state, Attributes: attrs}})
}

// Do is the simulated device: the command obeyed after the delay, as a state the feed reports.
func (h *home) Do(kind, role, op string, value any) bool {
	go func() {
		time.Sleep(h.delay)
		switch kind {
		case "unit":
			p := h.m.Units[role].Prefix
			for _, f := range []string{"supply", "extract"} {
				e := fmt.Sprintf("fan.%s_%s_fan", p, f)
				st, pr := h.state[e], h.preset[e]
				switch {
				case op == "on":
					st = "on"
				case op == "off":
					st, pr = "off", ""
				case op == "preset":
					if v, _ := value.(string); v == "normal" {
						pr = ""
					} else {
						pr, st = v, "on"
					}
				}
				h.ha(e, st, pr)
			}
		case "light":
			if e := h.m.Lights[role].Entity; e != nil {
				h.ha(*e, map[string]string{"on": "on", "off": "off"}[op], "")
			}
		}
	}()
	return true
}

// changed tells the running task the world lines whose words changed (core.project's, here).
func (h *home) changed() {
	h.mu.Lock()
	ls := h.w.Lines(now())
	if h.port == nil {
		h.port = port.New(port.Default)
	}
	var ch []string
	for _, l := range ls {
		if l.Stream != "env" || l.Key == "" {
			continue
		}
		if changed, _ := h.port.Step(l); changed { // core's quantized port
			ch = append(ch, ages.ReplaceAllString(l.Text, "recently"))
		}
	}
	h.mu.Unlock()
	if h.r != nil {
		for _, c := range ch {
			h.r.World(c)
		}
	}
}

var ages = regexp.MustCompile(`just now|\d+ (min|h|d) ago`)

func (h *home) world() []string {
	h.mu.Lock()
	ls := h.w.Lines(now())
	h.mu.Unlock()
	var out []string
	for _, l := range ls {
		if l.Stream == "env" {
			out = append(out, ages.ReplaceAllString(l.Text, "recently"))
		}
	}
	return out
}

func newHome(root string, delay time.Duration) (*home, error) {
	m, err := semhome.LoadMap(filepath.Join(root, "world", "home.map.json"))
	if err != nil {
		return nil, err
	}
	h := &home{w: semhome.New(m, semhome.DefaultParams), m: m, delay: delay, state: map[string]string{}, preset: map[string]string{}}
	for _, p := range metishome.Packages {
		man, err := sw.ReadManifest(filepath.Join(root, "domains", p, "domain.yaml"), p)
		if err == nil && (len(man.Beliefs) > 0 || len(man.Levels) > 0) {
			h.w.B.Join(man)
		}
	}
	for _, u := range []string{"u1", "u2"} {
		p := m.Units[u].Prefix
		for _, f := range []string{"supply", "extract"} {
			h.ha(fmt.Sprintf("fan.%s_%s_fan", p, f), "off", "")
		}
		h.ha(fmt.Sprintf("switch.%s_heater", p), "off", "")
		h.ha(fmt.Sprintf("switch.%s_winter", p), "off", "")
	}
	h.ha("sun.sun", "below_horizon", "")
	return h, nil
}

type scenario struct {
	Name, Goal string
	During     func(h *home, t *tasks.Task) // a person's change mid-run (nil: none)
	Check      func(result string, h *home) []string
}

func has(s string, words ...string) bool {
	s = strings.ToLower(s)
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

var scenarios = []scenario{
	{Name: "state", Goal: "Tell me which ventilation units are running now. If the lab one is off, start it in night mode, then say what you did.",
		Check: func(res string, h *home) []string {
			var miss []string
			if !has(res, "night") {
				miss = append(miss, "says night mode")
			}
			if h.preset["fan.lab_supply_fan"] != "night" {
				miss = append(miss, "the lab unit in night mode")
			}
			return miss
		}},
	{Name: "scene", Goal: "Set the home to night mode, then tell me what each part of the home (the lights, the ventilation, the TV) did about it.",
		Check: func(res string, h *home) []string {
			var miss []string
			for _, w := range []string{"light", "ventilation", "tv"} {
				if !has(res, w) {
					miss = append(miss, "reports "+w)
				}
			}
			return miss
		}},
	{Name: "change", Goal: "Start the bedroom ventilation and boost it for a while, then tell me how the bedroom ventilation is now.",
		During: func(h *home, t *tasks.Task) {
			// once the start has been obeyed, someone switches the bedroom unit off at its panel: mid-run
			for i := 0; i < 120 && h.state["fan.bedroom_badriom_supply_fan"] != "on"; i++ {
				time.Sleep(100 * time.Millisecond)
			}
			time.Sleep(300 * time.Millisecond)
			for _, f := range []string{"supply", "extract"} {
				h.ha("fan.bedroom_badriom_"+f+"_fan", "off", "")
			}
		},
		Check: func(res string, h *home) []string {
			// the answer must agree with the home at the end
			if h.state["fan.bedroom_badriom_supply_fan"] == "off" {
				if !has(res, "off", "stopped", "not running") {
					return []string{"reports the person's change (off)"}
				}
			} else if !has(res, "boost", "running") {
				return []string{"reports it running"}
			}
			return nil
		}},
}

var conditions = map[string]tasks.Mechanisms{
	"none":      {},
	"transform": {Transform: true},
	"signals":   {Steer: true, FollowUp: true},
	"all":       tasks.All,
}

type record struct {
	Scenario, Condition string
	Model               string
	Result              string
	Missing             []string
	Turns, Calls        int
	Steered, Followed   int
	Seconds             float64
	Err                 string `json:",omitempty"`
}

const system = `You are Nina's task runner: a goal a person gave Nina, carried out with the home's tools, then answered in two or three short sentences the voice will say.
- The home's tools ask the home's rules; a refusal says why: believe it.
- A message beginning "HOME" is what the home's systems report: beliefs and outcomes; use them, never read them out as such.
- Say only what the tools and HOME tell you; say what you did and how things are now.`

func main() {
	root := flag.String("root", "catalog", "the home's catalog (nina-live/catalog)")
	metisc := flag.String("metisc", os.Getenv("METISC"), "metis-lang's metisc")
	provider := flag.String("provider", defProvider, "pi-ai's provider")
	model := flag.String("model", defModel, "the model, by its id in pi-ai's catalog")
	key := flag.String("key", defKey, "the key: a bare key's file, or an env file (OPENROUTER_API_KEY=...)")
	which := flag.String("scenarios", "all", "scenario names, comma-separated, or all")
	conds := flag.String("conditions", "none,transform,signals,all", "conditions, comma-separated")
	delay := flag.Duration("delay", 1500*time.Millisecond, "how long the device takes to obey")
	out := flag.String("out", "runs/exp.jsonl", "the records")
	flag.Parse()
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatal(err)
	}
	f, err := os.OpenFile(*out, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, sc := range scenarios {
		if *which != "all" && !strings.Contains(","+*which+",", ","+sc.Name+",") {
			continue
		}
		for _, cn := range strings.Split(*conds, ",") {
			mech, ok := conditions[cn]
			if !ok {
				log.Fatalf("no condition %q", cn)
			}
			rec := run(*root, *metisc, *provider, *model, *key, *delay, sc, cn, mech)
			enc.Encode(rec)
			fmt.Printf("%-7s %-9s turns %d calls %d steered %d followed %d %5.1fs missing %v\n   %s\n", rec.Scenario, rec.Condition,
				rec.Turns, rec.Calls, rec.Steered, rec.Followed, rec.Seconds, rec.Missing, rec.Result)
		}
	}
}

func run(root, metisc, provider, model, key string, delay time.Duration, sc scenario, cn string, mech tasks.Mechanisms) record {
	rec := record{Scenario: sc.Name, Condition: cn, Model: provider + "/" + model}
	h, err := newHome(root, delay)
	if err != nil {
		rec.Err = err.Error()
		return rec
	}
	tm := timers.New(now, nil, nil)
	svc, err := metishome.Open(metishome.Options{Root: root, Metisc: metisc, Records: "off", Composed: true,
		Packages: append(slices.Clone(metishome.Packages), "timers", "agenda", "music"),
		Hosts: map[string]metishome.HostSpec{"timers": {New: func() metishome.Host { return tm }, Outcomes: timers.Outcomes},
			"agenda": {New: func() metishome.Host { return &agenda.Host{Entities: agenda.Default} }, Outcomes: agenda.Outcomes},
			"music":  {New: func() metishome.Host { return music.New(nil) }, Outcomes: music.Outcomes}},
		Sem: h.w, Apply: h.apply, Device: h,
		OnReasoning: func(r metishome.Reasoning) {
			h.lines = append(h.lines, r.Line)
			if h.r != nil {
				h.r.Reasoning(r.Domain, r.Kind, r.Line)
			}
		},
		OnTick: func(kind string, f map[string]any) {
			if kind == "tick-relay" && f["what"] == "goal" && h.r != nil {
				h.r.Routed("scenes", fmt.Sprint(f["domain"]))
			}
		}})
	if err != nil {
		rec.Err = err.Error()
		return rec
	}
	h.svc = svc
	stop := make(chan struct{})
	go func() {
		tk := time.NewTicker(time.Second)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				svc.Beat()
				h.changed()
			}
		}
	}()
	defer close(stop)
	time.Sleep(2 * time.Second) // the world's first beats
	var tools []tasks.Tool
	for _, t := range svc.Tools() {
		name := t.Function.Name
		tools = append(tools, tasks.Tool{Name: name, Description: t.Function.Description, Parameters: t.Function.Parameters,
			Domain: strings.TrimPrefix(name, "home_"),
			Call: func(ctx context.Context, args map[string]any) (string, string) {
				c, cancel := context.WithTimeout(ctx, 6*time.Second)
				defer cancel()
				o := svc.Call(c, name, args)
				return o.Status, o.Text
			}})
	}
	r, err := tasks.New(tasks.Config{Provider: provider, Model: model, KeyFile: key, System: system, Tools: tools,
		World: h.world, Mech: mech, MaxTurns: 10, Timeout: 4 * time.Minute})
	if err != nil {
		rec.Err = err.Error()
		return rec
	}
	h.r = r
	t0 := time.Now()
	t := r.Delegate(sc.Goal, "important")
	if sc.During != nil {
		go sc.During(h, t)
	}
	<-t.Done
	rec.Seconds = time.Since(t0).Seconds()
	rec.Result, rec.Turns, rec.Calls, rec.Steered, rec.Followed = t.Result, t.Turns, t.Calls, t.Steered, t.Followed
	if t.Err != nil {
		rec.Err = t.Err.Error()
	}
	time.Sleep(delay + time.Second) // the last command obeyed, for the check
	rec.Missing = sc.Check(t.Result, h)
	if os.Getenv("NINA_EXP_DEBUG") != "" {
		for _, m := range t.Agent.State().Messages {
			b, _ := json.Marshal(m)
			fmt.Fprintf(os.Stderr, "  msg %.300s\n", b)
		}
		fmt.Fprintf(os.Stderr, "  world at the end: %v\n  rules' lines: %v\n", h.world(), h.lines)
	}
	return rec
}
