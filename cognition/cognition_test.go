package cognition

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// liveRoot is a catalog root whose domains/ is nina-narratives' live workspace (NINA_TEST_LIVE, else the sibling
// checkout's): the test skips without one.
func liveRoot(t *testing.T) string {
	d := os.Getenv("NINA_TEST_LIVE")
	if d == "" {
		d, _ = filepath.Abs("../../nina-narratives/workspaces/live/domains")
	}
	if _, err := os.Stat(filepath.Join(d, "composition.yaml")); err != nil {
		t.Skipf("no live workspace at %s", d)
	}
	root := t.TempDir()
	if err := os.Symlink(d, filepath.Join(root, "domains")); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestTheReportVerbsAreTheConversationsEdges(t *testing.T) {
	w, err := Open(liveRoot(t), append([]string{"lights"}, Members...))
	if err != nil {
		t.Fatal(err)
	}
	if got := w.Hosted(); !reflect.DeepEqual(got, []string{"attention", "commitments", "curiosity", "persona", "self_model", "situations", "social"}) {
		t.Fatalf("hosted %v", got)
	}
	var said []string
	w.Say = func(words string) { said = append(said, words) }
	slot, _, err := w.Report("keep", "call the plumber")
	if err != nil || slot != "k1" {
		t.Fatalf("KEEP: %s %v", slot, err)
	}
	if got := w.Host("commitments").Tick(nil); !reflect.DeepEqual(got, []string{"+arrived(k1)"}) {
		t.Fatalf("commitments took %v", got)
	}
	if got := w.Host("attention").Tick(nil); len(got) != 0 {
		t.Fatalf("attention does not take arrived: %v", got)
	}
	if s, _, err := w.Report("DONE", "the plumber"); err != nil || s != "k1" {
		t.Fatalf("DONE by part of the words: %s %v", s, err)
	}
	if got := w.Host("attention").Tick(nil); !reflect.DeepEqual(got, []string{"+completion_report(k1)"}) {
		t.Fatalf("attention took %v", got)
	}
	if _, _, err := w.Report("AGAIN", "the dentist"); err == nil {
		t.Fatal("a report of nothing kept was taken")
	}
	// the floor: one beat, once however often given, retracted the beat after
	w.Floor()
	w.Floor()
	a := w.Host("attention")
	if got := a.Tick(nil); !reflect.DeepEqual(got, []string{"+floor"}) {
		t.Fatalf("floor %v", got)
	}
	if got := a.Tick(map[string]int{"floor": 1}); !reflect.DeepEqual(got, []string{"-floor 1"}) {
		t.Fatalf("the floor was not retracted: %v", got)
	}
	// speak is the say command: the slot's words; close releases the slot
	a.OnEvent("speak[story]-live", []string{"k1"}, nil)
	if !reflect.DeepEqual(said, []string{"call the plumber"}) {
		t.Fatalf("said %v", said)
	}
	w.Host("commitments").OnEvent("close", []string{"k1", "spoken"}, nil)
	if len(w.Kept()) != 0 {
		t.Fatalf("still kept: %v", w.Kept())
	}
}
