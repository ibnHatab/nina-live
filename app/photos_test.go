package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ibnHatab/nina-live/services/google"
)

type fakePhotos struct {
	mu     sync.Mutex
	polls  int
	copied string
}

func (f *fakePhotos) NewPicking(context.Context) (google.PickerSession, error) {
	return google.PickerSession{ID: "s1", URI: "https://photos.google.com/picker/s1"}, nil
}
func (f *fakePhotos) Picking(context.Context, string) (google.PickerSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	return google.PickerSession{ID: "s1", Done: f.polls >= 1}, nil
}
func (f *fakePhotos) CopyPicked(_ context.Context, id, album string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.copied = id + " → " + album
	return 3, nil
}

// TestAddPhotosShowsTheCodeThenSaysHowMany: the QR code on her screens at once, the copy after Done, the note.
func TestAddPhotosShowsTheCodeThenSaysHowMany(t *testing.T) {
	f := &fakePhotos{}
	var mu sync.Mutex
	var states []string
	var noted string
	tool := pickTool(f, func(kind string, d map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		states = append(states, d["state"].(string))
		if d["state"] == "open" && !strings.HasPrefix(d["png"].(string), "data:image/png;base64,") {
			t.Errorf("no QR code: %v", d)
		}
	}, func(_, text string) { mu.Lock(); noted = text; mu.Unlock() })
	st, text := tool.Call(context.Background(), map[string]any{})
	if st != "done" || !strings.Contains(text, "Nina's gallery") {
		t.Fatalf("%s %s", st, text)
	}
	for i := 0; i < 80; i++ {
		mu.Lock()
		n := noted
		mu.Unlock()
		if n != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(states, ",") != "open,copying,done" || noted != "I added 3 photos to Nina's gallery." || f.copied != "s1 → Nina's gallery" {
		t.Fatalf("states %v, noted %q, copied %q", states, noted, f.copied)
	}
}
