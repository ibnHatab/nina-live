package app

import (
	"testing"

	"github.com/metis-lang-dev/metis-agent/go/pkg/metishome"
)

// a place with no devices: every command refused (the tablet's empty house)
func TestNoDevice(t *testing.T) {
	var d metishome.Device = NoDevice{}
	for _, c := range [][3]string{{"light", "living", "on"}, {"unit", "bedroom", "off"}, {"tv", "", "on"}} {
		if d.Do(c[0], c[1], c[2], nil) {
			t.Fatalf("%v: done, want refused", c)
		}
	}
}
