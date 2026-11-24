package explain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	nlog "github.com/ibnHatab/nina-live/log"
)

// TestTheTraceAndTheScripts: the trace is the log's story across domains (a request, the rules'
// lines, the director's routing); the scripts are the domains' rules with their meanings, the
// lifted base included; the narrator gets both and the question.
func TestTheTraceAndTheScripts(t *testing.T) {
	L, err := nlog.Open(filepath.Join(t.TempDir(), "nina.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer L.Close()
	L.Append(100, "call", "scenes", "", "", map[string]any{"tool": "home_scenes", "args": map[string]any{"intent": "NIGHT"}}, 1)
	L.Append(101, "reasoning", "result", "scenes", "", map[string]string{"line": "scenes done: I switch the home to its night mode."}, 1)
	L.Append(101, "tick", "tick-relay", "", "", map[string]any{"what": "goal", "domain": "music", "kind": "scene_music"}, 1)
	L.Append(102, "reasoning", "event", "music", "", map[string]string{"line": "music noticed: Good night: the music fades to a whisper."}, 1)
	var got string
	x := &Explainer{Log: L, Root: "../catalog", Now: func() float64 { return 200 },
		Ask: func(_ context.Context, sys, prompt string) (string, error) { got = sys + "\n" + prompt; return "because", nil }}
	ans, err := x.Explain(context.Background(), "why is the music so quiet?", 5)
	if err != nil || ans != "because" {
		t.Fatalf("%q %v", ans, err)
	}
	for _, want := range []string{
		"call: the person asked home_scenes",
		"scenes (result): scenes done: I switch the home to its night mode.",
		"route: the director carried scene_music to music",
		"## music", `night: "Good night: the music fades to a whisper."`,
		`something: "Something you like now: {O}."`, // the taste's lifted base, through the include
		"## scenes", "## director",
		"why is the music so quiet?",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the narrator's prompt lacks %q:\n%s", want, got)
		}
	}
}
