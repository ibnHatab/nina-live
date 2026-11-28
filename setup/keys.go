package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Paths are where a machine keeps what the setup writes and checks (her config's: each key a file,
// chmod 600; ~/ the home directory).
type Paths struct {
	Gemini     string // the voice's key (the dialogue's key:), her tasks' and her web search's when the same
	YouTube    string // youtube.key
	OpenRouter string // her tasks' backup models (tasks.fallback's openrouter key: OPENROUTER_API_KEY=…)
	Google     string // services.google.token: the client and the person's refresh token
	Client     string // an OAuth client's JSON, when pasted (Google Cloud's download)
	Clients    string // the directory where OAuth clients are looked for (the ones downloaded before)
	HassURL    string // her Home Assistant (hass.url)
	HassToken  string // its long-lived token's file (hass.token)
	SearXNG    string // her web search's own engine (search.searxng)
}

// Endpoints are the far ends the checks call (tests: an httptest server's).
type Endpoints struct {
	Gemini      string // https://generativelanguage.googleapis.com
	YouTube     string // https://www.googleapis.com/youtube/v3
	OpenRouter  string // https://openrouter.ai/api/v1
	GoogleAuth  string // https://accounts.google.com/o/oauth2/v2/auth
	GoogleToken string // https://oauth2.googleapis.com/token
	// Google: one base for the Calendar, Tasks and Photos APIs ("": Google's own; the SITL's fake: its
	// /calendar, /tasks, /photos)
	Google string
}

// Google is Google's own endpoints.
func Google() Endpoints {
	return Endpoints{Gemini: "https://generativelanguage.googleapis.com", YouTube: "https://www.googleapis.com/youtube/v3",
		OpenRouter: "https://openrouter.ai/api/v1", GoogleAuth: "https://accounts.google.com/o/oauth2/v2/auth",
		GoogleToken: "https://oauth2.googleapis.com/token"}
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[2:])
	}
	return p
}

// readKey is a key file's key: the bare key, or NAME=key (an env file), the first such line.
func readKey(path string) string {
	b, err := os.ReadFile(expand(path))
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if _, v, ok := strings.Cut(l, "="); ok && !strings.ContainsAny(l[:strings.Index(l, "=")], " \t") {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
		return l
	}
	return ""
}

