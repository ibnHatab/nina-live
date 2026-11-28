// Package setup is Nina's guided setup (the user, 2026-11-28): her keys, her Google account and her
// channels, added from her settings page one integration at a time, the way Home Assistant adds
// integrations, so a long setup is a few short ones. No coding agent drives a browser for the person:
// Nina guides them herself, in a loop over each integration's steps:
//
//   - the step's page opens in a new tab (where the person does it: Google Cloud's console, AI Studio, …);
//   - Nina says what to do there, word for word (her narration goes on while the person is in the other
//     tab); the screen shows only the step's short line, the narration in full when asked;
//   - the step's check proves it was done (the key answers, the sign-in gives the scopes, the device is
//     linked) before the next step; a failed check says why and what to try;
//   - a step the person cannot do is skipped: that integration stays incomplete, the others go on.
//
// What a machine already has is found first: a key that is set and answers needs nothing but its check.
// A new key is checked before it is written, and the file it replaces is kept beside it (.prev): a key
// already allocated is never removed. Nothing secret reaches her screens or her log: only whether a key is
// set, and what its check found.
package setup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Result is what a check found.
type Result struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"` // what was found (never a secret)
	Hint   string `json:"hint,omitempty"`   // what to do about it (said when it fails)
}

// Step is one thing the person does.
type Step struct {
	ID    string
	Title string
	Text  string // the screen's line
	Say   string // her narration, said word for word (the screen shows it when asked)
	URL   string // the page for it, opened in a new tab ("{hass}": the config's Home Assistant)
	// Input is what the person gives here: "" (nothing: the check alone, or "done" when Check is nil),
	// "secret" (a key, pasted), "client" (an OAuth client's JSON, pasted, or a file found chosen),
	// "signin" (a sign-in, started here), "whatsapp" (her device linked by its QR)
	Input string
	// Check proves the step (value: what the person gave; "" checks what is there now); nil: the person
	// says it is done
	Check func(ctx context.Context, s *Setup, value string) Result
	// Save keeps the value (a key's file), after its check passed
	Save func(s *Setup, value string) error
	// Shortcut: the words of a button that completes the integration here (the project made already:
	// the rest of its steps are for making one)
	Shortcut string
}

// Integration is one thing added: a key, an account, a channel.
type Integration struct {
	ID, Title string
	Why       string   // what she does with it
	Needs     []string // the integrations it builds on (Google's project before its keys)
	Optional  bool
	Steps     []Step
	// Status: set and working now (nil: by its last step's check)
	Status func(ctx context.Context, s *Setup) Result
}

// WhatsApp is her WhatsApp channel as the setup sees it.
type WhatsApp interface {
	Linked() bool
	StartLink(ctx context.Context) error
}

// Setup is the guided setup of one machine.
type Setup struct {
	P    Paths
	E    Endpoints
	HTTP *http.Client
	// Say: her narration, spoken word for word (nil: none, the screen's text alone)
	Say func(text string)
	// Changed: the setup's view changed (her screens told: the settings event)
	Changed func()
	// WhatsApp: her channel (nil, or a nil result: not enabled in her config)
	WhatsApp func() WhatsApp
	// Restart: her process ended to start again (systemd's Restart), for the keys she reads at the start
	Restart func()
	// Log: what was done, for her log (no secrets)
	Log func(kind string, data map[string]any)
	// SignInPort: the loopback port of Google's redirect (http://localhost:PORT/, the OAuth client's)
	SignInPort int

	List []Integration

	mu      sync.Mutex
	status  map[string]Result
	at      *session
	narrate bool
	restart bool // a key written: she reads it at her next start
	signin  *signin
	said    map[string]bool // integrations done by the person's word (their steps have no check: a project made)
}

type session struct {
	ID      string
	Step    int
	Result  *Result         // the current step's last check
	Skipped map[string]bool // step ids
	Done    bool            // the integration's steps all passed or skipped
}

