package setup

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ibnHatab/nina-live/creds"
)

const (
	goodKey = "AIza-good-key-0123456789"
	newKey  = "AIza-new-key-9876543210"
	orKey   = "sk-or-v1-good"
	haTok   = "ha-good-token"
)

// far is every far end the checks call: Gemini, YouTube, OpenRouter, Home Assistant, SearXNG, Google's
// token service and its Calendar, Tasks and Photos.
func far(t *testing.T, scope string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("x-goog-api-key")
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		switch p := r.URL.Path; {
		case p == "/gemini/v1beta/models", p == "/youtube/videoCategories":
			if key != goodKey && key != newKey {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":{"status":"INVALID_ARGUMENT","message":"API key not valid."}}`)
				return
			}
			fmt.Fprint(w, `{"models":[]}`)
		case p == "/openrouter/key":
			if bearer != orKey {
				w.WriteHeader(401)
				return
			}
			fmt.Fprint(w, `{"data":{"limit":20,"limit_remaining":11.92,"limit_reset":"weekly"}}`)
		case p == "/hass/api/":
			if bearer != haTok {
				w.WriteHeader(401)
				return
			}
			fmt.Fprint(w, `{"message":"API running."}`)
		case p == "/searxng/search":
			fmt.Fprint(w, `{"results":[]}`)
		case p == "/token":
			r.ParseForm()
			if r.Form.Get("grant_type") == "authorization_code" {
				if r.Form.Get("code") != "the-code" || r.Form.Get("code_verifier") == "" { // PKCE: the verifier with the code
					w.WriteHeader(400)
					fmt.Fprint(w, `{"error":"invalid_grant"}`)
					return
				}
				if granted := r.Form.Get("client_id"); granted == "id-incremental" { // an incremental sign-in: Google's answer for the added scopes
					fmt.Fprintf(w, `{"refresh_token":"the-refresh-2","scope":%q,"id_token":%q}`, scope, idToken("person@example.com"))
					return
				}
				if false {
					w.WriteHeader(400)
					fmt.Fprint(w, `{"error":"invalid_grant"}`)
					return
				}
				fmt.Fprintf(w, `{"refresh_token":"the-refresh","scope":%q}`, scope)
				return
			}
			fmt.Fprint(w, `{"access_token":"the-access","expires_in":3600}`)
		case p == "/revoke":
			r.ParseForm()
			if r.Form.Get("token") == "" {
				w.WriteHeader(400)
			}
		case strings.HasPrefix(p, "/google/"):
			if bearer != "the-access" {
				w.WriteHeader(401)
				return
			}
			fmt.Fprint(w, `{}`)
		default:
			t.Errorf("unexpected call %s", r.URL)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type wa struct{ linked bool }

func (w *wa) Linked() bool                        { return w.linked }
func (w *wa) StartLink(ctx context.Context) error { w.linked = true; return nil }

// machine: a home with the keys it has (the sunny day): Gemini, OpenRouter, Home Assistant; no YouTube
// key, no Google sign-in
func machine(t *testing.T, scope string) (*Setup, string, *[]string) {
	dir := t.TempDir()
	srv := far(t, scope)
	put := func(name, data string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(data), 0o600)
		return p
	}
	p := Paths{Gemini: put("gemini.key", goodKey+"\n"), YouTube: filepath.Join(dir, "youtube.key"),
		OpenRouter: put("openrouter.env", "OPENROUTER_API_KEY="+orKey+"\n"), Google: filepath.Join(dir, "google-token.json"),
		Client: filepath.Join(dir, "google-oauth-client.json"), Clients: dir,
		HassURL: srv.URL + "/hass", HassToken: put("hass.token", haTok), SearXNG: srv.URL + "/searxng"}
	s := New(p)
	s.E = Endpoints{Gemini: srv.URL + "/gemini", YouTube: srv.URL + "/youtube", OpenRouter: srv.URL + "/openrouter",
		GoogleAuth: srv.URL + "/auth", GoogleToken: srv.URL + "/token", GoogleRevoke: srv.URL + "/revoke", Google: srv.URL + "/google"}
	said := &[]string{}
	s.Say = func(t string) { *said = append(*said, t) }
	w := &wa{}
	s.WhatsApp = func() WhatsApp { return w }
	ln, _ := net.Listen("tcp", "127.0.0.1:0") // a free port for the sign-in's redirect
	s.SignInPort = ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return s, dir, said
}

func do(t *testing.T, s *Setup, action, arg string) {
	t.Helper()
	if ok, err := s.Action(action, arg); !ok || err != nil {
		t.Fatalf("%s %q: %v %v", action, arg, ok, err)
	}
}

func view(s *Setup) map[string]any {
	b, _ := json.Marshal(s.View())
	var v map[string]any
	json.Unmarshal(b, &v)
	return v
}

func status(s *Setup) map[string]bool {
	out := map[string]bool{}
	for _, e := range view(s)["integrations"].([]any) {
		m := e.(map[string]any)
		st, _ := m["status"].(map[string]any)
		out[m["id"].(string)] = st["ok"] == true
	}
	return out
}

func active(s *Setup) map[string]any { a, _ := view(s)["active"].(map[string]any); return a }

// TestTheSunnyDay: what the machine has is found and checked; a key that works needs nothing but Next.
func TestTheSunnyDay(t *testing.T) {
	s, _, said := machine(t, "")
	do(t, s, "setup_status", "")
	// her settings page shows two: Google (not signed in on this machine) and WhatsApp (not linked)
	want := map[string]bool{"google": false, "whatsapp": false}
	if got := status(s); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("status %v\nwant %v", got, want)
	}
	do(t, s, "setup_add", "gemini")
	a := active(s)
	cur := a["current"].(map[string]any)
	if cur["url"] != "https://aistudio.google.com/apikey" || cur["input"] != "secret" {
		t.Errorf("the step: %v", cur)
	}
	if r := a["result"].(map[string]any); r["ok"] != true || !strings.Contains(r["detail"].(string), "I have works") {
		t.Errorf("the key she has, checked on entering: %v", a["result"])
	}
	// the key she has works: that is what she says, not how to make one (claudia's test, 2026-11-28)
	if len(*said) != 1 || (*said)[0] != "The Gemini key I have works. Press Next to keep it, or paste a new one to replace it." {
		t.Errorf("narrated: %q", *said)
	}
	do(t, s, "setup_next", "")
	if a := active(s); a["done"] != true || view(s)["restart"] != false {
		t.Errorf("done, nothing to restart for: %v", view(s))
	}
}

