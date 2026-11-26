package core

// The core's integration tests: the log, the world rebuilt from it, the home composed under its
// director (nina-live/catalog's image), a simulated device, and System 2 on pi's faux provider
// (scripted answers that read what each request carried). No network, no API key used.

import (
	"errors"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ibnHatab/nina-live/agenda"
	"github.com/ibnHatab/nina-live/glow"
	nlog "github.com/ibnHatab/nina-live/log"
	"github.com/ibnHatab/nina-live/music"
	"github.com/ibnHatab/nina-live/tasks"
	"github.com/ibnHatab/nina-live/taste"
	"github.com/ibnHatab/nina-live/world"
	semhome "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/home"
	sw "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/semworld"
	"github.com/sky-valley/pi/ai"
	"github.com/sky-valley/pi/ai/providers"
)

type voice struct {
	mu      sync.Mutex
	notes   []string
	context []string
}

func (v *voice) Context(key, line string) {
	v.mu.Lock()
	v.context = append(v.context, line)
	v.mu.Unlock()
}
func (v *voice) Note(level, text string) error {
	v.mu.Lock()
	v.notes = append(v.notes, level+": "+text)
	v.mu.Unlock()
	return nil
}

// device obeys a unit's command after a moment, as the feed would report it.
type device struct{ k **Core }

func (d device) Do(kind, role, op string, value any) bool {
	go func() {
		time.Sleep(150 * time.Millisecond)
		k := *d.k
		if kind != "unit" || k == nil {
			return
		}
		p := map[string]string{"u1": "bedroom_badriom", "u2": "lab"}[role]
		st := map[string]string{"on": "on", "off": "off"}[op]
		if st == "" {
			return
		}
		for _, f := range []string{"supply", "extract"} {
			k.World.Apply(semhome.Event{At: k.Cfg.Now(), Kind: "ha_state", HA: &semhome.HAState{EntityID: "fan." + p + "_" + f + "_fan", State: st}})
		}
	}()
	return true
}

// rigAgenda: the agenda host of the next rig (nil: the default, which writes nothing)
var rigAgenda *agenda.Host

// rigMusic, rigLog: the music host and the log file of the next rig (zero: the defaults)
var (
	rigGlow  *glow.Host
	rigMusic *music.Host
	rigLog   string
)

type rig struct {
	k     *Core
	v     *voice
	faux  *providers.FauxProviderRegistration
	stop  context.CancelFunc
	log   string
	extra []tasks.Tool
}

func newRig(t *testing.T, mech tasks.Mechanisms, extra ...tasks.Tool) *rig {
	if os.Getenv("METISC") == "" {
		t.Skip("METISC: metis-lang's metisc (./build.sh test sets it)")
	}
	root, _ := filepath.Abs("../catalog")
	if _, err := os.Stat(filepath.Join(root, "domains", "build")); err != nil {
		t.Skip("the composition's image is not built (./build.sh)")
	}
	t.Setenv("METIS_HOME_HA", "")
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	os.WriteFile(key, []byte("faux"), 0o600)
	r := &rig{v: &voice{}, log: filepath.Join(dir, "nina.db"), extra: extra}
	if rigLog != "" {
		r.log = rigLog
	}
	r.faux = providers.RegisterFauxProvider(providers.RegisterFauxProviderOptions{Provider: "faux"})
	t.Cleanup(r.faux.Unregister)
	L, err := nlog.Open(r.log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { L.Close() })
	ctx, stop := context.WithCancel(context.Background())
	r.stop = stop
	t.Cleanup(stop)
	cfg := Config{Log: L, Root: root, Metisc: os.Getenv("METISC"), Composed: true, Device: device{&r.k},
		Code: "test", CheckpointEvery: 5,
		Tasks: &tasks.Config{ModelValue: r.faux.GetModel(), KeyFile: key, System: "test", Mech: mech, MaxTurns: 8,
			Timeout: 30 * time.Second, Tools: extra}}
	if rigAgenda != nil {
		cfg.Agenda = rigAgenda
	}
	if rigMusic != nil {
		cfg.Music = rigMusic
	}
	if rigGlow != nil {
		cfg.Glow = rigGlow
	}

	k, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.k = k
	k.SetVoice(r.v)
	for _, p := range []string{"lab", "bedroom_badriom"} {
		for _, e := range []string{"fan." + p + "_supply_fan", "fan." + p + "_extract_fan", "switch." + p + "_heater", "switch." + p + "_winter"} {
			k.World.Apply(semhome.Event{At: k.Cfg.Now(), Kind: "ha_state", HA: &semhome.HAState{EntityID: e, State: "off"}})
		}
	}
	go k.Run(ctx, 100*time.Millisecond)
	time.Sleep(400 * time.Millisecond)
	return r
}

