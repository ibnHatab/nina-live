package explain

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	nlog "github.com/ibnHatab/nina-live/log"
	"github.com/ibnHatab/nina-live/tasks"
)

// TestANarrationOnTheModel: NINA_LIVE_API=1: the explainer on the backend's model (OpenRouter's
// DeepSeek by default), over a short cross-domain trace with an inconsistency in it.
func TestANarrationOnTheModel(t *testing.T) {
	if os.Getenv("NINA_LIVE_API") == "" {
		t.Skip("NINA_LIVE_API=1 calls the model")
	}
	r, err := tasks.New(tasks.Config{Provider: "openrouter", Model: "deepseek/deepseek-v4.1-flash", KeyFile: "~/.config/metis/openrouter.env", System: "x"})
	if err != nil {
		t.Fatal(err)
	}
	L, _ := nlog.Open(filepath.Join(t.TempDir(), "nina.db"))
	defer L.Close()
	L.Append(100, "call", "music", "", "", map[string]any{"tool": "home_music", "args": map[string]any{"intent": "PLAY", "genre": "jazz"}}, 1)
	L.Append(101, "reasoning", "result", "music", "", map[string]string{"line": "music done: I put on jazz (Classic Vinyl HD)."}, 1)
	L.Append(300, "call", "scenes", "", "", map[string]any{"tool": "home_scenes", "args": map[string]any{"intent": "NIGHT"}}, 1)
	L.Append(301, "reasoning", "result", "scenes", "", map[string]string{"line": "scenes done: I switch the home to its night mode."}, 1)
	L.Append(301, "tick", "tick-relay", "", "", map[string]any{"what": "goal", "domain": "music", "kind": "scene_music"}, 1)
	L.Append(302, "reasoning", "event", "music", "", map[string]string{"line": "music noticed: Good night: the music fades to a whisper."}, 1)
	L.Append(301, "tick", "tick-relay", "", "", map[string]any{"what": "goal", "domain": "air", "kind": "scene_air"}, 1)
	L.Append(303, "reasoning", "result", "air", "", map[string]string{"line": "air refused: I can't put the lab ventilation in night mode yet: it has not confirmed whether it runs."}, 1)
	persona, _ := os.ReadFile("../prompts/nina.md")
	x := &Explainer{Log: L, Root: "../catalog", Ask: r.Ask, Persona: string(persona), Now: func() float64 { return 400 }}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	t0 := time.Now()
	ans, err := x.Explain(ctx, "Why is the music so quiet, and is the ventilation on night mode?", 10)
	t.Logf("%.1fs: %s", time.Since(t0).Seconds(), ans)
	if err != nil || ans == "" {
		t.Fatal(err)
	}
}