// New is the setup for these paths, with the integrations Nina knows.
func New(p Paths) *Setup {
	s := &Setup{P: p, E: Google(), HTTP: &http.Client{Timeout: 15 * time.Second}, SignInPort: 8765,
		status: map[string]Result{}, narrate: true, said: map[string]bool{}}
	s.List = Integrations()
	return s
}

func (s *Setup) find(id string) (*Integration, bool) {
	for i := range s.List {
		if s.List[i].ID == id {
			return &s.List[i], true
		}
	}
	return nil, false
}

// Action is a panel action of the setup's (false: not the setup's).
func (s *Setup) Action(action, arg string) (bool, error) {
	if !strings.HasPrefix(action, "setup_") {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := s.act(ctx, strings.TrimPrefix(action, "setup_"), arg)
	s.changed()
	return true, err
}

func (s *Setup) act(ctx context.Context, verb, arg string) error {
	switch verb {
	case "status": // every integration checked again (what the machine has)
		s.Refresh(ctx)
		return nil
	case "add": // an integration's guide, from its first step
		in, ok := s.find(strings.TrimSpace(arg))
		if !ok {
			return fmt.Errorf("setup: no integration %q", arg)
		}
		s.mu.Lock()
		s.at = &session{ID: in.ID, Skipped: map[string]bool{}}
		s.mu.Unlock()
		s.log("setup", map[string]any{"verb": "add", "integration": in.ID})
		s.enter(ctx)
		return nil
	case "narrate": // her narration on or off ("" toggles)
		s.mu.Lock()
		switch arg {
		case "on":
			s.narrate = true
		case "off":
			s.narrate = false
		default:
			s.narrate = !s.narrate
		}
		s.mu.Unlock()
		return nil
	case "restart":
		s.mu.Lock()
		need := s.restart
		s.mu.Unlock()
		if !need {
			return errors.New("setup: nothing to restart for")
		}
		if s.Restart == nil {
			return errors.New("setup: she cannot restart herself here; restart her service")
		}
		s.log("setup", map[string]any{"verb": "restart"})
		go func() { time.Sleep(time.Second); s.Restart() }()
		return nil
	}
	in, st, n, err := s.current()
	if err != nil {
		return err
	}
	switch verb {
	case "say": // the step's narration again
		s.say(in.Steps[n].Say)
	case "value": // what the person gives (a key, a client's JSON): checked, then kept
		return s.value(ctx, in, n, arg)
	case "check": // the step's check, with what is there now
		s.check(ctx, in, n, "")
	case "done": // the person says it is done (a step without a check), or the shortcut
		step := in.Steps[n]
		if step.Check != nil && step.Shortcut == "" {
			if r := s.check(ctx, in, n, ""); !r.OK {
				return fmt.Errorf("not yet: %s", r.Detail)
			}
		}
		if step.Shortcut != "" {
			s.finish(ctx, in, "shortcut")
			return nil
		}
		s.next(ctx, in)
	case "next": // on, once the step's check passed (a step without one: the person's word)
		s.mu.Lock()
		r := st.Result
		s.mu.Unlock()
		if in.Steps[n].Check != nil && (r == nil || !r.OK) {
			return errors.New("not yet: check it first, or skip it")
		}
		s.next(ctx, in)
	case "skip": // a step the person cannot do: on without it
		s.mu.Lock()
		st.Skipped[in.Steps[n].ID] = true
		s.mu.Unlock()
		s.log("setup", map[string]any{"verb": "skip", "integration": in.ID, "step": in.Steps[n].ID})
		s.next(ctx, in)
	case "back":
		s.mu.Lock()
		if st.Step > 0 {
			st.Step--
		}
		st.Result = nil
		s.mu.Unlock()
		s.enter(ctx)
	case "close": // the guide closed (what was done is kept)
		s.mu.Lock()
		s.at = nil
		s.mu.Unlock()
		s.stopSignIn()
	case "signin": // Google's sign-in started: its page for the person's browser
		return s.startSignIn(in, n)
	case "link": // her WhatsApp device: the QR on her screens
		w := s.whatsApp()
		if w == nil {
			return errors.New("whatsapp: not enabled in her config (whatsapp.enabled)")
		}
		return w.StartLink(ctx)
	default:
		return fmt.Errorf("setup: no action %q", verb)
	}
	return nil
}

func (s *Setup) current() (*Integration, *session, int, error) {
	s.mu.Lock()
	st := s.at
	s.mu.Unlock()
	if st == nil {
		return nil, nil, 0, errors.New("setup: no integration being added")
	}
	in, _ := s.find(st.ID)
	if st.Done || st.Step >= len(in.Steps) {
		return nil, nil, 0, errors.New("setup: " + in.Title + " is done; close it or add another")
	}
	return in, st, st.Step, nil
}

// enter: the current step begun: said, and checked when it has nothing to give (what is there may
// already pass: the sunny day, a key the machine has)
func (s *Setup) enter(ctx context.Context) {
	in, _, n, err := s.current()
	if err != nil {
		return
	}
	step := in.Steps[n]
	s.say(step.Say)
	if step.Check != nil && step.Input != "signin" {
		s.check(ctx, in, n, "")
	}
}

func (s *Setup) check(ctx context.Context, in *Integration, n int, value string) Result {
	step := in.Steps[n]
	r := Result{OK: true}
	if step.Check != nil {
		r = step.Check(ctx, s, value)
	}
	s.mu.Lock()
	if s.at != nil && s.at.ID == in.ID && s.at.Step == n {
		s.at.Result = &r
	}
	s.mu.Unlock()
	s.log("setup", map[string]any{"verb": "check", "integration": in.ID, "step": step.ID, "ok": r.OK, "detail": r.Detail})
	return r
}

func (s *Setup) value(ctx context.Context, in *Integration, n int, v string) error {
	step := in.Steps[n]
	v = strings.TrimSpace(v)
	if step.Input != "secret" && step.Input != "client" {
		return fmt.Errorf("setup: %s takes nothing to paste", step.Title)
	}
	if v == "" {
		return errors.New("setup: nothing given")
	}
	r := s.check(ctx, in, n, v)
	if !r.OK {
		if r.Hint != "" {
			s.say(r.Hint)
		}
		return nil // the result is on her screen
	}
	if step.Save != nil {
		if err := step.Save(s, v); err != nil {
			r = Result{Detail: "it works, but it could not be kept: " + err.Error()}
			s.mu.Lock()
			s.at.Result = &r
			s.mu.Unlock()
			return err
		}
		s.mu.Lock()
		s.restart = true
		s.mu.Unlock()
		s.log("setup", map[string]any{"verb": "saved", "integration": in.ID, "step": step.ID})
	}
	return nil
}

func (s *Setup) next(ctx context.Context, in *Integration) {
	s.mu.Lock()
	st := s.at
	st.Step++
	st.Result = nil
	last := st.Step >= len(in.Steps)
	s.mu.Unlock()
	if last {
		s.finish(ctx, in, "steps")
		return
	}
	s.enter(ctx)
}

func (s *Setup) finish(ctx context.Context, in *Integration, how string) {
	s.mu.Lock()
	if manual(in) && len(s.at.Skipped) == 0 { // nothing to check: the person's word
		s.said[in.ID] = true
	}
	s.mu.Unlock()
	r := s.statusOf(ctx, in)
	s.mu.Lock()
	s.at.Done = true
	s.status[in.ID] = r
	skipped := len(s.at.Skipped)
	restart := s.restart
	s.mu.Unlock()
	s.log("setup", map[string]any{"verb": "finished", "integration": in.ID, "how": how, "ok": r.OK, "skipped": skipped})
	switch {
	case r.OK && restart:
		s.say(in.Title + " is set. I use it from my next start: press Restart when you are ready.")
	case r.OK:
		s.say(in.Title + " is set and works.")
	case skipped > 0:
		s.say(in.Title + " is not complete: you skipped a step. You can come back to it any time.")
	default:
		s.say(in.Title + " does not work yet: " + r.Detail)
	}
}

// manual: no step of in has a check
func manual(in *Integration) bool {
	for _, st := range in.Steps {
		if st.Check != nil {
			return false
		}
	}
	return true
}

func (s *Setup) declared(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.said[id]
}

// statusOf: an integration set and working (its Status, or its steps' checks of what is there)
func (s *Setup) statusOf(ctx context.Context, in *Integration) Result {
	if in.Status != nil {
		return in.Status(ctx, s)
	}
	for i := len(in.Steps) - 1; i >= 0; i-- {
		if c := in.Steps[i].Check; c != nil {
			return c(ctx, s, "")
		}
	}
	return Result{Detail: "nothing to check"}
}

// Refresh checks every integration (at the start, and when the settings page asks).
func (s *Setup) Refresh(ctx context.Context) {
	var wg sync.WaitGroup
	out := make([]Result, len(s.List))
	for i := range s.List {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			out[i] = s.statusOf(c, &s.List[i])
		}(i)
	}
	wg.Wait()
	s.mu.Lock()
	for i := range s.List {
		s.status[s.List[i].ID] = out[i]
	}
	s.mu.Unlock()
}

