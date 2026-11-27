package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ibnHatab/fsttm-live/pkg/dialogue"
	"github.com/ibnHatab/nina-live/core"
	"github.com/ibnHatab/nina-live/tasks"
	"google.golang.org/genai"
)

// Two cards (the redesign's step 5, V1, s-b478fe59; config cards: two): the voice holds two stable tools
// instead of one per package, director(verb, …) and attended(card, intent, slots). The card it speaks to
// is the attended one: its full schema (the per-package tool's) goes into her silent context, keyed "card",
// whenever attention moves, and a call naming another card moves it in the same call. Every call is still
// the package's own (core.Call: through the director when home.route is director), so the rules, their
// refusals and their words are unchanged; only what the voice is declared changes.

type cardSet struct {
	k     *core.Core
	cards map[string]tasks.Tool // name → the per-package tool
	order []string
	mu    sync.Mutex
	at    string // the attended card
}

func newCards(k *core.Core) *cardSet {
	c := &cardSet{k: k, cards: map[string]tasks.Tool{}}
	for _, t := range k.HomeTools() {
		n := strings.TrimPrefix(t.Name, "home_")
		c.cards[n] = t
		c.order = append(c.order, n)
	}
	sort.Strings(c.order)
	return c
}

// line is a card's one line (its tool's description's first sentence).
func (c *cardSet) line(n string) string {
	d := c.cards[n].Description
	if i := strings.IndexAny(d, ".\n"); i > 0 {
		d = d[:i]
	}
	return n + ": " + d
}

// text is a card's whole schema for her context: its description and its parameters.
func (c *cardSet) text(n string) string {
	t := c.cards[n]
	b, _ := json.Marshal(t.Parameters)
	return fmt.Sprintf("CARD %s (the attended card; call attended with card %q, intent and slots as this schema says): %s\nSchema: %s", n, n, t.Description, b)
}

// attend moves attention to card n: its text into her context.
func (c *cardSet) attend(n string) {
	c.mu.Lock()
	moved := c.at != n
	c.at = n
	c.mu.Unlock()
	if moved {
		if v := c.k.Voice(); v != nil {
			v.Context("card", c.text(n))
		}
	}
}

func (c *cardSet) attended() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

// twoCards are the voice's tools in the two-card form: director, attended, then the read tools and delegate as before.
func twoCards(k *core.Core, reads []tasks.Tool, delegate *genai.FunctionDeclaration, delegateFn dialogue.ToolFunc) dialogue.Tools {
	c := newCards(k)
	var lines []string
	for _, n := range c.order {
		lines = append(lines, c.line(n))
	}
	obj := func(props map[string]any, req ...string) map[string]any {
		o := map[string]any{"type": "object", "properties": props}
		if len(req) > 0 {
			r := make([]any, len(req))
			for i, x := range req {
				r[i] = x
			}
			o["required"] = r
		}
		return o
	}
	cardEnum := make([]any, len(c.order))
	for i, n := range c.order {
		cardEnum[i] = n
	}
	decls := []*genai.FunctionDeclaration{
		{Name: "director", Description: "The home's director: which card you speak to. route: attend a card (to), its schema comes back and into your context; status: the card attended now; help: the cards. The cards: " + strings.Join(lines, "; ") + ".",
			ParametersJsonSchema: obj(map[string]any{
				"verb": map[string]any{"type": "string", "enum": []any{"route", "status", "help"}},
				"to":   map[string]any{"type": "string", "enum": cardEnum, "description": "the card to attend (route)"}}, "verb")},
		{Name: "attended", Description: "Ask the attended card (or name another card: attention moves to it): its intent and its slots, as the card's schema in your context says. The rules decide; a refusal says why, in the card's words.",
			ParametersJsonSchema: obj(map[string]any{
				"card":   map[string]any{"type": "string", "enum": cardEnum, "description": "the card, when not the attended one"},
				"intent": map[string]any{"type": "string", "description": "one of the card's intents"},
				"slots":  map[string]any{"type": "object", "description": "the card's slots for the intent, by name"}}, "intent")},
	}
	funcs := map[string]dialogue.ToolFunc{
		"director": func(_ context.Context, a map[string]any) map[string]any {
			switch verb, _ := a["verb"].(string); verb {
			case "route":
				to, _ := a["to"].(string)
				if _, ok := c.cards[to]; !ok {
					return map[string]any{"status": "refused", "text": "no card " + to + "; the cards: " + strings.Join(c.order, ", ")}
				}
				c.attend(to)
				return map[string]any{"status": "done", "text": c.text(to)}
			case "status":
				if n := c.attended(); n != "" {
					return map[string]any{"status": "done", "text": "attended: " + n}
				}
				return map[string]any{"status": "done", "text": "no card attended yet"}
			default:
				return map[string]any{"status": "done", "text": strings.Join(lines, "\n")}
			}
		},
		"attended": func(ctx context.Context, a map[string]any) map[string]any {
			n, _ := a["card"].(string)
			if n == "" {
				n = c.attended()
			}
			t, ok := c.cards[n]
			if !ok {
				return map[string]any{"status": "refused", "text": "no card attended: name one (" + strings.Join(c.order, ", ") + ")"}
			}
			c.attend(n)
			args := map[string]any{}
			if s, ok := a["slots"].(map[string]any); ok {
				for k, v := range s {
					args[k] = v
				}
			}
			args["intent"] = a["intent"]
			musicWords(t.Name, args)
			status, text := t.Call(ctx, args)
			return map[string]any{"status": status, "text": text}
		},
	}
	for _, t := range reads {
		t := t
		decls = append(decls, &genai.FunctionDeclaration{Name: t.Name, Description: t.Description, ParametersJsonSchema: t.Parameters})
		funcs[t.Name] = func(ctx context.Context, args map[string]any) map[string]any {
			status, text := t.Call(ctx, args)
			return map[string]any{"status": status, "text": text}
		}
	}
	if delegate != nil {
		decls = append(decls, delegate)
		funcs[delegate.Name] = delegateFn
	}
	return dialogue.Tools{Decls: []*genai.Tool{{FunctionDeclarations: decls}}, Funcs: funcs}
}
