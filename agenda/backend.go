package agenda

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// Backend keeps the agenda's calendars and lists: the far end of the package's host (the redesign's
// Google services, s-b478fe59: the rules and the host are the same whichever keeps them). An id is
// the Host's Entities value for a target: a Home Assistant entity, a Google calendar or task list.
type Backend interface {
	// CreateEvent puts an event in a calendar; start and end in local time, "2006-01-02 15:04:05"
	CreateEvent(ctx context.Context, id, summary, start, end, note string) error
	// AddItem puts an item on a list; due "2006-01-02" or ""
	AddItem(ctx context.Context, id, summary, due, note string) error
	// RemoveItem takes an item off a list, by its own name on it
	RemoveItem(ctx context.Context, id, item string) error
	// Items are a list's open items (ErrNoItems: not known; an item is then removed by its name as said)
	Items(ctx context.Context, id string) ([]string, error)
}

// ErrNoItems: the backend cannot list a list's items.
var ErrNoItems = errors.New("the list's items are not known")

// HA is the agenda in Home Assistant's service vocabulary (calendar.create_event, todo.add_item,
// todo.remove_item, todo.get_items): Call is its client's service call (acasa's hass), Response the
// same with return_response (nil: items not known).
type HA struct {
	Call     func(ctx context.Context, domain, service string, data map[string]any) error
	Response func(ctx context.Context, domain, service string, data map[string]any) (map[string]json.RawMessage, error)
}

func (a HA) CreateEvent(ctx context.Context, id, summary, start, end, note string) error {
	return a.Call(ctx, "calendar", "create_event", map[string]any{"entity_id": id, "summary": summary,
		"start_date_time": start, "end_date_time": end, "description": note})
}

func (a HA) AddItem(ctx context.Context, id, summary, due, note string) error {
	data := map[string]any{"entity_id": id, "item": summary, "description": note}
	if due != "" {
		data["due_date"] = due
	}
	err := a.Call(ctx, "todo", "add_item", data)
	if err != nil && strings.Contains(err.Error(), "does not support setting field: description") {
		delete(data, "description") // Home Assistant's own shopping list has no description (Pascal, 2026-11-26)
		err = a.Call(ctx, "todo", "add_item", data)
	}
	return err
}

func (a HA) RemoveItem(ctx context.Context, id, item string) error {
	return a.Call(ctx, "todo", "remove_item", map[string]any{"entity_id": id, "item": item})
}

func (a HA) Items(ctx context.Context, id string) ([]string, error) {
	if a.Response == nil {
		return nil, ErrNoItems
	}
	r, err := a.Response(ctx, "todo", "get_items", map[string]any{"entity_id": id})
	if err != nil {
		return nil, err
	}
	var l struct {
		Items []struct{ Summary string } `json:"items"`
	}
	if err := json.Unmarshal(r[id], &l); err != nil {
		return nil, err
	}
	var out []string
	for _, it := range l.Items {
		out = append(out, it.Summary)
	}
	return out, nil
}
