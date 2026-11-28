package google

// Google Photos without Home Assistant (the redesign's Google services, s-b478fe59). Since 31 March 2025 the
// Photos Library API lets an app read only what that app put there: the albums it created and the photos it
// uploaded (scopes photoslibrary.readonly.appcreateddata, photoslibrary.appendonly). Home Assistant's Google
// Photos integration lives under the same rule: its photos card shows the albums the integration made. So Nina
// keeps her albums the same way: she creates them and uploads into them, then lists and shows them. Photos the
// person took elsewhere reach her through an upload (hers, or the Picker), not by reading the whole library.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Album is one of her albums.
type Album struct {
	ID, Title string
	Count     int
}

// Photo is one photo of an album: its URL is Google's (an hour's base URL; =w<width> sizes it).
type Photo struct {
	ID, Name, URL string
}

func (c *Client) photosURL() string {
	if c.PhotosURL != "" {
		return c.PhotosURL
	}
	return "https://photoslibrary.googleapis.com/v1"
}

// Albums are the albums she made (the only ones the API shows her since 2025).
func (c *Client) Albums(ctx context.Context) ([]Album, error) {
	var out []Album
	tok := ""
	for i := 0; i < 20; i++ {
		var r struct {
			Albums []struct {
				ID, Title       string
				MediaItemsCount string `json:"mediaItemsCount"`
			}
			NextPageToken string
		}
		u := c.photosURL() + "/albums?pageSize=50"
		if tok != "" {
			u += "&pageToken=" + url.QueryEscape(tok)
		}
		if err := c.call(ctx, "GET", u, nil, &r); err != nil {
			return nil, err
		}
		for _, a := range r.Albums {
			n := 0
			fmt.Sscan(a.MediaItemsCount, &n)
			out = append(out, Album{ID: a.ID, Title: a.Title, Count: n})
		}
		if tok = r.NextPageToken; tok == "" {
			break
		}
	}
	return out, nil
}

// AlbumTitles: the albums' names (her screen's tool: an album asked for by name).
func (c *Client) AlbumTitles(ctx context.Context) ([]string, error) {
	as, err := c.Albums(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, a := range as {
		out = append(out, a.Title)
	}
	return out, nil
}

// Photos are an album's photos (at most n).
func (c *Client) Photos(ctx context.Context, albumID string, n int) ([]Photo, error) {
	var out []Photo
	tok := ""
	for len(out) < n {
		body := map[string]any{"albumId": albumID, "pageSize": 100}
		if tok != "" {
			body["pageToken"] = tok
		}
		var r struct {
			MediaItems []struct {
				ID, Filename, BaseURL, MimeType string
			}
			NextPageToken string
		}
		if err := c.call(ctx, "POST", c.photosURL()+"/mediaItems:search", body, &r); err != nil {
			return nil, err
		}
		for _, m := range r.MediaItems {
			if strings.HasPrefix(m.MimeType, "image/") && len(out) < n {
				out = append(out, Photo{ID: m.ID, Name: m.Filename, URL: m.BaseURL})
			}
		}
		if tok = r.NextPageToken; tok == "" {
			break
		}
	}
	return out, nil
}

// Upload puts a photo into her album titled album (made if she has none of that name).
func (c *Client) Upload(ctx context.Context, album, name string, data []byte) error {
	as, err := c.Albums(ctx)
	if err != nil {
		return err
	}
	id := ""
	for _, a := range as {
		if strings.EqualFold(a.Title, album) {
			id = a.ID
		}
	}
	if id == "" {
		var r struct{ ID string }
		if err := c.call(ctx, "POST", c.photosURL()+"/albums", map[string]any{"album": map[string]any{"title": album}}, &r); err != nil {
			return err
		}
		id = r.ID
	}
	tok, err := c.token(ctx)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", c.photosURL()+"/uploads", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Goog-Upload-Protocol", "raw")
	req.Header.Set("X-Goog-Upload-File-Name", name)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("google: the photo's upload: %s", resp.Status)
	}
	body := map[string]any{"albumId": id, "newMediaItems": []any{map[string]any{"simpleMediaItem": map[string]any{"uploadToken": string(b), "fileName": name}}}}
	return c.call(ctx, "POST", c.photosURL()+"/mediaItems:batchCreate", body, nil)
}
