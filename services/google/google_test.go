package google

import (
	"context"
	"encoding/json"
	"io"
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

// TestHerPhotoAlbums: the albums she made, an album's photos (images only), and an upload into an album
// made for it.
func TestHerPhotoAlbums(t *testing.T) {
	var made []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			w.Write([]byte(`{"access_token":"A","expires_in":3600}`))
		case r.URL.Path == "/ph/albums" && r.Method == "GET":
			w.Write([]byte(`{"albums":[{"id":"a1","title":"Nina's gallery","mediaItemsCount":"2"}]}`))
		case r.URL.Path == "/ph/albums" && r.Method == "POST":
			made = append(made, "album")
			w.Write([]byte(`{"id":"a2"}`))
		case r.URL.Path == "/ph/mediaItems:search":
			w.Write([]byte(`{"mediaItems":[{"id":"m1","filename":"a.jpg","baseUrl":"https://lh3/x","mimeType":"image/jpeg"},{"id":"m2","filename":"v.mp4","baseUrl":"https://lh3/v","mimeType":"video/mp4"}]}`))
		case r.URL.Path == "/ph/uploads":
			if r.Header.Get("X-Goog-Upload-Protocol") != "raw" {
				http.Error(w, "raw", 400)
				return
			}
			made = append(made, "upload")
			w.Write([]byte(`UPTOKEN`))
		case r.URL.Path == "/ph/mediaItems:batchCreate":
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			if b["albumId"] != "a2" {
				http.Error(w, "album", 400)
				return
			}
			made = append(made, "item")
			w.Write([]byte(`{}`))
		default:
			http.Error(w, r.URL.Path, 404)
		}
	}))
	defer srv.Close()
	c := New(Token{ClientID: "C", ClientSecret: "S", RefreshToken: "R"})
	c.TokenURL, c.PhotosURL = srv.URL+"/token", srv.URL+"/ph"
	ctx := context.Background()
	if ts, err := c.AlbumTitles(ctx); err != nil || len(ts) != 1 || ts[0] != "Nina's gallery" {
		t.Fatalf("albums: %v %v", ts, err)
	}
	if ps, err := c.Photos(ctx, "a1", 10); err != nil || len(ps) != 1 || ps[0].URL != "https://lh3/x" {
		t.Fatalf("photos: %v %v", ps, err)
	}
	if err := c.Upload(ctx, "Kitchen", "k.jpg", []byte("jpeg")); err != nil || strings.Join(made, ",") != "album,upload,item" {
		t.Fatalf("upload: %v %v", made, err)
	}
}

// TestThePickedPhotosBecomeHers: a picking's link, its end, the picked photos downloaded with her token and
// uploaded into her album, the session deleted.
func TestThePickedPhotosBecomeHers(t *testing.T) {
	var log []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			w.Write([]byte(`{"access_token":"A","expires_in":3600}`))
		case r.URL.Path == "/pk/sessions" && r.Method == "POST":
			w.Write([]byte(`{"id":"s1","pickerUri":"https://photos.google.com/picker/s1","mediaItemsSet":false}`))
		case r.URL.Path == "/pk/sessions/s1" && r.Method == "GET":
			w.Write([]byte(`{"id":"s1","mediaItemsSet":true}`))
		case r.URL.Path == "/pk/sessions/s1" && r.Method == "DELETE":
			log = append(log, "ended")
		case r.URL.Path == "/pk/mediaItems":
			w.Write([]byte(`{"mediaItems":[{"id":"p1","type":"PHOTO","mediaFile":{"baseUrl":"` + srv.URL + `/lh/p1","mimeType":"image/jpeg","filename":"desk.jpg"}},{"id":"v1","type":"VIDEO","mediaFile":{"baseUrl":"x","mimeType":"video/mp4"}}]}`))
		case r.URL.Path == "/lh/p1=d":
			if r.Header.Get("Authorization") != "Bearer A" {
				http.Error(w, "no", 401)
				return
			}
			w.Write([]byte("JPEG"))
		case r.URL.Path == "/ph/albums" && r.Method == "GET":
			w.Write([]byte(`{"albums":[{"id":"a1","title":"Office"}]}`))
		case r.URL.Path == "/ph/uploads":
			b, _ := io.ReadAll(r.Body)
			log = append(log, "upload "+string(b))
			w.Write([]byte("TOK"))
		case r.URL.Path == "/ph/mediaItems:batchCreate":
			log = append(log, "into a1")
			w.Write([]byte(`{}`))
		default:
			http.Error(w, r.URL.Path, 404)
		}
	}))
	defer srv.Close()
	c := New(Token{ClientID: "C", ClientSecret: "S", RefreshToken: "R"})
	c.TokenURL, c.PhotosURL, c.PickerURL = srv.URL+"/token", srv.URL+"/ph", srv.URL+"/pk"
	ctx := context.Background()
	s, err := c.NewPicking(ctx)
	if err != nil || s.URI == "" || s.Done {
		t.Fatalf("the picking: %+v %v", s, err)
	}
	if s, _ = c.Picking(ctx, s.ID); !s.Done {
		t.Fatal("not done")
	}
	n, err := c.CopyPicked(ctx, s.ID, "office")
	if err != nil || n != 1 || strings.Join(log, ",") != "upload JPEG,into a1,ended" {
		t.Fatalf("copied %d: %v %v", n, log, err)
	}
}
