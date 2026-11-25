package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// a server answering as an event stream (as Home Assistant does), with a session
func TestStreamServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		if m["id"] == nil {
			w.WriteHeader(202)
			return
		}
		var res any
		switch m["method"] {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "s1")
			res = map[string]any{"protocolVersion": "2025-03-26"}
		case "tools/list":
			if r.Header.Get("Mcp-Session-Id") != "s1" {
				t.Error("no session")
			}
			res = map[string]any{"tools": []any{
				map[string]any{"name": "GetLiveContext", "description": "the home now", "inputSchema": map[string]any{"type": "object"}},
				map[string]any{"name": "HassTurnOn", "description": "on"}}}
		case "tools/call":
			p := m["params"].(map[string]any)
			res = map[string]any{"content": []any{map[string]any{"type": "text", "text": fmt.Sprint("called ", p["name"])}}}
		}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m["id"], "result": res})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
	}))
	defer srv.Close()
	dir := t.TempDir()
	tf := dir + "/tok"
	writeFile(t, tf, "tok\n")
	ctx := context.Background()
	c, err := Dial(ctx, Server{Name: "ha", URL: srv.URL, TokenFile: tf, Deny: []string{"HassTurnOn"}})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := c.Tools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "ha_GetLiveContext" || tools[0].Domain != "home" {
		t.Fatalf("tools %+v", tools)
	}
	st, text := tools[0].Call(ctx, nil)
	if st != "done" || text != "called GetLiveContext" {
		t.Fatalf("%s %q", st, text)
	}
}

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}