// writeFile keeps data at path (chmod 600); a different file there before is kept beside it (.prev).
func writeFile(path string, data []byte) error {
	if path == "" {
		return fmt.Errorf("no file for it in her config")
	}
	p := expand(path)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if old, err := os.ReadFile(p); err == nil {
		if string(old) == string(data) {
			return nil
		}
		if err := os.WriteFile(p+".prev", old, 0o600); err != nil {
			return err
		}
	}
	tmp := p + ".new"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// keyOr: the value given, or the key in the file
func keyOr(value, path string) (string, bool) {
	if value != "" {
		return value, true
	}
	k := readKey(path)
	return k, false
}

// get calls u with the headers; the status and up to 4 KiB of the answer.
func (s *Setup) get(ctx context.Context, u string, h map[string]string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return 0, nil, err
	}
	for k, v := range h {
		req.Header.Set(k, v)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, b, nil
}

// googleError is a Google API's error message (its status and message, never the request).
func googleError(code int, b []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error.Message != "" {
		m := e.Error.Message
		if len(m) > 200 {
			m = m[:200] + "…"
		}
		return fmt.Sprintf("%d %s: %s", code, e.Error.Status, m)
	}
	return fmt.Sprintf("HTTP %d", code)
}

func missing(what, path string) Result {
	if path == "" {
		return Result{Detail: what + ": her config names no file for it", Hint: "Her config needs a line naming the " + what + "'s file."}
	}
	return Result{Detail: what + ": not set"}
}

// checkGemini: the key lists Gemini's models.
func checkGemini(ctx context.Context, s *Setup, value string) Result {
	k, given := keyOr(value, s.P.Gemini)
	if k == "" {
		return missing("the Gemini key", s.P.Gemini)
	}
	code, b, err := s.get(ctx, s.E.Gemini+"/v1beta/models?pageSize=1", map[string]string{"x-goog-api-key": k})
	switch {
	case err != nil:
		return Result{Detail: "Gemini is not reachable: " + err.Error()}
	case code == 200:
		return Result{OK: true, Detail: answers(given, "Gemini key")}
	}
	return Result{Detail: "Gemini refused the key: " + googleError(code, b),
		Hint: "Google refused that key. Copy it again from AI Studio, the whole of it, and paste it here."}
}

// checkYouTube: the key reads YouTube's video categories (1 unit of the day's quota).
func checkYouTube(ctx context.Context, s *Setup, value string) Result {
	k, given := keyOr(value, s.P.YouTube)
	if k == "" {
		return missing("the YouTube key", s.P.YouTube)
	}
	code, b, err := s.get(ctx, s.E.YouTube+"/videoCategories?part=id&regionCode=US", map[string]string{"x-goog-api-key": k})
	switch {
	case err != nil:
		return Result{Detail: "YouTube is not reachable: " + err.Error()}
	case code == 200:
		return Result{OK: true, Detail: answers(given, "YouTube key")}
	}
	r := Result{Detail: "YouTube refused the key: " + googleError(code, b),
		Hint: "Google refused that key for YouTube. Check that the YouTube Data API is enabled in the project, and that the key's restriction allows it."}
	if strings.Contains(string(b), "SERVICE_DISABLED") || strings.Contains(string(b), "has not been used") {
		r.Hint = "The YouTube Data API is not enabled in that key's project yet. Enable it, wait a minute, and check again."
	}
	return r
}

// checkOpenRouter: the key is OpenRouter's, with what is left of its limit.
func checkOpenRouter(ctx context.Context, s *Setup, value string) Result {
	k, given := keyOr(value, s.P.OpenRouter)
	if k == "" {
		return missing("the OpenRouter key", s.P.OpenRouter)
	}
	code, b, err := s.get(ctx, s.E.OpenRouter+"/key", map[string]string{"Authorization": "Bearer " + k})
	if err != nil {
		return Result{Detail: "OpenRouter is not reachable: " + err.Error()}
	}
	if code != 200 {
		return Result{Detail: fmt.Sprintf("OpenRouter refused the key (HTTP %d)", code),
			Hint: "OpenRouter refused that key. Copy it again from your keys page: it is shown only once, when it is made."}
	}
	var d struct {
		Data struct {
			Limit     *float64 `json:"limit"`
			Remaining *float64 `json:"limit_remaining"`
			Reset     string   `json:"limit_reset"`
		} `json:"data"`
	}
	json.Unmarshal(b, &d)
	r := Result{OK: true, Detail: answers(given, "OpenRouter key")}
	if d.Data.Limit != nil && d.Data.Remaining != nil {
		r.Detail += fmt.Sprintf(" ($%.2f of $%.0f left", *d.Data.Remaining, *d.Data.Limit)
		if d.Data.Reset != "" {
			r.Detail += ", " + d.Data.Reset
		}
		r.Detail += ")"
		if *d.Data.Remaining <= 0 {
			r = Result{Detail: r.Detail, Hint: "That key has spent its limit. Raise the limit on OpenRouter, or wait for it to reset."}
		}
	}
	return r
}

// checkHass: the token opens Home Assistant's API.
func checkHass(ctx context.Context, s *Setup, value string) Result {
	if s.P.HassURL == "" {
		return Result{Detail: "her config names no Home Assistant (hass.url)"}
	}
	k, given := keyOr(value, s.P.HassToken)
	if k == "" {
		return missing("the Home Assistant token", s.P.HassToken)
	}
	code, _, err := s.get(ctx, strings.TrimRight(s.P.HassURL, "/")+"/api/", map[string]string{"Authorization": "Bearer " + k})
	switch {
	case err != nil:
		return Result{Detail: "Home Assistant is not reachable: " + err.Error()}
	case code == 200:
		return Result{OK: true, Detail: answers(given, "Home Assistant token")}
	}
	return Result{Detail: fmt.Sprintf("Home Assistant refused the token (HTTP %d)", code),
		Hint: "Home Assistant refused that token. Make a new long-lived token on your profile's security page, copy it at once, and paste it here."}
}

// checkSearXNG: her own search engine answers a query in JSON.
func checkSearXNG(ctx context.Context, s *Setup, _ string) Result {
	if s.P.SearXNG == "" {
		return Result{Detail: "her config names no SearXNG (search.searxng): her web search uses Gemini's grounding alone"}
	}
	code, b, err := s.get(ctx, strings.TrimRight(s.P.SearXNG, "/")+"/search?q=weather&format=json", nil)
	if err != nil {
		return Result{Detail: "SearXNG is not reachable: " + err.Error(), Hint: "Her search engine is not running. Start its container, then check again."}
	}
	if code != 200 || !strings.HasPrefix(strings.TrimSpace(string(b)), "{") { // the answer's start: it is cut at 4 KiB
		return Result{Detail: fmt.Sprintf("SearXNG answered HTTP %d, not JSON", code),
			Hint: "Her search engine answers, but not in JSON: its settings need json among the formats."}
	}
	return Result{OK: true, Detail: "SearXNG answers"}
}

// answers: what a check that passed says (the key pasted, or the one she has)
func answers(given bool, what string) string {
	if given {
		return "the new " + what + " works"
	}
	return "the " + what + " she has works"
}

func saveKey(path func(Paths) string, env string) func(s *Setup, v string) error {
	return func(s *Setup, v string) error {
		data := v + "\n"
		if env != "" {
			data = env + "=" + v + "\n"
		}
		return writeFile(path(s.P), []byte(data))
	}
}

// query is url.Values encoded (a tiny helper for the sign-in's URL).
func query(kv ...string) string {
	q := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		q.Set(kv[i], kv[i+1])
	}
	return q.Encode()
}
