// Package youtube finds videos for her screen's stage (nina-live, 2026-11-26; the user: YouTube from
// any channel, and her own playlists by name). The YouTube Data API v3 with an API key: search.list
// (100 of the day's 10,000 units a search) for videos that may be embedded, and for public playlists;
// her own (unlisted ones included) are named in her config, the key cannot list them.
package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Video is a search's result.
type Video struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Channel string `json:"channel"`
}

// Client searches with a key.
type Client struct {
	Key       string            // the API key (its file's contents)
	Playlists map[string]string // her own playlists: name → id
}

// Load reads the key's file.
func Load(keyFile string, playlists map[string]string) (*Client, error) {
	b, err := os.ReadFile(keyFile)
	if err != nil {
		return &Client{Playlists: playlists}, err
	}
	return &Client{Key: strings.TrimSpace(string(b)), Playlists: playlists}, nil
}

func (c *Client) get(ctx context.Context, q url.Values, v any) error {
	if c.Key == "" {
		return fmt.Errorf("youtube: no API key (youtube.key in her config)")
	}
	q.Set("key", c.Key)
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://www.googleapis.com/youtube/v3/search?"+q.Encode(), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("youtube: %s: %s", resp.Status, e.Error.Message)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// Search: up to n videos for the words that may be embedded (her player is YouTube's embedded one).
func (c *Client) Search(ctx context.Context, words string, n int) ([]Video, error) {
	var r struct {
		Items []struct {
			ID      struct{ VideoID string } `json:"id"`
			Snippet struct {
				Title        string `json:"title"`
				ChannelTitle string `json:"channelTitle"`
			} `json:"snippet"`
		} `json:"items"`
	}
	err := c.get(ctx, url.Values{"part": {"snippet"}, "type": {"video"}, "videoEmbeddable": {"true"}, "safeSearch": {"moderate"},
		"maxResults": {fmt.Sprint(n)}, "q": {words}}, &r)
	var out []Video
	for _, it := range r.Items {
		if it.ID.VideoID != "" {
			out = append(out, Video{ID: it.ID.VideoID, Title: it.Snippet.Title, Channel: it.Snippet.ChannelTitle})
		}
	}
	return out, err
}

// Playlist: a playlist's id by name: hers first (her config, a name contained either way, any case),
// then a public one found by search; its title.
func (c *Client) Playlist(ctx context.Context, name string) (id, title string, err error) {
	n := strings.ToLower(strings.TrimSpace(name))
	for k, v := range c.Playlists {
		if k := strings.ToLower(k); k == n || strings.Contains(n, k) || strings.Contains(k, n) {
			return v, name, nil
		}
	}
	var r struct {
		Items []struct {
			ID      struct{ PlaylistID string } `json:"id"`
			Snippet struct{ Title string }      `json:"snippet"`
		} `json:"items"`
	}
	if err := c.get(ctx, url.Values{"part": {"snippet"}, "type": {"playlist"}, "maxResults": {"1"}, "q": {name}}, &r); err != nil {
		return "", "", err
	}
	if len(r.Items) == 0 {
		return "", "", fmt.Errorf("no playlist %q", name)
	}
	return r.Items[0].ID.PlaylistID, r.Items[0].Snippet.Title, nil
}
