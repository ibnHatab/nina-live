// Package mcp lets Nina's tasks use MCP servers' tools (the user, 2026-11-25: pi-core has web search and
// the models' transport; MCP adapters next, Home Assistant's first, so Nina can configure the home
// herself). A client of the streamable HTTP transport (MCP 2025-03-26): initialize, tools/list,
// tools/call, answered as JSON or as an event stream; each server's tools become tasks.Tools named
// <prefix>_<tool>, in the tasks only (a delegated job), not in the voice's live list.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ibnHatab/nina-live/tasks"
)

// Server is one MCP server in Nina's config.
type Server struct {
	Name      string   `yaml:"name"`       // its tools' prefix (ha: ha_GetLiveContext)
	URL       string   `yaml:"url"`        // the streamable HTTP endpoint (Home Assistant: /api/mcp)
	TokenFile string   `yaml:"token_file"` // a bearer token, in a file (never in the config)
	Domain    string   `yaml:"domain"`     // the package its tools reach (steering), default home
	Allow     []string `yaml:"allow"`      // only these tools (empty: all)
	Deny      []string `yaml:"deny"`       // never these
}

// Client is one server's session.
type Client struct {
	S       Server
	token   string
	hc      *http.Client
	mu      sync.Mutex
	session string
	id      int
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[2:])
	}
	return p
}

// Dial opens a session: initialize, then the initialized notification.
func Dial(ctx context.Context, s Server) (*Client, error) {
	c := &Client{S: s, hc: &http.Client{Timeout: 60 * time.Second}}
	if s.TokenFile != "" {
		b, err := os.ReadFile(expand(s.TokenFile))
		if err != nil {
			return nil, fmt.Errorf("mcp %s: token: %w", s.Name, err)
		}
		c.token = strings.TrimSpace(string(b))
	}
	if _, err := c.call(ctx, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "nina", "version": "1"}}); err != nil {
		return nil, fmt.Errorf("mcp %s: initialize: %w", s.Name, err)
	}
	c.notify(ctx, "notifications/initialized")
	return c, nil
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) post(ctx context.Context, msg map[string]any) (*http.Response, error) {
	b, _ := json.Marshal(msg)
	req, err := http.NewRequestWithContext(ctx, "POST", c.S.URL, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	c.mu.Lock()
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
	}
	c.mu.Unlock()
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.mu.Lock()
		c.session = sid
		c.mu.Unlock()
	}
	return resp, nil
}

func (c *Client) notify(ctx context.Context, method string) {
	if resp, err := c.post(ctx, map[string]any{"jsonrpc": "2.0", "method": method}); err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// call sends a request and reads its answer (a JSON body, or the event stream's message with its id).
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.id++
	id := c.id
	c.mu.Unlock()
	resp, err := c.post(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound && c.session != "" && method != "initialize" { // the session expired: again
		c.mu.Lock()
		c.session = ""
		c.mu.Unlock()
		if _, err := c.call(ctx, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{},
			"clientInfo": map[string]any{"name": "nina", "version": "1"}}); err != nil {
			return nil, err
		}
		c.notify(ctx, "notifications/initialized")
		return c.call(ctx, method, params)
	}
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		return nil, fmt.Errorf("%s: HTTP %d %s", method, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var msg struct {
		ID     any             `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *rpcErr         `json:"error"`
	}
	answer := func(b []byte) (bool, error) {
		if json.Unmarshal(b, &msg) != nil || msg.ID == nil {
			return false, nil
		}
		if msg.Error != nil {
			return true, fmt.Errorf("%s: %s (%d)", method, msg.Error.Message, msg.Error.Code)
		}
		return true, nil
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		var data strings.Builder
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "data:"):
				data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			case line == "" && data.Len() > 0:
				if ok, err := answer([]byte(data.String())); ok {
					return msg.Result, err
				}
				data.Reset()
			}
		}
		if data.Len() > 0 {
			if ok, err := answer([]byte(data.String())); ok {
				return msg.Result, err
			}
		}
		return nil, fmt.Errorf("%s: the stream ended without an answer", method)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if ok, err := answer(b); ok {
		return msg.Result, err
	}
	return nil, fmt.Errorf("%s: no answer in %q", method, string(b[:min(len(b), 200)]))
}

// Spec is a server's tool as tools/list gives it.
type Spec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// List is the server's tools (every page).
func (c *Client) List(ctx context.Context) ([]Spec, error) {
	var all []Spec
	cursor := ""
	for {
		p := map[string]any{}
		if cursor != "" {
			p["cursor"] = cursor
		}
		raw, err := c.call(ctx, "tools/list", p)
		if err != nil {
			return nil, err
		}
		var r struct {
			Tools      []Spec `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		all = append(all, r.Tools...)
		if cursor = r.NextCursor; cursor == "" {
			return all, nil
		}
	}
}

// Call runs a tool: its text content, and whether the server called it an error.
func (c *Client) Call(ctx context.Context, name string, args map[string]any) (string, bool, error) {
	if args == nil {
		args = map[string]any{}
	}
	raw, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return "", true, err
	}
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent any  `json:"structuredContent"`
		IsError           bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return string(raw), false, nil
	}
	var parts []string
	for _, p := range r.Content {
		if p.Type == "text" {
			parts = append(parts, p.Text)
		} else {
			parts = append(parts, "["+p.Type+"]")
		}
	}
	if len(parts) == 0 && r.StructuredContent != nil {
		b, _ := json.Marshal(r.StructuredContent)
		parts = append(parts, string(b))
	}
	return strings.Join(parts, "\n"), r.IsError, nil
}

// Tools is the server's tools as Nina's tasks use them (allow/deny applied).
func (c *Client) Tools(ctx context.Context) ([]tasks.Tool, error) {
	specs, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	in := func(l []string, n string) bool {
		for _, x := range l {
			if strings.EqualFold(x, n) {
				return true
			}
		}
		return false
	}
	dom := c.S.Domain
	if dom == "" {
		dom = "home"
	}
	var out []tasks.Tool
	for _, sp := range specs {
		if (len(c.S.Allow) > 0 && !in(c.S.Allow, sp.Name)) || in(c.S.Deny, sp.Name) {
			continue
		}
		name := sp.Name
		schema := sp.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, tasks.Tool{
			Name:        c.S.Name + "_" + name,
			Description: fmt.Sprintf("[%s, by MCP] %s", c.S.Name, sp.Description),
			Parameters:  schema,
			Domain:      dom,
			Call: func(ctx context.Context, args map[string]any) (string, string) {
				text, isErr, err := c.Call(ctx, name, args)
				if err != nil {
					return "error", err.Error()
				}
				if isErr {
					return "error", text
				}
				if len(text) > 12000 { // a model's context is not a log
					text = text[:12000] + "\n…(cut)"
				}
				return "done", text
			},
		})
	}
	return out, nil
}
