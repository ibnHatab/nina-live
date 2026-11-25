package tasks

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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
