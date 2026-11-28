// Package google is Nina's agenda without Home Assistant: Google Calendar (v3) and Google Tasks (v1)
// called directly, as agenda.Backend (her rules' writes) and as the read tools' lists and calendars
// (the redesign's Google services, s-b478fe59). The person's own OAuth client and one sign-in give a
// refresh token kept in a file (chmod 600); its access tokens are fetched here as they expire.
//
// Scopes: https://www.googleapis.com/auth/calendar.events and https://www.googleapis.com/auth/tasks; for her
// photo albums (photos.go) photoslibrary.readonly.appcreateddata and photoslibrary.appendonly.
package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Token is the credential file: the OAuth client and the person's refresh token.
type Token struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
}

// Client calls Google's APIs with the person's token.
type Client struct {
	tok  Token
	HTTP *http.Client
	// the endpoints (tests: an httptest server)
	TokenURL, CalendarURL, TasksURL, PhotosURL, PickerURL string
	// Calendars: the agenda's calendar ids by name (Google's calendarList needs a wider scope than
	// calendar.events: the person names them in the config)
	Calendars map[string]string
	Now       func() time.Time

	mu     sync.Mutex
	access string
	until  time.Time
	lists  []List // the task lists, read once a minute at most
	listAt time.Time
}

// List is a task list.
type List struct{ ID, Title string }

// Load reads the credential file (it must not be readable by others).
func Load(path string) (*Client, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("google: %s is readable by others (chmod 600)", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t Token
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("google: %s: %w", path, err)
	}
	if t.RefreshToken == "" || t.ClientID == "" {
		return nil, fmt.Errorf("google: %s has no client or refresh token", path)
	}
	return New(t), nil
}

// New is a client for a token.
func New(t Token) *Client {
	return &Client{tok: t, HTTP: &http.Client{Timeout: 20 * time.Second},
		TokenURL: "https://oauth2.googleapis.com/token", CalendarURL: "https://www.googleapis.com/calendar/v3",
		TasksURL: "https://tasks.googleapis.com/tasks/v1", Now: time.Now}
}

func (c *Client) token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.access != "" && c.Now().Before(c.until) {
		return c.access, nil
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {c.tok.RefreshToken},
		"client_id": {c.tok.ClientID}, "client_secret": {c.tok.ClientSecret}}
	req, _ := http.NewRequestWithContext(ctx, "POST", c.TokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("google: the token: %w", err)
	}
	defer resp.Body.Close()
	var r struct {
		Access  string `json:"access_token"`
		Expires int    `json:"expires_in"`
		Error   string `json:"error"`
		Desc    string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil || r.Access == "" {
		return "", fmt.Errorf("google: the token: %s %s %s (%v)", resp.Status, r.Error, r.Desc, err)
	}
	c.access, c.until = r.Access, c.Now().Add(time.Duration(r.Expires-60)*time.Second)
	return c.access, nil
}

