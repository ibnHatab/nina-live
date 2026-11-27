// nina-google-auth: the one sign-in that gives Nina her Google agenda without Home Assistant (the redesign's
// Google services, s-b478fe59). The person runs it on a desktop with a browser; it is never run for them.
//
//	nina-google-auth -client ~/Downloads/client_secret_….json [-out ~/.config/nina/google-token.json]
//
// The client file is the person's own OAuth client of type Desktop app (Google Cloud console: APIs &
// Services, Credentials), its project with the Calendar and Tasks APIs enabled and its consent screen
// published (in Testing, Google expires the refresh token in 7 days). It prints the sign-in URL, waits on
// a loopback port for Google's redirect, exchanges the code and writes the client id, its secret and the
// refresh token to -out (chmod 600). Nothing is printed of the tokens. Put the file on another machine as
// the other keys are (skill nina-cred-google).
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const scopes = "https://www.googleapis.com/auth/calendar.events https://www.googleapis.com/auth/tasks"

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[2:])
	}
	return p
}

func main() {
	client := flag.String("client", "", "the OAuth client's JSON (type Desktop app)")
	out := flag.String("out", "~/.config/nina/google-token.json", "the token file to write (chmod 600)")
	flag.Parse()
	b, err := os.ReadFile(expand(*client))
	if err != nil {
		log.Fatalf("the client file: %v", err)
	}
	var cf struct {
		Installed struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			AuthURI      string `json:"auth_uri"`
			TokenURI     string `json:"token_uri"`
		} `json:"installed"`
	}
	if err := json.Unmarshal(b, &cf); err != nil || cf.Installed.ClientID == "" {
		log.Fatalf("the client file is not a Desktop app's (its JSON has no \"installed\" client): %v", err)
	}
	c := cf.Installed
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	redirect := "http://" + ln.Addr().String() + "/"
	sb := make([]byte, 16)
	rand.Read(sb)
	state := hex.EncodeToString(sb)
	q := url.Values{"client_id": {c.ClientID}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {scopes},
		"access_type": {"offline"}, "prompt": {"consent"}, "state": {state}}
	fmt.Printf("Open this in your browser and sign in with the account whose calendar and tasks Nina keeps:\n\n%s?%s\n\n", c.AuthURI, q.Encode())
	codes := make(chan string, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "not this sign-in", 400)
			return
		}
		if e := r.URL.Query().Get("error"); e != "" {
			fmt.Fprintf(w, "Google said: %s. Close this tab.", e)
			codes <- ""
			return
		}
		fmt.Fprint(w, "Signed in. Close this tab; nina-google-auth writes the token.")
		codes <- r.URL.Query().Get("code")
	})}
	go srv.Serve(ln)
	var code string
	select {
	case code = <-codes:
	case <-time.After(10 * time.Minute):
		log.Fatal("no sign-in within 10 minutes")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	srv.Shutdown(ctx)
	cancel()
	if code == "" {
		os.Exit(1)
	}
	resp, err := http.PostForm(c.TokenURI, url.Values{"code": {code}, "client_id": {c.ClientID}, "client_secret": {c.ClientSecret},
		"redirect_uri": {redirect}, "grant_type": {"authorization_code"}})
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	var t struct {
		Refresh string `json:"refresh_token"`
		Scope   string `json:"scope"`
		Error   string `json:"error"`
		Desc    string `json:"error_description"`
	}
	json.NewDecoder(resp.Body).Decode(&t)
	if t.Refresh == "" {
		log.Fatalf("no refresh token: %s %s %s", resp.Status, t.Error, t.Desc)
	}
	for _, s := range strings.Fields(scopes) {
		if !strings.Contains(t.Scope, s) {
			log.Fatalf("the sign-in did not grant %s (tick it on Google's consent page)", s)
		}
	}
	p := expand(*out)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		log.Fatal(err)
	}
	f, _ := json.Marshal(map[string]string{"client_id": c.ClientID, "client_secret": c.ClientSecret, "refresh_token": t.Refresh})
	if err := os.WriteFile(p, f, 0o600); err != nil {
		log.Fatal(err)
	}
	os.Chmod(p, 0o600)
	fmt.Printf("Written: %s (chmod 600). Nina reads it with services.google.token.\n", p)
}
