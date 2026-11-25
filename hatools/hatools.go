// Package hatools is Nina's own Home Assistant tool set (the user, 2026-11-25: combine the two MCP
// servers into our own, remove the complicated tools, align the tools to the domains; Flash is the
// default model, so no 30,000 tokens a turn; don't build what will not be used).
//
// The two servers are its backends: Home Assistant's own MCP server (assist: the system log, the
// history) and ha-mcp (config: search, the automations). Their tools are not given to the model; three
// short ones of Nina's are, each for a need of a domain in docs/DOMAINS.md that her own domains do
// not cover:
//
//	ha_find        what exists: entities, automations, scripts, scenes, by words (any domain's lookups)
//	ha_automation  routines: list, get, set, remove an automation, and its last runs (the user's ask)
//	ha_diagnose    diagnostics: the system log, an entity's history
//
// Controlling devices is not here: lights, air, music, the light's colours, timers, the agenda are her
// domains' own tools, whose rules decide (a change made through Home Assistant goes through a Metis
// domain). The servers' own tools stay available raw for a larger model (mcp: raw: true).
package hatools

import (
	"context"
	"strings"
	"time"

	"github.com/ibnHatab/nina-live/mcp"
	"github.com/ibnHatab/nina-live/tasks"
)

// Caller is a backend's tools/call (an *mcp.Client).
type Caller interface {
	Call(ctx context.Context, name string, args map[string]any) (string, bool, error)
}

// Max is a result's length at most (a model's context is not a log).
const Max = 6000

func result(text string, isErr bool, err error) (string, string) {
	if err != nil {
		return "error", err.Error()
	}
	if len(text) > Max {
		text = text[:Max] + "\n…(cut)"
	}
	if isErr {
		return "error", text
	}
	return "done", text
}

func str(a map[string]any, k string) string { s, _ := a[k].(string); return strings.TrimSpace(s) }

// Tools are Nina's tools over the backends; a nil backend leaves out the tools that need it.
func Tools(assist, config Caller, now func() time.Time) []tasks.Tool {
	if now == nil {
		now = time.Now
	}
	var out []tasks.Tool
	if config != nil {
		out = append(out, find(config), automation(config))
	}
	if assist != nil {
		out = append(out, diagnose(assist, now))
	}
	return out
}

func find(c Caller) tasks.Tool {
	return tasks.Tool{Name: "ha_find", Domain: "home",
		Description: "Find what exists in Home Assistant by words: entities (their ids and states), automations, scripts, scenes. Use it to get an exact entity_id before writing an automation.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"words": map[string]any{"type": "string", "description": "a name, a room, an entity_id or a word of its configuration"},
			"kind":  map[string]any{"type": "string", "enum": []string{"entity", "automation", "script", "scene"}, "description": "only this kind (default: all)"}},
			"required": []string{"words"}},
		Call: func(ctx context.Context, a map[string]any) (string, string) {
			args := map[string]any{"query": str(a, "words"), "exact_match": false, "limit": 10,
				"result_fields": []string{"entity_id", "friendly_name", "state", "area"}}
			switch k := str(a, "kind"); k {
			case "entity":
				args["fields"] = []string{"entities"}
				args["search_types"] = []string{}
			case "automation", "script", "scene":
				args["search_types"] = []string{k}
				args["fields"] = []string{k + "s"}
			}
			return result(c.Call(ctx, "ha_search", args))
		}}
}

