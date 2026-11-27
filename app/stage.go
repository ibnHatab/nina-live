package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/ibnHatab/nina-live/tasks"
	"github.com/ibnHatab/nina-live/youtube"
)

// screenTool: her screen's stage (acasa ha-ui: the Videos and Photos cards fly out large): the videos
// (her playlist; a search's videos, from any channel; one of her playlists by name; a public one),
// the photos (an album by name), or close. What it found goes to her screens as a "stage" event
// (videos: the ids, in order, ⏮ ⏭ between them; playlist: an id; album: a name).
//
// albums lists the Google Photos albums there are (nil: not checked): an album asked for by name is
// looked up first, so she knows when it is not there (the screen alone had said "no album").
func screenTool(yt *youtube.Client, albums func(context.Context) ([]string, error), stage func(map[string]any)) tasks.Tool {
	return tasks.Tool{Name: "screen", Domain: "screen",
		Description: "Show something large on Nina's screen, over its right side, until it is closed: videos (her own playlist; " +
			"or a YouTube search, from any channel: query; or a playlist by its name: playlist), photos (the gallery, or an album " +
			"by its name: album), or close what is shown; while something is shown: next, previous, pause or play on it (the next " +
			"video or photo: next, not home_media). Only when the person asks to see videos or photos, to step through or pause " +
			"them, or to close them.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"show": map[string]any{"type": "string", "enum": []any{"videos", "photos", "close", "next", "previous", "pause", "play"},
				"description": "videos, photos, close; or next, previous, pause, play on what is shown"},
			"query":    map[string]any{"type": "string", "description": "videos: what to search YouTube for (a topic, a channel, a song)"},
			"playlist": map[string]any{"type": "string", "description": "videos: a playlist's name (hers, or a public one)"},
			"album":    map[string]any{"type": "string", "description": "photos: an album's name"},
		}, "required": []any{"show"}},
		Call: func(ctx context.Context, a map[string]any) (string, string) {
			str := func(k string) string { s, _ := a[k].(string); return strings.TrimSpace(s) }
			switch str("show") {
			case "close":
				stage(map[string]any{"show": "close"})
				return "done", "closed"
			case "next", "previous", "pause", "play": // on what the stage shows (its ⏭ ⏮ ⏸ ▶)
				stage(map[string]any{"show": str("show")})
				return "done", map[string]string{"next": "the next one is on the screen", "previous": "the previous one is on the screen",
					"pause": "paused", "play": "playing"}[str("show")]
			case "photos":
				ev := map[string]any{"show": "photos"}
				al := str("album")
				if al != "" && albums != nil {
					have, err := albums(ctx)
					switch {
					case err != nil: // not known: the screen looks for it, and says when it is not there
					case findAlbum(have, al) == "":
						if len(have) == 0 {
							return "done", "there is no album " + al + ": Google Photos has none you can show (you see only the albums you made yourself); nothing was opened"
						}
						return "done", "there is no album " + al + "; the albums there are: " + strings.Join(have, ", ") + "; nothing was opened"
					default:
						al = findAlbum(have, al)
					}
				}
				if al != "" {
					ev["album"] = al
				}
				stage(ev)
				return "done", "the photos are on the screen" + map[bool]string{true: " (the album " + al + ")", false: ""}[al != ""] +
					"; the person can close them with ✕"
			case "videos":
				ev := map[string]any{"show": "videos"}
				what := "her playlist"
				switch {
				case str("query") != "":
					vs, err := yt.Search(ctx, str("query"), 10)
					if err != nil {
						return "error", err.Error()
					}
					if len(vs) == 0 {
						return "done", "no video found for " + str("query")
					}
					var ids []string
					for _, v := range vs {
						ids = append(ids, v.ID)
					}
					ev["videos"], ev["title"] = ids, str("query")
					what = fmt.Sprintf("%q by %s (%d found for %q; next and previous go through them)", vs[0].Title, vs[0].Channel, len(vs), str("query"))
				case str("playlist") != "":
					id, title, err := yt.Playlist(ctx, str("playlist"))
					if err != nil {
						return "error", err.Error()
					}
					ev["playlist"], ev["title"] = id, title
					what = "the playlist " + title
				}
				stage(ev)
				return "done", "on the screen: " + what + "; the person can close it with ✕"
			}
			return "error", "show: videos, photos or close"
		}}
}

// albumKey: an album's name as said ("Nina's Gallery", "ninas gallery") and as titled match.
func albumKey(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		switch r {
		case '\'', '’', '‘', '`':
			return -1
		}
		return r
	}, strings.ToLower(s))), " ")
}

// findAlbum: the album's own title for a name said, or "".
func findAlbum(albums []string, said string) string {
	k := albumKey(said)
	for _, a := range albums {
		if albumKey(a) == k {
			return a
		}
	}
	for _, a := range albums { // "the gallery" for "Nina's gallery": one album containing what was said
		if k != "" && strings.Contains(albumKey(a), k) {
			return a
		}
	}
	return ""
}
