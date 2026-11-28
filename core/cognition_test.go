package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ibnHatab/nina-live/tasks"
)

// liveCatalog is ../catalog with its domains the live workspace's (nina-narratives workspaces/live/domains: the
// devices and the cognition under one director, the redesign's W4), its image built; the test skips without it.
func liveCatalog(t *testing.T) string {
	live := os.Getenv("NINA_TEST_LIVE")
	if live == "" {
		live, _ = filepath.Abs("../../nina-narratives/workspaces/live/domains")
	}
	if _, err := os.Stat(filepath.Join(live, "composition.yaml")); err != nil {
		t.Skipf("no live workspace at %s", live)
	}
	if os.Getenv("METISC") == "" {
		t.Skip("METISC: metis-lang's metisc")
	}
	cmd := exec.Command(os.Getenv("METISC"), "build", ".")
	cmd.Dir = live
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("metisc build (the live workspace): %v\n%s", err, out)
	}
	cat, _ := filepath.Abs("../catalog")
	root := t.TempDir()
	for _, f := range []string{"character.yaml", "persona.yaml", "world"} {
		if err := os.Symlink(filepath.Join(cat, f), filepath.Join(root, f)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(live, filepath.Join(root, "domains")); err != nil {
		t.Fatal(err)
	}
	return root
}

func (v *voice) said(words string) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	n := 0
	for _, s := range v.notes {
		if strings.Contains(s, words) {
			n++
		}
	}
	return n
}

// The cognition inside real Nina: a thing the person hands her to keep is brought up once at the next
// conversation (commitments' due and offer, the director's route to attention, its speak on the floor), and
// forgotten when it is done.
func TestTheCognitionBringsUpWhatShesKeeping(t *testing.T) {
	rigRoot = liveCatalog(t)
	defer func() { rigRoot = "" }()
	r := newRig(t, tasks.Mechanisms{})
	k := r.k
	if k.Cognition == nil {
		t.Fatal("the live composition opened without its cognition")
	}
	rt := k.ReportTool()
	if st, txt := rt.Call(context.Background(), map[string]any{"verb": "KEEP", "words": "call the plumber"}); st != "done" {
		t.Fatalf("KEEP: %s %s", st, txt)
	}
	time.Sleep(600 * time.Millisecond) // due, offer, the director's forward, enqueue
	if n := r.v.said("call the plumber"); n != 0 {
		t.Fatalf("brought up before a conversation: %d", n)
	}
	k.Woke()
	deadline := time.Now().Add(3 * time.Second)
	for r.v.said("call the plumber") == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := r.v.said("call the plumber"); n != 1 {
		t.Fatalf("brought up %d times at the conversation's start (notes %q)", n, r.v.notes)
	}
	k.Woke() // spoken once: not again at the next floor
	time.Sleep(600 * time.Millisecond)
	if n := r.v.said("call the plumber"); n != 1 {
		t.Fatalf("brought up again unasked: %d", n)
	}
	if st, txt := rt.Call(context.Background(), map[string]any{"verb": "DONE", "words": "the plumber"}); st != "done" {
		t.Fatalf("DONE: %s %s", st, txt)
	}
	deadline = time.Now().Add(3 * time.Second)
	for len(k.Cognition.Kept()) != 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if kept := k.Cognition.Kept(); len(kept) != 0 {
		t.Fatalf("still kept after DONE: %v", kept)
	}
	// the slot again, clean: kept, brought up, done as the first time (no closed entry, no stale report)
	time.Sleep(600 * time.Millisecond)
	if st, txt := rt.Call(context.Background(), map[string]any{"verb": "KEEP", "words": "water the plants"}); st != "done" || !strings.Contains(txt, "water the plants") {
		t.Fatalf("KEEP again: %s %s", st, txt)
	}
	time.Sleep(600 * time.Millisecond)
	k.Woke()
	deadline = time.Now().Add(3 * time.Second)
	for r.v.said("water the plants") == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := r.v.said("water the plants"); n != 1 {
		t.Fatalf("the reused slot brought up %d times", n)
	}
}
