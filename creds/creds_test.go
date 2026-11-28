package creds

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// a fake Google token and revocation service: refresh "good" works, anything else is invalid_grant
func fakeGoogle(t *testing.T, scope string) (*httptest.Server, *int32, *int32) {
	var refreshes, revokes int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		switch r.URL.Path {
		case "/token":
			atomic.AddInt32(&refreshes, 1)
			if r.Form.Get("refresh_token") != "good" || r.Form.Get("client_id") != "cid" {
				w.WriteHeader(400)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "Token has been expired or revoked."})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at-1", "expires_in": 3599, "scope": scope})
		case "/revoke":
			atomic.AddInt32(&revokes, 1)
			w.WriteHeader(200)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &refreshes, &revokes
}

func store(t *testing.T, srv *httptest.Server, tok *Token) *Store {
	dir := t.TempDir()
	s := Open(filepath.Join(dir, "google-token.json"))
	s.TokenURL, s.RevokeURL = srv.URL+"/token", srv.URL+"/revoke"
	if tok != nil {
		b, _ := json.Marshal(tok)
		if err := os.WriteFile(s.Path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// none, then a refresh cached for its hour, the granted scopes learned from Google's answer
func TestAccessTokenAndScopes(t *testing.T) {
	srv, refreshes, _ := fakeGoogle(t, "https://www.googleapis.com/auth/tasks https://www.googleapis.com/auth/calendar.events")
	s := store(t, srv, nil)
	if _, err := s.AccessToken(context.Background()); err != ErrNone {
		t.Fatalf("no file: %v", err)
	}
	if h := s.Health(context.Background()); h.State != "none" {
		t.Fatalf("health without a file: %+v", h)
	}
	b, _ := json.Marshal(Token{ClientID: "cid", ClientSecret: "sec", RefreshToken: "good"})
	os.WriteFile(s.Path, b, 0o600)
	for i := 0; i < 3; i++ {
		if a, err := s.AccessToken(context.Background()); err != nil || a != "at-1" {
			t.Fatalf("access: %q %v", a, err)
		}
	}
	if n := atomic.LoadInt32(refreshes); n != 1 {
		t.Fatalf("refreshes: %d, want 1 (cached)", n)
	}
	if tk := s.Token(); len(tk.Scopes) != 2 || tk.Scopes[0] != "https://www.googleapis.com/auth/calendar.events" {
		t.Fatalf("scopes kept: %v", tk.Scopes)
	}
	s.Want = func() []string {
		return []string{"https://www.googleapis.com/auth/tasks", "https://www.googleapis.com/auth/photoslibrary.appendonly"}
	}
	if h := s.Health(context.Background()); h.State != "scope_missing" || len(h.Missing) != 1 || !strings.Contains(h.Detail, "photoslibrary.appendonly") {
		t.Fatalf("missing scope: %+v", h)
	}
	if st, _ := os.Stat(s.Path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode: %v", st.Mode())
	}
}

// Google refusing the refresh token: invalid_grant, said as such
func TestInvalidGrant(t *testing.T) {
	srv, _, _ := fakeGoogle(t, "")
	s := store(t, srv, &Token{ClientID: "cid", ClientSecret: "sec", RefreshToken: "expired"})
	if _, err := s.AccessToken(context.Background()); err != ErrInvalidGrant {
		t.Fatalf("err: %v", err)
	}
	if h := s.Health(context.Background()); h.State != "invalid_grant" {
		t.Fatalf("health: %+v", h)
	}
}

// a file readable by others is refused
func TestModeChecked(t *testing.T) {
	srv, _, _ := fakeGoogle(t, "")
	s := store(t, srv, &Token{ClientID: "cid", ClientSecret: "sec", RefreshToken: "good"})
	os.Chmod(s.Path, 0o644)
	if _, err := s.AccessToken(context.Background()); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("err: %v", err)
	}
}

// a new sign-in keeps the old file as .prev; sign-out revokes and keeps it as .prev too
func TestSaveAndSignOut(t *testing.T) {
	srv, _, revokes := fakeGoogle(t, "")
	s := store(t, srv, &Token{ClientID: "cid", ClientSecret: "sec", RefreshToken: "old"})
	if err := s.Save(Token{ClientID: "cid", ClientSecret: "sec", RefreshToken: "good", Scopes: []string{"b", "a"}, Granted: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	prev, _ := os.ReadFile(s.Path + ".prev")
	if !strings.Contains(string(prev), `"old"`) {
		t.Fatalf(".prev: %s", prev)
	}
	if tk := s.Token(); tk.RefreshToken != "good" || tk.Scopes[0] != "a" {
		t.Fatalf("saved: %+v", tk)
	}
	if err := s.SignOut(context.Background()); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(revokes) != 1 {
		t.Fatal("not revoked")
	}
	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Fatal("the token file is still there")
	}
	prev, _ = os.ReadFile(s.Path + ".prev")
	if !strings.Contains(string(prev), `"good"`) {
		t.Fatalf(".prev after sign-out: %s", prev)
	}
	if h := s.Health(context.Background()); h.State != "none" {
		t.Fatalf("after sign-out: %+v", h)
	}
}

// the transport puts the access token on each request
func TestTransport(t *testing.T) {
	srv, _, _ := fakeGoogle(t, "")
	s := store(t, srv, &Token{ClientID: "cid", ClientSecret: "sec", RefreshToken: "good"})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(r.Header.Get("Authorization"))) }))
	defer api.Close()
	c := &http.Client{Transport: s.Transport(nil)}
	resp, err := c.Get(api.URL)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 64)
	n, _ := resp.Body.Read(b)
	resp.Body.Close()
	if string(b[:n]) != "Bearer at-1" {
		t.Fatalf("header: %q", b[:n])
	}
}

func TestUnion(t *testing.T) {
	if u := Union([]string{"b", "a"}, []string{"c", "a", ""}); strings.Join(u, ",") != "a,b,c" {
		t.Fatalf("union: %v", u)
	}
	if m := Missing(nil, []string{"x"}); m != nil {
		t.Fatalf("granted unknown: %v", m)
	}
}
