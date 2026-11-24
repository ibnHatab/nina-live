package explain

// The surprise channel of the Metis loop's workspace (metis-loop docs/WORKSPACE.md §2.2): Nina, the
// world's host, drops each surprise as .metis/surprises/<id>.json in the domain repository (written
// whole, then renamed) with a journal line; the pi loop takes it (<id>.taken) and rules it
// (rulings/<id>.json). A status is a file's existence, never a field edited in place.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Workspace is a workspace's .metis directory.
type Workspace struct{ Dir string }

// Surprise is one dropped surprise and where the loop is with it.
type Surprise struct {
	ID      string         `json:"id"`
	TS      string         `json:"ts"`
	Who     string         `json:"who"`
	Member  string         `json:"member"`
	Reading map[string]any `json:"reading,omitempty"`
	Words   string         `json:"words,omitempty"`
	Status  string         `json:"status"` // open as dropped; open, taken or ruled as read
	Verdict string         `json:"-"`      // the ruling's, when ruled
}

func atomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Drop writes a surprise for the loop: member (the domain), the reading (what the runtime saw),
// the person's words if any. It returns the id.
func (w *Workspace) Drop(member string, reading map[string]any, words string, now time.Time) (string, error) {
	r := make([]byte, 3)
	rand.Read(r)
	id := now.UTC().Format("20060102T150405") + "-" + hex.EncodeToString(r)
	s := Surprise{ID: id, TS: now.UTC().Format(time.RFC3339), Who: "nina", Member: member, Reading: reading, Words: words, Status: "open"}
	if err := atomic(filepath.Join(w.Dir, "surprises", id+".json"), s); err != nil {
		return "", err
	}
	j, _ := json.Marshal(map[string]any{"ts": s.TS, "who": "world", "kind": "surprise", "id": id, "member": member})
	f, err := os.OpenFile(filepath.Join(w.Dir, "loop", "journal.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		f.Write(append(j, '\n'))
		f.Close()
	} else if os.MkdirAll(filepath.Join(w.Dir, "loop"), 0o755) == nil {
		os.WriteFile(filepath.Join(w.Dir, "loop", "journal.jsonl"), append(j, '\n'), 0o644)
	}
	return id, nil
}

// List is every dropped surprise with its status (open, taken: the loop is on it, ruled: with the
// ruling's verdict), newest first.
func (w *Workspace) List() []Surprise {
	files, _ := filepath.Glob(filepath.Join(w.Dir, "surprises", "*.json"))
	var out []Surprise
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var s Surprise
		if json.Unmarshal(b, &s) != nil {
			continue
		}
		if s.ID == "" {
			s.ID = strings.TrimSuffix(filepath.Base(f), ".json")
		}
		s.Status = "open"
		if _, err := os.Stat(strings.TrimSuffix(f, ".json") + ".taken"); err == nil {
			s.Status = "taken"
		}
		if b, err := os.ReadFile(filepath.Join(w.Dir, "rulings", s.ID+".json")); err == nil {
			var r struct {
				Verdict string `json:"verdict"`
			}
			json.Unmarshal(b, &r)
			s.Status, s.Verdict = "ruled", r.Verdict
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// Counts are the surprises by status.
func Counts(ss []Surprise) map[string]int {
	out := map[string]int{}
	for _, s := range ss {
		out[s.Status]++
	}
	return out
}
