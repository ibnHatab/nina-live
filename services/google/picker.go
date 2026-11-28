package google

// The Google Photos Picker (the user, 2026-11-28: "something like a picker in the office settings"): the person
// opens Google's own picker (a link: on a computer, or her screen's QR code), chooses photos from the whole
// library, and Nina copies them into her own album. The copy is needed: the Library API shows an app only the
// photos it uploaded (per photo, not per album: a photo added to her album in Google Photos' own pages stays
// invisible to her), and a picked item is readable only while its session lives, through base URLs that last
// an hour. Scope: https://www.googleapis.com/auth/photospicker.mediaitems.readonly.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// PickerSession is one picking: the link the person opens, and whether they have finished.
type PickerSession struct {
	ID, URI string
	Done    bool
	Expires time.Time
}

func (c *Client) pickerURL() string {
	if c.PickerURL != "" {
		return c.PickerURL
	}
	return "https://photospicker.googleapis.com/v1"
}

func (c *Client) session(ctx context.Context, method, u string) (PickerSession, error) {
	var r struct {
		ID            string `json:"id"`
		PickerURI     string `json:"pickerUri"`
		MediaItemsSet bool   `json:"mediaItemsSet"`
		ExpireTime    string `json:"expireTime"`
	}
	var body any
	if method == "POST" {
		body = map[string]any{}
	}
	if err := c.call(ctx, method, u, body, &r); err != nil {
		return PickerSession{}, err
	}
	s := PickerSession{ID: r.ID, URI: r.PickerURI, Done: r.MediaItemsSet}
	s.Expires, _ = time.Parse(time.RFC3339, r.ExpireTime)
	return s, nil
}

// NewPicking opens a picking session: its URI is what the person opens.
func (c *Client) NewPicking(ctx context.Context) (PickerSession, error) {
	return c.session(ctx, "POST", c.pickerURL()+"/sessions")
}

// Picking is the session now (Done once the person pressed Done in the picker).
func (c *Client) Picking(ctx context.Context, id string) (PickerSession, error) {
	return c.session(ctx, "GET", c.pickerURL()+"/sessions/"+url.PathEscape(id))
}

// Picked is one chosen photo.
type Picked struct {
	ID, Name, URL, MimeType string
}

// PickedItems are a finished session's photos.
func (c *Client) PickedItems(ctx context.Context, id string) ([]Picked, error) {
	var out []Picked
	tok := ""
	for i := 0; i < 50; i++ {
		u := c.pickerURL() + "/mediaItems?pageSize=100&sessionId=" + url.QueryEscape(id)
		if tok != "" {
			u += "&pageToken=" + url.QueryEscape(tok)
		}
		var r struct {
			MediaItems []struct {
				ID        string `json:"id"`
				Type      string `json:"type"`
				MediaFile struct {
					BaseURL  string `json:"baseUrl"`
					MimeType string `json:"mimeType"`
					Filename string `json:"filename"`
				} `json:"mediaFile"`
			} `json:"mediaItems"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := c.call(ctx, "GET", u, nil, &r); err != nil {
			return nil, err
		}
		for _, m := range r.MediaItems {
			if m.Type == "PHOTO" || strings.HasPrefix(m.MediaFile.MimeType, "image/") {
				out = append(out, Picked{ID: m.ID, Name: m.MediaFile.Filename, URL: m.MediaFile.BaseURL, MimeType: m.MediaFile.MimeType})
			}
		}
		if tok = r.NextPageToken; tok == "" {
			break
		}
	}
	return out, nil
}

// Download is a picked photo's bytes (its base URL with =d: the original), with her token.
func (c *Client) Download(ctx context.Context, p Picked) ([]byte, error) {
	tok, err := c.token(ctx)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", p.URL+"=d", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("google: the picked photo %s: %s", p.Name, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// EndPicking deletes the session (Google asks for it once its items are taken).
func (c *Client) EndPicking(ctx context.Context, id string) error {
	return c.call(ctx, "DELETE", c.pickerURL()+"/sessions/"+url.PathEscape(id), nil, nil)
}

// CopyPicked puts a finished session's photos into her album (made if needed) and ends the session: the
// photos are then hers to show, for good. → how many were copied.
func (c *Client) CopyPicked(ctx context.Context, id, album string) (int, error) {
	items, err := c.PickedItems(ctx, id)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range items {
		b, err := c.Download(ctx, p)
		if err != nil {
			return n, err
		}
		name := p.Name
		if name == "" {
			name = p.ID + ".jpg"
		}
		if err := c.Upload(ctx, album, name, b); err != nil {
			return n, err
		}
		n++
	}
	return n, c.EndPicking(ctx, id)
}
