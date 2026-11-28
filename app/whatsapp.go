package app

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/ibnHatab/nina-live/core"
	"github.com/ibnHatab/nina-live/tasks"
	"github.com/ibnHatab/nina-live/whatsapp"
)

// waAnswer: a WhatsApp request to her task runner, as a quiet task (its result is the answer, no
// NOTE), with the chat's recent messages as its context.
func waAnswer(k *core.Core) func(ctx context.Context, r whatsapp.Request) (string, error) {
	return func(ctx context.Context, r whatsapp.Request) (string, error) {
		where := "directly"
		if r.Self {
			where = "in the person's own WhatsApp chat (their \"Message yourself\" notes: it is them, writing to you)"
		} else if r.Group {
			where = fmt.Sprintf("in the WhatsApp group %q", r.ChatName)
		}
		said := fmt.Sprintf("%q", r.Text)
		switch r.Kind {
		case "voice":
			said = "a voice message (you cannot listen to voice messages yet: say so briefly)"
		case "image", "video", "document", "sticker":
			said = "a " + r.Kind
			if r.Text != "" {
				said += fmt.Sprintf(" with the caption %q", r.Text)
			}
		}
		var lines []string
		for _, m := range r.Recent {
			lines = append(lines, waLine(m, "you (Nina)"))
		}
		goal := fmt.Sprintf("WhatsApp: %s wrote to you %s: %s.\nThe chat's last messages, oldest first:\n%s\n"+
			"Answer them as Nina in one short WhatsApp message: plain text, no markdown, in their language. Use your tools "+
			"when the request needs them (the calendar, the tasks, timers, the web). Your answer is sent as it is: only the message.",
			firstOf(r.SenderName, "someone"), where, said, strings.Join(lines, "\n"))
		t := k.Tasks.Delegate(goal, tasks.Quiet)
		select {
		case <-t.Done:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		if t.Err != nil {
			return "", t.Err
		}
		return strings.TrimSpace(t.Result), nil
	}
}

func waLine(m whatsapp.Msg, me string) string {
	who := m.SenderName
	if m.FromMe {
		who = me
	}
	t := m.Text
	if t == "" {
		t = "[" + m.Kind + "]"
	}
	return fmt.Sprintf("- %s (%s): %s", who, m.At.Local().Format("Mon 15:04"), t)
}

// waReadTool: what was written in a WhatsApp chat (her voice's and her tasks': "what did the office
// group say?"); read only.
func waReadTool(wa *whatsapp.Client) tasks.Tool {
	return tasks.Tool{Name: "whatsapp_read", Domain: "whatsapp",
		Description: "The last messages of one of Nina's WhatsApp chats (a group, or a person who wrote to her), oldest first. Read only.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"chat":  map[string]any{"type": "string", "description": "the group's or the person's name"},
			"count": map[string]any{"type": "integer", "description": "how many messages (default 15)"}}, "required": []any{"chat"}},
		Call: func(ctx context.Context, a map[string]any) (string, string) {
			name, _ := a["chat"].(string)
			n := 15
			if f, ok := a["count"].(float64); ok && f > 0 {
				n = int(math.Min(f, 50))
			}
			chat := wa.ByName(name)
			if chat == "" {
				return "done", fmt.Sprintf("no WhatsApp chat named %q", name)
			}
			var out []string
			for _, m := range wa.Recent(chat, n) {
				out = append(out, waLine(m, "Nina"))
			}
			if len(out) == 0 {
				return "done", "nothing written there yet"
			}
			return "done", strings.Join(out, "\n")
		}}
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
