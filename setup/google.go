package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ibnHatab/nina-live/services/google"
)

// Service is one of the person's Google services she may be let use: by the account's sign-in (its scopes)
// or by an API key (YouTube's search, Gemini).
type Service struct {
	ID, Title, Why string
	Scopes         []string // the sign-in's permissions it needs
	APIs           []string // the APIs enabled in the project beyond what Google's personal setup enables
	Key            bool     // used with an API key
	Off            string   // why it cannot be chosen yet ("": it can)
}

const auth = "https://www.googleapis.com/auth/"

// Services are the Google services her one Google integration offers (the user, 2026-11-28: one card for
// Google, its services by the person's choice). Personal mode's page enables the Workspace APIs; Photos,
// YouTube and Gemini are enabled by the step after it.
var Services = []Service{
	{ID: "calendar", Title: "Calendar", Why: "your events, and the ones she adds", Scopes: []string{auth + "calendar.events"}},
	{ID: "tasks", Title: "Tasks", Why: "your lists and to-dos", Scopes: []string{auth + "tasks"}},
	{ID: "photos", Title: "Photos", Why: "her albums, and the photos you pick for her screen",
		Scopes: []string{auth + "photoslibrary.readonly.appcreateddata", auth + "photoslibrary.appendonly", auth + "photospicker.mediaitems.readonly"},
		APIs:   []string{"photoslibrary.googleapis.com", "photospicker.googleapis.com"}},
	{ID: "youtube", Title: "YouTube", Why: "videos found by search, on her screen", Key: true, APIs: []string{"youtube.googleapis.com"}},
	{ID: "gemini", Title: "Gemini", Why: "her voice, her ears and her tasks", Key: true, APIs: []string{"generativelanguage.googleapis.com"}},
	{ID: "gmail", Title: "Gmail", Why: "your mail", Off: "not yet: Google keeps sending mail for its partners"},
}

// Scopes are every service's permissions (nina-google-auth's sign-in: all of them).
var Scopes = func() []string {
	var out []string
	for _, sv := range Services {
		out = append(out, sv.Scopes...)
	}
	return out
}()

// Choose sets the services she may use (unknown ids or ones not offered yet: an error, nothing changed).
func (s *Setup) Choose(ids []string) error {
	var out []string
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			continue
		}
		sv, ok := service(id)
		if !ok {
			return fmt.Errorf("setup: no Google service %q", id)
		}
		if sv.Off != "" {
			return fmt.Errorf("setup: %s: %s", sv.Title, sv.Off)
		}
		out = append(out, id)
	}
	s.cmu.Lock()
	s.chosen = out
	s.cmu.Unlock()
	return nil
}

// Chosen: the services she may use.
func (s *Setup) Chosen() []string {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	return append([]string(nil), s.chosen...)
}

func service(id string) (Service, bool) {
	for _, sv := range Services {
		if sv.ID == id {
			return sv, true
		}
	}
	return Service{}, false
}

func (s *Setup) chose(id string) bool {
	for _, c := range s.Chosen() {
		if c == id {
			return true
		}
	}
	return false
}

// scopes: the chosen services' permissions
func (s *Setup) scopes() []string {
	var out []string
	for _, id := range s.Chosen() {
		sv, _ := service(id)
		out = append(out, sv.Scopes...)
	}
	return out
}

func (s *Setup) needsSignIn() bool { return len(s.scopes()) > 0 }
func (s *Setup) needsKey() bool    { return s.chose("youtube") || s.chose("gemini") }

// moreAPIs: the APIs the chosen services need beyond personal mode's (Photos, YouTube, Gemini)
func (s *Setup) moreAPIs() []string {
	var out []string
	for _, id := range s.Chosen() {
		sv, _ := service(id)
		out = append(out, sv.APIs...)
	}
	return out
}

func (s *Setup) servicesView() []map[string]any {
	var out []map[string]any
	for _, sv := range Services {
		e := map[string]any{"id": sv.ID, "title": sv.Title, "why": sv.Why, "on": s.chose(sv.ID)}
		if sv.Off != "" {
			e["off"] = sv.Off
		}
		out = append(out, e)
	}
	return out
}

