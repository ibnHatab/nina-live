// Package creds is the one place that holds Nina's Google credential (the user, 2026-11-28: the Google
// services' authentication end to end). The token file (the OAuth client and the person's refresh token,
// chmod 600) is read here and nowhere else; every Google consumer gets its access tokens from a Store. It
// refreshes them as they expire, knows the scopes the person granted, and reports its health to the setup
// (ok, none, invalid_grant, scope missing). A token replaced or revoked is kept beside it as .prev: a key
// once allocated is never deleted. No token goes to the LLMs, the Metis rules or her log: the Store gives
// access tokens to HTTP clients only, and its Health has no secret in it.
package creds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Token is the credential file. Its first three fields are what nina-google-auth and the setup have always
// written (services/google.Token's JSON); the rest are added by the setup's sign-in: the scopes Google
// granted, the account's address (when the sign-in asked for it), when it was granted.
type Token struct {
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret"`
	RefreshToken string    `json:"refresh_token"`
	Scopes       []string  `json:"scopes,omitempty"`
	Account      string    `json:"account,omitempty"`
	Granted      time.Time `json:"granted,omitempty"`
}

// Health is what the setup shows of the credential (nothing secret).
type Health struct {
	State   string    `json:"state"` // ok | none | invalid_grant | scope_missing | error
	Detail  string    `json:"detail"`
	Scopes  []string  `json:"scopes,omitempty"`  // granted
	Missing []string  `json:"missing,omitempty"` // wanted (Want) and not granted
	Account string    `json:"account,omitempty"`
	Granted time.Time `json:"granted,omitempty"`
}

// Store holds the token at Path and its access token.
type Store struct {
	Path string
	HTTP *http.Client
	// Google's endpoints (tests: a fake's)
	TokenURL, RevokeURL string
	Now                 func() time.Time
	// Want: the scopes the person's choice needs (the setup sets it): one not granted is scope_missing
	Want func() []string

	mu     sync.Mutex
	tok    *Token
	mtime  time.Time
	access string
	until  time.Time
	last   error // the last refresh's failure (nil: it worked, or none yet)
}

// Google's token and revocation endpoints.
const (
	GoogleToken  = "https://oauth2.googleapis.com/token"
	GoogleRevoke = "https://oauth2.googleapis.com/revoke"
)

// Open is a Store for the token file at path (it need not exist yet: the sign-in writes it).
func Open(path string) *Store {
	return &Store{Path: path, HTTP: &http.Client{Timeout: 20 * time.Second}, TokenURL: GoogleToken, RevokeURL: GoogleRevoke, Now: time.Now}
}

// ErrNone: there is no sign-in (no token file, or none in it).
var ErrNone = errors.New("not signed in to Google")

// ErrInvalidGrant: Google no longer accepts the refresh token (revoked, expired, the password changed).
var ErrInvalidGrant = errors.New("Google no longer accepts the sign-in (invalid_grant)")

// load reads the file again when it changed (the setup's sign-in, nina-google-auth, a copy from another
// machine); under mu.
func (s *Store) load() (*Token, error) {
	st, err := os.Stat(s.Path)
	if err != nil {
		s.tok, s.access = nil, ""
		return nil, ErrNone
	}
	if s.tok != nil && st.ModTime().Equal(s.mtime) {
		return s.tok, nil
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("creds: %s is readable by others (chmod 600)", s.Path)
	}
	b, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, err
	}
	var t Token
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("creds: %s: %w", s.Path, err)
	}
	if t.RefreshToken == "" || t.ClientID == "" {
		return nil, ErrNone
	}
	s.tok, s.mtime, s.access, s.last = &t, st.ModTime(), "", nil
	return s.tok, nil
}

// Token is the credential as kept (the setup's own use: the client for a new sign-in); nil: none.
func (s *Store) Token() *Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.load()
	if err != nil {
		return nil
	}
	c := *t
	return &c
}

// AccessToken is a valid access token, refreshed when it is near its end.
func (s *Store) AccessToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.load()
	if err != nil {
		return "", err
	}
	if s.access != "" && s.Now().Before(s.until) {
		return s.access, nil
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {t.RefreshToken},
		"client_id": {t.ClientID}, "client_secret": {t.ClientSecret}}
	req, _ := http.NewRequestWithContext(ctx, "POST", s.TokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.HTTP.Do(req)
	if err != nil {
		s.last = fmt.Errorf("Google's token service: %w", err)
		return "", s.last
	}
	defer resp.Body.Close()
	var r struct {
		Access  string `json:"access_token"`
		Expires int    `json:"expires_in"`
		Scope   string `json:"scope"`
		Error   string `json:"error"`
		Desc    string `json:"error_description"`
	}
	json.NewDecoder(resp.Body).Decode(&r)
	if r.Error == "invalid_grant" {
		s.last = ErrInvalidGrant
		return "", s.last
	}
	if r.Access == "" {
		s.last = fmt.Errorf("Google's token service: %s %s %s", resp.Status, r.Error, r.Desc)
		return "", s.last
	}
	if r.Expires <= 0 {
		r.Expires = 3600
	}
	s.access, s.until, s.last = r.Access, s.Now().Add(time.Duration(r.Expires-60)*time.Second), nil
	if r.Scope != "" { // Google says what the token may do: the file's scopes follow it (an older file had none)
		if sc := fields(r.Scope); !same(sc, t.Scopes) {
			t.Scopes = sc
			access, until := s.access, s.until
			s.writeLocked(*t, false)
			s.access, s.until = access, until // the same sign-in: its access token stays good
		}
	}
	return s.access, nil
}

