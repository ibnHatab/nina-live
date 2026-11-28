package stage

import (
	"context"
	"fmt"
	"time"

	"github.com/ibnHatab/nina-live/services/google"
)

// Her screens' slideshow off Home Assistant (the user, 2026-11-28: the cards move to her own panel and the
// tablet, the photos from her own Google albums): the card asks her panel for an album's photos (the
// action album_photos, its arg the album's name) and gets them as an event (album_photos: album, photos,
// or error), kept out of the log. Google's URLs last an hour: the card asks again before then.

// Library is her albums and their photos (services/google's Client).
type Library interface {
	Albums(ctx context.Context) ([]google.Album, error)
	Photos(ctx context.Context, albumID string, n int) ([]google.Photo, error)
}

// Gallery is her own album, the slideshow's when none is named.
const Gallery = "Nina's gallery"

// MaxPhotos: an album's photos in one slideshow (Google pages 100 at a time).
const MaxPhotos = 300

// AlbumPhotos: the photos of the album named (its own title or as said; "" her gallery), as URLs sized for
// a screen, and the album's own title.
func AlbumPhotos(ctx context.Context, lib Library, said string) (string, []string, error) {
	if said == "" {
		said = Gallery
	}
	as, err := lib.Albums(ctx)
	if err != nil {
		return "", nil, err
	}
	titles := make([]string, len(as))
	for i, a := range as {
		titles[i] = a.Title
	}
	t := FindAlbum(titles, said)
	if t == "" {
		return "", nil, fmt.Errorf("no album %q", said)
	}
	for _, a := range as {
		if a.Title != t {
			continue
		}
		ps, err := lib.Photos(ctx, a.ID, MaxPhotos)
		if err != nil {
			return t, nil, err
		}
		urls := make([]string, 0, len(ps))
		for _, p := range ps {
			if p.URL != "" {
				urls = append(urls, p.URL+"=w1600-h1600") // a base URL's size: at most 1600 px a side
			}
		}
		return t, urls, nil
	}
	return t, nil, nil
}

// Action is her panel's action album_photos: it answers at once and emits the album's photos (or why not)
// as the event album_photos. emit is her screens' event (the dialogue's Log).
func Action(lib Library, album string, emit func(kind string, data map[string]any)) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		t, urls, err := AlbumPhotos(ctx, lib, album)
		ev := map[string]any{"album": t, "asked": album}
		if err != nil {
			ev["error"] = err.Error()
		} else {
			ev["photos"] = urls
		}
		emit("album_photos", ev)
	}()
}
