package view

import (
	"errors"
	"reflect"
	"testing"
)

type voice struct{ got []string }

func (v *voice) Context(key, line string) { v.got = append(v.got, "context "+key+" | "+line) }
func (v *voice) Note(level, text string) error {
	if level == "bogus" {
		return errors.New("no such level")
	}
	v.got = append(v.got, "note "+level+" | "+text)
	return nil
}

type tasks struct{ got []string }

func (t *tasks) Reasoning(domain, kind, line string) {
	t.got = append(t.got, "reasoning "+domain+" "+kind+" | "+line)
}
func (t *tasks) World(line string) { t.got = append(t.got, "world | "+line) }

// TestTheOnePort: each source reaches the voice and the tasks as its consumer's state wants, and the
// log has what each one got.
func TestTheOnePort(t *testing.T) {
	v, k := &voice{}, &tasks{}
	var log []string
	p := &Port{Voice: func() Voice { return v }, Tasks: func() Tasks { return k },
		Levels: map[string]string{"result": "context", "event": "info", "error": "drop", "odd": "bogus"},
		Skip:   func(d string) bool { return d == "attention" },
		Log:    func(l Line, voice, tasks string) { log = append(log, l.Source+" "+voice+" "+tasks) }}
	p.Deliver(Line{Source: Rules, Domain: "lights", Kind: "result", Text: "lights done: on"})
	p.Deliver(Line{Source: Rules, Domain: "timers", Kind: "event", Text: "timers noticed: Time's up"})
	p.Deliver(Line{Source: Rules, Domain: "air", Kind: "error", Text: "air: an error"})
	p.Deliver(Line{Source: Rules, Domain: "air", Kind: "odd", Text: "air: refused note"})
	p.Deliver(Line{Source: Rules, Domain: "attention", Kind: "event", Text: "attention: a citation"})
	p.Deliver(Line{Source: World, Key: "r1.presence", Env: true, Text: "someone in the kitchen"})
	p.Deliver(Line{Source: World, Key: "metis.scenes", Text: "the scenes concluded night"})
	p.Deliver(Line{Source: Service, Key: "calendar", Text: "calendar: the dentist at 10"})
	p.Say("timers", "urgent", "A timer has rung")
	wantV := []string{
		"context  | lights done: on",
		"note info | timers noticed: Time's up",
		"context  | air: refused note",
		"context r1.presence | r1.presence: someone in the kitchen",
		"context metis.scenes | the scenes concluded night",
		"context calendar | calendar: the dentist at 10",
		"note urgent | A timer has rung",
	}
	wantT := []string{
		"reasoning lights result | lights done: on",
		"reasoning timers event | timers noticed: Time's up",
		"reasoning air error | air: an error",
		"reasoning air odd | air: refused note",
		"world | someone in the kitchen",
		"world | calendar: the dentist at 10",
	}
	wantL := []string{"rules context reasoning", "rules note:info reasoning", "rules drop reasoning",
		"rules context reasoning", "rules skip skip", "world context world", "world context ",
		"service context world", "note note:urgent "}
	if !reflect.DeepEqual(v.got, wantV) {
		t.Errorf("the voice:\n%q\nwant\n%q", v.got, wantV)
	}
	if !reflect.DeepEqual(k.got, wantT) {
		t.Errorf("the tasks:\n%q\nwant\n%q", k.got, wantT)
	}
	if !reflect.DeepEqual(log, wantL) {
		t.Errorf("the log:\n%q\nwant\n%q", log, wantL)
	}
	// no voice and no runner yet: nothing delivered, still logged
	log = nil
	q := &Port{Log: p.Log, Voice: func() Voice { return nil }}
	q.Deliver(Line{Source: Rules, Domain: "lights", Kind: "result", Text: "x"})
	q.Say("tasks", "info", "y")
	if !reflect.DeepEqual(log, []string{"rules  ", "note  "}) {
		t.Errorf("without consumers: %q", log)
	}
}
