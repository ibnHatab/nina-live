// Package websearch is Nina's web_search tool, a pi tool the voice and the delegated tasks share
// (the user, 2026-11-25: she suffered without the information). Its backend is SearXNG (self-hosted
// metasearch, its JSON API: the top results' titles, snippets and sites, for the asking model to
// answer from; no account, no cost), and, when the Gemini key's project allows it, Google Search
// grounding through Gemini Flash (a grounded answer with its sources; Gemini 3: 5,000 searches a
// month free, then $14 per 1,000, so a day has a cap, counted from the log).
package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	nlog "github.com/ibnHatab/nina-live/log"
	"github.com/ibnHatab/nina-live/tasks"
	"google.golang.org/genai"
)

// Stream is the log's stream of searches (each: the query, the model, the web queries it ran).
const Stream = "search"

type Searcher struct {
	SearXNG string        // its URL (http://127.0.0.1:8888): tried first; "" none
	Top     int           // SearXNG's results given (0: 5)
	Client  *genai.Client // nil: no grounding
	Models  []string      // tried in order (an overloaded one gives way to the next)
	Daily   int           // web queries a day at most (0: 150, ~4,500 a month)
	Try     time.Duration
	Log     *nlog.Log
	Now     func() float64
	Strain  func(err error) // the grounding's outcome (nil: it answered): what its errors say about her

	mu    sync.Mutex
	day   string
	count int
}

// New makes a searcher on the Gemini API with the key, its count for today read from the log.
func New(ctx context.Context, searxng, key string, models []string, daily int, l *nlog.Log, now func() float64) (*Searcher, error) {
	s := &Searcher{SearXNG: strings.TrimRight(searxng, "/"), Models: models, Daily: daily, Log: l, Now: now, Try: 25 * time.Second}
	if key != "" && len(models) > 0 {
		c, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: key, Backend: genai.BackendGeminiAPI})
		if err != nil {
			return nil, err
		}
		s.Client = c
	}
	if s.Daily <= 0 {
		s.Daily = 150
	}
	if s.Now == nil {
		s.Now = func() float64 { return float64(time.Now().UnixNano()) / 1e9 }
	}
	s.day = time.Unix(int64(s.Now()), 0).Format("2006-01-02")
	if l != nil {
		midnight, _ := time.ParseInLocation("2006-01-02", s.day, time.Local)
		l.Read(ctx, 0, func(e nlog.Event) error {
			if e.At >= float64(midnight.Unix()) && e.Kind == "searched" {
				s.count += max(1, strings.Count(string(e.Data), `"q:`))
			}
			return nil
		}, Stream)
	}
	return s, nil
}

const instruction = "Answer from a web search, for a voice assistant that will say it aloud: two to four short sentences, " +
	"the facts asked for, with the date they are from when it matters; say plainly when the search does not settle it. " +
	"No lists, no markup. The question: "

