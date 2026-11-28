package stage

import (
	"context"
	"strings"
	"testing"

	"github.com/ibnHatab/nina-live/services/google"
)

// next, previous, pause, play: on what the stage shows, a stage event of their own (no search)
func TestScreenToolSteps(t *testing.T) {
	var got []map[string]any
	tool := Tool(nil, nil, func(ev map[string]any) { got = append(got, ev) })
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
	tool := Tool(nil, func(context.Context) ([]string, error) { return have, nil }, func(ev map[string]any) { got = append(got, ev) })
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

type fakeLib struct{ albums []google.Album }

func (f fakeLib) Albums(context.Context) ([]google.Album, error) { return f.albums, nil }
func (f fakeLib) Photos(_ context.Context, id string, n int) ([]google.Photo, error) {
	if id != "g1" {
		return nil, nil
	}
	return []google.Photo{{ID: "a", URL: "https://lh3/a"}, {ID: "b"}, {ID: "c", URL: "https://lh3/c"}}, nil
}

// the slideshow's album: her gallery by default, named as said; the URLs sized, a photo without one left out
func TestAlbumPhotos(t *testing.T) {
	lib := fakeLib{[]google.Album{{ID: "h", Title: "Holidays 2025"}, {ID: "g1", Title: "Nina's gallery"}}}
	title, urls, err := AlbumPhotos(context.Background(), lib, "")
	if err != nil || title != "Nina's gallery" || len(urls) != 2 || urls[0] != "https://lh3/a=w1600-h1600" {
		t.Fatalf("gallery: %q %v %v", title, urls, err)
	}
	if title, _, _ := AlbumPhotos(context.Background(), lib, "ninas Gallery"); title != "Nina's gallery" {
		t.Fatalf("as said: %q", title)
	}
	if _, _, err := AlbumPhotos(context.Background(), lib, "Wedding"); err == nil || !strings.Contains(err.Error(), "Wedding") {
		t.Fatalf("missing: %v", err)
	}
	got := make(chan map[string]any, 1)
	Action(lib, "", func(kind string, d map[string]any) {
		if kind == "album_photos" {
			got <- d
		}
	})
	if d := <-got; d["album"] != "Nina's gallery" || len(d["photos"].([]string)) != 2 {
		t.Fatalf("action: %v", d)
	}
}