// client is an OAuth client: Google Cloud's download (a Desktop app's "installed", a Web client's "web").
type client struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	AuthURI      string   `json:"auth_uri"`
	TokenURI     string   `json:"token_uri"`
	Redirects    []string `json:"redirect_uris"`
	kind         string
}

func parseClient(b []byte) (client, error) {
	var cf struct {
		Installed *client `json:"installed"`
		Web       *client `json:"web"`
	}
	if err := json.Unmarshal(b, &cf); err != nil {
		return client{}, errors.New("that is not an OAuth client's JSON (Google Cloud's Download JSON)")
	}
	var c client
	switch {
	case cf.Web != nil:
		c, c.kind = *cf.Web, "web"
	case cf.Installed != nil:
		c, c.kind = *cf.Installed, "desktop"
	default:
		return client{}, errors.New("that JSON has no OAuth client in it (\"web\" or \"installed\")")
	}
	if c.ClientID == "" || c.ClientSecret == "" {
		return client{}, errors.New("that client has no id or no secret")
	}
	return c, nil
}

// clients are the OAuth clients' files found (their names: no secret).
func (s *Setup) clients() []string {
	dir := expand(s.P.Clients)
	if dir == "" {
		return nil
	}
	es, _ := os.ReadDir(dir)
	var out []string
	for _, e := range es {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
			if _, err := parseClient(b); err == nil {
				out = append(out, e.Name())
			}
		}
	}
	sort.Strings(out)
	return out
}

// theClient: the client chosen or pasted ("file:NAME": one found), else the one kept (s.P.Client)
func (s *Setup) theClient(value string) (client, []byte, error) {
	var b []byte
	switch {
	case strings.HasPrefix(value, "file:"):
		name := filepath.Base(strings.TrimPrefix(value, "file:"))
		var err error
		if b, err = os.ReadFile(filepath.Join(expand(s.P.Clients), name)); err != nil {
			return client{}, nil, fmt.Errorf("no client %s", name)
		}
	case value != "":
		b = []byte(value)
	default:
		var err error
		if b, err = os.ReadFile(expand(s.P.Client)); err != nil {
			return client{}, nil, errors.New("no OAuth client yet")
		}
	}
	c, err := parseClient(b)
	return c, b, err
}

func (s *Setup) redirect() string { return fmt.Sprintf("http://localhost:%d/", s.SignInPort) }

// checkClient: an OAuth client given (or kept) that can do her sign-in.
func checkClient(_ context.Context, s *Setup, value string) Result {
	c, _, err := s.theClient(value)
	if err != nil {
		if value == "" {
			return Result{Detail: "no OAuth client kept yet"}
		}
		return Result{Detail: err.Error(), Hint: "That is not the client's file. On the client's page, press Download JSON, open the file, and paste all of it."}
	}
	r := Result{OK: true, Detail: "an OAuth client (" + c.kind + " app)", Say: "I found your OAuth client."}
	if c.kind == "web" {
		ok := false
		for _, u := range c.Redirects {
			ok = ok || strings.TrimRight(u, "/") == strings.TrimRight(s.redirect(), "/")
		}
		r.Detail += ", redirect " + s.redirect()
		if !ok && len(c.Redirects) > 0 {
			r.Detail += " not among its redirects (as downloaded; add it if Google says redirect_uri_mismatch)"
			r.Say = "I found your OAuth client. Google will need localhost, port 8765, among its redirects: add it on the client's page, then press Next."
		}
	}
	return r
}

// saveClient keeps the client pasted or chosen at s.P.Client.
func saveClient(s *Setup, value string) error {
	_, b, err := s.theClient(value)
	if err != nil {
		return err
	}
	return writeFile(s.P.Client, b)
}

type signin struct {
	url, redirect, state string
	srv                  *http.Server
}

