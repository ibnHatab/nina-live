package tasks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sky-valley/pi/agent"
	"github.com/sky-valley/pi/ai"
	"github.com/sky-valley/pi/ai/providers"
)

// an overloaded model mid-task: the task goes on with the fallback from its transcript, the call
// made before not made again (2026-11-25: Gemini 3.8 Flash's "high demand" ended her MCP task)
func TestTaskFallsBack(t *testing.T) {
	key := filepath.Join(t.TempDir(), "key")
	os.WriteFile(key, []byte("faux"), 0o600)
	first := providers.RegisterFauxProvider(providers.RegisterFauxProviderOptions{Provider: "faux1"})
	t.Cleanup(first.Unregister)
	next := providers.RegisterFauxProvider(providers.RegisterFauxProviderOptions{Provider: "faux2"})
	t.Cleanup(next.Unregister)
	busy := providers.FauxStatic(providers.FauxAssistantMessage(nil, ai.StopError,
		providers.FauxMessageOptions{ErrorMessage: "This model is currently experiencing high demand."}))
	first.SetResponses([]providers.FauxResponseStep{
		providers.FauxStatic(providers.FauxAssistantMessage(ai.ContentList{providers.FauxToolCall("probe", map[string]any{})}, ai.StopToolUse)),
		busy, busy, busy, busy,
	})
	next.SetResponses([]providers.FauxResponseStep{
		providers.FauxStatic(providers.FauxAssistantMessage(ai.ContentList{providers.FauxText("No errors in the log.")}, ai.StopStop)),
	})
	fm := next.GetModel()
	calls := 0
	r, err := New(Config{ModelValue: first.GetModel(), KeyFile: key, System: "test",
		Fallbacks: []Fallback{{ModelValue: fm, KeyFile: key}},
		Tools: []Tool{{Name: "probe", Description: "probe", Parameters: map[string]any{"type": "object"},
			Call: func(context.Context, map[string]any) (string, string) { calls++; return "done", "[]" }}},
		Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	task := r.Delegate("is the log clean?", "info")
	<-task.Done
	if task.Err != nil || task.Result != "No errors in the log." {
		t.Fatalf("err %v result %q", task.Err, task.Result)
	}
	if calls != 1 {
		t.Fatalf("the tool ran %d times", calls)
	}
}

// the next model gets the first request and a plain account of what was done: no other model's calls
func TestHandOverIsPlain(t *testing.T) {
	ms := handOver([]agent.AgentMessage{
		user("is the log clean?"),
		&ai.AssistantMessage{Content: ai.ContentList{ai.ToolCall{ID: "1", Name: "ha_diagnose", Arguments: map[string]any{"what": "log"}}}},
		ai.ToolResultMessage{ToolCallID: "1", ToolName: "ha_diagnose", Content: ai.ContentList{ai.TextContent{Text: "[]"}}},
	})
	if len(ms) != 2 {
		t.Fatalf("%d messages", len(ms))
	}
	got := textOf(ms[1])
	if !strings.Contains(got, `called ha_diagnose {"what":"log"}`) || !strings.Contains(got, "→ []") {
		t.Fatalf("%q", got)
	}
	for _, m := range ms {
		if m.MessageRole() == ai.RoleAssistant {
			t.Fatal("an assistant message handed over")
		}
	}
}

// a model that answered "overloaded" is passed over by the next task (it began with the fallback)
func TestOverloadedSkipped(t *testing.T) {
	key := filepath.Join(t.TempDir(), "key")
	os.WriteFile(key, []byte("faux"), 0o600)
	first := providers.RegisterFauxProvider(providers.RegisterFauxProviderOptions{Provider: "faux3"})
	t.Cleanup(first.Unregister)
	next := providers.RegisterFauxProvider(providers.RegisterFauxProviderOptions{Provider: "faux4"})
	t.Cleanup(next.Unregister)
	busy := providers.FauxStatic(providers.FauxAssistantMessage(nil, ai.StopError,
		providers.FauxMessageOptions{ErrorMessage: "This model is currently experiencing high demand."}))
	first.SetResponses([]providers.FauxResponseStep{busy, busy, busy, busy, busy, busy})
	ok := providers.FauxStatic(providers.FauxAssistantMessage(ai.ContentList{providers.FauxText("fine")}, ai.StopStop))
	next.SetResponses([]providers.FauxResponseStep{ok, ok})
	r, err := New(Config{ModelValue: first.GetModel(), KeyFile: key, System: "test",
		Fallbacks: []Fallback{{ModelValue: next.GetModel(), KeyFile: key}}, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	a := r.Delegate("one", "info")
	<-a.Done
	b := r.Delegate("two", "info")
	<-b.Done
	if a.Result != "fine" || b.Result != "fine" || b.start != 1 {
		t.Fatalf("a %q, b %q began at %d (want the fallback, 1)", a.Result, b.Result, b.start)
	}
}

// a model that sends nothing (pi gives Google requests no timeout): past Idle the task goes to the
// next model instead of waiting out its whole Timeout (2026-11-26: a WhatsApp answer took 3 min)
func TestSilentModelHandsOver(t *testing.T) {
	key := filepath.Join(t.TempDir(), "key")
	os.WriteFile(key, []byte("faux"), 0o600)
	first := providers.RegisterFauxProvider(providers.RegisterFauxProviderOptions{Provider: "faux_hang"})
	t.Cleanup(first.Unregister)
	next := providers.RegisterFauxProvider(providers.RegisterFauxProviderOptions{Provider: "faux_ok"})
	t.Cleanup(next.Unregister)
	first.SetResponses([]providers.FauxResponseStep{
		func(ai.TranscriptContext, *ai.SimpleStreamOptions, *providers.FauxState, *ai.Model) *ai.AssistantMessage {
			time.Sleep(1500 * time.Millisecond) // hung
			return providers.FauxAssistantMessage(ai.ContentList{providers.FauxText("too late")}, ai.StopStop)
		},
	})
	next.SetResponses([]providers.FauxResponseStep{
		providers.FauxStatic(providers.FauxAssistantMessage(ai.ContentList{providers.FauxText("Cloudy, 6°C.")}, ai.StopStop)),
	})
	r, err := New(Config{ModelValue: first.GetModel(), KeyFile: key, System: "test",
		Fallbacks: []Fallback{{ModelValue: next.GetModel(), KeyFile: key}},
		Idle:      300 * time.Millisecond, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	task := r.Delegate("how is the weather?", "info")
	<-task.Done
	if task.Err != nil || task.Result != "Cloudy, 6°C." {
		t.Fatalf("err %v result %q", task.Err, task.Result)
	}
	if d := time.Since(t0); d > 5*time.Second {
		t.Fatalf("took %s", d)
	}
}

// her screens choose the chain: a subset of the configured models in an order; tasks then start on
// its first; an unknown model is refused; empty: the configured chain again
func TestSetChain(t *testing.T) {
	key := filepath.Join(t.TempDir(), "key")
	os.WriteFile(key, []byte("faux"), 0o600)
	a := providers.RegisterFauxProvider(providers.RegisterFauxProviderOptions{Provider: "faux_a"})
	t.Cleanup(a.Unregister)
	b := providers.RegisterFauxProvider(providers.RegisterFauxProviderOptions{Provider: "faux_b"})
	t.Cleanup(b.Unregister)
	b.SetResponses([]providers.FauxResponseStep{
		providers.FauxStatic(providers.FauxAssistantMessage(ai.ContentList{providers.FauxText("from b")}, ai.StopStop)),
	})
	r, err := New(Config{ModelValue: a.GetModel(), KeyFile: key, System: "test",
		Fallbacks: []Fallback{{ModelValue: b.GetModel(), KeyFile: key}}, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	all, active := r.Models()
	if len(all) != 2 || strings.Join(all, ",") != strings.Join(active, ",") {
		t.Fatalf("models: %v %v", all, active)
	}
	if err := r.SetChain([]string{"nope/model"}); err == nil {
		t.Fatal("an unknown model taken")
	}
	if err := r.SetChain([]string{all[1]}); err != nil {
		t.Fatal(err)
	}
	if _, active = r.Models(); len(active) != 1 || active[0] != all[1] {
		t.Fatalf("active: %v", active)
	}
	task := r.Delegate("hi", "info")
	<-task.Done
	if task.Err != nil || task.Result != "from b" {
		t.Fatalf("err %v result %q", task.Err, task.Result)
	}
	r.SetChain(nil)
	if _, active = r.Models(); len(active) != 2 {
		t.Fatalf("back: %v", active)
	}
}
