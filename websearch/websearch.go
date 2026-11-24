// Package websearch is Nina's web_search tool (the user, 2026-11-25: she suffered without the
// information; Google Search grounding through Gemini Flash, as a pi tool): one query asked of a
// Gemini Flash model with the GoogleSearch tool, its grounded answer with the sources. The voice and
// the delegated tasks share it. Google bills searches per query the model runs (Gemini 3 and newer:
// 5,000 a month free, shared, then $14 per 1,000), so a day has a cap, counted from the log.
package websearch

import (
	"context"
	"fmt"
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
	Client *genai.Client
	Models []string // tried in order (an overloaded one gives way to the next)
	Daily  int      // web queries a day at most (0: 150, ~4,500 a month)
	Try    time.Duration
	Log    *nlog.Log
	Now    func() float64

	mu    sync.Mutex
	day   string
	count int
}

// New makes a searcher on the Gemini API with the key, its count for today read from the log.
func New(ctx context.Context, key string, models []string, daily int, l *nlog.Log, now func() float64) (*Searcher, error) {
	c, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: key, Backend: genai.BackendGeminiAPI})
	if err != nil {
		return nil, err
	}
	s := &Searcher{Client: c, Models: models, Daily: daily, Log: l, Now: now, Try: 25 * time.Second}
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
	for _, m := range s.Models {
		c, cancel := context.WithTimeout(ctx, s.Try)
		resp, err := s.Client.Models.GenerateContent(c, m, genai.Text(instruction+query),
			&genai.GenerateContentConfig{Tools: []*genai.Tool{{GoogleSearch: &genai.GoogleSearch{}}}})
		cancel()
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