// TestEveryActionTellsHerScreens: each action, its result on her screens (the settings event).
func TestEveryActionTellsHerScreens(t *testing.T) {
	s, _, said := machine(t, "")
	n := 0
	s.Changed = func() { n++ }
	for _, a := range [][2]string{{"setup_status", ""}, {"setup_add", "youtube"}, {"setup_say", ""}, {"setup_skip", ""}, {"setup_close", ""}} {
		before := n
		do(t, s, a[0], a[1])
		if n != before+1 {
			t.Errorf("%s: her screens not told", a[0])
		}
	}
	// a key not set yet: how to make it is what she says
	if len(*said) < 1 || !strings.Contains((*said)[0], "Create credentials") {
		t.Errorf("narrated: %q", *said)
	}
}

// TestANewKey: a bad key is refused and changes nothing; a good one replaces the file, the old one kept
// beside it; nothing secret on her screens.
func TestANewKey(t *testing.T) {
	s, dir, said := machine(t, "")
	do(t, s, "setup_add", "gemini")
	do(t, s, "setup_value", "AIza-wrong")
	if r := active(s)["result"].(map[string]any); r["ok"] != false || !strings.Contains(r["detail"].(string), "API key not valid") {
		t.Errorf("a bad key: %v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "gemini.key")); string(b) != goodKey+"\n" {
		t.Errorf("the file changed by a bad key: %q", b)
	}
	if !strings.Contains((*said)[len(*said)-1], "refused") {
		t.Errorf("the hint not said: %q", *said)
	}
	do(t, s, "setup_value", "  "+newKey+"\n")
	if b, _ := os.ReadFile(filepath.Join(dir, "gemini.key")); string(b) != newKey+"\n" {
		t.Errorf("the new key not kept: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "gemini.key.prev")); string(b) != goodKey+"\n" {
		t.Errorf("the old key not kept beside it: %q", b)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "gemini.key")); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
	b, _ := json.Marshal(s.View())
	if strings.Contains(string(b), newKey) || strings.Contains(string(b), goodKey) {
		t.Errorf("a key on her screens: %s", b)
	}
	do(t, s, "setup_next", "")
	if view(s)["restart"] != true || !strings.Contains((*said)[len(*said)-1], "Restart") {
		t.Errorf("a key written: her restart offered: %v %q", view(s)["restart"], *said)
	}
	// the env file's form: NAME=key
	do(t, s, "setup_add", "openrouter")
	do(t, s, "setup_value", orKey)
	if b, _ := os.ReadFile(filepath.Join(dir, "openrouter.env")); string(b) != "OPENROUTER_API_KEY="+orKey+"\n" {
		t.Errorf("openrouter: %q", b)
	}
	if d := active(s)["result"].(map[string]any)["detail"].(string); !strings.Contains(d, "$11.92 of $20 left") {
		t.Errorf("openrouter's limit: %s", d)
	}
}

// TestTheProjectInOneStep: a project made before is one step (the shortcut); a new one is the person's
// word, step by step.
func TestTheProjectInOneStep(t *testing.T) {
	s, _, _ := machine(t, "")
	do(t, s, "setup_add", "google_project")
	if c := active(s)["current"].(map[string]any); c["shortcut"] != "I have one already" || c["manual"] != true {
		t.Errorf("the first step: %v", c)
	}
	do(t, s, "setup_done", "")
	if a := active(s); a["done"] != true || a["status"].(map[string]any)["ok"] != true {
		t.Errorf("the shortcut: %v", a)
	}
	s2, _, said := machine(t, "")
	do(t, s2, "setup_add", "google_project")
	for i := 0; i < 5; i++ {
		do(t, s2, "setup_next", "")
	}
	if a := active(s2); a["done"] != true || a["status"].(map[string]any)["ok"] != true || len(*said) != 6 {
		t.Errorf("made step by step: %v, said %d", a, len(*said))
	}
}

// TestTheGoogleSignIn: the services chosen; Google's personal setup's client pasted (its ID and secret) and
// kept; the rest of the APIs; the sign-in's redirect gives the refresh token for the chosen services' scopes,
// kept, and each chosen service answers with it.
func TestTheGoogleSignIn(t *testing.T) {
	s, dir, said := machine(t, strings.Join(Scopes[:5], " "))
	do(t, s, "setup_add", "google")
	if r := active(s)["result"].(map[string]any); r["ok"] != true || r["detail"] != "chosen: Calendar, Tasks, Photos" {
		t.Errorf("the services chosen by default: %v", r)
	}
	if (*said)[0] != "You chose Calendar, Tasks and Photos. Press Next to keep it." {
		t.Errorf("said: %q", *said)
	}
	do(t, s, "setup_next", "")
	cur := active(s)["current"].(map[string]any)
	if cur["id"] != "client" || !strings.Contains(cur["url"].(string), "auth/overview/personal") || !strings.Contains(cur["url"].(string), "isAgent=true") {
		t.Errorf("the client's step: %v", cur)
	}
	do(t, s, "setup_value", `{"nope":1}`)
	if r := active(s)["result"].(map[string]any); r["ok"] != false {
		t.Errorf("not a client: %v", r)
	}
	cl := `{"installed":{"client_id":"id-1.apps","client_secret":"sec-1"}}` // as her screen makes it of the two fields
	do(t, s, "setup_value", cl)
	if b, _ := os.ReadFile(filepath.Join(dir, "google-oauth-client.json")); string(b) != cl {
		t.Errorf("the client not kept: %q", b)
	}
	if view(s)["restart"] != false { // a client is read by the sign-in, not at her start (claudia's test)
		t.Error("restart offered for an OAuth client")
	}
	do(t, s, "setup_next", "")
	cur = active(s)["current"].(map[string]any)
	if cur["id"] != "apis" || cur["url"] != "https://console.cloud.google.com/flows/enableapi?apiid=photoslibrary.googleapis.com,photospicker.googleapis.com" {
		t.Errorf("the rest of the APIs: %v", cur)
	}
	do(t, s, "setup_done", "")
	do(t, s, "setup_signin", "")
	si := active(s)["signin"].(map[string]any)
	u, _ := url.Parse(si["url"].(string))
	if u.Query().Get("redirect_uri") != fmt.Sprintf("http://localhost:%d/", s.SignInPort) || u.Query().Get("access_type") != "offline" ||
		u.Query().Get("scope") != strings.Join(Scopes[:5], " ")+" openid email" || u.Query().Get("include_granted_scopes") != "true" ||
		u.Query().Get("code_challenge_method") != "S256" || u.Query().Get("code_challenge") == "" {
		t.Fatalf("the sign-in's page: %s", u)
	}
	if b, _ := json.Marshal(s.View()); strings.Contains(string(b), "sec-1") {
		t.Errorf("the client's secret on her screens")
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/?state=wrong&code=the-code", s.SignInPort))
	if err != nil || resp.StatusCode != 400 {
		t.Fatalf("another sign-in's redirect taken: %v %v", resp, err)
	}
	resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%d/?state=%s&code=the-code", s.SignInPort, u.Query().Get("state")))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("the redirect: %v %v", resp, err)
	}
	var tok map[string]any
	b, _ := os.ReadFile(filepath.Join(dir, "google-token.json"))
	if json.Unmarshal(b, &tok); tok["refresh_token"] != "the-refresh" || tok["client_id"] != "id-1.apps" {
		t.Errorf("the token: %v", tok)
	}
	if r := active(s)["result"].(map[string]any); r["ok"] != true || r["detail"] != "my Google account works: calendar, tasks, photos" {
		t.Errorf("signed in: %v", r)
	}
	do(t, s, "setup_next", "") // no key: neither YouTube nor Gemini chosen
	if a := active(s); a["done"] != true || a["status"].(map[string]any)["ok"] != true {
		t.Errorf("google done: %v", a)
	}
	// a client found among the files: chosen by name
	s2, _, _ := machine(t, "")
	os.WriteFile(filepath.Join(s2.P.Clients, "made-before.json"), []byte(cl), 0o600)
	do(t, s2, "setup_add", "google")
	do(t, s2, "setup_next", "")
	if c := active(s2)["clients"].([]any); len(c) != 1 || c[0] != "made-before.json" {
		t.Errorf("clients found: %v", c)
	}
	do(t, s2, "setup_value", "file:made-before.json")
	if r := active(s2)["result"].(map[string]any); r["ok"] != true {
		t.Errorf("the client chosen: %v", r)
	}
}

// TestChoosingServices: the steps follow the choice: no Photos, no sign-in for Photos' scopes; YouTube and
// Gemini: their APIs and one key, written for YouTube and not over the Gemini key she has; Gmail not offered.
func TestChoosingServices(t *testing.T) {
	s, dir, _ := machine(t, "")
	if _, err := s.Action("setup_services", "calendar,gmail"); err == nil || !strings.Contains(err.Error(), "partners") {
		t.Errorf("gmail chosen: %v", err)
	}
	if got := s.Chosen(); fmt.Sprint(got) != "[calendar tasks photos]" {
		t.Errorf("a refused choice changed it: %v", got)
	}
	do(t, s, "setup_services", "calendar, youtube,gemini")
	if fmt.Sprint(s.scopes()) != "[https://www.googleapis.com/auth/calendar.events]" {
		t.Errorf("scopes: %v", s.scopes())
	}
	do(t, s, "setup_add", "google")
	do(t, s, "setup_next", "")
	os.WriteFile(s.P.Client, []byte(`{"installed":{"client_id":"id-3","client_secret":"sec-3"}}`), 0o600)
	do(t, s, "setup_check", "")
	do(t, s, "setup_next", "")
	if u := active(s)["current"].(map[string]any)["url"]; u != "https://console.cloud.google.com/flows/enableapi?apiid=youtube.googleapis.com,generativelanguage.googleapis.com" {
		t.Errorf("the APIs page: %v", u)
	}
	do(t, s, "setup_done", "")
	do(t, s, "setup_skip", "") // the sign-in skipped: on to the key
	a := active(s)
	if a["current"].(map[string]any)["id"] != "key" || a["result"].(map[string]any)["ok"] != false {
		t.Fatalf("the key's step (no YouTube key yet): %v", a)
	}
	do(t, s, "setup_value", newKey)
	if b, _ := os.ReadFile(filepath.Join(dir, "youtube.key")); string(b) != newKey+"\n" {
		t.Errorf("youtube's key: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "gemini.key")); string(b) != goodKey+"\n" {
		t.Errorf("the Gemini key she has replaced: %q", b)
	}
	v := view(s) // the services with what is chosen; the hidden integrations not listed
	if len(v["integrations"].([]any)) != 2 {
		t.Errorf("listed: %v", v["integrations"])
	}
	on := map[string]bool{}
	for _, e := range v["google_services"].([]any) {
		m := e.(map[string]any)
		on[m["id"].(string)] = m["on"] == true
		if m["id"] == "gmail" && m["off"] == nil {
			t.Error("gmail offered")
		}
	}
	if fmt.Sprint(on) != "map[calendar:true gemini:true gmail:false photos:false tasks:false youtube:true]" {
		t.Errorf("services view: %v", on)
	}
}

// TestAScopeNotGiven: a box left unticked on Google's consent page: no token kept, the person told.
func TestAScopeNotGiven(t *testing.T) {
	s, dir, said := machine(t, Scopes[0]+" "+Scopes[1])
	os.WriteFile(s.P.Client, []byte(`{"installed":{"client_id":"id-2","client_secret":"sec-2"}}`), 0o600)
	do(t, s, "setup_add", "google")
	do(t, s, "setup_next", "") // the services
	do(t, s, "setup_next", "") // the client (the file kept)
	do(t, s, "setup_done", "") // the rest of the APIs
	do(t, s, "setup_signin", "")
	u, _ := url.Parse(active(s)["signin"].(map[string]any)["url"].(string))
	http.Get(fmt.Sprintf("http://127.0.0.1:%d/?state=%s&code=the-code", s.SignInPort, u.Query().Get("state")))
	if r := active(s)["result"].(map[string]any); r["ok"] != false || !strings.Contains(r["detail"].(string), "photoslibrary") {
		t.Errorf("a scope lacking: %v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "google-token.json")); err == nil {
		t.Error("a token kept without its scopes")
	}
	if !strings.Contains((*said)[len(*said)-1], "tick all") {
		t.Errorf("said: %q", *said)
	}
}

// TestSkipping: a step the person cannot do is skipped; the integration stays incomplete; WhatsApp's
// link is checked when it is done.
func TestSkipping(t *testing.T) {
	s, _, said := machine(t, "")
	do(t, s, "setup_add", "youtube")
	do(t, s, "setup_skip", "")
	if a := active(s); a["done"] != true || a["status"].(map[string]any)["ok"] != false || len(a["skipped"].([]any)) != 1 {
		t.Errorf("skipped: %v", a)
	}
	if !strings.Contains((*said)[len(*said)-1], "skipped") {
		t.Errorf("said: %q", *said)
	}
	if _, err := s.Action("setup_next", ""); err == nil {
		t.Error("next after done")
	}
	do(t, s, "setup_add", "whatsapp")
	if _, err := s.Action("setup_next", ""); err == nil {
		t.Error("on before it is linked")
	}
	do(t, s, "setup_link", "")
	do(t, s, "setup_check", "")
	do(t, s, "setup_next", "")
	if a := active(s); a["status"].(map[string]any)["ok"] != true {
		t.Errorf("whatsapp linked: %v", a)
	}
	do(t, s, "setup_narrate", "off")
	n := len(*said)
	do(t, s, "setup_add", "gemini")
	if len(*said) != n {
		t.Error("said with the narration off")
	}
	if ok, _ := s.Action("name", "x"); ok {
		t.Error("not the setup's action taken")
	}
}

// idToken: an id_token with an email claim (unsigned: the setup reads, it does not verify, what Google's
// token endpoint gave over TLS)
func idToken(email string) string {
	b, _ := json.Marshal(map[string]string{"email": email})
	return "e30." + base64.RawURLEncoding.EncodeToString(b) + ".sig"
}

// signIn runs the sign-in's redirect against the fake (the person allowed it in Google's page)
func signIn(t *testing.T, s *Setup) *url.URL {
	t.Helper()
	for i := 0; i < 6 && active(s)["current"].(map[string]any)["id"] != "signin"; i++ { // the steps before it: each passed, or done by word
		if s.Action("setup_next", ""); active(s)["current"].(map[string]any)["id"] == "apis" {
			do(t, s, "setup_done", "")
		}
	}
	do(t, s, "setup_signin", "")
	u, _ := url.Parse(active(s)["signin"].(map[string]any)["url"].(string))
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/?state=%s&code=the-code", s.SignInPort, u.Query().Get("state")))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("the redirect: %v %v", resp, err)
	}
	resp.Body.Close()
	return u
}

// TestIncrementalConsent: signed in for Calendar and Tasks; Photos ticked afterwards: the sign-in asks only
// for Photos' scopes (and the account), with include_granted_scopes, and the grant kept is both; the account's
// address is read from the id_token; the old token file stays as .prev.
func TestIncrementalConsent(t *testing.T) {
	photos := strings.Join(Scopes[2:5], " ")
	s, dir, _ := machine(t, photos)
	cl := `{"installed":{"client_id":"id-incremental","client_secret":"sec-2"}}`
	os.WriteFile(filepath.Join(dir, "google-oauth-client.json"), []byte(cl), 0o600)
	old, _ := json.Marshal(creds.Token{ClientID: "id-incremental", ClientSecret: "sec-2", RefreshToken: "the-refresh", Scopes: Scopes[:2]})
	os.WriteFile(filepath.Join(dir, "google-token.json"), old, 0o600)
	do(t, s, "setup_add", "google")
	do(t, s, "setup_services", "calendar,tasks,photos")
	u := signIn(t, s)
	if got := u.Query().Get("scope"); got != photos+" openid email" {
		t.Fatalf("asked for %q, want only Photos' and the account", got)
	}
	if u.Query().Get("include_granted_scopes") != "true" {
		t.Fatal("not incremental")
	}
	tk := s.Creds.Token()
	if tk == nil || tk.RefreshToken != "the-refresh-2" || len(tk.Scopes) != 5 || tk.Account != "person@example.com" || tk.Granted.IsZero() {
		t.Fatalf("kept: %+v", tk)
	}
	if prev, _ := os.ReadFile(filepath.Join(dir, "google-token.json.prev")); !strings.Contains(string(prev), `"the-refresh"`) {
		t.Fatalf(".prev: %s", prev)
	}
	if r := active(s)["result"].(map[string]any); r["ok"] != true || !strings.Contains(r["detail"].(string), "person@example.com") {
		t.Errorf("signed in: %v", r)
	}
	if g := view(s)["google"].(map[string]any); g["signed_in"] != true || g["account"] != "person@example.com" || len(g["scopes"].([]any)) != 5 || g["prev"] != true {
		t.Errorf("her screens' google: %v", g)
	}
	if b, _ := json.Marshal(s.View()); strings.Contains(string(b), "the-refresh") || strings.Contains(string(b), "sec-2") {
		t.Error("a secret on her screens")
	}
}

// TestSignOut: revoked at Google, the file kept as .prev, her screens told; Google's check then says not
// signed in, and her next start drops the backends (restart offered).
func TestSignOut(t *testing.T) {
	s, dir, said := machine(t, strings.Join(Scopes[:5], " "))
	tok, _ := json.Marshal(creds.Token{ClientID: "id-1.apps", ClientSecret: "sec-1", RefreshToken: "the-refresh", Account: "person@example.com"})
	os.WriteFile(filepath.Join(dir, "google-token.json"), tok, 0o600)
	do(t, s, "setup_signout", "")
	if _, err := os.Stat(filepath.Join(dir, "google-token.json")); !os.IsNotExist(err) {
		t.Fatal("the token file is still there")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "google-token.json.prev")); !strings.Contains(string(b), "the-refresh") {
		t.Fatalf(".prev: %s", b)
	}
	v := view(s)
	if g := v["google"].(map[string]any); g["signed_in"] != false || g["prev"] != true {
		t.Errorf("google after sign-out: %v", g)
	}
	if v["restart"] != true {
		t.Error("no restart offered: her backends would keep the old sign-in")
	}
	if !strings.Contains(strings.Join(*said, " "), "signed out") {
		t.Errorf("said: %q", *said)
	}
	if r := checkGoogle(context.Background(), s, ""); r.OK || r.Detail != "not signed in yet" {
		t.Errorf("check after sign-out: %+v", r)
	}
}

// TestDesktopClientOnly: Google's personal setup makes a Desktop app client: a Web client pasted is refused,
// and a Web client kept from an older setup shows as not this step's (not as a ✓).
func TestDesktopClientOnly(t *testing.T) {
	s, dir, _ := machine(t, "")
	web := `{"web":{"client_id":"w.apps","client_secret":"ws","redirect_uris":["https://my.home-assistant.io/redirect/oauth"]}}`
	os.WriteFile(filepath.Join(dir, "google-oauth-client.json"), []byte(web), 0o600)
	do(t, s, "setup_add", "google")
	do(t, s, "setup_next", "")
	if r := active(s)["result"].(map[string]any); r["ok"] != false || !strings.Contains(r["detail"].(string), "not the Desktop app client") {
		t.Errorf("a kept web client on entering: %v", r)
	}
	do(t, s, "setup_value", web)
	if r := active(s)["result"].(map[string]any); r["ok"] != false || !strings.Contains(r["detail"].(string), "Desktop app client") {
		t.Errorf("a web client pasted: %v", r)
	}
	do(t, s, "setup_value", `{"installed":{"client_id":"d.apps","client_secret":"ds"}}`)
	if r := active(s)["result"].(map[string]any); r["ok"] != true {
		t.Errorf("a desktop client: %v", r)
	}
}