func automation(c Caller) tasks.Tool {
	return tasks.Tool{Name: "ha_automation", Domain: "home",
		Description: "Home Assistant's automations. list: by words; get: one's whole configuration; set: create one (no id) or replace one (its id) with a whole configuration (alias, description, triggers, conditions, actions, mode), get it first when changing; remove: delete one; runs: its last runs (did it fire, what happened).",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"list", "get", "set", "remove", "runs"}},
			"id":     map[string]any{"type": "string", "description": "the automation's entity_id (automation.x); set: omit to create"},
			"words":  map[string]any{"type": "string", "description": "list: what to look for (empty: all)"},
			"config": map[string]any{"type": "object", "description": "set: the whole automation (alias, description, triggers, conditions, actions, mode)"}},
			"required": []string{"action"}},
		Call: func(ctx context.Context, a map[string]any) (string, string) {
			id := str(a, "id")
			switch str(a, "action") {
			case "list":
				return result(c.Call(ctx, "ha_search", map[string]any{"query": str(a, "words"), "exact_match": false,
					"search_types": []string{"automation"}, "fields": []string{"automations"}, "limit": 25}))
			case "get":
				if id == "" {
					return "error", "get: the automation's id"
				}
				return result(c.Call(ctx, "ha_config_get_automation", map[string]any{"entity_id": id}))
			case "set":
				cfg, _ := a["config"].(map[string]any)
				if len(cfg) == 0 {
					return "error", "set: the whole configuration (alias, triggers, actions)"
				}
				args := map[string]any{"config": cfg, "MandatoryBPS": false}
				if id != "" {
					args["entity_id"] = id
				}
				return result(c.Call(ctx, "ha_config_set_automation", args))
			case "remove":
				if id == "" {
					return "error", "remove: the automation's id"
				}
				return result(c.Call(ctx, "ha_config_remove_automation", map[string]any{"entity_id": id}))
			case "runs":
				if id == "" {
					return "error", "runs: the automation's id"
				}
				return result(c.Call(ctx, "ha_get_automation_traces", map[string]any{"automation_id": id, "limit": 3}))
			}
			return "error", "action: list, get, set, remove or runs"
		}}
}

func diagnose(c Caller, now func() time.Time) tasks.Tool {
	return tasks.Tool{Name: "ha_diagnose", Domain: "diagnostics",
		Description: "Find why something in Home Assistant failed or disagrees. log: the system log's errors and warnings (optionally one integration's); history: how entities changed over the last hours.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"what":     map[string]any{"type": "string", "enum": []string{"log", "history"}},
			"about":    map[string]any{"type": "string", "description": "log: an integration or logger (hue, automation, alexa_devices); history: entity_ids, comma separated"},
			"hours":    map[string]any{"type": "number", "description": "history: how far back (default 6)"},
			"warnings": map[string]any{"type": "boolean", "description": "log: warnings too (default: errors only)"}},
			"required": []string{"what"}},
		Call: func(ctx context.Context, a map[string]any) (string, string) {
			switch str(a, "what") {
			case "log":
				args := map[string]any{"limit": 20}
				if w, _ := a["warnings"].(bool); !w {
					args["level"] = "error"
				}
				if l := str(a, "about"); l != "" {
					args["logger"] = l
				}
				return result(c.Call(ctx, "system_log__get_entries", args))
			case "history":
				var ids []string
				for _, s := range strings.Split(str(a, "about"), ",") {
					if s = strings.TrimSpace(s); s != "" {
						ids = append(ids, s)
					}
				}
				if len(ids) == 0 {
					return "error", "history: entity_ids in about"
				}
				h, _ := a["hours"].(float64)
				if h <= 0 {
					h = 6
				}
				start := now().Add(-time.Duration(h * float64(time.Hour))).Format(time.RFC3339)
				return result(c.Call(ctx, "history__get_history", map[string]any{"entity_ids": ids, "start_time": start}))
			}
			return "error", "what: log or history"
		}}
}

// Dial opens the configured servers: Nina's tools over assist and config (by name), and any raw
// server's own tools (raw: true, the open road for a larger model). A server that does not answer is
// left out, said in the log.
func Dial(ctx context.Context, servers []mcp.Server, logf func(string, ...any)) []tasks.Tool {
	var assist, config Caller
	var out []tasks.Tool
	for _, s := range servers {
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		c, err := mcp.Dial(cctx, s)
		if err == nil && s.Raw {
			var ts []tasks.Tool
			if ts, err = c.Tools(cctx); err == nil {
				out = append(out, ts...)
				logf("mcp %s: %d of its own tools (raw)", s.Name, len(ts))
			}
		}
		cancel()
		if err != nil {
			logf("mcp %s: %v (left out)", s.Name, err)
			continue
		}
		switch s.Role {
		case "assist":
			assist = c
		case "config":
			config = c
		}
	}
	ts := Tools(assist, config, nil)
	var names []string
	for _, t := range ts {
		names = append(names, t.Name)
	}
	logf("Home Assistant: her tools %s", strings.Join(names, ", "))
	return append(out, ts...)
}
