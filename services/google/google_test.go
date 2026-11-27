package google

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fake is Google's three endpoints as the client uses them: the token, one calendar, two task lists.
type fake struct {
	mu      sync.Mutex
	tokens  int
	events  []map[string]any
	tasks   map[string][]map[string]string
	deleted []string
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	switch {
	case p == "/token":
		r.ParseForm()
		if r.Form.Get("refresh_token") != "R" || r.Form.Get("grant_type") != "refresh_token" {
			http.Error(w, `{"error":"invalid_grant"}`, 400)
			return
		}
		f.tokens++
		json.NewEncoder(w).Encode(map[string]any{"access_token": "A", "expires_in": 3600})
		return
	}
	if r.Header.Get("Authorization") != "Bearer A" {
		http.Error(w, `{"error":{"message":"no"}}`, 401)
		return
	}
	switch {
	case p == "/cal/calendars/nina@group/events" && r.Method == "POST":
		var e map[string]any
		json.NewDecoder(r.Body).Decode(&e)
		f.events = append(f.events, e)
		w.Write([]byte(`{}`))
	case p == "/cal/calendars/nina@group/events" && r.Method == "GET":
		w.Write([]byte(`{"items":[{"summary":"Dentist","start":{"dateTime":"2026-11-30T10:00:00+02:00"}},{"summary":"Holiday","start":{"date":"2026-12-01"}}]}`))
	case p == "/tasks/users/@me/lists":
		w.Write([]byte(`{"items":[{"id":"L1","title":"My Tasks"},{"id":"L2","title":"Shopping"}]}`))
	case strings.HasPrefix(p, "/tasks/lists/"):
		parts := strings.Split(strings.TrimPrefix(p, "/tasks/lists/"), "/")
		switch {
		case r.Method == "POST":
			var t map[string]string
			json.NewDecoder(r.Body).Decode(&t)
			t["id"] = "t" + t["title"]
			f.tasks[parts[0]] = append(f.tasks[parts[0]], t)
			w.Write([]byte(`{}`))
		case r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"items": f.tasks[parts[0]]})
		case r.Method == "DELETE":
			f.deleted = append(f.deleted, parts[0]+"/"+parts[2])
			w.WriteHeader(204)
		}
	default:
		http.Error(w, `{"error":{"message":"not found `+p+`"}}`, 404)
	}
}

func TestTheAgendaOnGoogle(t *testing.T) {
	f := &fake{tasks: map[string][]map[string]string{"L2": {{"id": "tBread", "title": "Bread"}}}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := New(Token{ClientID: "C", ClientSecret: "S", RefreshToken: "R"})
	c.TokenURL, c.CalendarURL, c.TasksURL = srv.URL+"/token", srv.URL+"/cal", srv.URL+"/tasks"
	ctx := context.Background()
	if err := c.CreateEvent(ctx, "nina@group", "Dentist", "2026-11-30 10:00:00", "2026-11-30 10:30:00", "(set by Nina)"); err != nil {
		t.Fatal(err)
	}
	if st := f.events[0]["start"].(map[string]any)["dateTime"].(string); !strings.HasPrefix(st, "2026-11-30T10:00:00") {
		t.Fatalf("the start: %v", f.events[0])
	}
	if err := c.AddItem(ctx, "Shopping", "Milk", "", "(set by Nina)"); err != nil { // a list by its title
		t.Fatal(err)
	}
	if err := c.AddItem(ctx, "L1", "Call the plumber", "2026-12-02", ""); err != nil { // by its id
		t.Fatal(err)
	}
	if f.tasks["L1"][0]["due"] != "2026-12-02T00:00:00.000Z" {
		t.Fatalf("the due date: %v", f.tasks["L1"])
	}
	items, err := c.Items(ctx, "Shopping")
	if err != nil || strings.Join(items, ",") != "Bread,Milk" {
		t.Fatalf("items: %v %v", items, err)
	}
	if err := c.RemoveItem(ctx, "Shopping", "Bread"); err != nil || f.deleted[0] != "L2/tBread" {
		t.Fatalf("removed: %v %v", f.deleted, err)
	}
	if err := c.RemoveItem(ctx, "Shopping", "Cheese"); err == nil {
		t.Fatal("removed what is not on it")
	}
	if err := c.AddItem(ctx, "Groceries", "Eggs", "", ""); err == nil {
		t.Fatal("a list there is not")
	}
	evs, err := c.Events(ctx, "nina@group", 7*24*time.Hour, 10)
	if err != nil || len(evs) != 2 || !strings.HasSuffix(evs[0], "Dentist") || !strings.Contains(evs[1], "(all day) Holiday") {
		t.Fatalf("events: %v %v", evs, err)
	}
	if f.tokens != 1 {
		t.Fatalf("tokens fetched: %d (one, until it expires)", f.tokens)
	}
}

func TestATokenFileReadableByOthersIsRefused(t *testing.T) {
	p := t.TempDir() + "/google-token.json"
	b, _ := json.Marshal(Token{ClientID: "C", ClientSecret: "S", RefreshToken: "R"})
	if err := writeFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("loaded: %v", err)
	}
	writeFile(p, b, 0o600)
	if _, err := Load(p); err != nil {
		t.Fatal(err)
	}
}

func writeFile(p string, b []byte, mode uint32) error {
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return err
	}
	return os.Chmod(p, os.FileMode(mode))
}
