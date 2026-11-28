package app

import (
	"context"
	stdlog "log"
	"os"
	"strings"

	"github.com/ibnHatab/fsttm-live/pkg/dialogue"
	nlog "github.com/ibnHatab/nina-live/log"
	"github.com/ibnHatab/nina-live/setup"
)

// newSetup is her guided setup on this machine's files (her config's, or where a new machine keeps
// them: ~/.config/nina). Her narration is said word for word (the dialogue's SAY); her screens hear of
// it in the settings event; what was done goes to her log (stream setup), nothing secret in it.
func newSetup(c Config, voiceKey string, b Backends, d *dialogue.Dialogue, a *App, L *nlog.Log) *setup.Setup {
	or := ""
	for _, f := range c.Tasks.Fallback {
		if f.Provider == "openrouter" && or == "" {
			or = f.Key
		}
	}
	s := setup.New(setup.Paths{
		Gemini:     firstOf(voiceKey, firstOf(c.Tasks.Key, "~/.config/nina/gemini.key")),
		YouTube:    firstOf(c.YouTube.Key, "~/.config/nina/youtube.key"),
		OpenRouter: firstOf(or, "~/.config/nina/openrouter.env"),
		Google:     firstOf(c.Services.Google.Token, "~/.config/nina/google-token.json"),
		Client:     "~/.config/nina/google-oauth-client.json",
		Clients:    "~/.config/nina",
		HassURL:    b.HassURL,
		HassToken:  b.HassToken,
		SearXNG:    c.Search.SearXNG,
	})
	if e := strings.TrimRight(c.Services.Google.Endpoint, "/"); e != "" { // the SITL's fake Google
		s.E.GoogleToken, s.E.Google = e+"/token", e
	}
	s.Say = func(text string) { go d.Do("say", text) }
	s.WhatsApp = func() setup.WhatsApp {
		if a.wa == nil {
			return nil
		}
		return waLink{a}
	}
	s.Restart = func() {
		stdlog.Print("setup: ending, to start again with her new keys (her service starts her)")
		os.Exit(75) // EX_TEMPFAIL: systemd's Restart=on-failure starts her again
	}
	s.Log = func(kind string, data map[string]any) {
		verb, _ := data["verb"].(string)
		in, _ := data["integration"].(string)
		L.Append(now(), "setup", verb, in, "", data, 1)
	}
	return s
}

// waLink is her WhatsApp as the setup links it.
type waLink struct{ a *App }

func (w waLink) Linked() bool {
	linked, _ := w.a.wa.Info()["linked"].(bool)
	return linked
}

func (w waLink) StartLink(ctx context.Context) error { return w.a.wa.StartLink(ctx) }
