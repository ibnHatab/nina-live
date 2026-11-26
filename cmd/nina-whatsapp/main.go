// nina-whatsapp: Nina's WhatsApp channel, set up by hand (nina-live whatsapp/, 2026-11-26).
//
//	nina-whatsapp link PHONE    her number (+40…): the 8-character code to enter on that phone, in
//	                            WhatsApp → Linked devices → Link a device → Link with phone number
//	                            instead; waits until it is linked
//	nina-whatsapp link-qr       a QR code to scan with that phone (Linked devices → Link a device), as a
//	                            page that follows its new codes: http://HOST:8770/nina/wa-link.html
//	                            (-out: the folder her panel serves at /nina/)
//	nina-whatsapp groups        the groups her number is in (the card's whatsapp.show)
//	nina-whatsapp status        linked or not
//
// While Nina runs with whatsapp.enabled, she holds the connection: stop her (or disable it) first.
// [-store FILE] (default ~/.local/share/nina/whatsapp.db)
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/ibnHatab/nina-live/whatsapp"
	"github.com/skip2/go-qrcode"
)

func main() {
	home, _ := os.UserHomeDir()
	store := flag.String("store", filepath.Join(home, ".local/share/nina/whatsapp.db"), "the channel's SQLite file")
	out := flag.String("out", filepath.Join(home, "repo/acasa/ha-ui/www/nina"), "link-qr: where the page goes (her panel's /nina/)")
	debug := flag.Bool("debug", false, "whatsmeow's own log")
	flag.Parse()
	ctx := context.Background()
	c, err := whatsapp.Open(ctx, whatsapp.Config{Store: *store, Debug: *debug})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer c.Close()
	switch flag.Arg(0) {
	case "status":
		fmt.Println("linked:", c.LoggedIn())
	case "link":
		if c.LoggedIn() {
			fmt.Println("already linked")
			return
		}
		if flag.Arg(1) == "" {
			fmt.Fprintln(os.Stderr, "nina-whatsapp link PHONE (+40…)")
			os.Exit(2)
		}
		code, done, err := c.Link(ctx, flag.Arg(1))
		if err != nil {
			fmt.Fprintln(os.Stderr, "link:", err)
			os.Exit(1)
		}
		fmt.Printf("On the phone of %s: WhatsApp → Settings → Linked devices → Link a device → Link with phone number instead, and enter:\n\n    %s\n\nwaiting (the code lasts about 2.5 min)…\n", flag.Arg(1), code)
		select {
		case r := <-done:
			if r != "success" {
				fmt.Fprintln(os.Stderr, "not linked:", r, "(run it again for a new code)")
				os.Exit(1)
			}
			for i := 0; i < 15 && !c.LoggedIn(); i++ {
				time.Sleep(time.Second)
			}
			fmt.Println("linked:", c.LoggedIn())
			time.Sleep(5 * time.Second) // the first sync settles
		case <-time.After(4 * time.Minute):
			fmt.Fprintln(os.Stderr, "not linked in 4 min: run it again for a new code")
			os.Exit(1)
		}
	case "link-qr":
		if c.LoggedIn() {
			fmt.Println("already linked")
			return
		}
		qr, err := c.LinkQR(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "link-qr:", err)
			os.Exit(1)
		}
		page, png := filepath.Join(*out, "wa-link.html"), filepath.Join(*out, "wa-link.png")
		defer os.Remove(png)
		write := func(body string) {
			os.WriteFile(page, []byte(`<!DOCTYPE html><meta charset="utf-8"><meta http-equiv="refresh" content="2"><title>Nina: link WhatsApp</title>
<body style="margin:0;background:#14100e;color:#eee;font:18px system-ui;display:grid;place-items:center;height:100vh;text-align:center">
<div>`+body+`</div>`), 0o644)
		}
		n := 0
		for it := range qr {
			switch it.Event {
			case "code":
				n++
				if err := qrcode.WriteFile(it.Code, qrcode.Medium, 512, png); err != nil {
					fmt.Fprintln(os.Stderr, "qr:", err)
					os.Exit(1)
				}
				write(fmt.Sprintf(`<p>On Nina's phone: WhatsApp → Settings → Linked devices → Link a device, and scan:</p>
<img src="wa-link.png?%d" width="420" height="420" style="background:#fff;padding:12px;border-radius:12px"><p style="opacity:.6">code %d · a new one every ~20 s</p>`, n, n))
				fmt.Printf("code %d on the page (%s)\n", n, page)
			case "success":
				write(`<p>Linked. This page can be closed.</p>`)
				for i := 0; i < 15 && !c.LoggedIn(); i++ {
					time.Sleep(time.Second)
				}
				fmt.Println("linked:", c.LoggedIn())
				time.Sleep(5 * time.Second)
				go func() { time.Sleep(time.Minute); os.Remove(page) }()
				return
			default:
				write(`<p>Not linked (` + it.Event + `): run it again.</p>`)
				fmt.Fprintln(os.Stderr, "not linked:", it.Event, it.Error)
				os.Exit(1)
			}
		}
	case "groups":
		if err := c.Connect(ctx); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		time.Sleep(4 * time.Second)
		gs := c.Groups(ctx)
		var names []string
		for j, n := range gs {
			names = append(names, fmt.Sprintf("%-40s %s", n, j))
		}
		sort.Strings(names)
		for _, l := range names {
			fmt.Println(l)
		}
		if len(names) == 0 {
			fmt.Println("in no group yet: add her number to one")
		}
	default:
		fmt.Fprintln(os.Stderr, "nina-whatsapp link PHONE | groups | status")
		os.Exit(2)
	}
}