// startSignIn listens on the loopback port for Google's redirect and puts the sign-in's page on her
// screens (the person opens it in their own browser, on this machine: localhost).
func (s *Setup) startSignIn(in *Integration, n int) error {
	c, _, err := s.theClient("")
	if err != nil {
		return errors.New("sign-in: " + err.Error() + " (the step before keeps it)")
	}
	s.stopSignIn()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.SignInPort))
	if err != nil {
		return fmt.Errorf("sign-in: port %d is busy (nina-google-auth running?): %w", s.SignInPort, err)
	}
	sb := make([]byte, 16)
	rand.Read(sb)
	si := &signin{redirect: s.redirect(), state: hex.EncodeToString(sb)}
	auth := s.E.GoogleAuth
	if c.AuthURI != "" && s.E.GoogleAuth == Google().GoogleAuth {
		auth = c.AuthURI
	}
	si.url = auth + "?" + query("client_id", c.ClientID, "redirect_uri", si.redirect, "response_type", "code",
		"scope", strings.Join(s.scopes(), " "), "access_type", "offline", "prompt", "consent", "state", si.state)
	si.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" || r.URL.Query().Get("state") != si.state {
			http.Error(w, "not this sign-in", 400)
			return
		}
		msg := s.signedIn(c, si, r.URL.Query())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><meta name=viewport content='width=device-width'><body style='font:18px system-ui;padding:2em'>%s<p>You can close this tab: Nina goes on.</body>", htmlEscape(msg))
		go func() { time.Sleep(500 * time.Millisecond); s.stopSignIn() }()
	}), ReadHeaderTimeout: 10 * time.Second}
	s.mu.Lock()
	s.signin = si
	s.mu.Unlock()
	go si.srv.Serve(ln)
	go func() { // no sign-in within 15 minutes: closed
		time.Sleep(15 * time.Minute)
		s.mu.Lock()
		same := s.signin == si
		s.mu.Unlock()
		if same {
			s.stopSignIn()
			s.changed()
		}
	}()
	s.log("setup", map[string]any{"verb": "signin", "integration": in.ID, "step": in.Steps[n].ID})
	return nil
}

func (s *Setup) stopSignIn() {
	s.mu.Lock()
	si := s.signin
	s.signin = nil
	s.mu.Unlock()
	if si != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		si.srv.Shutdown(ctx)
		cancel()
	}
}

// signedIn: Google's redirect: the code exchanged for the refresh token, kept, and checked.
func (s *Setup) signedIn(c client, si *signin, q url.Values) string {
	r := s.exchange(c, si, q)
	s.mu.Lock()
	if st := s.at; st != nil {
		if in, ok := s.find(st.ID); ok && st.Step < len(in.Steps) && in.Steps[st.Step].Input == "signin" {
			st.Result = &r
		}
	}
	if r.OK {
		s.restart = true
	}
	s.mu.Unlock()
	s.log("setup", map[string]any{"verb": "signed_in", "ok": r.OK, "detail": r.Detail})
	s.changed()
	if r.OK {
		s.say("You are signed in, and your calendar, tasks and photos answer me. Come back to my settings and press Next.")
		return "Signed in: " + r.Detail + "."
	}
	if r.Hint != "" {
		s.say(r.Hint)
	}
	return "Not signed in: " + r.Detail + "."
}

