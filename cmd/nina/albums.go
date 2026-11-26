package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ibnHatab/acasa/hass"
)

// googleAlbums lists the Google Photos albums Home Assistant can show (its media source, as her
// photos card browses it: the albums the integration made; Google's rule since 2025). One short
// websocket session: auth, the source's accounts, each account's albums.
func googleAlbums(ctx context.Context, ha *hass.Client) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	u := strings.Replace(strings.Replace(ha.URL, "https://", "wss://", 1), "http://", "ws://", 1) + "/api/websocket"
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u, nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	go func() { <-ctx.Done(); conn.Close() }()
	conn.SetReadLimit(1 << 24)
	type node struct {
		Title    string `json:"title"`
		ID       string `json:"media_content_id"`
		Children []node `json:"children"`
	}
	var m struct {
		Type    string                   `json:"type"`
		ID      int                      `json:"id"`
		Success bool                     `json:"success"`
		Result  node                     `json:"result"`
		Error   struct{ Message string } `json:"error"`
	}
	if err := conn.ReadJSON(&m); err != nil {
		return nil, err
	}
	if err := conn.WriteJSON(map[string]string{"type": "auth", "access_token": ha.Token}); err != nil {
		return nil, err
	}
	if err := conn.ReadJSON(&m); err != nil {
		return nil, err
	}
	if m.Type != "auth_ok" {
		return nil, fmt.Errorf("home assistant: %s", m.Type)
	}
	id := 0
	browse := func(mid string) (node, error) {
		id++
		if err := conn.WriteJSON(map[string]any{"id": id, "type": "media_source/browse_media", "media_content_id": mid}); err != nil {
			return node{}, err
		}
		for {
			m.Result, m.Success, m.Error.Message = node{}, false, ""
			if err := conn.ReadJSON(&m); err != nil {
				return node{}, err
			}
			if m.Type == "result" && m.ID == id {
				if !m.Success {
					return node{}, fmt.Errorf("google photos: %s", m.Error.Message)
				}
				return m.Result, nil
			}
		}
	}
	root, err := browse("media-source://google_photos")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, acct := range root.Children {
		al, err := browse(acct.ID)
		if err != nil {
			return nil, err
		}
		for _, a := range al.Children {
			out = append(out, a.Title)
		}
	}
	return out, nil
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
