package setup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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

	"github.com/ibnHatab/nina-live/creds"
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
	if c.kind != "desktop" { // Google's personal setup makes a Desktop app client; a Web client (an older setup's) is not this step's
		if value == "" {
			return Result{Detail: "the client kept is a " + c.kind + " client, not the Desktop app client Google's personal setup makes",
				Hint: "On Google's personal setup page press Continue, then copy that client's ID and secret here."}
		}
		return Result{Detail: "that is a " + c.kind + " client; this step needs the Desktop app client Google's personal setup makes",
			Hint: "Copy the client ID and secret from Google's personal setup page (Desktop app), not from another client."}
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
	url, redirect, state, verifier string
	asked                          []string // the scopes this sign-in asked for
	srv                            *http.Server
}

// identity: the account's address in the sign-in's id_token (asked with openid email)
var identity = []string{"openid", "email"}

// askScopes: what a sign-in asks for. Signed in already with this client: only the chosen scopes not yet
// granted (Google adds them to the grant: include_granted_scopes), else all the chosen ones; the account's
// address each time.
func (s *Setup) askScopes(c client) []string {
	want := s.scopes()
	if t := s.store().Token(); t != nil && t.ClientID == c.ClientID && len(t.Scopes) > 0 {
		if m := creds.Missing(t.Scopes, want); len(m) > 0 {
			want = m
		}
	}
	return append(append([]string(nil), want...), identity...)
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
	sb, vb := make([]byte, 16), make([]byte, 32)
	rand.Read(sb)
	rand.Read(vb)
	si := &signin{redirect: s.redirect(), state: hex.EncodeToString(sb), verifier: base64.RawURLEncoding.EncodeToString(vb), asked: s.askScopes(c)}
	sum := sha256.Sum256([]byte(si.verifier))
	auth := s.E.GoogleAuth
	if c.AuthURI != "" && s.E.GoogleAuth == Google().GoogleAuth {
		auth = c.AuthURI
	}
	si.url = auth + "?" + query("client_id", c.ClientID, "redirect_uri", si.redirect, "response_type", "code",
		"scope", strings.Join(si.asked, " "), "access_type", "offline", "prompt", "consent", "include_granted_scopes", "true",
		"state", si.state, "code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]), "code_challenge_method", "S256")
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
		"redirect_uri": {si.redirect}, "grant_type": {"authorization_code"}, "code_verifier": {si.verifier}}
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
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
		Desc    string `json:"error_description"`
	}
	json.NewDecoder(resp.Body).Decode(&t)
	if t.Refresh == "" {
		return Result{Detail: fmt.Sprintf("no refresh token (%s %s)", t.Error, t.Desc), Hint: "Google gave no lasting sign-in. Start the sign-in again."}
	}
	granted := strings.Fields(t.Scope)
	old := s.store().Token()
	if old != nil && old.ClientID == c.ClientID { // include_granted_scopes: Google's answer has the earlier grant too; kept either way
		granted = creds.Union(granted, old.Scopes)
	}
	var lacking []string
	for _, sc := range s.scopes() {
		if !contains(granted, sc) {
			lacking = append(lacking, strings.TrimPrefix(sc, "https://www.googleapis.com/auth/"))
		}
	}
	if len(lacking) > 0 {
		return Result{Detail: "not allowed: " + strings.Join(lacking, ", "),
			Hint: "Some boxes on Google's consent page were not ticked. Sign in again and tick all of them."}
	}
	nt := creds.Token{ClientID: c.ClientID, ClientSecret: c.ClientSecret, RefreshToken: t.Refresh, Scopes: granted,
		Account: emailOf(t.IDToken), Granted: time.Now().UTC()}
	if nt.Account == "" && old != nil {
		nt.Account = old.Account
	}
	if err := s.store().Save(nt); err != nil {
		return Result{Detail: "signed in, but the token could not be kept: " + err.Error()}
	}
	return s.googleAPIs(ctx)
}

// checkGoogle: her sign-in (the credential's health: none, invalid_grant, a chosen service's scope missing),
// then each of Google's APIs she uses called with it.
func checkGoogle(ctx context.Context, s *Setup, _ string) Result {
	h := s.store().Health(ctx)
	switch h.State {
	case "none":
		return Result{Detail: "not signed in yet"}
	case "invalid_grant":
		return Result{Detail: h.Detail, Hint: "Google has ended that sign-in. Sign in again."}
	case "scope_missing":
		return Result{Detail: h.Detail, Hint: "You chose a service the sign-in does not cover yet. Sign in again: I ask only for what is missing."}
	case "error":
		return Result{Detail: h.Detail}
	}
	return s.googleAPIs(ctx)
}

// googleAPIs: Calendar, Tasks and Photos called with her credential (what each said).
func (s *Setup) googleAPIs(ctx context.Context) Result {
	g := google.FromSource(s.store())
	g.HTTP = s.HTTP
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
	if _, err := s.store().AccessToken(ctx); err != nil { // the token's refresh tells first
		if errors.Is(err, creds.ErrInvalidGrant) {
			return Result{Detail: err.Error(), Hint: "Google has ended that sign-in. Sign in again."}
		}
		return Result{Detail: err.Error()}
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
	r := Result{OK: true, Detail: "my Google account works: " + strings.Join(ok, ", ")}
	if t := s.store().Token(); t != nil && t.Account != "" {
		r.Detail = "my Google account (" + t.Account + ") works: " + strings.Join(ok, ", ")
	}
	return r
}

// signOut: the sign-in revoked at Google, its file kept as .prev.
func (s *Setup) signOut(ctx context.Context) error {
	err := s.store().SignOut(ctx)
	if errors.Is(err, creds.ErrNone) { // nothing to sign out of: nothing changed
		return errors.New("setup: not signed in to Google")
	}
	s.log("setup", map[string]any{"verb": "signout", "integration": "google", "ok": err == nil})
	if err != nil && !strings.HasPrefix(err.Error(), "Google's revocation") {
		return err
	}
	s.mu.Lock()
	s.status["google"] = Result{Detail: "signed out (the old sign-in kept as google-token.json.prev)"}
	s.restart = true // her agenda and albums stop using it at her next start
	s.mu.Unlock()
	s.say("I have signed out of your Google account. Your calendar, tasks and photos are no longer mine until you sign in again.")
	return err
}

// googleView: the sign-in as her screens show it (nothing secret)
func (s *Setup) googleView() map[string]any {
	t := s.store().Token()
	v := map[string]any{"signed_in": t != nil}
	if t != nil {
		v["account"] = t.Account
		if !t.Granted.IsZero() {
			v["granted"] = t.Granted.Format(time.RFC3339)
		}
		var sc []string
		for _, x := range t.Scopes {
			if strings.HasPrefix(x, auth) {
				sc = append(sc, strings.TrimPrefix(x, auth))
			}
		}
		v["scopes"] = sc
	}
	if _, err := os.Stat(expand(s.P.Google) + ".prev"); err == nil {
		v["prev"] = true
	}
	return v
}

// emailOf: the email claim of an id_token (from Google's token endpoint, over TLS: read, not verified)
func emailOf(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var c struct {
		Email string `json:"email"`
	}
	json.Unmarshal(b, &c)
	return c.Email
}

func contains(l []string, x string) bool {
	for _, y := range l {
		if y == x {
			return true
		}
	}
	return false
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