// Health checks the credential: a refresh (cheap: one token call, cached for its hour) and the scopes.
func (s *Store) Health(ctx context.Context) Health {
	_, err := s.AccessToken(ctx)
	s.mu.Lock()
	t := s.tok
	s.mu.Unlock()
	switch {
	case errors.Is(err, ErrNone):
		return Health{State: "none", Detail: "not signed in yet"}
	case errors.Is(err, ErrInvalidGrant):
		h := Health{State: "invalid_grant", Detail: ErrInvalidGrant.Error()}
		if t != nil {
			h.Account, h.Granted = t.Account, t.Granted
		}
		return h
	case err != nil:
		return Health{State: "error", Detail: err.Error()}
	}
	h := Health{State: "ok", Detail: "signed in", Scopes: t.Scopes, Account: t.Account, Granted: t.Granted}
	if s.Want != nil {
		h.Missing = Missing(t.Scopes, s.Want())
		if len(h.Missing) > 0 {
			h.State, h.Detail = "scope_missing", "signed in, but not allowed: "+strings.Join(short(h.Missing), ", ")
		}
	}
	if h.Account != "" && h.State == "ok" {
		h.Detail = "signed in as " + h.Account
	}
	return h
}

// Missing are the scopes wanted and not granted (granted unknown, as in an older file: none missing).
func Missing(granted, want []string) []string {
	if len(granted) == 0 {
		return nil
	}
	var out []string
	for _, w := range want {
		if !contains(granted, w) {
			out = append(out, w)
		}
	}
	return out
}

// Save keeps a new token (a sign-in's): the one it replaces goes to .prev; chmod 600.
func (s *Store) Save(t Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked(t, true)
}

func (s *Store) writeLocked(t Token, keepPrev bool) error {
	sort.Strings(t.Scopes)
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	if keepPrev {
		if _, err := os.Stat(s.Path); err == nil {
			if err := os.Rename(s.Path, s.Path+".prev"); err != nil {
				return err
			}
		}
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		return err
	}
	st, _ := os.Stat(s.Path)
	c := t
	s.tok, s.access, s.last = &c, "", nil
	if st != nil {
		s.mtime = st.ModTime()
	}
	return nil
}

// SignOut revokes the sign-in at Google and keeps the file as .prev (Google's answer does not stop it: a
// token already invalid is revoked as far as Google is concerned).
func (s *Store) SignOut(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.load()
	if err != nil {
		return err
	}
	var revokeErr error
	req, _ := http.NewRequestWithContext(ctx, "POST", s.RevokeURL, strings.NewReader(url.Values{"token": {t.RefreshToken}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if resp, err := s.HTTP.Do(req); err != nil {
		revokeErr = fmt.Errorf("Google's revocation: %w", err)
	} else {
		resp.Body.Close()
		if resp.StatusCode >= 300 && resp.StatusCode != http.StatusBadRequest { // 400: already invalid
			revokeErr = fmt.Errorf("Google's revocation: %s", resp.Status)
		}
	}
	if err := os.Rename(s.Path, s.Path+".prev"); err != nil {
		return err
	}
	s.tok, s.access, s.last = nil, "", nil
	return revokeErr
}

// Transport adds the access token to each request (an http.Client for a Google API).
func (s *Store) Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return roundTrip(func(r *http.Request) (*http.Response, error) {
		tok, err := s.AccessToken(r.Context())
		if err != nil {
			return nil, err
		}
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+tok)
		return base.RoundTrip(r)
	})
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fields(s string) []string { f := strings.Fields(s); sort.Strings(f); return f }

func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	b = append([]string(nil), b...)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func short(sc []string) []string {
	out := make([]string, len(sc))
	for i, s := range sc {
		out[i] = strings.TrimPrefix(s, "https://www.googleapis.com/auth/")
	}
	return out
}

// Union: the scopes of both, once each (an incremental sign-in's grant with the earlier one's).
func Union(a, b []string) []string {
	m := map[string]bool{}
	for _, s := range append(append([]string(nil), a...), b...) {
		if s != "" {
			m[s] = true
		}
	}
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
