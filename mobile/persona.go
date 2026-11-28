package nina

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ibnHatab/fsttm-live/pkg/dialogue"
	fmobile "github.com/ibnHatab/fsttm-live/pkg/mobile"
	"github.com/ibnHatab/nina-live/app"
)

func init() {
	fmobile.SetPersona(start)
	// her keys, typed on the app's start page when missing (kept in its private files); the rest of her keys
	// and her Google account: her guided setup (her settings page, the setup card)
	fmobile.SetKeys(fmobile.Key{Name: "gemini", Label: "Gemini API key (Google AI Studio)", Path: ".config/fsttm/gemini.key"},
		fmobile.Key{Name: "openrouter", Label: "OpenRouter API key (the tasks' last fallback)", Path: ".config/nina/openrouter.env", Optional: true})
	// her own screen: her face and the conversation (acasa's ha-ui www/nina, put in the app's files as
	// nina-ui and served by her panel at /nina/), remote: her card alone, no Home Assistant beside it
	fmobile.SetPanel("http://127.0.0.1:8770/nina/shell.html?remote&fps=20,8&lite&scale=0.7")
}

// start is Nina in the app (2026-11-28): the same persona as at home (nina-live app: her rules, her voice,
// her tasks, her settings and guided setup, her screen's stage), from the app's files: nina/nina.yaml (the
// tablet's config: no Home Assistant), nina/dialogue.yaml, nina/nina.md, nina/voice.md, catalog/ (her rules'
// image, built with the APK). The tablet's own radio, alarms and timers are the deployment's tools.
func start(ctx context.Context, files string) (*dialogue.Dialogue, error) {
	os.Setenv("HOME", files) // the config's ~/ (Android's own HOME is not the app's)
	raw, err := os.ReadFile(filepath.Join(files, "nina", "nina.yaml"))
	if err != nil {
		return nil, fmt.Errorf("her config: %w", err)
	}
	var c app.Config
	if err := app.LoadConfig(raw, &c); err != nil {
		return nil, fmt.Errorf("her config: %w", err)
	}
	a, err := app.New(ctx, c, app.Backends{Device: app.NoDevice{}, Tools: deviceTools()}) // no devices: an empty house (nina/home.map.json)
	if err != nil {
		return nil, err
	}
	if err := a.Start(ctx); err != nil {
		return nil, err
	}
	return a.D, nil
}