// call is one API request; out (if not nil) is the JSON answer.
func (c *Client) call(ctx context.Context, method, u string, body, out any) error {
	tok, err := c.token(ctx)
	if err != nil {
		return err
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, u, rd)
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		json.Unmarshal(b, &e)
		return fmt.Errorf("google: %s %s: %s %s", method, strings.SplitN(u, "?", 2)[0], resp.Status, e.Error.Message)
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

// ---- agenda.Backend

// CreateEvent: an event in a calendar (start and end local "2006-01-02 15:04:05").
func (c *Client) CreateEvent(ctx context.Context, id, summary, start, end, note string) error {
	s, err := time.ParseInLocation("2006-01-02 15:04:05", start, time.Local)
	if err != nil {
		return err
	}
	e, err := time.ParseInLocation("2006-01-02 15:04:05", end, time.Local)
	if err != nil {
		return err
	}
	body := map[string]any{"summary": summary, "description": note,
		"start": map[string]string{"dateTime": s.Format(time.RFC3339)}, "end": map[string]string{"dateTime": e.Format(time.RFC3339)}}
	return c.call(ctx, "POST", c.CalendarURL+"/calendars/"+url.PathEscape(id)+"/events", body, nil)
}

// AddItem: a task on a task list (due "2006-01-02" or "").
func (c *Client) AddItem(ctx context.Context, id, summary, due, note string) error {
	list, err := c.listID(ctx, id)
	if err != nil {
		return err
	}
	body := map[string]any{"title": summary, "notes": note}
	if due != "" {
		body["due"] = due + "T00:00:00.000Z" // Tasks keeps the date only
	}
	return c.call(ctx, "POST", c.TasksURL+"/lists/"+url.PathEscape(list)+"/tasks", body, nil)
}

type task struct {
	ID, Title, Status, Due string
}

func (c *Client) tasks(ctx context.Context, list string) ([]task, error) {
	var r struct{ Items []task }
	err := c.call(ctx, "GET", c.TasksURL+"/lists/"+url.PathEscape(list)+"/tasks?showCompleted=false&maxResults=100", nil, &r)
	return r.Items, err
}

// RemoveItem: a task off a list, by its own title on it.
func (c *Client) RemoveItem(ctx context.Context, id, item string) error {
	list, err := c.listID(ctx, id)
	if err != nil {
		return err
	}
	ts, err := c.tasks(ctx, list)
	if err != nil {
		return err
	}
	for _, t := range ts {
		if t.Title == item {
			return c.call(ctx, "DELETE", c.TasksURL+"/lists/"+url.PathEscape(list)+"/tasks/"+url.PathEscape(t.ID), nil, nil)
		}
	}
	return fmt.Errorf("no %q on it", item)
}

// Items: a list's open tasks, their titles.
func (c *Client) Items(ctx context.Context, id string) ([]string, error) {
	list, err := c.listID(ctx, id)
	if err != nil {
		return nil, err
	}
	ts, err := c.tasks(ctx, list)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, t := range ts {
		out = append(out, t.Title)
	}
	return out, nil
}

// TaskLists are the person's task lists (cached a minute).
func (c *Client) TaskLists(ctx context.Context) ([]List, error) {
	c.mu.Lock()
	if c.lists != nil && c.Now().Sub(c.listAt) < time.Minute {
		l := c.lists
		c.mu.Unlock()
		return l, nil
	}
	c.mu.Unlock()
	var r struct {
		Items []struct{ ID, Title string }
	}
	if err := c.call(ctx, "GET", c.TasksURL+"/users/@me/lists?maxResults=100", nil, &r); err != nil {
		return nil, err
	}
	var out []List
	for _, l := range r.Items {
		out = append(out, List{ID: l.ID, Title: l.Title})
	}
	c.mu.Lock()
	c.lists, c.listAt = out, c.Now()
	c.mu.Unlock()
	return out, nil
}

// listID: a list given by its id or its title ("@default": the person's first list).
func (c *Client) listID(ctx context.Context, id string) (string, error) {
	if id == "@default" {
		return id, nil
	}
	ls, err := c.TaskLists(ctx)
	if err != nil {
		return "", err
	}
	for _, l := range ls {
		if l.ID == id {
			return id, nil
		}
	}
	for _, l := range ls {
		if strings.EqualFold(l.Title, id) {
			return l.ID, nil
		}
	}
	return "", fmt.Errorf("no task list %q", id)
}

// ---- reading

// Events: a calendar's events from now on, within the window, in words ("Mon 10:00 Dentist").
func (c *Client) Events(ctx context.Context, id string, window time.Duration, n int) ([]string, error) {
	now := c.Now()
	q := url.Values{"timeMin": {now.Format(time.RFC3339)}, "timeMax": {now.Add(window).Format(time.RFC3339)},
		"singleEvents": {"true"}, "orderBy": {"startTime"}, "maxResults": {fmt.Sprint(n)}}
	var r struct {
		Items []struct {
			Summary  string
			Location string
			Start    struct{ DateTime, Date string }
		}
	}
	if err := c.call(ctx, "GET", c.CalendarURL+"/calendars/"+url.PathEscape(id)+"/events?"+q.Encode(), nil, &r); err != nil {
		return nil, err
	}
	var out []string
	for _, e := range r.Items {
		when := e.Start.Date
		if t, err := time.Parse(time.RFC3339, e.Start.DateTime); err == nil {
			when = t.Local().Format("Mon 15:04")
		} else if t, err := time.Parse("2006-01-02", e.Start.Date); err == nil {
			when = t.Format("Mon") + " (all day)"
		}
		s := when + " " + e.Summary
		if e.Location != "" {
			s += " (" + e.Location + ")"
		}
		out = append(out, s)
	}
	return out, nil
}
