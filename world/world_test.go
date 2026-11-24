package world

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	nlog "github.com/ibnHatab/nina-live/log"
	semhome "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/home"
	_ "modernc.org/sqlite"
)

func cfg(code string) Config {
	return Config{Map: "testdata/home.wired.map.json", Code: code, Every: 2,
		Members: []Member{{"comfort", "testdata/comfort.domain.yaml"}}}
}

// day is semantic-world's test day (the replay tool's steps) as events.
func day(t *testing.T) []semhome.Event {
	f, err := os.Open("testdata/home.replay.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var evs []semhome.Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var s struct {
			T        float64          `json:"t"`
			HA       *semhome.HAState `json:"ha"`
			Command  *struct{ ID, Var, Value string }
			Report   *struct{ Who, ID, Var, Value, Ref string }
			Conclude *struct {
				Member, Fact, Rule string
				On                 bool
			}
			Feed   string `json:"feed"`
			Settle bool   `json:"settle"`
			Note   string `json:"note"`
		}
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			t.Fatal(err)
		}
		switch {
		case s.HA != nil:
			evs = append(evs, semhome.Event{At: s.T, Kind: "ha_state", HA: s.HA})
		case s.Command != nil:
			evs = append(evs, semhome.Event{At: s.T, Kind: "command", ID: s.Command.ID, Var: s.Command.Var, Value: s.Command.Value})
		case s.Report != nil:
			evs = append(evs, semhome.Event{At: s.T, Kind: "report", Person: s.Report.Who, ID: s.Report.ID, Var: s.Report.Var, Value: s.Report.Value, Ref: s.Report.Ref})
		case s.Conclude != nil:
			evs = append(evs, semhome.Event{At: s.T, Kind: "conclude", Member: s.Conclude.Member, Fact: s.Conclude.Fact, On: s.Conclude.On, Rule: s.Conclude.Rule})
		case s.Feed == "down":
			evs = append(evs, semhome.Event{At: s.T, Kind: "feed_down"})
		}
		if s.Settle || s.Note != "" {
			evs = append(evs, semhome.Event{At: s.T, Kind: "settle"})
		}
	}
	return evs
}

func live(t *testing.T, path string) string {
	ctx := context.Background()
	l, err := nlog.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	w, rep, err := Open(ctx, l, cfg("test-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Genesis || rep.Events != 0 {
		t.Fatalf("a fresh log: %+v", rep)
	}
	for _, ev := range day(t) {
		w.Apply(ev)
	}
	if err := w.Checkpoint(1e6); err != nil {
		t.Fatal(err)
	}
	return w.Home.Hash()
}

// TestTheWorldIsRebuiltFromTheLog: a day through Apply, the process gone, a new one rebuilds the
// same world from the log and every checkpoint holds.
func TestTheWorldIsRebuiltFromTheLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nina.db")
	want := live(t, path)
	l, err := nlog.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	w, rep, err := Open(context.Background(), l, cfg("test-1"))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Events < 20 || rep.Checked < 3 || len(rep.Mismatch) != 0 || rep.Genesis {
		t.Fatalf("the rebuild: %+v", rep)
	}
	if got := w.Home.Hash(); got != want {
		t.Fatalf("the rebuilt world %s, the live one %s", got, want)
	}
	// another code's checkpoints are not held against this one
	l2, _ := nlog.Open(path)
	defer l2.Close()
	_, rep2, err := Open(context.Background(), l2, cfg("test-2"))
	if err != nil || rep2.Checked != 0 || !rep2.Genesis {
		t.Fatalf("another code: %+v %v", rep2, err)
	}
}

// TestAnEditedLogIsCaught: an event changed in the file after the fact makes the replay miss
// the checkpoints that follow.
func TestAnEditedLogIsCaught(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nina.db")
	live(t, path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec(`UPDATE events SET data = replace(data, '"state":"off"', '"state":"on"') WHERE seq = (SELECT MIN(seq) FROM events WHERE kind = 'ha_state' AND data LIKE '%fan.lab_supply_fan%')`)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("edited %d rows", n)
	}
	db.Close()
	l, _ := nlog.Open(path)
	defer l.Close()
	_, rep, err := Open(context.Background(), l, cfg("test-1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Mismatch) == 0 {
		t.Fatalf("the edit went unseen: %+v", rep)
	}
}

// TestRecall: an episode indexed is found again.
func TestRecall(t *testing.T) {
	l, err := nlog.Open(filepath.Join(t.TempDir(), "nina.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ev, err := l.Append(1, "dialog", "heard", "", "", map[string]string{"text": "boost the lab ventilation"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Episode(ev, "boost the lab ventilation"); err != nil {
		t.Fatal(err)
	}
	got, err := l.Recall(context.Background(), "ventilation", 5)
	if err != nil || len(got) != 1 || got[0].Seq != ev.Seq {
		t.Fatalf("recall: %+v %v", got, err)
	}
}

// TestAnotherGenesisIsNotChecked: the same events under other manifests (a member's beliefs
// changed) give another world; its old checkpoints are not held against it.
func TestAnotherGenesisIsNotChecked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nina.db")
	live(t, path)
	l, _ := nlog.Open(path)
	defer l.Close()
	c := cfg("test-1")
	c.Members = nil // comfort no longer reads the world's ports
	_, rep, err := Open(context.Background(), l, c)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Checked != 0 || len(rep.Mismatch) != 0 || !rep.Genesis {
		t.Fatalf("another genesis: %+v", rep)
	}
}
