// Package tasks is Nina's System 2 (ARCHITECTURE.md §3, milestone 3): a goal delegated by the voice
// (Gemini Live's delegate tool), or put out by a rule, runs as a pi agent (sky-valley/pi) with the
// home's cards and recall as its tools. Metis's second loop reaches a running task through pi's
// own mechanisms, none of them holding policy (§3.3):
//
//   - TransformContext (read-only): before each request the world's current lines are put in
//     front of the transcript, replaced every time, so nothing piles up;
//   - Steer: a new conclusion of a package the task has used, delivered after the current tool
//     batch, before the next request;
//   - FollowUp: an outcome that comes after the run would have ended (a call answered "pending",
//     its outcome on a later beat): the run goes on with it.
//
// Each is a switch (Mechanisms), for the experiments. Every message of a run, its tool calls and
// its end are events in the log (stream task), and the result is a NOTE for the voice.
package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	nlog "github.com/ibnHatab/nina-live/log"
	"github.com/sky-valley/pi/agent"
	"github.com/sky-valley/pi/ai"
	_ "github.com/sky-valley/pi/ai/providers" // the providers register themselves
)

const Stream = "task"

// Mechanisms: which of pi's channels carry Metis into a run.
type Mechanisms struct {
	Transform bool // the world's lines before each request
	Steer     bool // the used packages' new conclusions, mid-run
	FollowUp  bool // late outcomes, after the run would end
}

// All is every mechanism on.
var All = Mechanisms{true, true, true}

// Tool is one of the home's tools as a task uses it: its card (name, description, JSON schema) and
// its call (the rules' outcome: status and text).
type Tool struct {
	Name, Description string
	Parameters        map[string]any
	Call              func(ctx context.Context, args map[string]any) (status, text string)
	Domain            string // the package it reaches (its conclusions are steered into the task)
}

// Fallback is another model a one-shot Ask turns to when the ones before it fail (a provider's
// overload: Google's "high demand", the user's quota).
type Fallback struct {
	Provider, Model, KeyFile string
	ModelValue               *ai.Model // instead of Provider/Model (a test's faux model)
}

type Config struct {
	Provider, Model string     // google, gemini-3.5-flash
	Fallbacks       []Fallback // Ask's: tried in order after the model, each once, within AskTry
	ModelValue      *ai.Model  // instead of Provider/Model (a test's faux model)
	KeyFile         string
	// Strain hears the primary model's outcome on each request (nil: it answered): its API errors
	// are what they say about her (core.Strain: busy, tired, offline)
	Strain   func(err error)
	System   string
	Tools    []Tool
	World    func() []string          // the world's lines now (TransformContext)
	Log      *nlog.Log                // nil: nothing logged
	Note     func(level, text string) // the result to the voice
	Now      func() float64
	Mech     Mechanisms
	MaxTurns int           // a run's turns before it is stopped (0: 12)
	AskTry   time.Duration // Ask: one model's time before the next is tried (0: 25 s; the last gets what is left)
	Timeout  time.Duration
	// Idle: a model attempt that sends nothing this long (no tool running) is given up and the task
	// handed to the next model (0: 45 s). pi gives Google requests no timeout of their own: a hung
	// Gemini call had held a WhatsApp answer for the task's whole 3 minutes (2026-11-26).
	Idle time.Duration
}

// Runner holds the running tasks.
type Runner struct {
	cfg   Config
	key   string
	model *ai.Model
	alts  []alt                // the fallbacks: Ask's, and a task's when its model is overloaded
	hot   map[string]time.Time // a model that answered "overloaded": skipped until then (5 min)
	keys  map[string]string    // the API keys by provider
	mu    sync.Mutex
	n     int
	live  map[string]*Task
}

// Task is one run.
type Task struct {
	ID, Goal, Level   string
	Agent             *agent.Agent
	used              map[string]bool // the packages it called
	pending           map[string]bool // its calls answered pending, by package
	named             map[string]bool // what its calls named (their string arguments, lower case)
	refusedOn         map[string]bool // … in a call the rules refused (a unit not yet confirmed: worth a retry)
	Done              chan struct{}
	start             int // the chain's model it began with (pick)
	Result            string
	Err               error
	Turns, Calls      int
	Steered, Followed int
	stopped           bool         // at MaxTurns (its abort is not a provider's)
	last              atomic.Int64 // the last agent event (unix ns): the watchdog's
	tooling           atomic.Int32 // tools running: their time is not the model's silence
}

