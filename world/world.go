// Package world is the semantic world as a projection of the log: the log's one writer for the
// world stream, and its rebuild at start.
//
// Apply writes the event to the log first, then folds it into the world (write-ahead: what the
// world saw, the log has). Every Every settles it writes a checkpoint: the world's hash, under
// the code that computed it. Open rebuilds the world by replaying the world stream from the
// start and checks every checkpoint written by the same code; a mismatch is reported with its seq
// (a bug in the world's determinism, or a log edited by hand).
//
// The genesis (the map, the parameters, the members' manifests) is the configuration of the
// process that starts. It is written to the log when it changes, for the record, but a rebuild
// always applies the whole stream with the current one: an old reading is still a reading.
//
// A snapshot (kind "snapshot") is the world's state at its seq; every SnapshotEvery checkpoints,
// at the start and at a clean stop the world writes one and prunes the world stream before it (the
// user's choice, 2026-11-24: the snapshot, and the older state thrown away; the other streams, the
// conversations among them, are kept whole). A rebuild restores the latest snapshot and applies
// the events after it. A snapshot of another map or other members is not restored (another map
// gives another world): the world then starts empty and its readings come back from the feed; the
// taste and the schedule, which depend on no map, are kept.
package world

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	nlog "github.com/ibnHatab/nina-live/log"
	semhome "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/home"
	sw "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/semworld"
)

const Stream = "world"

// Member is a Metis package reading the world's ports: its name and its manifest (domain.yaml).
type Member struct {
	Name, Manifest string
}

// Config is the world's genesis.
type Config struct {
	Map     string         // home.map.json
	Params  semhome.Params // the zero value: semhome.DefaultParams
	Members []Member
	Code    string // what computed the checkpoints (a build's id); a checkpoint of other code is not checked
	Every   int    // a checkpoint every this many settles (0: 60)
	// SnapshotEvery: a snapshot (and the prune) every this many checkpoints (0: 60, an hour of
	// beats at the defaults); negative: none, the stream kept whole
	SnapshotEvery int
	Now           func() float64
}

// World is the home's world, kept by the log.
type World struct {
	Home    *semhome.Home
	Log     *nlog.Log
	cfg     Config
	mu      sync.Mutex
	n       int // settles since the last checkpoint
	cps     int // checkpoints since the last snapshot
	gen     genesis
	genesis string
	// OnOut sees every applied event and its outputs (the members' lines, the projection, the
	// surprises), live and never during the rebuild.
	OnOut func(ev semhome.Event, out semhome.Out)
}

// Report is what the rebuild found.
type Report struct {
	Events   int     // world events applied
	Failed   int     // events the world refused (as it did live: they replay the same)
	Checked  int     // checkpoints checked
	Mismatch []int64 // the seqs of checkpoints the replay did not reach
	Genesis  bool    // a new genesis was written (the configuration changed)
	Snapshot int64   // the seq of the snapshot restored (0: none, the stream from its start)
	Refused  string  // why the latest snapshot was not restored ("" restored, or none)
	Pruned   int64   // world events pruned at this start
}

// snap is a snapshot event's data.
type snap struct {
	Code    string      `json:"code"`
	MapHash string      `json:"map_hash"`
	Members []memberRec `json:"members"`
	Hash    string      `json:"hash"`
	State   []byte      `json:"state"` // semhome's Snapshot (base64 in the JSON)
}

type genesis struct {
	MapHash  string         `json:"map_hash"`
	Map      string         `json:"map"`
	Params   semhome.Params `json:"params"`
	Members  []memberRec    `json:"members"`
	Code     string         `json:"code"`
	GenesisH string         `json:"genesis"`
}

type memberRec struct {
	Name, Hash string
}

// checkpoint: the world's hash under the code and the genesis that computed it; a rebuild checks
// only the checkpoints of its own code and genesis (another map or other manifests give another
// world from the same events, rightly)
type checkpoint struct {
	Code    string `json:"code"`
	Genesis string `json:"genesis"`
	Hash    string `json:"hash"`
}

