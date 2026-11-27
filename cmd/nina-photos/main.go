// nina-photos: Nina's photo albums from a computer (the user, 2026-11-28: her own albums, filled from the PC,
// shown by any of her instances). Google's Photos Library API shows an app only what that app uploaded, and
// the app is the OAuth client: an upload made here, with the token of her client (nina-google-auth), is hers
// to show on every machine signed in with the same client and Google account (a Pi's Nina: the same token
// file, or its own sign-in with the same client). An upload through photos.google.com is Google's, not hers.
//
//	nina-photos albums                                   her albums and their photo counts
//	nina-photos upload -album "Nina's gallery" FILE...    photos into her album (made if she has none of that name)
//	nina-photos list -album "Nina's gallery"              the album's photos
//
// -token the credential file (default ~/.config/nina/google-token.json, chmod 600).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ibnHatab/nina-live/services/google"
)

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[2:])
	}
	return p
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: nina-photos albums | upload -album NAME FILE... | list -album NAME  [-token FILE]")
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	token := fs.String("token", "~/.config/nina/google-token.json", "her credential file (nina-google-auth)")
	album := fs.String("album", "Nina's gallery", "the album")
	fs.Parse(os.Args[2:])
	c, err := google.Load(expand(*token))
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	switch cmd {
	case "albums":
		as, err := c.Albums(ctx)
		if err != nil {
			log.Fatal(err)
		}
		if len(as) == 0 {
			fmt.Println("no albums of hers yet (upload makes one)")
		}
		for _, a := range as {
			fmt.Printf("%-30s %d photos\n", a.Title, a.Count)
		}
	case "upload":
		if fs.NArg() == 0 {
			log.Fatal("no files")
		}
		for _, f := range fs.Args() {
			b, err := os.ReadFile(f)
			if err != nil {
				log.Fatal(err)
			}
			if err := c.Upload(ctx, *album, filepath.Base(f), b); err != nil {
				log.Fatalf("%s: %v", f, err)
			}
			fmt.Printf("%s → %s\n", filepath.Base(f), *album)
		}
	case "list":
		as, err := c.Albums(ctx)
		if err != nil {
			log.Fatal(err)
		}
		for _, a := range as {
			if strings.EqualFold(a.Title, *album) {
				ps, err := c.Photos(ctx, a.ID, 1000)
				if err != nil {
					log.Fatal(err)
				}
				for _, p := range ps {
					fmt.Println(p.Name)
				}
				return
			}
		}
		log.Fatalf("no album %q of hers", *album)
	default:
		log.Fatalf("no command %s", cmd)
	}
}
