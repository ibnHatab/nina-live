package agenda

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ibnHatab/acasa/hass"
)

// TestWritesReachHomeAssistant: on the real Home Assistant (NINA_LIVE_HA=1), an event on the local
// test calendar and a to-do on the local list arrive, marked as Nina's; the to-do is removed after.
func TestWritesReachHomeAssistant(t *testing.T) {
	if os.Getenv("NINA_LIVE_HA") == "" {
		t.Skip("NINA_LIVE_HA=1 writes to the local test calendar and list")
	}
	c, err := hass.New("http://homeassistant.local:8123", "~/.config/fsttm/hass.token")
	if err != nil {
		t.Fatal(err)
	}
	var saved []Write
	h := &Host{Entities: Default, Save: func(w Write) { saved = append(saved, w) },
		Call: func(ctx context.Context, d, s string, data map[string]any) error {
			_, err := c.Service(ctx, d, s, data, false)
			return err
		}}
	start := time.Now().Add(2 * time.Hour).Truncate(time.Minute)
	h.write("event", "nina_local", map[string]any{"summary": "Nina's write check", "start": start.Format("2006-01-02T15:04"), "minutes": 15.0})
	h.write("todo", "nina_todo", map[string]any{"summary": "Nina's write check"})
	for _, w := range saved {
		if w.Err != "" {
			t.Fatalf("%+v", w)
		}
	}
	ctx := context.Background()
	r, err := c.Service(ctx, "calendar", "get_events", map[string]any{"entity_id": "calendar.nina_local",
		"start_date_time": start.Add(-time.Minute).Format(time.RFC3339), "end_date_time": start.Add(time.Hour).Format(time.RFC3339)}, true)
	if err != nil || !strings.Contains(string(r["calendar.nina_local"]), "Nina's write check") || !strings.Contains(string(r["calendar.nina_local"]), Mark) {
		t.Fatalf("the event: %s %v", r["calendar.nina_local"], err)
	}
	r, err = c.Service(ctx, "todo", "get_items", map[string]any{"entity_id": "todo.nina_local"}, true)
	var x struct {
		Items []struct{ Summary, Description string }
	}
	json.Unmarshal(r["todo.nina_local"], &x)
	found := false
	for _, it := range x.Items {
		found = found || (it.Summary == "Nina's write check" && it.Description == Mark)
	}
	if err != nil || !found {
		t.Fatalf("the to-do: %s %v", r["todo.nina_local"], err)
	}
	if _, err := c.Service(ctx, "todo", "remove_item", map[string]any{"entity_id": "todo.nina_local", "item": "Nina's write check"}, false); err != nil {
		t.Fatal(err)
	}
	t.Logf("written and read back: %+v", saved)
}
