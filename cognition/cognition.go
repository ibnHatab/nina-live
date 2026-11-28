// Package cognition is the cognition members' world host in real Nina (the redesign's W4, nina-narratives
// notes/step3-cognition-live.md): the Go counterpart of the SITL's nina_world.host and of its loop's feeding.
//
// The cognition members (situations, commitments, attention, social, curiosity, self_model, persona) name
// `host: nina_world.host:<member>` and nothing of the runtime. What reaches them is the world contracts'
// (interfaces/world/*.yaml beside the catalog): an outside event that matches a contract edge's `when:` becomes an
// atom for every member whose manifest takes it (`world.out`), for one beat (lifetime beat) or until a rule uses
// it (consumed). A fired rule named by a contract's command is the command: `say` is her voice's note.
//
// The person's words are open-ended; the rules' tasks are slots (the live capacity's k1..k8). The ledger keeps a
// slot's words outside the rules, as the agenda keeps an event's summary: KEEP allocates one, LATER, AGAIN and
// DONE find it by the words, the commitments' close releases it.
package cognition

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/ibnHatab/nina-live/agenda"
)

// Members are the cognition's members: the ones this host serves when the composition has them.
var Members = []string{"situations", "commitments", "attention", "social", "curiosity", "self_model", "persona"}

// Slots are the live capacity's task slots (workspaces/live/domains/capacity.llp).
var Slots = []string{"k1", "k2", "k3", "k4", "k5", "k6", "k7", "k8"}

// Edge is a contract's edge: an outside event (When: its kind and fields) as an atom (Pred, its args the event's
// fields Args, by default its id).
type Edge struct {
	Pred     string            `yaml:"pred"`
	When     map[string]string `yaml:"when"`
	Lifetime string            `yaml:"lifetime"` // beat (default) | consumed
	Args     []string          `yaml:"args"`
}

// Command is a contract's command: the rules whose firing is it, the args they carry.
type Command struct {
	Rules []string `yaml:"rules"`
	Args  []string `yaml:"args"`
}

type contract struct {
	Family   string             `yaml:"family"`
	Edges    []Edge             `yaml:"edges"`
	Commands map[string]Command `yaml:"commands"`
}

// Contracts are the world contracts' edges and commands (interfaces/world/*.yaml).
type Contracts struct {
	Edges    []Edge
	Commands map[string]Command // command name → its rules
}

// LoadContracts reads every contract in dir.
func LoadContracts(dir string) (Contracts, error) {
	fs, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return Contracts{}, err
	}
	sort.Strings(fs)
	out := Contracts{Commands: map[string]Command{}}
	for _, f := range fs {
		b, err := os.ReadFile(f)
		if err != nil {
			return Contracts{}, err
		}
		var c contract
		if err := yaml.Unmarshal(b, &c); err != nil {
			return Contracts{}, fmt.Errorf("%s: %w", f, err)
		}
		out.Edges = append(out.Edges, c.Edges...)
		for n, cmd := range c.Commands {
			out.Commands[n] = cmd
		}
	}
	return out, nil
}

// Atom is the edge an event stands for ("arrived(k1)"), its lifetime, false when no edge matches.
func (c Contracts) Atom(ev map[string]string) (string, string, bool) {
	for _, e := range c.Edges {
		if e.When["kind"] != ev["kind"] {
			continue
		}
		ok := true
		for k, v := range e.When {
			if k != "kind" && ev[k] != v {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		pred, _, hasArgs := strings.Cut(e.Pred, "(")
		args := e.Args
		if args == nil && hasArgs {
			args = []string{"id"}
		}
		var vals []string
		for _, a := range args {
			vals = append(vals, ev[a])
		}
		atom := pred
		if len(vals) > 0 {
			atom += "(" + strings.Join(vals, ",") + ")"
		}
		lt := e.Lifetime
		if lt == "" {
			lt = "beat"
		}
		return atom, lt, true
	}
	return "", "", false
}

// command is the contract command a fired rule is ("say"), "" when none.
func (c Contracts) command(rule string) string {
	for n, cmd := range c.Commands {
		for _, r := range cmd.Rules {
			if r == rule {
				return n
			}
		}
	}
	return ""
}

// Manifest is what the host reads of a member's domain.yaml: the edges it takes.
type Manifest struct {
	World struct {
		Out []string `yaml:"out"`
	} `yaml:"world"`
}

// World is the cognition's world in real Nina: the contracts, the members' hosts, the ledger.
type World struct {
	C Contracts
	// Say is the say command: her voice's note with the slot's words (nil: not spoken)
	Say func(words string)
	// Save logs a ledger change (kind keep, release), so the log shows a slot's words
	Save func(kind, slot, words string)

	mu      sync.Mutex
	hosts   map[string]*Host
	ledger  map[string]string // slot → the person's words
	sweeps  map[string]int    // slot → the members still to sweep it (not free until none)
	reports []string          // the verbs recognised, for the log and the gate
}

// Open reads the contracts beside the catalog (root/domains/interfaces/world) and each cognition member's
// manifest; members gives the composition's specialists (only the cognition's present get a host).
func Open(root string, members []string) (*World, error) {
	c, err := LoadContracts(filepath.Join(root, "domains", "interfaces", "world"))
	if err != nil {
		return nil, err
	}
	w := &World{C: c, hosts: map[string]*Host{}, ledger: map[string]string{}, sweeps: map[string]int{}}
	for _, m := range members {
		if !isMember(m) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, "domains", m, "domain.yaml"))
		if err != nil {
			return nil, err
		}
		var mf Manifest
		if err := yaml.Unmarshal(b, &mf); err != nil {
			return nil, fmt.Errorf("%s/domain.yaml: %w", m, err)
		}
		takes := map[string]bool{}
		for _, p := range mf.World.Out {
			takes[p] = true
		}
		w.hosts[m] = &Host{w: w, member: m, takes: takes}
	}
	return w, nil
}

