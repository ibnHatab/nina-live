// Package view is the one view port (the redesign's dataflow streamlining, channel B: docs/design/
// channel-streamlining.md): what the home's rules say, the world's changed lines, the services'
// observations and what she is to say on her own reach both LLMs through it, each delivered by its
// consumer's state:
//
//   - the voice: silent context (keyed: a later line of the same key replaces it), or a NOTE by level
//     (info, important, urgent), the voice deciding by its floor and whether she is awake;
//   - the tasks: a rules line to the tasks that used its package (Steer mid-run, FollowUp at its end),
//     a world or service line to the tasks that named what it is about.
//
// One place decides, and logs what each consumer got (stream "view"), so the lines that reached
// the voice and the tasks can be compared from the log.
package view

import "strings"

// Sources of a line.
const (
	Rules   = "rules"   // a package's reply (Domain, Kind)
	World   = "world"   // the world's projection (Key; Env: an environment line)
	Service = "service" // an integration's observation (Key)
	Note    = "note"    // something she is to say on her own (Domain: who asks; Level)
)

// Line is one line through the port.
type Line struct {
	Source string
	Domain string // rules: the package; note: who asks (timers, tasks, surprise, cognition, photos)
	Kind   string // rules: result | event | surprise | error
	Key    string // world, service: the voice's context key
	Env    bool   // world: an environment line (the tasks' too, keyed in the voice's words)
	Level  string // note: info | important | urgent
	Text   string
}

// Voice is the dialogue as the port reaches it.
type Voice interface {
	Context(key, line string)
	Note(level, text string) error
}

// Tasks is the task runner as the port reaches it.
type Tasks interface {
	Reasoning(domain, kind, line string)
	World(line string)
}

// Port is the one view port.
type Port struct {
	Voice func() Voice // nil, or a nil result: no voice yet
	Tasks func() Tasks // nil, or a nil result: no task runner
	// Levels: a rules line's kind → drop | context (or "") | info | important | urgent
	Levels map[string]string
	// Skip: rules lines of these packages reach neither LLM (the cognition's bookkeeping: what it
	// has to say comes as a note)
	Skip func(domain string) bool
	// Log: each line and what the voice and the tasks got (nil: not logged)
	Log func(l Line, voice, tasks string)
}

// Deliver puts L through to the consumers. It holds no lock of its own: a voice's note may wait on
// its engine, which may be in a tool call that delivers a line.
func (p *Port) Deliver(l Line) {
	var v Voice
	if p.Voice != nil {
		v = p.Voice()
	}
	var t Tasks
	if p.Tasks != nil {
		t = p.Tasks()
	}
	vd, td := "", ""
	switch l.Source {
	case Rules:
		if p.Skip != nil && p.Skip(l.Domain) {
			vd, td = "skip", "skip"
			break
		}
		if t != nil {
			t.Reasoning(l.Domain, l.Kind, l.Text)
			td = "reasoning"
		}
		vd = deliverVoice(v, "", l.Text, p.Levels[l.Kind])
	case World:
		text := l.Text
		if l.Env && l.Key != "" {
			if t != nil {
				t.World(text)
				td = "world"
			}
			text = l.Key + ": " + text
		}
		vd = deliverVoice(v, l.Key, text, "context")
	case Service:
		if t != nil {
			t.World(l.Text)
			td = "world"
		}
		vd = deliverVoice(v, l.Key, l.Text, "context")
	case Note:
		vd = deliverVoice(v, "", l.Text, l.Level)
	}
	if p.Log != nil {
		p.Log(l, vd, td)
	}
}

// Say is something she is to say on her own (a ring, a late outcome, a surprise, what she keeps).
func (p *Port) Say(domain, level, text string) {
	p.Deliver(Line{Source: Note, Domain: domain, Level: level, Text: text})
}

// deliverVoice: context, a note (context when the voice refuses it), or nothing; what was done.
func deliverVoice(v Voice, key, text, level string) string {
	if v == nil {
		return ""
	}
	switch strings.TrimSpace(level) {
	case "drop":
		return "drop"
	case "", "context":
		v.Context(key, text)
		return "context"
	}
	if v.Note(level, text) != nil {
		v.Context(key, text)
		return "context"
	}
	return "note:" + level
}
