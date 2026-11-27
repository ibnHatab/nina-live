// Package nina (directory mobile/) embeds Nina in fsttm-live's Android app (bound with it: gomobile bind
// github.com/ibnHatab/fsttm-live/pkg/mobile github.com/ibnHatab/nina-live/mobile). Proof stage
// (2026-11-27): the persona's checks, registered with the app's bridge, run pi's task loop and open
// the home's composition on the device.
package nina

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	fmobile "github.com/ibnHatab/fsttm-live/pkg/mobile"
	"github.com/ibnHatab/nina-live/tasks"
	"github.com/metis-lang-dev/metis-agent/go/pkg/metishome"
	"github.com/sky-valley/pi/ai"
	"github.com/sky-valley/pi/ai/providers"
)

func init() {
	fmobile.Register("pi", piCheck)
	fmobile.Register("metis", func(dir string) string { return metisCheck(filepath.Join(dir, "catalog")) })
}

// piCheck runs one task through pi's loop on a faux model: a tool call, its result, the answer.
func piCheck(dir string) string {
	t0 := time.Now()
	key := filepath.Join(dir, "faux.key")
	os.WriteFile(key, []byte("faux"), 0o600)
	p := providers.RegisterFauxProvider(providers.RegisterFauxProviderOptions{Provider: "faux"})
	p.SetResponses([]providers.FauxResponseStep{
		providers.FauxStatic(providers.FauxAssistantMessage(ai.ContentList{providers.FauxToolCall("clock", map[string]any{})}, ai.StopToolUse)),
		providers.FauxStatic(providers.FauxAssistantMessage(ai.ContentList{providers.FauxText("The device's clock answered.")}, ai.StopStop)),
	})
	calls := 0
	r, err := tasks.New(tasks.Config{ModelValue: p.GetModel(), KeyFile: key, System: "proof",
		Tools: []tasks.Tool{{Name: "clock", Description: "the time", Parameters: map[string]any{"type": "object"},
			Call: func(context.Context, map[string]any) (string, string) {
				calls++
				return "done", time.Now().Format(time.RFC3339)
			}}}})
	if err != nil {
		return "pi: " + err.Error()
	}
	t := r.Delegate("what time is it?", "info")
	for i := 0; i < 100 && t.Result == "" && t.Err == nil; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Sprintf("pi: result %q, tool ran %d time(s), err %v, %v", t.Result, calls, t.Err, time.Since(t0).Round(time.Millisecond))
}

// metisCheck opens the home's composition (its image built on the desktop) on the simulated world
// and lists its tools.
func metisCheck(root string) string {
	t0 := time.Now()
	os.Setenv("METIS_HOME_HA", "")
	s, err := metishome.Open(metishome.Options{Root: root, Composed: true, Records: "off",
		Packages: []string{"lights", "air", "media", "comfort", "scenes", "timers", "agenda", "music", "habits", "glow", "persona"}})
	if err != nil {
		return "metis: " + err.Error()
	}
	var names []string
	for _, tl := range s.Tools() {
		names = append(names, tl.Function.Name)
	}
	sort.Strings(names)
	return fmt.Sprintf("metis: backend %s, %d tools (%s), opened in %v", s.Backend(), len(names), strings.Join(names, " "), time.Since(t0).Round(time.Millisecond))
}

// Persona is the embedded persona's name (gomobile binds a package only with an exported name).
func Persona() string { return "Nina" }
