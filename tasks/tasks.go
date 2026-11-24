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

type Config struct {
	Provider, Model string // google, gemini-3.5-flash
	KeyFile         string
	System          string
	Tools           []Tool
	World           func() []string          // the world's lines now (TransformContext)
	Log             *nlog.Log                // nil: nothing logged
	Note            func(level, text string) // the result to the voice
	Now             func() float64
	Mech            Mechanisms
	MaxTurns        int // a run's turns before it is stopped (0: 12)
	Timeout         time.Duration
}

// Runner holds the running tasks.
type Runner struct {
	cfg   Config
	key   string
	model *ai.Model
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
	Done              chan struct{}
	Result            string
	Err               error
	Turns, Calls      int
	Steered, Followed int
}

func New(c Config) (*Runner, error) {
	b, err := os.ReadFile(expand(c.KeyFile))
	if err != nil {
		return nil, fmt.Errorf("tasks: the key: %w", err)
	}
	m := ai.GetModel(c.Provider, c.Model)
	if m == nil {
		return nil, fmt.Errorf("tasks: no model %s/%s in pi's catalog", c.Provider, c.Model)
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
	return &Runner{cfg: c, key: strings.TrimSpace(string(b)), model: m, live: map[string]*Task{}}, nil
}

func (r *Runner) log(kind, key string, data any) {
	if r.cfg.Log != nil {
		r.cfg.Log.Append(r.cfg.Now(), Stream, kind, key, "", data, 1)
	}
}

func user(text string) ai.UserMessage {
	return ai.UserMessage{Content: ai.ContentList{ai.TextContent{Text: text}}, Timestamp: time.Now().UnixMilli()}
}

// Delegate starts a run for the goal → its id at once; the result comes as a NOTE at the level.
func (r *Runner) Delegate(goal, level string) *Task {
	if level == "" {
		level = "important"
	}
	r.mu.Lock()
	r.n++
	t := &Task{ID: fmt.Sprintf("task%d", r.n), Goal: goal, Level: level, used: map[string]bool{},
		pending: map[string]bool{}, Done: make(chan struct{})}
	r.live[t.ID] = t
	r.mu.Unlock()
	t.Agent = agent.NewAgent(agent.AgentOptions{
		InitialState: &agent.AgentState{SystemPrompt: r.cfg.System, Model: r.model, Tools: r.tools(t)},
		GetApiKey:    func(string) string { return r.key },
		MaxRetries:   6, // the free tier's 503 "high demand" is transient: pi backs off (Retry-After honoured)
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
		switch e.Type {
		case agent.EvTurnEnd: // the turns counted from the events, no hook: past MaxTurns the run is stopped
			t.Turns++
			if t.Turns >= r.cfg.MaxTurns {
				r.log("stopped", t.ID, map[string]any{"why": "max turns", "turns": t.Turns})
				go t.Agent.Abort()
			}
		case agent.EvMessageEnd:
			r.log("message", t.ID, map[string]any{"role": string(e.Message.MessageRole()), "text": textOf(e.Message)})
		case agent.EvToolExecutionEnd:
			r.log("tool", t.ID, map[string]any{"name": e.ToolName, "args": e.Args, "error": e.IsError, "result": resultText(e.Result)})
		}
		return nil
	})
	r.log("created", t.ID, map[string]any{"goal": goal, "level": level, "mech": r.cfg.Mech})
	go r.run(t)
	return t
}

func (r *Runner) run(t *Task) {
	defer close(t.Done)
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.Timeout)
	defer cancel()
	t.Err = t.Agent.Prompt(ctx, t.Goal)
	st := t.Agent.State()
	if t.Err == nil && st.ErrorMessage != "" { // the provider's error ends the run on its state, not as an error
		t.Err = fmt.Errorf("%s", firstLine(st.ErrorMessage))
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
		if r.cfg.Note != nil {
			r.cfg.Note(t.Level, "I couldn't finish that: "+t.Err.Error())
		}
		return
	}
	r.log("done", t.ID, f)
	if r.cfg.Note != nil && t.Result != "" {
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
				if status == "pending" && tl.Domain != "" {
					r.mu.Lock()
					t.pending[tl.Domain] = true
					r.mu.Unlock()
				}
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