func (s *Setup) say(text string) {
	s.mu.Lock()
	on := s.narrate
	s.mu.Unlock()
	if text = strings.TrimSpace(text); text != "" && on && s.Say != nil {
		s.Say(text)
	}
}

func (s *Setup) changed() {
	if s.Changed != nil {
		s.Changed()
	}
}

func (s *Setup) log(kind string, data map[string]any) {
	if s.Log != nil {
		s.Log(kind, data)
	}
}

func (s *Setup) whatsApp() WhatsApp {
	if s.WhatsApp == nil {
		return nil
	}
	return s.WhatsApp()
}

// View is the setup as her screens show it (the settings event's setup): no secret in it.
func (s *Setup) View() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var list []map[string]any
	for _, in := range s.List {
		e := map[string]any{"id": in.ID, "title": in.Title, "why": in.Why, "optional": in.Optional, "steps": len(in.Steps)}
		if len(in.Needs) > 0 {
			e["needs"] = in.Needs
		}
		if r, ok := s.status[in.ID]; ok {
			e["status"] = r
		}
		list = append(list, e)
	}
	v := map[string]any{"integrations": list, "narrate": s.narrate, "restart": s.restart}
	if st := s.at; st != nil {
		in, _ := s.find(st.ID)
		a := map[string]any{"integration": in.ID, "title": in.Title, "step": st.Step, "of": len(in.Steps), "done": st.Done}
		var sk []string
		for id := range st.Skipped {
			sk = append(sk, id)
		}
		sort.Strings(sk)
		a["skipped"] = sk
		if st.Done {
			a["status"] = s.status[in.ID]
		} else if st.Step < len(in.Steps) {
			step := in.Steps[st.Step]
			a["current"] = map[string]any{"id": step.ID, "title": step.Title, "text": step.Text, "say": step.Say,
				"url": s.url(step.URL), "input": step.Input, "shortcut": step.Shortcut, "manual": step.Check == nil}
			if st.Result != nil {
				a["result"] = *st.Result
			}
			if step.Input == "client" {
				a["clients"] = s.clients()
			}
			if step.Input == "signin" && s.signin != nil {
				a["signin"] = map[string]any{"url": s.signin.url, "redirect": s.signin.redirect}
			}
		}
		v["active"] = a
	}
	return v
}

func (s *Setup) url(u string) string {
	return strings.ReplaceAll(u, "{hass}", strings.TrimRight(s.P.HassURL, "/"))
}
