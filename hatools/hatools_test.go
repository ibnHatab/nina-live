package hatools

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type fake struct {
	name string
	args map[string]any
}

func (f *fake) Call(_ context.Context, name string, args map[string]any) (string, bool, error) {
	f.name, f.args = name, args
	return `{"ok":true}`, false, nil
}

func tool(t *testing.T, assist, config *fake, name string) func(map[string]any) (string, string) {
	for _, tl := range Tools(assist, config, func() time.Time { return time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC) }) {
		if tl.Name == name {
			return func(a map[string]any) (string, string) { return tl.Call(context.Background(), a) }
		}
	}
	t.Fatalf("no %s", name)
	return nil
}

func TestTheToolsReachTheirBackends(t *testing.T) {
	a, c := &fake{}, &fake{}
	set := tool(t, a, c, "ha_automation")
	if st, _ := set(map[string]any{"action": "set", "config": map[string]any{"alias": "x"}}); st != "done" || c.name != "ha_config_set_automation" || c.args["MandatoryBPS"] != false || c.args["entity_id"] != nil {
		t.Fatalf("set: %s %v", c.name, c.args)
	}
	set(map[string]any{"action": "set", "id": "automation.x", "config": map[string]any{"alias": "x"}})
	if c.args["entity_id"] != "automation.x" {
		t.Fatalf("replace: %v", c.args)
	}
	if st, _ := set(map[string]any{"action": "remove"}); st != "error" {
		t.Fatal("remove without an id")
	}
	set(map[string]any{"action": "runs", "id": "automation.x"})
	if c.name != "ha_get_automation_traces" {
		t.Fatal(c.name)
	}
	tool(t, a, c, "ha_find")(map[string]any{"words": "bedroom", "kind": "automation"})
	if c.name != "ha_search" || c.args["query"] != "bedroom" {
		t.Fatalf("find: %v", c.args)
	}
	d := tool(t, a, c, "ha_diagnose")
	d(map[string]any{"what": "history", "about": "light.bedroom_led, sensor.x", "hours": 2.0})
	if a.name != "history__get_history" || len(a.args["entity_ids"].([]string)) != 2 || a.args["start_time"] != "2026-11-25T10:00:00Z" {
		t.Fatalf("history: %v", a.args)
	}
	d(map[string]any{"what": "log"})
	if a.name != "system_log__get_entries" || a.args["level"] != "error" {
		t.Fatalf("log: %v", a.args)
	}
	// light enough for Flash: the three cards' size
	n := 0
	for _, tl := range Tools(a, c, nil) {
		b, _ := json.Marshal(tl.Parameters)
		n += len(tl.Description) + len(b) + len(tl.Name)
	}
	t.Logf("three tools: %d chars", n)
	if n > 3000 {
		t.Fatalf("%d chars", n)
	}
}