func (s *Setup) exchange(c client, si *signin, q url.Values) Result {
	if e := q.Get("error"); e != "" {
		h := "Google stopped the sign-in. Start it again, and allow every box on the consent page."
		if e == "access_denied" {
			h = "The sign-in was refused. If Google said the app is not verified, press Advanced, then Go to the app; if the account is not a test user, add it on the Audience page or publish the app."
		}
		return Result{Detail: "Google said " + e, Hint: h}
	}
	tokenURL := s.E.GoogleToken
	if c.TokenURI != "" && s.E.GoogleToken == Google().GoogleToken {
		tokenURL = c.TokenURI
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	form := url.Values{"code": {q.Get("code")}, "client_id": {c.ClientID}, "client_secret": {c.ClientSecret},
		"redirect_uri": {si.redirect}, "grant_type": {"authorization_code"}}
	req, _ := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return Result{Detail: "Google's token service is not reachable: " + err.Error()}
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
		return Result{Detail: fmt.Sprintf("no refresh token (%s %s)", t.Error, t.Desc), Hint: "Google gave no lasting sign-in. Start the sign-in again."}
	}
	var lacking []string
	for _, sc := range s.scopes() {
		if !strings.Contains(" "+t.Scope+" ", " "+sc+" ") {
			lacking = append(lacking, strings.TrimPrefix(sc, "https://www.googleapis.com/auth/"))
		}
	}
	if len(lacking) > 0 {
		return Result{Detail: "not allowed: " + strings.Join(lacking, ", "),
			Hint: "Some boxes on Google's consent page were not ticked. Sign in again and tick all of them."}
	}
	tok, _ := json.Marshal(google.Token{ClientID: c.ClientID, ClientSecret: c.ClientSecret, RefreshToken: t.Refresh})
	if err := writeFile(s.P.Google, tok); err != nil {
		return Result{Detail: "signed in, but the token could not be kept: " + err.Error()}
	}
	return s.googleAPIs(ctx, google.Token{ClientID: c.ClientID, ClientSecret: c.ClientSecret, RefreshToken: t.Refresh})
}

// checkGoogle: her token kept, and each of Google's APIs she uses answers with it.
func checkGoogle(ctx context.Context, s *Setup, _ string) Result {
	b, err := os.ReadFile(expand(s.P.Google))
	if err != nil {
		if s.P.Google == "" {
			return missing("the Google token", "")
		}
		return Result{Detail: "not signed in yet"}
	}
	var t google.Token
	if json.Unmarshal(b, &t) != nil || t.RefreshToken == "" {
		return Result{Detail: "the token file has no sign-in in it", Hint: "Sign in again."}
	}
	return s.googleAPIs(ctx, t)
}

// googleAPIs: Calendar, Tasks and Photos called with the token (what each said).
func (s *Setup) googleAPIs(ctx context.Context, t google.Token) Result {
	g := google.New(t)
	g.HTTP = s.HTTP
	if s.E.GoogleToken != Google().GoogleToken {
		g.TokenURL = s.E.GoogleToken
	}
	if e := strings.TrimRight(s.E.Google, "/"); e != "" {
		g.CalendarURL, g.TasksURL, g.PhotosURL, g.PickerURL = e+"/calendar", e+"/tasks", e+"/photos", e+"/picker"
	}
	var ok, bad []string
	try := func(name string, f func() error) {
		if err := f(); err != nil {
			m := err.Error()
			if len(m) > 120 {
				m = m[:120] + "…"
			}
			bad = append(bad, name+" ("+m+")")
		} else {
			ok = append(ok, name)
		}
	}
	if _, err := g.TaskLists(ctx); err != nil && strings.Contains(err.Error(), "invalid_grant") { // the token's refresh: any call tells
		return Result{Detail: "Google no longer accepts the sign-in (invalid_grant)",
			Hint: "Google has ended that sign-in: in Testing it lasts seven days. Publish the app on the Audience page, then sign in again."}
	}
	if s.chose("calendar") {
		try("calendar", func() error { _, err := g.Events(ctx, "primary", 24*time.Hour, 1); return err })
	}
	if s.chose("tasks") {
		try("tasks", func() error { _, err := g.TaskLists(ctx); return err })
	}
	if s.chose("photos") {
		try("photos", func() error { _, err := g.Albums(ctx); return err })
	}
	if len(bad) > 0 {
		r := Result{Detail: "works: " + strings.Join(ok, ", ") + "; fails: " + strings.Join(bad, "; ")}
		r.Hint = "Some of Google's services refused me. Enable their APIs in the project, then check again."
		if len(ok) == 0 {
			r.Detail = "fails: " + strings.Join(bad, "; ")
		}
		return r
	}
	return Result{OK: true, Detail: "my Google account works: " + strings.Join(ok, ", ")}
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