func isMember(m string) bool {
	for _, x := range Members {
		if x == m {
			return true
		}
	}
	return false
}

// Host is a member's host (metishome.Host).
func (w *World) Host(member string) *Host { return w.hosts[member] }

// Hosted are the members this world serves.
func (w *World) Hosted() []string {
	var out []string
	for m := range w.hosts {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// Event gives an outside event to the members that take its edge; false when it matches no contract edge.
func (w *World) Event(ev map[string]string) bool {
	atom, lt, ok := w.C.Atom(ev)
	if !ok {
		return false
	}
	pred, _, _ := strings.Cut(atom, "(")
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, h := range w.hosts {
		if h.takes[pred] {
			h.queue = append(h.queue, edge{atom: atom, beat: lt == "beat"})
		}
	}
	return true
}

// Floor: the voice's floor machine opened a turn Nina may take (conversation's floor edge).
func (w *World) Floor() { w.Event(map[string]string{"kind": "floor"}) }

// Verbs are the report verbs and the conversation's report var each stands for.
var Verbs = map[string]map[string]string{
	"KEEP":  {"var": "arrived"},
	"LATER": {"var": "deferral", "value": "later"},
	"AGAIN": {"var": "renewed"},
	"DONE":  {"var": "done"},
}

// Report is the person's words as a report (a verb of Verbs): KEEP takes a free slot for the words, the others
// find the slot that holds them. It answers the slot and its words as kept.
func (w *World) Report(verb, words string) (string, string, error) {
	verb = strings.ToUpper(strings.TrimSpace(verb))
	r, ok := Verbs[verb]
	if !ok {
		return "", "", fmt.Errorf("no report verb %q (KEEP, LATER, AGAIN, DONE)", verb)
	}
	words = strings.TrimSpace(words)
	if words == "" {
		return "", "", fmt.Errorf("the words are needed")
	}
	w.mu.Lock()
	slot, kept := w.find(words)
	if verb == "KEEP" && slot == "" {
		for _, s := range Slots {
			if _, used := w.ledger[s]; !used && w.sweeps[s] == 0 {
				slot, kept = s, words
				w.ledger[s] = words
				break
			}
		}
		if slot == "" {
			w.mu.Unlock()
			return "", "", fmt.Errorf("all %d slots are kept: one must be done first", len(Slots))
		}
		if w.Save != nil {
			w.Save("keep", slot, words)
		}
	}
	w.reports = append(w.reports, verb+" "+slot)
	w.mu.Unlock()
	if slot == "" {
		return "", "", fmt.Errorf("nothing kept is %q", words)
	}
	ev := map[string]string{"kind": "report", "id": slot}
	for k, v := range r {
		ev[k] = v
	}
	w.Event(ev)
	return slot, kept, nil
}

// find is the slot whose words are these (agenda.Match: the same words, else the one containing them).
func (w *World) find(words string) (string, string) {
	var items []string
	by := map[string]string{}
	for s, ws := range w.ledger {
		items = append(items, ws)
		by[ws] = s
	}
	sort.Strings(items)
	if m := agenda.Match(items, words); m != "" {
		return by[m], m
	}
	return "", ""
}

// Kept is the ledger: slot → words.
func (w *World) Kept() map[string]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := map[string]string{}
	for s, ws := range w.ledger {
		out[s] = ws
	}
	return out
}

// Restore puts a slot's words back (the log's keep, after a restart).
func (w *World) Restore(slot, words string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if words == "" {
		delete(w.ledger, slot)
		return
	}
	w.ledger[slot] = words
}

// fired: a member's rule fired; a contract's command is carried out, a closed commitment releases its slot.
func (w *World) fired(member, rule string, args []string) {
	switch w.C.command(rule) {
	case "say":
		if len(args) > 0 && w.Say != nil {
			w.mu.Lock()
			words := w.ledger[args[0]]
			w.mu.Unlock()
			if words != "" {
				w.Say(words)
			}
		}
	}
	if member == "commitments" && rule == "close" && len(args) > 0 {
		slot := args[0]
		w.mu.Lock()
		words, ok := w.ledger[slot]
		delete(w.ledger, slot)
		// the slot's atoms leave every member (a closed ledger entry, a completion report no pending reminder
		// took), so the slot starts clean when it is kept again
		for _, h := range w.hosts {
			h.sweep = append(h.sweep, slot)
			w.sweeps[slot]++
		}
		w.mu.Unlock()
		if ok && w.Save != nil {
			w.Save("release", args[0], words)
		}
	}
}

type edge struct {
	atom string
	beat bool // retracted the beat after (else kept until a rule consumes it)
}

// Host is one cognition member's host: its edges at its next tick, the one-beat ones retracted the beat after.
type Host struct {
	w      *World
	member string
	takes  map[string]bool
	queue  []edge
	beat   []string // one-beat atoms asserted last tick
	sweep  []string // released slots whose atoms leave at the next tick with nothing to hand over
}

// Pending: edges to hand over, or last tick's one-beat atoms to retract (the runtime beats a member that wakes on
// change only then: metishome's hosted, the dagent's Pender).
func (h *Host) Pending() bool {
	h.w.mu.Lock()
	defer h.w.mu.Unlock()
	return len(h.queue) > 0 || len(h.beat) > 0 || len(h.sweep) > 0
}

// Tick hands the member the edges given since its last tick and retracts last tick's one-beat atoms.
func (h *Host) Tick(counts map[string]int) []string {
	h.w.mu.Lock()
	q := h.queue
	h.queue = nil
	var sweep []string
	if len(q) == 0 { // after the slot's last edges were handed over and the rules used them
		sweep, h.sweep = h.sweep, nil
		for _, s := range sweep {
			if h.w.sweeps[s]--; h.w.sweeps[s] <= 0 {
				delete(h.w.sweeps, s)
			}
		}
	}
	h.w.mu.Unlock()
	var out []string
	if len(sweep) > 0 {
		var atoms []string
		for a, n := range counts {
			if n > 0 && hasArg(a, sweep) {
				atoms = append(atoms, a)
			}
		}
		sort.Strings(atoms)
		for _, a := range atoms {
			out = append(out, fmt.Sprintf("-%s %d", a, counts[a]))
		}
	}
	for _, a := range h.beat {
		if n := counts[a]; n > 0 {
			out = append(out, fmt.Sprintf("-%s %d", a, n))
		}
	}
	h.beat = nil
	seen := map[string]bool{}
	for _, e := range q {
		if e.beat { // a one-beat fact is there or not: one token however often it was given
			if seen[e.atom] {
				continue
			}
			seen[e.atom] = true
			h.beat = append(h.beat, e.atom)
		}
		out = append(out, "+"+e.atom)
	}
	if os.Getenv("NINA_DEBUG_COGNITION") != "" && len(out) > 0 {
		fmt.Fprintf(os.Stderr, "[cognition] %s tick %v\n", h.member, out)
	}
	return out
}

// OnEvent is a fired rule (its name "speak[story]-…" and ground args).
func (h *Host) OnEvent(name string, args []string, _ map[string]any) {
	if os.Getenv("NINA_DEBUG_COGNITION") != "" {
		fmt.Fprintf(os.Stderr, "[cognition] %s fired %s %v\n", h.member, name, args)
	}
	rule, _, _ := strings.Cut(name, "[")
	rule, _, _ = strings.Cut(rule, "-")
	h.w.fired(h.member, rule, args)
}

// hasArg: the atom ("ledger(k1,closed)") has one of the slots among its arguments.
func hasArg(atom string, slots []string) bool {
	_, args, ok := strings.Cut(atom, "(")
	if !ok {
		return false
	}
	for _, a := range strings.Split(strings.TrimSuffix(args, ")"), ",") {
		for _, s := range slots {
			if strings.TrimSpace(a) == s {
				return true
			}
		}
	}
	return false
}