func New(c Config) (*Runner, error) {
	m := c.ModelValue
	if m == nil {
		m = ai.GetModel(c.Provider, c.Model)
	}
	if m == nil {
		return nil, fmt.Errorf("tasks: no model %s/%s in pi's catalog", c.Provider, c.Model)
	}
	key, err := Key(m.Provider, c.KeyFile)
	if err != nil {
		return nil, err
	}
	if c.MaxTurns <= 0 {
		c.MaxTurns = 12
	}
	if c.Timeout <= 0 {
		c.Timeout = 3 * time.Minute
	}
	if c.Now == nil {
		c.Now = func() float64 { return float64(time.Now().UnixNano()) / 1e9 }
	}
	r := &Runner{cfg: c, key: key, model: m, live: map[string]*Task{}, keys: map[string]string{string(m.Provider): key}}
	for _, f := range c.Fallbacks {
		fm := f.ModelValue
		if fm == nil {
			fm = ai.GetModel(f.Provider, f.Model)
		}
		if fm == nil {
			return nil, fmt.Errorf("tasks: no fallback model %s/%s in pi's catalog", f.Provider, f.Model)
		}
		fk, err := Key(fm.Provider, f.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("tasks: fallback %s/%s: %w", f.Provider, f.Model, err)
		}
		r.alts = append(r.alts, alt{fm, fk})
		if _, ok := r.keys[string(fm.Provider)]; !ok {
			r.keys[string(fm.Provider)] = fk
		}
	}
	if r.cfg.AskTry <= 0 {
		r.cfg.AskTry = 25 * time.Second
	}
	return r, nil
}

type alt struct {
	model *ai.Model
	key   string
}

func (r *Runner) log(kind, key string, data any) {
	if r.cfg.Log != nil {
		r.cfg.Log.Append(r.cfg.Now(), Stream, kind, key, "", data, 1)
	}
}

func user(text string) ai.UserMessage {
	return ai.UserMessage{Content: ai.ContentList{ai.TextContent{Text: text}}, Timestamp: time.Now().UnixMilli()}
}

// Quiet is the level of a task whose result is its caller's (a WhatsApp answer): no NOTE.
const Quiet = "quiet"

// Delegate starts a run for the goal → its id at once; the result comes as a NOTE at the level.
func (r *Runner) Delegate(goal, level string) *Task {
	if level == "" {
		level = "important"
	}
	r.mu.Lock()
	r.n++
	t := &Task{ID: fmt.Sprintf("task%d", r.n), Goal: goal, Level: level, used: map[string]bool{}, named: map[string]bool{}, refusedOn: map[string]bool{},
		pending: map[string]bool{}, Done: make(chan struct{})}
	r.live[t.ID] = t
	r.mu.Unlock()
	t.start = r.pick()
	r.agent(t, r.chain()[t.start].model)
	r.log("created", t.ID, map[string]any{"goal": goal, "level": level, "mech": r.cfg.Mech, "model": r.chain()[t.start].model.ID})
	go r.run(t)
	return t
}

// chain: the model, then its fallbacks.
func (r *Runner) chain() []alt { return append([]alt{{r.model, r.key}}, r.alts...) }

// hotFor: how long a model that answered "overloaded" is passed over (Google's "high demand" lasts
// minutes: each task had spent ~2 min on it before its fallback, 2026-11-26, past a WhatsApp answer's time).
const hotFor = 5 * time.Minute

// pick: the first model of the chain not overloaded lately (the first, when all are).
func (r *Runner) pick() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, a := range r.chain() {
		if time.Now().After(r.hot[modelKey(a.model)]) {
			return i
		}
	}
	return 0
}

func (r *Runner) overheat(m *ai.Model) {
	r.mu.Lock()
	if r.hot == nil {
		r.hot = map[string]time.Time{}
	}
	r.hot[modelKey(m)] = time.Now().Add(hotFor)
	r.mu.Unlock()
}

func (r *Runner) isHot(m *ai.Model) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return time.Now().Before(r.hot[modelKey(m)])
}

func modelKey(m *ai.Model) string { return string(m.Provider) + "/" + m.ID }

