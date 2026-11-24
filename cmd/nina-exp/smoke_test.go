package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/ibnHatab/nina-live/tasks"
)

// TestPiOnGemini: one pi run on the AI Studio key with a toy tool (NINA_LIVE_API=1: it calls the API).
func TestPiOnGemini(t *testing.T) {
	if os.Getenv("NINA_LIVE_API") == "" {
		t.Skip("NINA_LIVE_API=1 calls Gemini")
	}
	called := 0
	r, err := tasks.New(tasks.Config{Provider: "google", Model: os.Getenv("NINA_MODEL"), KeyFile: "~/.config/fsttm/gemini.key",
		System: "You are a careful home assistant. Use the tools; answer in one sentence.",
		Tools: []tasks.Tool{{Name: "lab_temperature", Description: "The lab's temperature now, in °C.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
			Call:       func(context.Context, map[string]any) (string, string) { called++; return "done", "21.5 °C" }}},
		World: func() []string { return []string{"lab ventilation: stopped (sure, sensor, just now)"} }, Mech: tasks.All})
	if err != nil {
		t.Fatal(err)
	}
	tk := r.Delegate("How warm is the lab, and is its ventilation running?", "info")
	select {
	case <-tk.Done:
	case <-time.After(90 * time.Second):
		t.Fatal("no end")
	}
	st := tk.Agent.State()
	for _, m := range st.Messages {
		b, _ := json.Marshal(m)
		t.Logf("%s", b)
	}
	t.Logf("state error %q", st.ErrorMessage)
	t.Logf("result %q, err %v, turns %d, calls %d (tool called %d)", tk.Result, tk.Err, tk.Turns, tk.Calls, called)
	if tk.Err != nil || called == 0 || tk.Result == "" {
		t.Fatal("the run")
	}
	_ = context.Background
}