// Open builds the world from its configuration and the log's world stream.
func Open(ctx context.Context, l *nlog.Log, c Config) (*World, Report, error) {
	var rep Report
	if c.Params == (semhome.Params{}) {
		c.Params = semhome.DefaultParams
	}
	if c.Every <= 0 {
		c.Every = 60
	}
	if c.SnapshotEvery == 0 {
		c.SnapshotEvery = 60
	}
	mb, err := os.ReadFile(c.Map)
	if err != nil {
		return nil, rep, err
	}
	m, err := semhome.LoadMap(c.Map)
	if err != nil {
		return nil, rep, err
	}
	h := semhome.New(m, c.Params)
	g := genesis{MapHash: digest(mb), Map: c.Map, Params: c.Params, Code: c.Code}
	for _, mem := range c.Members {
		mf, err := os.ReadFile(mem.Manifest)
		if err != nil {
			return nil, rep, err
		}
		man, err := sw.ReadManifest(mem.Manifest, mem.Name)
		if err != nil {
			return nil, rep, err
		}
		g.Members = append(g.Members, memberRec{mem.Name, digest(mf)})
		if len(man.Beliefs) == 0 && len(man.Levels) == 0 {
			continue
		}
		if err := h.B.Join(man); err != nil {
			return nil, rep, err
		}
	}
	gb, _ := json.Marshal(struct {
		M, C string
		P    semhome.Params
		Mem  []memberRec
	}{g.MapHash, g.Code, g.Params, g.Members})
	g.GenesisH = digest(gb)
	w := &World{Home: h, Log: l, cfg: c, genesis: g.GenesisH, gen: g}

	last := "" // the latest genesis recorded (it may precede the snapshot)
	if ge, ok, err := l.Latest(ctx, Stream, "genesis"); err != nil {
		return nil, rep, err
	} else if ok {
		var x genesis
		if json.Unmarshal(ge.Data, &x) == nil {
			last = x.GenesisH
		}
	}
	var from int64
	if se, ok, err := l.Latest(ctx, Stream, "snapshot"); err != nil {
		return nil, rep, err
	} else if ok {
		from = se.Seq
		var x snap
		switch {
		case json.Unmarshal(se.Data, &x) != nil:
			rep.Refused = "unreadable"
		case x.MapHash != g.MapHash || !sameMembers(x.Members, g.Members):
			rep.Refused = "another map or other members"
		default:
			if err := h.Restore(x.State); err != nil {
				rep.Refused = err.Error()
			} else {
				rep.Snapshot = se.Seq
			}
		}
		if rep.Refused != "" { // the events before it are gone: the world starts empty, but for the taste
			h = semhome.New(m, c.Params)
			for _, mem := range c.Members {
				if man, err := sw.ReadManifest(mem.Manifest, mem.Name); err == nil && (len(man.Beliefs) > 0 || len(man.Levels) > 0) {
					h.B.Join(man)
				}
			}
			if x.State != nil && h.RestoreTaste(x.State) == nil {
				rep.Refused += " (the taste and the schedule kept)"
			}
			w.Home = h
		}
	}
	err = l.Read(ctx, from, func(e nlog.Event) error {
		switch e.Kind {
		case "genesis":
			var x genesis
			if json.Unmarshal(e.Data, &x) == nil {
				last = x.GenesisH
			}
		case "snapshot":
		case "checkpoint":
			var x checkpoint
			if json.Unmarshal(e.Data, &x) == nil && x.Code == c.Code && x.Genesis == g.GenesisH {
				rep.Checked++
				if h.Hash() != x.Hash {
					rep.Mismatch = append(rep.Mismatch, e.Seq)
				}
			}
		default:
			var ev semhome.Event
			if err := json.Unmarshal(e.Data, &ev); err != nil {
				return fmt.Errorf("world event %d: %w", e.Seq, err)
			}
			rep.Events++
			if _, err := h.Apply(ev); err != nil {
				rep.Failed++
			}
		}
		return nil
	}, Stream)
	if err != nil {
		return nil, rep, err
	}
	if last != g.GenesisH {
		if _, err := l.Append(w.now(), Stream, "genesis", "", "", g, 1); err != nil {
			return nil, rep, err
		}
		rep.Genesis = true
	}
	if c.SnapshotEvery > 0 {
		n, err := w.snapshot(w.now())
		if err != nil {
			return nil, rep, err
		}
		rep.Pruned = n
		if n > 10000 {
			l.Vacuum()
		}
	}
	return w, rep, nil
}

