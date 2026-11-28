package app

import (
	"sort"
	"sync"
	"time"
)

// view is her screens' copy of the lines her context gets from the house and the direct services (the
// weather, the calendars, the lists, the timers, the players, the people): the domain cards on her own
// screens show them, as Home Assistant's cards showed its entities (the user, 2026-11-28: the cards off the
// Home Assistant dashboards). One "view" event with every line, at most once a second; sticky on her panel
// (a page loading gets the last), not in the log.
type view struct {
	mu    sync.Mutex
	lines map[string]string
	due   bool
	emit  func(kind string, data map[string]any)
	every time.Duration
}

func newView(emit func(string, map[string]any)) *view {
	return &view{lines: map[string]string{}, emit: emit, every: time.Second}
}

func (v *view) set(key, text string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.lines[key] == text {
		return
	}
	if text == "" {
		delete(v.lines, key)
	} else {
		v.lines[key] = text
	}
	if v.due {
		return
	}
	v.due = true
	time.AfterFunc(v.every, v.send)
}

func (v *view) send() {
	v.mu.Lock()
	v.due = false
	keys := make([]string, 0, len(v.lines))
	for k := range v.lines {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ls := make([]map[string]string, len(keys))
	for i, k := range keys {
		ls[i] = map[string]string{"key": k, "text": v.lines[k]}
	}
	v.mu.Unlock()
	v.emit("view", map[string]any{"lines": ls})
}