func text(m ai.Message) string {
	var cs ai.ContentList
	switch x := m.(type) {
	case ai.UserMessage:
		cs = x.Content
	case *ai.UserMessage:
		cs = x.Content
	default:
		return ""
	}
	var b strings.Builder
	for _, c := range cs {
		if tc, ok := c.(ai.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func count(req ai.TranscriptContext, prefix string) int {
	n := 0
	for _, m := range req.Messages {
		if strings.HasPrefix(text(m), prefix) {
			n++
		}
	}
	return n
}

func joined(req ai.TranscriptContext) string {
	var b strings.Builder
	for _, m := range req.Messages {
		b.WriteString(text(m))
		b.WriteString("\n")
	}
	return b.String()
}

func call(name string, args map[string]any) providers.FauxResponseStep {
	return providers.FauxStatic(providers.FauxAssistantMessage(ai.ContentList{providers.FauxToolCall(name, args)}, ai.StopToolUse))
}

func say(s string) *ai.AssistantMessage {
	return providers.FauxAssistantMessage(ai.ContentList{providers.FauxText(s)}, ai.StopStop)
}

func wait(t *testing.T, tk *tasks.Task) {
	select {
	case <-tk.Done:
	case <-time.After(30 * time.Second):
		t.Fatal("the task did not end")
	}
	if tk.Err != nil {
		t.Fatalf("the task: %v", tk.Err)
	}
}

// TestADelegatedSceneIsSteeredWithThePackagesLines: a task asks for the night scene; the director
// routes its wishes to the lights, the air and the media; their lines are steered into the task
// (it used the planner, and the forwards reached them) and the next request carries them; the
// result is a NOTE.
func TestADelegatedSceneIsSteeredWithThePackagesLines(t *testing.T) {
	r := newRig(t, tasks.All)
	var tk *tasks.Task
	var sawSteer string
	r.faux.SetResponses([]providers.FauxResponseStep{
		call("home_scenes", map[string]any{"intent": "NIGHT"}),
		func(req ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxState, _ *ai.Model) *ai.AssistantMessage {
			// the packages' lines arrive on the next beats: wait until they have, and stopped coming
			for i, last := 0, -1; i < 60 && (tk.Steered < 3 || tk.Steered != last); i++ {
				last = tk.Steered
				time.Sleep(150 * time.Millisecond)
			}
			return providers.FauxAssistantMessage(ai.ContentList{providers.FauxToolCall("home_comfort", map[string]any{"intent": "QUERY", "room": "lab"})}, ai.StopToolUse)
		},
		func(req ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxState, _ *ai.Model) *ai.AssistantMessage {
			sawSteer = joined(req)
			return say("The home is in night mode: the lights and the TV were already off, the ventilation was stopped.")
		},
	})
	tk = r.k.Tasks.Delegate("Set the home to night mode and tell me what each part did.", "important")
	wait(t, tk)
	for _, p := range []string{"HOME (reported by the home's systems, after your call): lights", "air", "media"} {
		if !strings.Contains(sawSteer, p) {
			t.Fatalf("the request after the steer lacks %q:\n%s", p, sawSteer)
		}
	}
	if tk.Steered < 3 || tk.Calls != 2 {
		t.Fatalf("steered %d, calls %d", tk.Steered, tk.Calls)
	}
	time.Sleep(100 * time.Millisecond)
	r.v.mu.Lock()
	defer r.v.mu.Unlock()
	if len(r.v.notes) == 0 || !strings.Contains(r.v.notes[len(r.v.notes)-1], "important: The home is in night mode") {
		t.Fatalf("the result to the voice: %v", r.v.notes)
	}
}

// TestTheWorldBeforeEachRequestIsReplacedNotPiled: with TransformContext, every request begins
// with the world's lines, once; without it, never.
func TestTheWorldBeforeEachRequestIsReplacedNotPiled(t *testing.T) {
	for _, on := range []bool{true, false} {
		r := newRig(t, tasks.Mechanisms{Transform: on})
		var seen []int
		var first []string
		step := func(next *ai.AssistantMessage) providers.FauxResponseStep {
			return func(req ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxState, _ *ai.Model) *ai.AssistantMessage {
				seen = append(seen, count(req, "HOME now"))
				if len(req.Messages) > 0 {
					first = append(first, text(req.Messages[0]))
				}
				return next
			}
		}
		r.faux.SetResponses([]providers.FauxResponseStep{
			step(providers.FauxAssistantMessage(ai.ContentList{providers.FauxToolCall("home_comfort", map[string]any{"intent": "QUERY", "room": "lab"})}, ai.StopToolUse)),
			step(providers.FauxAssistantMessage(ai.ContentList{providers.FauxToolCall("home_comfort", map[string]any{"intent": "QUERY", "room": "bedroom"})}, ai.StopToolUse)),
			step(say("Both units are stopped.")),
		})
		wait(t, r.k.Tasks.Delegate("Which ventilation units are running?", "info"))
		want := 1
		if !on {
			want = 0
		}
		if len(seen) != 3 || seen[0] != want || seen[1] != want || seen[2] != want {
			t.Fatalf("transform %v: HOME now per request %v", on, seen)
		}
		if on && (!strings.HasPrefix(first[0], "HOME now") || !strings.Contains(first[0], "living room ventilation: stopped")) {
			t.Fatalf("the world's lines: %q", first[0])
		}
	}
}

// TestALateOutcomeIsAFollowUp: a call answered pending; its outcome arrives while the model writes
// its answer; the run would end, and FollowUp carries the outcome into one more request.
func TestALateOutcomeIsAFollowUp(t *testing.T) {
	slow := tasks.Tool{Name: "slow_unit", Description: "a unit that answers later", Domain: "air",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
		Call: func(context.Context, map[string]any) (string, string) {
			return "pending", "the rules have not decided yet"
		}}
	for _, on := range []bool{true, false} {
		r := newRig(t, tasks.Mechanisms{FollowUp: on}, slow)
		var tk *tasks.Task
		var last string
		r.faux.SetResponses([]providers.FauxResponseStep{
			call("slow_unit", map[string]any{}),
			func(req ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxState, _ *ai.Model) *ai.AssistantMessage {
				r.k.Tasks.Reasoning("air", "result", "air done: I start the lab ventilation.") // the outcome, late
				return say("I asked the ventilation to start; it has not confirmed yet.")
			},
			func(req ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxState, _ *ai.Model) *ai.AssistantMessage {
				last = joined(req)
				return say("The lab ventilation is started.")
			},
		})
		tk = r.k.Tasks.Delegate("Start the lab ventilation.", "info")
		wait(t, tk)
		if on {
			if tk.Followed != 1 || !strings.Contains(last, "air done: I start the lab ventilation.") || tk.Result != "The lab ventilation is started." {
				t.Fatalf("followed %d, result %q, last request:\n%s", tk.Followed, tk.Result, last)
			}
		} else if tk.Followed != 0 || tk.Result != "I asked the ventilation to start; it has not confirmed yet." {
			t.Fatalf("without FollowUp: followed %d, result %q", tk.Followed, tk.Result)
		}
	}
}

// TestTheCoresLogRebuildsItsWorld: what the core did (a task's command through the rules and the
// device, the feed's read-back, the beats) is in the log, and a world rebuilt from it reaches the
// live world's checkpoint.
func TestTheCoresLogRebuildsItsWorld(t *testing.T) {
	r := newRig(t, tasks.All)
	r.faux.SetResponses([]providers.FauxResponseStep{
		call("home_air", map[string]any{"intent": "ON", "device": "lab ventilation"}),
		providers.FauxStatic(say("The lab ventilation is on.")),
		providers.FauxStatic(say("The lab ventilation is on.")), // its confirmation, steered: one more request
	})
	wait(t, r.k.Tasks.Delegate("Start the lab ventilation.", "info"))
	time.Sleep(600 * time.Millisecond) // the device obeys, the feed reads it back, a few beats
	kinds := map[string]int{}
	r.k.Log.Read(context.Background(), 0, func(e nlog.Event) error { kinds[e.Stream+"/"+e.Kind]++; return nil })
	for _, k := range []string{"world/command", "world/ha_state", "world/settle", "world/checkpoint", "task/created", "task/done", "reasoning/result"} {
		if kinds[k] == 0 {
			t.Fatalf("no %s in the log: %v", k, kinds)
		}
	}
	r.stop() // the clean stop: a snapshot, the world events before it pruned
	time.Sleep(200 * time.Millisecond)
	live := r.k.World.Home.Hash()
	after := map[string]int{}
	r.k.Log.Read(context.Background(), 0, func(e nlog.Event) error { after[e.Stream+"/"+e.Kind]++; return nil })
	if after["world/settle"] != 0 || after["world/snapshot"] != 1 || after["task/done"] != kinds["task/done"] {
		t.Fatalf("after the stop's snapshot: %v", after)
	}
	root, _ := filepath.Abs("../catalog")
	var members []world.Member
	for _, p := range r.k.Cfg.Packages {
		members = append(members, world.Member{Name: p, Manifest: filepath.Join(root, "domains", p, "domain.yaml")})
	}
	W, rep, err := world.Open(context.Background(), r.k.Log, world.Config{Map: filepath.Join(root, "world", "home.map.json"),
		Members: members, Code: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Mismatch) != 0 || rep.Snapshot == 0 || rep.Refused != "" || W.Home.Hash() != live {
		t.Fatalf("the rebuild: %+v (live %s, rebuilt %s)", rep, live, W.Home.Hash())
	}
}

func (v *voice) heard(sub string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, s := range append(append([]string{}, v.notes...), v.context...) {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func waitHeard(t *testing.T, v *voice, sub string) {
	for i := 0; i < 60 && !v.heard(sub); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if !v.heard(sub) {
		v.mu.Lock()
		defer v.mu.Unlock()
		t.Fatalf("never heard %q: notes %v, context %v", sub, v.notes, v.context)
	}
}

func homeCall(t *testing.T, k *Core, tool string, args map[string]any) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	o := k.Call(ctx, tool, args)
	if o.Status != "done" {
		t.Fatalf("%s %v: %s %s", tool, args, o.Status, o.Text)
	}
	return o.Text
}

// TestATimerRingsAndAReminderWaitsForTheScene: a countdown set at a word rings when its time has
// come, named by what it is for; a reminder for leaving waits until the director routes the away
// scene's wish to the timers.
func TestATimerRingsAndAReminderWaitsForTheScene(t *testing.T) {
	r := newRig(t, tasks.All)
	homeCall(t, r.k, "home_timers", map[string]any{"intent": "SET", "minutes": 0.01, "subject": "check the printer"})
	waitHeard(t, r.v, "Time's up: check the printer")
	homeCall(t, r.k, "home_timers", map[string]any{"intent": "REMIND", "occasion": "leaving", "subject": "take the keys"})
	time.Sleep(500 * time.Millisecond)
	if r.v.heard("Reminder: take the keys") {
		t.Fatal("the reminder rang before the scene")
	}
	homeCall(t, r.k, "home_scenes", map[string]any{"intent": "AWAY"})
	waitHeard(t, r.v, "Reminder: take the keys")
	// the away wish lasted its beat: a reminder for leaving set now waits for the next time
	homeCall(t, r.k, "home_timers", map[string]any{"intent": "REMIND", "occasion": "leaving", "subject": "the bins"})
	time.Sleep(800 * time.Millisecond)
	if r.v.heard("Reminder: the bins") {
		t.Fatal("a stale away wish rang the new reminder")
	}
	waitHeard(t, r.v, "timers set: the bins when you leave")
	homeCall(t, r.k, "home_timers", map[string]any{"intent": "QUERY"})
}

// TestATimerOutlivesARestart: a countdown set before a restart is still set after it (the host
// rebuilt from the log's timers stream, the rules' slot re-asserted), and the query lists it.
func TestATimerOutlivesARestart(t *testing.T) {
	r := newRig(t, tasks.All)
	homeCall(t, r.k, "home_timers", map[string]any{"intent": "SET", "minutes": 30, "subject": "the oven"})
	r.stop()
	time.Sleep(300 * time.Millisecond)
	root, _ := filepath.Abs("../catalog")
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	k, err := Open(ctx, Config{Log: r.k.Log, Root: root, Metisc: os.Getenv("METISC"), Composed: true, Device: device{&r.k}, Code: "test"})
	if err != nil {
		t.Fatal(err)
	}
	v := &voice{}
	k.SetVoice(v)
	go k.Run(ctx, 100*time.Millisecond)
	waitHeard(t, v, "the oven (in 30 min) carries on after a restart")
	waitHeard(t, v, "timers set: the oven at")
	if set := k.Timers.Set(); len(set) != 1 || !strings.Contains(set[0], "the oven") {
		t.Fatalf("after the restart: %v", set)
	}
	homeCall(t, k, "home_timers", map[string]any{"intent": "CANCEL"})
	waitHeard(t, v, "I cancel the oven")
	waitHeard(t, v, "timers: none set")
}

// TestNinaWritesHerOwnCalendarAndYourTasks: an event goes on Nina's calendar with its time from the
// payload, a to-do on the person's task list, each marked as hers; an event for the person's own
// calendar is refused by the rules and never reaches Home Assistant; a write Home Assistant refuses
// is said.
func TestNinaWritesHerOwnCalendarAndYourTasks(t *testing.T) {
	var mu sync.Mutex
	var calls []map[string]any
	fail := false
	rigAgenda = &agenda.Host{Entities: agenda.Default, Call: func(_ context.Context, domain, service string, data map[string]any) error {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			return fmt.Errorf("HTTP 500")
		}
		d := map[string]any{"service": domain + "." + service}
		for k, v := range data {
			d[k] = v
		}
		calls = append(calls, d)
		return nil
	}}
	defer func() { rigAgenda = nil }()
	r := newRig(t, tasks.All)
	homeCall(t, r.k, "home_agenda", map[string]any{"intent": "EVENT", "summary": "Dentist", "due": "2026-11-25T10:00:00", "minutes": 60}) // the time in due (pero's voice)
	homeCall(t, r.k, "home_agenda", map[string]any{"intent": "TODO", "summary": "Buy batteries", "due": "2026-11-27"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if o := r.k.Home.Call(ctx, "home_agenda", map[string]any{"intent": "EVENT", "summary": "Gym", "calendar": "my own calendar"}); o.Status != "refused" {
		t.Fatalf("the person's calendar: %s %s", o.Status, o.Text)
	}
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	if len(calls) != 2 {
		mu.Unlock()
		t.Fatalf("calls: %v", calls)
	}
	ev, td := calls[0], calls[1]
	mu.Unlock()
	if ev["service"] != "calendar.create_event" || ev["entity_id"] != "calendar.nina" || ev["start_date_time"] != "2026-11-25 10:00:00" ||
		ev["end_date_time"] != "2026-11-25 11:00:00" || ev["description"] != agenda.Mark {
		t.Fatalf("the event: %v", ev)
	}
	if td["service"] != "todo.add_item" || td["entity_id"] != "todo.my_tasks" || td["item"] != "Buy batteries" || td["due_date"] != "2026-11-27" {
		t.Fatalf("the to-do: %v", td)
	}
	mu.Lock()
	fail = true
	mu.Unlock()
	homeCall(t, r.k, "home_agenda", map[string]any{"intent": "TODO", "summary": "Call the bank"})
	waitHeard(t, r.v, "It did not reach your tasks")
}

// TestTheMusicLearnsThePerson: the music's rules on a fake player and a fake station directory.
// A station kept on leaves a taste token for this time of day; "something" then plays the
// favourite; "something" again explores a genre never kept at this hour; the person's own words,
// once kept, find their station again by fuzzy match; the music ducks while anyone holds the
// floor and comes back; good night fades it; after a restart the taste is still there.
func TestTheMusicLearnsThePerson(t *testing.T) {
	var mu sync.Mutex
	var played []string
	var vols []float64
	fake := &music.Host{KeepAfter: 700 * time.Millisecond, SkipWithin: 300 * time.Millisecond,
		Call: func(_ context.Context, domain, service string, data map[string]any) error {
			mu.Lock()
			defer mu.Unlock()
			switch service {
			case "play_media":
				played = append(played, data["media_content_id"].(string))
			case "volume_set":
				vols = append(vols, data["volume_level"].(float64))
			}
			return nil
		},
		Search: func(_ context.Context, tag, words string) ([]music.Station, error) {
			if tag == "" {
				tag = "words-" + strings.ReplaceAll(words, " ", "-")
			}
			return []music.Station{{Name: tag + " FM", URL: "http://radio/" + tag}}, nil
		}}
	rigMusic, rigLog = fake, filepath.Join(t.TempDir(), "nina.db")
	defer func() { rigMusic, rigLog = nil, "" }()
	lastPlayed := func() string {
		mu.Lock()
		defer mu.Unlock()
		if len(played) == 0 {
			return ""
		}
		return played[len(played)-1]
	}
	lastVol := func() float64 {
		mu.Lock()
		defer mu.Unlock()
		if len(vols) == 0 {
			return -1
		}
		return vols[len(vols)-1]
	}
	waitFor := func(what string, ok func() bool) {
		for i := 0; i < 60 && !ok(); i++ {
			time.Sleep(100 * time.Millisecond)
		}
		if !ok() {
			t.Fatalf("never: %s (played %v, volumes %v)", what, played, vols)
		}
	}

	r := newRig(t, tasks.All)
	homeCall(t, r.k, "home_music", map[string]any{"intent": "PLAY", "genre": "jazz", "words": "some jazz"})
	waitFor("jazz playing", func() bool { return lastPlayed() == "http://radio/jazz" })
	waitHeard(t, r.v, "You kept jazz (jazz FM) on: I'll remember you like it")
	time.Sleep(300 * time.Millisecond) // the next beat: the favourite asserted
	learned := 0
	r.k.Log.Read(context.Background(), 0, func(e nlog.Event) error {
		if e.Kind == "prefer" {
			learned++
		}
		return nil
	}, "world")
	if learned != 1 {
		t.Fatalf("the world's evidence: %d prefer events", learned)
	}
	homeCall(t, r.k, "home_music", map[string]any{"intent": "STOP"})
	t.Logf("something: %s", homeCall(t, r.k, "home_music", map[string]any{"intent": "SOMETHING"}))
	waitHeard(t, r.v, "Something you like now: jazz (jazz FM)")
	time.Sleep(400 * time.Millisecond)
	homeCall(t, r.k, "home_music", map[string]any{"intent": "SOMETHING"})
	waitHeard(t, r.v, "Something new:")

	// the person's own words: kept once, theirs from then on
	homeCall(t, r.k, "home_music", map[string]any{"intent": "PLAY", "genre": "something for reading", "words": "something calm for reading"})
	waitFor("the words' station", func() bool { return lastPlayed() == "http://radio/words-something-calm-for-reading" })
	waitHeard(t, r.v, `You kept "something calm for reading"`)
	time.Sleep(300 * time.Millisecond)
	homeCall(t, r.k, "home_music", map[string]any{"intent": "STOP"})
	homeCall(t, r.k, "home_music", map[string]any{"intent": "PLAY", "genre": "the reading one", "words": "calm reading music"})
	waitFor("the same station by fuzzy words", func() bool { return lastPlayed() == "http://radio/words-something-calm-for-reading" })

	// the floor, and good night
	r.k.Floor("USER")
	waitFor("ducked", func() bool { return lastVol() > 0 && lastVol() < 0.15 })
	r.k.Floor("FREEu")
	waitFor("back up", func() bool { return lastVol() > 0.25 })
	homeCall(t, r.k, "home_scenes", map[string]any{"intent": "NIGHT"})
	waitFor("a whisper", func() bool { return lastVol() == 0.08 })
	waitHeard(t, r.v, "Good night: the music fades to a whisper")

	// a restart: the taste is the log's
	r.stop()
	time.Sleep(300 * time.Millisecond)
	r.k.Log.Close()
	r2 := newRig(t, tasks.All)
	homeCall(t, r2.k, "home_music", map[string]any{"intent": "SOMETHING"})
	waitHeard(t, r2.v, "Something you like now:")
}

// TestAgainAndTheUsual: "again" repeats the most recent request, the person's words narrowing it
// to a domain; "the usual" is the most frequent at this time of day; a calendar entry is never
// repeated; with no history, the rules say so.
func TestAgainAndTheUsual(t *testing.T) {
	var mu sync.Mutex
	var played []string
	rigMusic = &music.Host{Call: func(_ context.Context, domain, service string, data map[string]any) error {
		mu.Lock()
		defer mu.Unlock()
		if service == "play_media" {
			played = append(played, data["media_content_id"].(string))
		}
		return nil
	}, Search: func(_ context.Context, tag, words string) ([]music.Station, error) {
		return []music.Station{{Name: tag + " FM", URL: "http://radio/" + tag}}, nil
	}}
	rigAgenda = &agenda.Host{Entities: agenda.Default, Call: func(context.Context, string, string, map[string]any) error { return nil }}
	defer func() { rigMusic, rigAgenda = nil, nil }()
	r := newRig(t, tasks.All)
	homeCall(t, r.k, "home_habits", map[string]any{"intent": "USUAL"})
	waitHeard(t, r.v, "I don't know what you mean yet")
	for _, g := range []string{"classical", "jazz", "jazz", "rock"} { // jazz the most often, rock the last
		homeCall(t, r.k, "home_music", map[string]any{"intent": "PLAY", "genre": g, "words": g})
		time.Sleep(200 * time.Millisecond)
	}
	homeCall(t, r.k, "home_agenda", map[string]any{"intent": "TODO", "summary": "Call the bank"})
	homeCall(t, r.k, "home_music", map[string]any{"intent": "STOP"})
	lastPlayed := func() string {
		mu.Lock()
		defer mu.Unlock()
		return played[len(played)-1]
	}
	// "the usual": the most frequent at this hour
	homeCall(t, r.k, "home_habits", map[string]any{"intent": "USUAL", "words": "music"})
	waitHeard(t, r.v, "As before: music: play jazz")
	for i := 0; i < 30 && lastPlayed() != "http://radio/jazz"; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if lastPlayed() != "http://radio/jazz" {
		r.k.Log.Read(context.Background(), 0, func(e nlog.Event) error { t.Logf("%s %s %s", e.Stream, e.Kind, e.Data); return nil }, "habits")
		t.Fatalf("the usual played %s", lastPlayed())
	}
	// "again", narrowed by the words
	homeCall(t, r.k, "home_habits", map[string]any{"intent": "AGAIN", "words": "rock"})
	waitHeard(t, r.v, "As before: music: play rock")
	// a calendar entry is never repeated
	homeCall(t, r.k, "home_habits", map[string]any{"intent": "AGAIN", "words": "the bank"})
	waitHeard(t, r.v, "I won't repeat that")
}

// TestTheHomesCharacterChoosesFirst: with stations known and nothing said yet, "something" plays
// what the home's character leans to at this time of day (catalog/character.yaml: news in the
// morning, lofi in the afternoon, jazz in the evening, ambient at night).
func TestTheHomesCharacterChoosesFirst(t *testing.T) {
	var mu sync.Mutex
	var played []string
	rigMusic = &music.Host{Call: func(_ context.Context, domain, service string, data map[string]any) error {
		mu.Lock()
		defer mu.Unlock()
		if service == "play_media" {
			played = append(played, data["media_content_id"].(string))
		}
		return nil
	}}
	defer func() { rigMusic = nil }()
	r := newRig(t, tasks.All)
	for _, g := range []string{"news", "lofi", "jazz", "ambient", "rock"} {
		r.k.Taste.Register(sw.Item{ID: "station:" + g, Kind: "station", Name: g + " FM", Ref: "http://radio/" + g, Tags: []string{g}})
	}
	want := map[string]string{"morning": "news", "afternoon": "lofi", "evening": "jazz", "night": "ambient"}[taste.Part(time.Now())]
	time.Sleep(300 * time.Millisecond) // a beat: the favourite asserted
	c, ok := r.k.Taste.Select("station", nil, sw.Context{Part: taste.Part(time.Now())}, nil)
	t.Logf("the taste selects %+v %v; priors %d", c, ok, len(r.k.World.Home.W.Taste.Priors))
	t.Logf("something: %s", homeCall(t, r.k, "home_music", map[string]any{"intent": "SOMETHING"}))
	waitHeard(t, r.v, "Something you like now: "+want)
	for i := 0; i < 30; i++ {
		mu.Lock()
		n := len(played)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(played) == 0 || played[0] != "http://radio/"+want {
		t.Fatalf("the character's choice: %v, want %s", played, want)
	}
}

// TestTheBedroomLightOnTheSameRules: the glow, the taste's second instance: a colour by name sends
// its colour and brightness to Alexa; "something" chooses by the taste (the home's character first:
// warm after dark, bright in the morning), and a line names it; off turns it off; a quick change
// is a skip, evidence against that colour.
func TestTheBedroomLightOnTheSameRules(t *testing.T) {
	var mu sync.Mutex
	var said [][]string
	rigGlow = &glow.Host{SkipWithin: time.Hour, Say: func(_ context.Context, cmds []string) error {
		mu.Lock()
		said = append(said, cmds)
		mu.Unlock()
		return nil
	}}
	defer func() { rigGlow = nil }()
	r := newRig(t, tasks.All)
	lastSaid := func() string {
		mu.Lock()
		defer mu.Unlock()
		if len(said) == 0 {
			return ""
		}
		return strings.Join(said[len(said)-1], "; ")
	}
	waitSaid := func(sub string) {
		for i := 0; i < 30 && !strings.Contains(lastSaid(), sub); i++ {
			time.Sleep(100 * time.Millisecond)
		}
		if !strings.Contains(lastSaid(), sub) {
			t.Fatalf("Alexa never told %q: %q", sub, lastSaid())
		}
	}
	homeCall(t, r.k, "home_glow", map[string]any{"intent": "COLOUR", "colour": "amber", "words": "amber"})
	waitSaid("set LED Geiling lights to amber; set LED Geiling lights to 35 percent")
	waitHeard(t, r.v, "The bedroom light goes amber, 35%")
	homeCall(t, r.k, "home_glow", map[string]any{"intent": "COLOUR", "colour": "something cosy", "words": "something cosy"})
	waitSaid("set LED Geiling lights to")
	time.Sleep(300 * time.Millisecond)
	skips := 0
	r.k.Log.Read(context.Background(), 0, func(e nlog.Event) error {
		if e.Kind == "prefer" && strings.Contains(string(e.Data), "colour:bedroom:amber") {
			skips++
		}
		return nil
	}, "world")
	if skips != 1 {
		t.Fatalf("the quick change from amber: %d evidence events", skips)
	}
	homeCall(t, r.k, "home_glow", map[string]any{"intent": "OFF"})
	waitSaid("turn off LED Geiling lights")
	t.Logf("something: %s", homeCall(t, r.k, "home_glow", map[string]any{"intent": "SOMETHING"}))
	waitSaid("set LED Geiling lights to")
}

// TestAMoodReachesTheMusicAndTheLightTogether: one mood from the scenes (the planner), carried by
// the director to the music and the bedroom light; each picks its own expression by the mood's tags
// (the character) and the taste: calm music, a calm colour; sleep fades the light like a sunset and
// the music whispers.
func TestAMoodReachesTheMusicAndTheLightTogether(t *testing.T) {
	var mu sync.Mutex
	var played []string
	var vols []float64
	var said []string
	rigMusic = &music.Host{Call: func(_ context.Context, domain, service string, data map[string]any) error {
		mu.Lock()
		defer mu.Unlock()
		switch service {
		case "play_media":
			played = append(played, data["media_content_id"].(string))
		case "volume_set":
			vols = append(vols, data["volume_level"].(float64))
		}
		return nil
	}, Search: func(_ context.Context, tag, words string) ([]music.Station, error) {
		return []music.Station{{Name: tag + " FM", URL: "http://radio/" + tag}}, nil
	}}
	rigGlow = &glow.Host{FadeStep: 200 * time.Millisecond, Say: func(_ context.Context, cmds []string) error {
		mu.Lock()
		said = append(said, strings.Join(cmds, "; "))
		mu.Unlock()
		return nil
	}}
	defer func() { rigMusic, rigGlow = nil, nil }()
	r := newRig(t, tasks.All)
	r.k.Taste.Register(sw.Item{ID: "station:drone", Kind: "station", Name: "Drone Zone", Ref: "http://radio/drone", Tags: []string{"ambient", "calm"}})
	r.k.Taste.Register(sw.Item{ID: "station:pop", Kind: "station", Name: "Hits", Ref: "http://radio/pop", Tags: []string{"pop"}})
	time.Sleep(300 * time.Millisecond) // a beat: the moods' picks asserted
	snap := func() ([]string, []float64, []string) {
		mu.Lock()
		defer mu.Unlock()
		return append([]string{}, played...), append([]float64{}, vols...), append([]string{}, said...)
	}
	waitFor := func(what string, ok func() bool) {
		for i := 0; i < 50 && !ok(); i++ {
			time.Sleep(100 * time.Millisecond)
		}
		if !ok() {
			p, v, s := snap()
			t.Fatalf("never: %s (played %v, volumes %v, Alexa %v)", what, p, v, s)
		}
	}
	homeCall(t, r.k, "home_scenes", map[string]any{"intent": "MOOD", "mood": "calm"})
	waitFor("calm music", func() bool { p, _, _ := snap(); return len(p) > 0 && p[len(p)-1] == "http://radio/drone" })
	waitFor("a calm colour", func() bool {
		_, _, s := snap()
		return len(s) > 0 && (strings.Contains(s[len(s)-1], "warm white") || strings.Contains(s[len(s)-1], "amber"))
	})
	waitHeard(t, r.v, "For a calm mood:")

	homeCall(t, r.k, "home_scenes", map[string]any{"intent": "MOOD", "mood": "sleep"})
	waitFor("the sunset's three stages", func() bool {
		_, _, s := snap()
		all := strings.Join(s, " | ")
		return strings.Contains(all, "to warm white") && strings.Contains(all, "to orange") && strings.Contains(all, "to dark orange")
	})
	waitFor("the music whispering", func() bool { _, v, _ := snap(); return len(v) > 0 && v[len(v)-1] == 0.12 })
	waitHeard(t, r.v, "The mood turns sleep")
}

// Nina's own mood moves the light that follows a mood, never the person's own colour, never a light
// that is off (glow's her_glow; the user 2026-11-25: "mood is changed, the lighting has to change").
func TestHerMoodMovesTheLightItFollows(t *testing.T) {
	var mu sync.Mutex
	var said []string
	rigGlow = &glow.Host{FadeStep: 200 * time.Millisecond, Say: func(_ context.Context, cmds []string) error {
		mu.Lock()
		said = append(said, strings.Join(cmds, "; "))
		mu.Unlock()
		return nil
	}}
	defer func() { rigGlow = nil }()
	r := newRig(t, tasks.All)
	time.Sleep(300 * time.Millisecond) // a beat: the moods' picks asserted
	n := func() int { mu.Lock(); defer mu.Unlock(); return len(said) }
	wait := func(what string, ok func() bool) {
		for i := 0; i < 50 && !ok(); i++ {
			time.Sleep(100 * time.Millisecond)
		}
		if !ok() {
			mu.Lock()
			defer mu.Unlock()
			t.Fatalf("never: %s (Alexa %v)", what, said)
		}
	}
	still := func(what string, before int) {
		time.Sleep(800 * time.Millisecond)
		if n() != before {
			mu.Lock()
			defer mu.Unlock()
			t.Fatalf("%s: the light changed (Alexa %v)", what, said)
		}
	}

	homeCall(t, r.k, "home_scenes", map[string]any{"intent": "MOOD", "mood": "calm"}) // the home's mood: the light follows it
	wait("a calm colour", func() bool { return n() > 0 })
	before := n()
	r.k.Glow.HerMood("focus")
	wait("her focus on the light", func() bool { return n() > before })
	waitHeard(t, r.v, "My mood turns focus")

	homeCall(t, r.k, "home_glow", map[string]any{"intent": "COLOUR", "colour": "amber", "words": "amber"}) // the person's own
	wait("amber", func() bool { mu.Lock(); defer mu.Unlock(); return strings.Contains(said[len(said)-1], "amber") })
	before = n()
	r.k.Glow.HerMood("lively")
	still("her mood over the person's colour", before)

	homeCall(t, r.k, "home_glow", map[string]any{"intent": "OFF"})
	wait("off", func() bool { mu.Lock(); defer mu.Unlock(); return strings.Contains(said[len(said)-1], "turn off") })
	before = n()
	r.k.Glow.HerMood("cozy")
	still("her mood on a light that is off", before)
}

// TestAnAPIErrorIsHerStrain: her task model's 503s are a strain (busy), linked to her in the
// world and told through the quantized port: one line per new confidence level (a hint, likely,
// sure), none for a fourth error at the same level; an error that is a bug says nothing; a success
// moves it again.
func TestAnAPIErrorIsHerStrain(t *testing.T) {
	r := newRig(t, tasks.All)
	lines := func() []string {
		r.v.mu.Lock()
		defer r.v.mu.Unlock()
		var out []string
		for _, c := range r.v.context {
			if strings.Contains(c, "Nina's task model") {
				out = append(out, c)
			}
		}
		return out
	}
	r.k.Strain("mind", errors.New("Function call is missing a thought_signature")) // a bug: no strain
	busy := errors.New("Error 503, Message: This model is currently experiencing high demand.")
	for i := 0; i < 6 && !r.v.heard("Nina's task model: busy: the service is in high demand (sure"); i++ {
		r.k.Strain("mind", busy)
		time.Sleep(250 * time.Millisecond) // a beat: the world settles, the port reads it
	}
	waitHeard(t, r.v, "Nina's task model: busy: the service is in high demand (sure")
	n := len(lines())
	if n < 3 {
		t.Fatalf("the strain did not rise level by level: %q", lines())
	}
	r.k.Strain("mind", busy)
	time.Sleep(400 * time.Millisecond)
	if got := lines(); len(got) != n {
		t.Fatalf("an error at the same level was told again: %q", got)
	}
	r.k.Strain("mind", nil)
	time.Sleep(400 * time.Millisecond)
	if len(lines()) == n {
		t.Fatalf("a success after the strain was not told: %q", lines())
	}
}

// TestNinaTakesItemsOffLists: REMOVE finds the list's own item by its name as said and removes it;
// the shopping list is a list of its own; a calendar is not a list to take things off.
func TestNinaTakesItemsOffLists(t *testing.T) {
	var mu sync.Mutex
	var calls []map[string]any
	rigAgenda = &agenda.Host{Entities: agenda.Default,
		Call: func(_ context.Context, domain, service string, data map[string]any) error {
			mu.Lock()
			defer mu.Unlock()
			d := map[string]any{"service": domain + "." + service}
			for k, v := range data {
				d[k] = v
			}
			calls = append(calls, d)
			return nil
		},
		Items: func(_ context.Context, entity string) ([]string, error) {
			return map[string][]string{"todo.my_tasks": {"Call the plumber", "Buy batteries"}, "todo.shopping_list": {"Milk"}}[entity], nil
		}}
	defer func() { rigAgenda = nil }()
	r := newRig(t, tasks.All)
	homeCall(t, r.k, "home_agenda", map[string]any{"intent": "TODO", "summary": "Milk", "calendar": "shopping"}) // the voice put the list there (pero)
	time.Sleep(100 * time.Millisecond)
	homeCall(t, r.k, "home_agenda", map[string]any{"intent": "REMOVE", "summary": "call the plumber"})
	time.Sleep(100 * time.Millisecond)
	homeCall(t, r.k, "home_agenda", map[string]any{"intent": "REMOVE", "summary": "milk", "list": "shopping list"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if o := r.k.Home.Call(ctx, "home_agenda", map[string]any{"intent": "REMOVE", "summary": "Dentist", "list": "nina"}); o.Status != "refused" {
		t.Fatalf("off a calendar: %s %s", o.Status, o.Text)
	}
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 3 {
		t.Fatalf("calls: %v", calls)
	}
	if c := calls[0]; c["service"] != "todo.add_item" || c["entity_id"] != "todo.shopping_list" || c["item"] != "Milk" {
		t.Fatalf("to the shopping list: %v", c)
	}
	if c := calls[1]; c["service"] != "todo.remove_item" || c["entity_id"] != "todo.my_tasks" || c["item"] != "Call the plumber" {
		t.Fatalf("off my tasks: %v", c)
	}
	if c := calls[2]; c["service"] != "todo.remove_item" || c["entity_id"] != "todo.shopping_list" || c["item"] != "Milk" {
		t.Fatalf("off the shopping list: %v", c)
	}
}