func sameMembers(a, b []memberRec) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// snapshot writes the world's state and prunes the world stream before it → the events pruned.
// The caller holds w.mu, or is Open.
func (w *World) snapshot(at float64) (int64, error) {
	st, err := w.Home.Snapshot()
	if err != nil {
		return 0, err
	}
	e, err := w.Log.Append(at, Stream, "snapshot", "", "", snap{Code: w.cfg.Code, MapHash: w.gen.MapHash,
		Members: w.gen.Members, Hash: w.Home.Hash(), State: st}, 1)
	if err != nil {
		return 0, err
	}
	w.cps = 0
	return w.Log.Prune(Stream, e.Seq, "genesis") // the genesis records stay: the configurations' history
}

// Snapshot writes a snapshot now and prunes the world stream before it (at a clean stop).
func (w *World) Snapshot(at float64) (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cfg.SnapshotEvery < 0 {
		return 0, nil
	}
	return w.snapshot(at)
}

func (w *World) now() float64 {
	if w.cfg.Now != nil {
		return w.cfg.Now()
	}
	return 0
}

// Apply is the world's one writer: the event to the log, then into the world. A settle counts
// toward the next checkpoint.
func (w *World) Apply(ev semhome.Event) (semhome.Out, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.Log.Append(ev.At, Stream, ev.Kind, key(ev), "", ev, 1); err != nil {
		return semhome.Out{}, err
	}
	out, err := w.Home.Apply(ev)
	if err != nil {
		return out, err
	}
	if ev.Kind == "settle" {
		if w.n++; w.n >= w.cfg.Every {
			w.n = 0
			if _, err := w.Log.Append(ev.At, Stream, "checkpoint", "", "", checkpoint{w.cfg.Code, w.genesis, w.Home.Hash()}, 1); err != nil {
				return out, err
			}
			if w.cps++; w.cfg.SnapshotEvery > 0 && w.cps >= w.cfg.SnapshotEvery {
				if _, err := w.snapshot(ev.At); err != nil {
					return out, err
				}
			}
		}
	}
	if w.OnOut != nil {
		w.OnOut(ev, out)
	}
	return out, nil
}

// Checkpoint writes a checkpoint now (at a clean stop: the next start checks it).
func (w *World) Checkpoint(at float64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := w.Log.Append(at, Stream, "checkpoint", "", "", checkpoint{w.cfg.Code, w.genesis, w.Home.Hash()}, 1)
	return err
}

func key(ev semhome.Event) string {
	switch ev.Kind {
	case "ha_state":
		if ev.HA != nil {
			return ev.HA.EntityID
		}
	case "command", "report":
		return ev.ID + "." + ev.Var
	case "conclude":
		return ev.Member
	case "schedule":
		if ev.Sched != nil {
			return ev.Sched.Owner + "/" + ev.Sched.Slot
		}
	case "unschedule":
		return ev.Member + "/" + ev.ID
	}
	return ""
}

func digest(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:8])
}

// Schedule is the world's schedule as OWNER writes it (its own entries; an owner is a member or a
// part of Nina's that sets what is due when): every change an event, through Apply.
func (w *World) Schedule(owner string) *Schedule { return &Schedule{w: w, owner: owner} }

// Schedule is one owner's part of the world's schedule.
type Schedule struct {
	w     *World
	owner string
}

// Set enters the owner's entry for its slot (its Owner is the owner's).
func (s *Schedule) Set(e sw.Sched) error {
	e.Owner = s.owner
	_, err := s.w.Apply(semhome.Event{At: s.w.now(), Kind: "schedule", Sched: &e})
	return err
}

// Cancel takes the owner's entry for the slot off.
func (s *Schedule) Cancel(slot string) error {
	_, err := s.w.Apply(semhome.Event{At: s.w.now(), Kind: "unschedule", Member: s.owner, ID: slot})
	return err
}

// Entries are the owner's entries now.
func (s *Schedule) Entries() []sw.Sched {
	s.w.Home.W.Lock()
	defer s.w.Home.W.Unlock()
	return s.w.Home.W.Sched.Of(s.owner)
}
