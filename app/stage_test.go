package app

import (
	"context"
	"strings"
	"testing"
)

// next, previous, pause, play: on what the stage shows, a stage event of their own (no search)
func TestScreenToolSteps(t *testing.T) {
	var got []map[string]any
	tool := screenTool(nil, nil, func(ev map[string]any) { got = append(got, ev) })
	for _, s := range []string{"next", "previous", "pause", "play", "close"} {
		got = nil
		st, _ := tool.Call(context.Background(), map[string]any{"show": s})
		if st != "done" || len(got) != 1 || got[0]["show"] != s {
			t.Fatalf("%s: %s %v", s, st, got)
		}
	}
	if st, _ := tool.Call(context.Background(), map[string]any{"show": "dance"}); st != "error" {
		t.Fatalf("unknown show: %s", st)
	}
}

// an album asked for by name: looked up first; its own title on the screen, or nothing opened
func TestScreenToolAlbum(t *testing.T) {
	var got []map[string]any
	have := []string{"Nina's gallery", "Holidays 2025"}
	tool := screenTool(nil, func(context.Context) ([]string, error) { return have, nil }, func(ev map[string]any) { got = append(got, ev) })
	if st, _ := tool.Call(context.Background(), map[string]any{"show": "photos", "album": "Ninas Gallery"}); st != "done" || len(got) != 1 || got[0]["album"] != "Nina's gallery" {
		t.Fatalf("found: %s %v", st, got)
	}
	got = nil
	if st, msg := tool.Call(context.Background(), map[string]any{"show": "photos", "album": "Wedding"}); st != "done" || len(got) != 0 || !strings.Contains(msg, "Holidays 2025") {
		t.Fatalf("missing: %s %q %v", st, msg, got)
	}
	have = nil
	if _, msg := tool.Call(context.Background(), map[string]any{"show": "photos", "album": "Nina's gallery"}); len(got) != 0 || !strings.Contains(msg, "none you can show") {
		t.Fatalf("none: %q %v", msg, got)
	}
}