// Search answers one question from the web: the text and its sources.
func (s *Searcher) Search(ctx context.Context, query string) (string, error) {
	s.mu.Lock()
	today := time.Unix(int64(s.Now()), 0).Format("2006-01-02")
	if today != s.day {
		s.day, s.count = today, 0
	}
	if s.count >= s.Daily {
		s.mu.Unlock()
		return "", fmt.Errorf("today's web searches are used up (%d): tomorrow again", s.Daily)
	}
	s.mu.Unlock()
	var errs []string
	if s.SearXNG != "" {
		text, err := s.searx(ctx, query)
		if err == nil {
			return text, nil
		}
		errs = append(errs, "searxng: "+err.Error())
	}
	for _, m := range s.Models {
		if s.Client == nil {
			break
		}
		c, cancel := context.WithTimeout(ctx, s.Try)
		resp, err := s.Client.Models.GenerateContent(c, m, genai.Text(instruction+query),
			&genai.GenerateContentConfig{Tools: []*genai.Tool{{GoogleSearch: &genai.GoogleSearch{}}}})
		cancel()
		if s.Strain != nil {
			s.Strain(err)
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %.160s", m, err))
			continue
		}
		text := strings.TrimSpace(resp.Text())
		if text == "" {
			errs = append(errs, m+": an empty answer")
			continue
		}
		var queries, sources []string
		if len(resp.Candidates) > 0 && resp.Candidates[0].GroundingMetadata != nil {
			g := resp.Candidates[0].GroundingMetadata
			queries = g.WebSearchQueries
			seen := map[string]bool{}
			for _, ch := range g.GroundingChunks {
				if ch.Web != nil && ch.Web.Title != "" && !seen[ch.Web.Title] && len(sources) < 4 {
					seen[ch.Web.Title] = true
					sources = append(sources, ch.Web.Title)
				}
			}
		}
		s.mu.Lock()
		s.count += max(1, len(queries))
		n := s.count
		s.mu.Unlock()
		if s.Log != nil {
			qs := make([]string, len(queries))
			for i, q := range queries {
				qs[i] = "q:" + q
			}
			s.Log.Append(s.Now(), Stream, "searched", "", "", map[string]any{"query": query, "model": m, "queries": qs,
				"sources": sources, "today": n}, 1)
		}
		if len(sources) > 0 {
			text += "\n(Sources: " + strings.Join(sources, "; ") + ")"
		}
		return text, nil
	}
	if s.Log != nil {
		s.Log.Append(s.Now(), Stream, "failed", "", "", map[string]any{"query": query, "errors": errs}, 1)
	}
	return "", fmt.Errorf("the web search failed: %s", strings.Join(errs, "; "))
}

// Tool is web_search for the voice and the tasks.
func (s *Searcher) Tool() tasks.Tool {
	return tasks.Tool{Name: "web_search", Domain: "web",
		Description: "Search the web (Google) for what you don't know: news and current events, facts about the world, " +
			"opening hours, prices, people, places, anything outside the home or newer than you. Use it rather than guessing " +
			"or saying you can't know. One clear question, in the person's language or English.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "the question, complete (who, what, where, when)"}},
			"required": []string{"query"}},
		Call: func(ctx context.Context, args map[string]any) (string, string) {
			q, _ := args["query"].(string)
			if strings.TrimSpace(q) == "" {
				return "error", "a question is needed"
			}
			text, err := s.Search(ctx, q)
			if err != nil {
				return "error", err.Error()
			}
			return "done", text
		}}
}

// searx asks SearXNG: the top results as lines the asking model answers from.
func (s *Searcher) searx(ctx context.Context, query string) (string, error) {
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(c, "GET", s.SearXNG+"/search?format=json&q="+url.QueryEscape(query), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var d struct {
		Results []struct {
			Title, URL, Content string
			Published           string `json:"publishedDate"`
		} `json:"results"`
		Answers []any `json:"answers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return "", err
	}
	if len(d.Results) == 0 {
		return "", fmt.Errorf("no results")
	}
	top := s.Top
	if top <= 0 {
		top = 5
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Web results for %q (answer from them, say the source in a few words; they may be partial):\n", query)
	var sites []string
	for i, r := range d.Results {
		if i == top {
			break
		}
		site := r.URL
		if u, err := url.Parse(r.URL); err == nil {
			site = strings.TrimPrefix(u.Host, "www.")
		}
		sites = append(sites, site)
		date := ""
		if len(r.Published) >= 10 {
			date = ", " + r.Published[:10]
		}
		fmt.Fprintf(&b, "%d. %s: %s (%s%s)\n", i+1, strings.TrimSpace(r.Title), strings.Join(strings.Fields(r.Content), " "), site, date)
	}
	s.mu.Lock()
	s.count++
	n := s.count
	s.mu.Unlock()
	if s.Log != nil {
		s.Log.Append(s.Now(), Stream, "searched", "", "", map[string]any{"query": query, "backend": "searxng", "sources": sites, "today": n}, 1)
	}
	return strings.TrimSpace(b.String()), nil
}
