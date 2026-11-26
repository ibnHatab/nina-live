package main

import (
	"context"
	"testing"
)

// next, previous, pause, play: on what the stage shows, a stage event of their own (no search)
func TestScreenToolSteps(t *testing.T) {
	var got []map[string]any
	tool := screenTool(nil, func(ev map[string]any) { got = append(got, ev) })
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
