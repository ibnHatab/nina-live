package app

import (
	"testing"
	"time"
)

// her context lines to her screens: changes gathered into one event, sorted, an emptied line gone
func TestView(t *testing.T) {
	got := make(chan map[string]any, 4)
	v := newView(func(kind string, d map[string]any) {
		if kind == "view" {
			got <- d
		}
	})
	v.every = 20 * time.Millisecond
	v.set("weather.home", "weather: sunny, 4°C")
	v.set("calendar.nina", "Nina: nothing soon")
	v.set("timer.tea", "tea: 3 min left")
	v.set("timer.tea", "")
	d := <-got
	ls := d["lines"].([]map[string]string)
	if len(ls) != 2 || ls[0]["key"] != "calendar.nina" || ls[1]["text"] != "weather: sunny, 4°C" {
		t.Fatalf("lines: %v", ls)
	}
	select {
	case d := <-got:
		t.Fatalf("one event for one burst: %v", d)
	case <-time.After(60 * time.Millisecond):
	}
}
