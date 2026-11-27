package app

import (
	"context"
	"encoding/base64"
	"fmt"
	stdlog "log"
	"time"

	"github.com/ibnHatab/nina-live/services/google"
	"github.com/ibnHatab/nina-live/tasks"
	"github.com/skip2/go-qrcode"
)

// Photos is her photo albums on Google Photos with the Picker (services/google): the person picks photos from
// the whole library, and she copies them into her own album, hers for good (the user, 2026-11-28: "we scan the
// code, give the picker, and Nina relocates photos to her own album").
type Photos interface {
	NewPicking(ctx context.Context) (google.PickerSession, error)
	Picking(ctx context.Context, id string) (google.PickerSession, error)
	CopyPicked(ctx context.Context, id, album string) (int, error)
}

// pickTool: add_photos. It opens a picking and puts its link on her screens as a QR code (event photos_pick),
// answers at once, waits in the background for the person's Done (or the session's end), copies the picked
// photos into the album and says how many as a note.
func pickTool(p Photos, show func(kind string, d map[string]any), note func(level, text string)) tasks.Tool {
	return tasks.Tool{Name: "add_photos", Domain: "photos",
		Description: "Add photos to one of Nina's albums from the person's Google Photos: shows a QR code (and its link) " +
			"on her screen; the person scans it, picks photos and presses Done; she copies them into the album and says how many. " +
			"Photos a friend shared as an album with Nina's Google account are picked the same way (accepted first in Google "+
			"Photos; the copies stay when the friend stops sharing). Only when the person asks to add or bring photos.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"album": map[string]any{"type": "string", "description": "the album's name (default Nina's gallery)"}}},
		Call: func(ctx context.Context, a map[string]any) (string, string) {
			album, _ := a["album"].(string)
			if album == "" {
				album = "Nina's gallery"
			}
			s, err := p.NewPicking(ctx)
			if err != nil {
				return "error", err.Error()
			}
			png, err := qrcode.Encode(s.URI, qrcode.Medium, 320)
			if err != nil {
				return "error", err.Error()
			}
			show("photos_pick", map[string]any{"state": "open", "album": album, "uri": s.URI,
				"png": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)})
			go waitPicking(p, s, album, show, note)
			return "done", fmt.Sprintf("The QR code for adding photos to %s is on my screen (the link: %s). Scan it, pick the photos and press Done; I'll copy them and say when.", album, s.URI)
		}}
}

func waitPicking(p Photos, s google.PickerSession, album string, show func(string, map[string]any), note func(string, string)) {
	end := s.Expires
	if end.IsZero() || time.Until(end) > 30*time.Minute {
		end = time.Now().Add(30 * time.Minute)
	}
	for time.Now().Before(end) {
		time.Sleep(5 * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		cur, err := p.Picking(ctx, s.ID)
		cancel()
		if err != nil {
			stdlog.Printf("[photos] the picking: %v", err)
			continue
		}
		if !cur.Done {
			continue
		}
		show("photos_pick", map[string]any{"state": "copying", "album": album})
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Minute)
		n, err := p.CopyPicked(ctx, s.ID, album)
		cancel()
		if err != nil {
			show("photos_pick", map[string]any{"state": "failed", "album": album, "copied": n, "error": err.Error()})
			note("important", fmt.Sprintf("I copied %d of the photos into %s, then it failed: %v.", n, album, err))
			return
		}
		show("photos_pick", map[string]any{"state": "done", "album": album, "copied": n})
		note("important", fmt.Sprintf("I added %d photos to %s.", n, album))
		return
	}
	show("photos_pick", map[string]any{"state": "expired", "album": album})
}