// agent gives the task its agent (its model can change during the run: run's fallbacks).
func (r *Runner) agent(t *Task, m *ai.Model) {
	t.Agent = agent.NewAgent(agent.AgentOptions{
		InitialState: &agent.AgentState{SystemPrompt: r.cfg.System, Model: m, Tools: r.tools(t)},
		GetApiKey:    r.keyOf,
		// the free tier's 503 "high demand" is transient: pi backs off (Retry-After honoured); twice,
		// then the next model (2026-11-25: six retries took two of the task's three minutes)
		MaxRetries: 0, // 2026-11-26: none: an overloaded model hands over at once and is passed over for a while (pick)
		// …and a short wait for each (pi's default ceiling is a minute: two cost 146 s of a task's five)
		MaxRetryDelayMs: &retryDelayMs,
		TimeoutMs:       45000, // a model that does not answer in 45 s hands the task over (3.8 Flash took 175 s, 2026-11-25)
		// every conclusion queued is delivered at once (pi's default is one a turn: ten package lines would
		// be ten more requests; found by the core's integration test)
		SteeringMode: agent.QueueAll,
		FollowUpMode: agent.QueueAll,
		TransformContext: func(ctx context.Context, ms []agent.AgentMessage) []agent.AgentMessage {
			if !r.cfg.Mech.Transform || r.cfg.World == nil {
				return ms
			}
			lines := r.cfg.World()
			if len(lines) == 0 {
				return ms
			}
			now := user("HOME now (the home's systems' current beliefs: read, not said):\n" + strings.Join(lines, "\n"))
			return append([]agent.AgentMessage{now}, ms...)
		},
	})
	t.Agent.Subscribe(func(_ context.Context, e agent.AgentEvent) error {
		t.last.Store(time.Now().UnixNano())
		switch e.Type {
		case agent.EvToolExecutionStart:
			t.tooling.Add(1)
		case agent.EvToolExecutionEnd:
			t.tooling.Add(-1)
		}
		switch e.Type {
		case agent.EvTurnEnd: // the turns counted from the events, no hook: past MaxTurns the run is stopped
			t.Turns++
			if t.Turns >= r.cfg.MaxTurns {
				r.log("stopped", t.ID, map[string]any{"why": "max turns", "turns": t.Turns})
				t.stopped = true // its own limit: no other model for it
				go t.Agent.Abort()
			}
		case agent.EvMessageEnd:
			r.log("message", t.ID, map[string]any{"role": string(e.Message.MessageRole()), "text": textOf(e.Message)})
		case agent.EvToolExecutionEnd:
			r.log("tool", t.ID, map[string]any{"name": e.ToolName, "args": e.Args, "error": e.IsError, "result": resultText(e.Result)})
		}
		return nil
	})
}

var retryDelayMs = 5000

// handOver is a transcript for another model: the first request as it was, and what was done since
// as one plain message (a model's tool calls carry its own signatures, which the next one refuses:
// Gemini's "missing a thought_signature", 2026-11-25). The calls made stay made: none again.
func handOver(ms []agent.AgentMessage) []agent.AgentMessage {
	var first agent.AgentMessage
	var done []string
	for _, m := range ms {
		switch x := m.(type) {
		case ai.UserMessage, *ai.UserMessage:
			if first == nil {
				first = m
			} else if s := strings.TrimSpace(textOf(m)); s != "" {
				done = append(done, "told: "+s)
			}
		case ai.AssistantMessage, *ai.AssistantMessage:
			var cs ai.ContentList
			if a, ok := x.(ai.AssistantMessage); ok {
				cs = a.Content
			} else {
				cs = x.(*ai.AssistantMessage).Content
			}
			for _, c := range cs {
				switch v := c.(type) {
				case ai.TextContent:
					if s := strings.TrimSpace(v.Text); s != "" {
						done = append(done, "said: "+s)
					}
				case ai.ToolCall:
					b, _ := json.Marshal(v.Arguments)
					done = append(done, "called "+v.Name+" "+string(b))
				}
			}
		case ai.ToolResultMessage, *ai.ToolResultMessage:
			r := textOf(m)
			if len(r) > 1500 {
				r = r[:1500] + "…"
			}
			done = append(done, "  → "+r)
		}
	}
	if first == nil || len(done) == 0 {
		return ms
	}
	return []agent.AgentMessage{first, user("Done so far in this task, by another model (go on from here; what was called was done, do not do it again):\n" + strings.Join(done, "\n"))}
}

