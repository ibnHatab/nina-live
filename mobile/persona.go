package nina

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	fmobile "github.com/ibnHatab/fsttm-live/pkg/mobile"
	"github.com/ibnHatab/fsttm-live/pkg/dialogue"
	"github.com/ibnHatab/nina-live/tasks"
	"google.golang.org/genai"
)

func init() { fmobile.SetPersona(start) }

// taskCharter: the task runner's part of the charter (after Nina's character).
const taskCharter = `You carry out a goal Nina's voice handed you: think it through, use your tools when they help,
and answer in one or two short sentences she can say aloud.`

// start is Nina in the app (2026-11-27, step 1): her voice (Gemini Live) with her character, and
// delegate(goal) to a pi task (Gemini Flash) whose result she says as a NOTE. The home's tools come
// with the core (Home Assistant over the network) in the next step. files: the app's files, with
// nina/dialogue.yaml, nina/nina.md, nina/voice.md and .config/fsttm/gemini.key.
func start(ctx context.Context, files string) (*dialogue.Dialogue, error) {
	os.Setenv("HOME", files) // the config's ~/ (Android's own HOME is not the app's)
	dir := filepath.Join(files, "nina")
	char, err1 := os.ReadFile(filepath.Join(dir, "nina.md"))
	voice, err2 := os.ReadFile(filepath.Join(dir, "voice.md"))
	if err1 != nil || err2 != nil {
		return nil, fmt.Errorf("her character: %v %v", err1, err2)
	}
	if err := os.WriteFile(filepath.Join(dir, "charter.md"), append(append(char, "\n\n"...), voice...), 0o600); err != nil {
		return nil, err
	}
	cfg, err := dialogue.LoadConfig(filepath.Join(dir, "dialogue.yaml"))
	if err != nil {
		return nil, err
	}
	var d *dialogue.Dialogue
	gkey := filepath.Join(files, ".config", "fsttm", "gemini.key")
	// pero's chain (acasa live/nina-pero.yaml): Google's Flash models answer "high demand" at times
	// (the first task here failed so, 2026-11-27); then OpenRouter's DeepSeek when all of Google is busy
	fbs := []tasks.Fallback{{Provider: "google", Model: "gemini-3.5-flash", KeyFile: gkey},
		{Provider: "google", Model: "gemini-3.5-flash-lite", KeyFile: gkey},
		{Provider: "google", Model: "gemini-3.1-flash-lite", KeyFile: gkey}}
	if okey := filepath.Join(files, ".config", "nina", "openrouter.env"); fileExists(okey) {
		fbs = append(fbs, tasks.Fallback{Provider: "openrouter", Model: "deepseek/deepseek-v4.1-flash", KeyFile: okey})
	}
	runner, err := tasks.New(tasks.Config{Provider: "google", Model: "gemini-3.8-flash", KeyFile: gkey, Fallbacks: fbs,
		MaxTurns: 10, Timeout: 5 * time.Minute,
		System:  string(char) + "\n\n# The task runner\n\n" + taskCharter,
		Note: func(level, text string) {
			fmt.Fprintf(os.Stderr, "nina: note %s: %s\n", level, text)
			if d != nil {
				d.Log("task_note", map[string]any{"level": level, "text": text})
				if err := d.Note(level, text); err != nil {
					fmt.Fprintf(os.Stderr, "nina: note refused: %v\n", err)
				}
			}
		}})
	if err != nil {
		return nil, err
	}
	tools := dialogue.Tools{Decls: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{Name: "delegate",
		Description: "Hand an open-ended goal to Nina's task runner: something that takes thinking or several steps. " +
			"It answers at once; the result comes later as a note.",
		ParametersJsonSchema: map[string]any{"type": "object", "properties": map[string]any{
			"goal": map[string]any{"type": "string", "description": "the goal, in the person's terms, complete"}},
			"required": []any{"goal"}}}}}},
		Funcs: map[string]dialogue.ToolFunc{"delegate": func(_ context.Context, args map[string]any) map[string]any {
			goal, _ := args["goal"].(string)
			if strings.TrimSpace(goal) == "" {
				return map[string]any{"status": "error", "text": "a goal is needed"}
			}
			t := runner.Delegate(goal, "important")
			fmt.Fprintf(os.Stderr, "nina: task %s: %s\n", t.ID, goal)
			d.Log("task_start", map[string]any{"task": t.ID, "goal": goal})
			go func() {
				select {
				case <-t.Done:
					f := map[string]any{"task": t.ID, "result": t.Result, "turns": t.Turns}
					if t.Err != nil {
						f["error"] = t.Err.Error()
					}
					fmt.Fprintf(os.Stderr, "nina: task %s ended: %v\n", t.ID, f)
					d.Log("task_end", f)
				case <-time.After(3 * time.Minute):
					fmt.Fprintf(os.Stderr, "nina: task %s: no end in 3 min\n", t.ID)
					d.Log("task_stuck", map[string]any{"task": t.ID})
				}
			}()
			return map[string]any{"status": "accepted", "task": t.ID, "text": "I'm on it; the result will come as a note."}
		}}}
	d, err = dialogue.New(ctx, cfg, dialogue.NewEvents(io.Discard), tools)
	if err != nil {
		return nil, err
	}
	if err := d.StartPlugins(ctx); err != nil {
		return nil, err
	}
	go clock(ctx, d)
	return d, nil
}

// clock gives her the time once a minute (as the core does: her session has no clock of its own).
func clock(ctx context.Context, d *dialogue.Dialogue) {
	last := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
		now := time.Now()
		if m := now.Format("Mon 2 Jan 2006 15:04"); m != last {
			last = m
			zone, _ := now.Zone()
			d.Context("now", fmt.Sprintf("now: %s (%s, local time)", m, zone))
		}
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