// failedTurn is an assistant turn the provider failed (an error, an abort): dropped before the next model.
func failedTurn(m agent.AgentMessage) bool {
	var a *ai.AssistantMessage
	switch v := m.(type) {
	case ai.AssistantMessage:
		a = &v
	case *ai.AssistantMessage:
		a = v
	default:
		return false
	}
	return a.StopReason == ai.StopError || a.StopReason == ai.StopAborted
}

// keyOf is the API key for a provider (the first model's, the fallbacks').
func (r *Runner) keyOf(provider string) string {
	if k, ok := r.keys[provider]; ok {
		return k
	}
	return r.key
}

// overloaded is a provider's failure that another model may not have (Google's "high demand", a
// request aborted, a quota), not the task's own.
func overloaded(err error) bool {
	if err == nil {
		return false
	}
	e := strings.ToLower(err.Error())
	for _, k := range []string{"high demand", "overloaded", "unavailable", "aborted", "503", "429", "quota", "rate limit", "resource_exhausted", "deadline", "timeout", "timed out"} {
		if strings.Contains(e, k) {
			return true
		}
	}
	return false
}

// watched runs one model attempt under the idle watchdog: silent past cfg.Idle (no event, no tool
// running), it is cancelled and reported as a timeout (overloaded: the next model takes the task).
func (r *Runner) watched(ctx context.Context, t *Task, f func(context.Context) error) error {
	idle := r.cfg.Idle
	if idle <= 0 {
		idle = 45 * time.Second
	}
	actx, cancel := context.WithCancel(ctx)
	defer cancel()
	t.last.Store(time.Now().UnixNano())
	var fired atomic.Bool
	go func() {
		tk := time.NewTicker(idle / 15)
		defer tk.Stop()
		for {
			select {
			case <-actx.Done():
				return
			case <-tk.C:
				if t.tooling.Load() == 0 && time.Since(time.Unix(0, t.last.Load())) > idle {
					fired.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	err := f(actx)
	if fired.Load() {
		return fmt.Errorf("timed out: the model sent nothing in %s", idle)
	}
	return err
}

func (r *Runner) run(t *Task) {
	defer close(t.Done)
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.Timeout)
	defer cancel()
	try := func() agent.AgentState {
		t.Err = r.watched(ctx, t, func(c context.Context) error { return t.Agent.Prompt(c, t.Goal) })
		st := t.Agent.State()
		if t.Err == nil && st.ErrorMessage != "" { // the provider's error ends the run on its state, not as an error
			t.Err = fmt.Errorf("%s", firstLine(st.ErrorMessage))
		}
		return st
	}
	st := try()
	if r.cfg.Strain != nil {
		r.cfg.Strain(t.Err)
	}
	// the model overloaded: the task goes on with the next model (tasks.fallback, as Ask's), from its
	// transcript (the calls made stay made, none again), the failed turn dropped; an overloaded model is
	// passed over by the tasks after it for a while (pick)
	cur := r.chain()[t.start].model
	for _, m := range r.chain()[t.start+1:] {
		if !overloaded(t.Err) || ctx.Err() != nil || t.stopped {
			break
		}
		r.overheat(cur)
		if r.isHot(m.model) {
			continue
		}
		cur = m.model
		r.log("fallback", t.ID, map[string]any{"error": t.Err.Error(), "model": string(m.model.Provider) + "/" + m.model.ID})
		ms := t.Agent.State().Messages
		for len(ms) > 0 && failedTurn(ms[len(ms)-1]) {
			ms = ms[:len(ms)-1]
		}
		t.Agent.SetMessages(handOver(ms))
		t.Agent.SetModel(m.model)
		t.Err = r.watched(ctx, t, t.Agent.Continue)
		st = t.Agent.State()
		if t.Err == nil && st.ErrorMessage != "" {
			t.Err = fmt.Errorf("%s", firstLine(st.ErrorMessage))
		}
	}
	for i := len(st.Messages) - 1; i >= 0; i-- {
		if st.Messages[i].MessageRole() == ai.RoleAssistant {
			if s := textOf(st.Messages[i]); strings.TrimSpace(s) != "" {
				t.Result = strings.TrimSpace(s)
				break
			}
		}
	}
	r.mu.Lock()
	delete(r.live, t.ID)
	r.mu.Unlock()
	f := map[string]any{"result": t.Result, "turns": t.Turns, "calls": t.Calls, "steered": t.Steered, "followed": t.Followed}
	if t.Err != nil {
		f["error"] = t.Err.Error()
		r.log("failed", t.ID, f)
		if r.cfg.Note != nil && t.Level != Quiet {
			r.cfg.Note(t.Level, "I couldn't finish that: "+t.Err.Error())
		}
		return
	}
	r.log("done", t.ID, f)
	if r.cfg.Note != nil && t.Result != "" && t.Level != Quiet {
		r.cfg.Note(t.Level, t.Result)
	}
}

// tools are the task's tools: each home card, its calls counted and its package marked used.
func (r *Runner) tools(t *Task) []agent.AgentTool {
	var out []agent.AgentTool
	for _, tl := range r.cfg.Tools {
		tl := tl
		var sch ai.Schema
		if b, err := jsonMarshal(tl.Parameters); err == nil {
			sch.UnmarshalJSON(b)
		}
		out = append(out, agent.AgentTool{Name: tl.Name, Description: tl.Description, Parameters: &sch,
			ExecutionMode: agent.ToolSequential, // a batch of acting calls in order, as the tick takes them
			Execute: func(ctx context.Context, id string, params map[string]any, _ agent.ToolUpdateFunc) (agent.AgentToolResult, error) {
				r.mu.Lock()
				t.Calls++
				if tl.Domain != "" {
					t.used[tl.Domain] = true
				}
				r.mu.Unlock()
				status, text := tl.Call(ctx, params)
				r.mu.Lock()
				if status == "pending" && tl.Domain != "" {
					t.pending[tl.Domain] = true
				}
				for _, v := range params {
					if s, ok := v.(string); ok && len(s) > 2 {
						s = strings.ToLower(s)
						t.named[s] = true
						if status == "refused" {
							t.refusedOn[s] = true
						}
					}
				}
				r.mu.Unlock()
				res := agent.AgentToolResult{Content: ai.ContentList{ai.TextContent{Text: status + ": " + text}}}
				res.IsError = status == "error"
				return res, nil
			}})
	}
	return out
}

// Reasoning is a package's line from the rules (an outcome, a thing noticed): steered into the
// tasks that used the package (Steer), or followed up into one that awaits its outcome (FollowUp).
func (r *Runner) Reasoning(domain, kind, line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.live {
		if !t.used[domain] {
			continue
		}
		msg := user("HOME (reported by the home's systems, after your call): " + line)
		switch {
		case kind == "result" && t.pending[domain] && r.cfg.Mech.FollowUp:
			delete(t.pending, domain)
			t.Agent.FollowUp(msg)
			t.Followed++
			r.log("followup", t.ID, map[string]any{"line": line})
		case r.cfg.Mech.Steer:
			t.Agent.Steer(msg)
			t.Steered++
			r.log("steer", t.ID, map[string]any{"line": line})
		}
	}
}

// World is a world line whose words changed ("bedroom ventilation: running (sure, sensor, just
// now)": what it is about before the colon): steered into the tasks whose calls named it, or
// followed up into one whose call about it was refused (the rules said "not yet": the change may
// be what it waited for, so the run goes on past its answer).
func (r *Runner) World(line string) {
	subject, _, _ := strings.Cut(line, ":")
	k := strings.ToLower(strings.TrimSpace(subject))
	if k == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.live {
		hit, refused := false, false
		for n := range t.named {
			if strings.Contains(n, k) || strings.Contains(k, n) {
				hit, refused = true, refused || t.refusedOn[n]
			}
		}
		if !hit {
			continue
		}
		msg := user("HOME now (it changed since your call): " + line)
		switch {
		case refused && r.cfg.Mech.FollowUp:
			for n := range t.refusedOn {
				if strings.Contains(n, k) || strings.Contains(k, n) {
					delete(t.refusedOn, n)
				}
			}
			t.Agent.FollowUp(msg)
			t.Followed++
			r.log("followup", t.ID, map[string]any{"world": line})
		case r.cfg.Mech.Steer:
			t.Agent.Steer(msg)
			t.Steered++
			r.log("steer", t.ID, map[string]any{"world": line})
		}
	}
}

// Live is the running tasks' ids.
func (r *Runner) Live() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for id := range r.live {
		out = append(out, id)
	}
	return out
}

func textOf(m agent.AgentMessage) string {
	var cs ai.ContentList
	switch x := m.(type) {
	case ai.AssistantMessage:
		cs = x.Content
	case *ai.AssistantMessage:
		cs = x.Content
	case ai.UserMessage:
		cs = x.Content
	case *ai.UserMessage:
		cs = x.Content
	case ai.ToolResultMessage:
		cs = x.Content
	case *ai.ToolResultMessage:
		cs = x.Content
	default:
		return ""
	}
	var b strings.Builder
	for _, c := range cs {
		if tc, ok := c.(ai.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func resultText(v any) string {
	if r, ok := v.(agent.AgentToolResult); ok {
		return textOf(ai.ToolResultMessage{Content: r.Content})
	}
	return fmt.Sprint(v)
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return h + p[1:]
	}
	return p
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, "\"message\"") {
			return strings.TrimSpace(l)
		}
	}
	l, _, _ := strings.Cut(s, "\n")
	return l
}

// Routed: the composition's director forwarded a goal from member from to member to: a task that
// used from has reached to too (its conclusions are the task's: a scene's wishes reach the packages).
func (r *Runner) Routed(from, to string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.live {
		if t.used[from] {
			t.used[to] = true
		}
	}
}

// AddTool gives the runner's tasks one more tool (from now on).
func (r *Runner) AddTool(t Tool) {
	r.mu.Lock()
	r.cfg.Tools = append(r.cfg.Tools, t)
	r.mu.Unlock()
}

// Key is a provider's API key, from pi-ai's names for it (OPENROUTER_API_KEY, GEMINI_API_KEY, ...):
// file is a bare key (fsttm's gemini.key) or an env file of NAME=value lines (metis's
// openrouter.env); with no file, the process's environment.
func Key(provider, file string) (string, error) {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	if file != "" {
		b, err := os.ReadFile(expand(file))
		if err != nil {
			return "", fmt.Errorf("tasks: the key: %w", err)
		}
		text := strings.TrimSpace(string(b))
		if !strings.Contains(text, "=") {
			return text, nil
		}
		env = map[string]string{}
		for _, l := range strings.Split(text, "\n") {
			l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "export "))
			if k, v, ok := strings.Cut(l, "="); ok && !strings.HasPrefix(k, "#") {
				env[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
			}
		}
	}
	if k := ai.GetEnvApiKey(provider, env); k != "" {
		return k, nil
	}
	return "", fmt.Errorf("tasks: no %s key in %s (pi-ai's names: %v)", provider, file, ai.FindEnvKeys(provider, env))
}

// Ask is one answer of the model, no tools: a system prompt and a prompt (the explainer's
// narration of a trace). Logged (stream task, kind ask).
func (r *Runner) Ask(ctx context.Context, system, prompt string) (string, error) {
	chain := append([]alt{{r.model, r.key}}, r.alts...)
	var out string
	var err error
	for i, m := range chain {
		last := i == len(chain)-1
		c, cancel := ctx, context.CancelFunc(func() {})
		if !last { // one model's turn: a short one, one retry at most; the last gets what is left
			c, cancel = context.WithTimeout(ctx, r.cfg.AskTry)
		}
		out, err = r.ask1(c, m, system, prompt, map[bool]int{true: 4, false: 1}[last])
		cancel()
		if i == 0 && r.cfg.Strain != nil {
			r.cfg.Strain(err)
		}
		f := map[string]any{"chars": len(prompt), "answer": out, "model": string(m.model.Provider) + "/" + m.model.ID}
		if err != nil {
			f["error"] = err.Error()
		}
		r.log("ask", "", f)
		if err == nil && out != "" || ctx.Err() != nil {
			break
		}
	}
	return out, err
}

func (r *Runner) ask1(ctx context.Context, m alt, system, prompt string, retries int) (string, error) {
	a := agent.NewAgent(agent.AgentOptions{
		InitialState: &agent.AgentState{SystemPrompt: system, Model: m.model},
		GetApiKey:    func(string) string { return m.key },
		MaxRetries:   retries,
	})
	err := a.Prompt(ctx, prompt)
	st := a.State()
	if err == nil && st.ErrorMessage != "" {
		err = fmt.Errorf("%s", firstLine(st.ErrorMessage))
	}
	out := ""
	for i := len(st.Messages) - 1; i >= 0; i-- {
		if st.Messages[i].MessageRole() == ai.RoleAssistant {
			if s := textOf(st.Messages[i]); strings.TrimSpace(s) != "" {
				out = strings.TrimSpace(s)
				break
			}
		}
	}
	if err == nil && out == "" {
		err = fmt.Errorf("an empty answer")
	}
	return out, err
}
