package whatsapp

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// Her name in a message: a word of its own, in any case, in any language's letters around it.
func TestNamed(t *testing.T) {
	re := nameRe([]string{"Nina"})
	for s, want := range map[string]bool{"nina, what's on today?": true, "Hey Nina!": true, "ask nina.": true,
		"ninanina": false, "the ninas": false, "Antonina": false, "NINA": true, "ниna": false} {
		if got := re.MatchString(lower(s)); got != want {
			t.Errorf("%q: %v, want %v", s, got, want)
		}
	}
}

func TestText(t *testing.T) {
	cases := []struct {
		m          *waE2E.Message
		text, kind string
	}{
		{&waE2E.Message{Conversation: proto.String("hi")}, "hi", "text"},
		{&waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("@Nina hi")}}, "@Nina hi", "text"},
		{&waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("the board")}}, "the board", "image"},
		{&waE2E.Message{AudioMessage: &waE2E.AudioMessage{}}, "", "voice"},
	}
	for _, c := range cases {
		if tx, k := text(c.m); tx != c.text || k != c.kind {
			t.Errorf("%v: %q %q", c.m, tx, k)
		}
	}
}

// What is kept: none listed, all; else self, the names, or all.
func TestKeeps(t *testing.T) {
	c := &Client{}
	if !c.keeps("Family", false) {
		t.Fatal("none listed: all kept")
	}
	c.cfg.Keep = []string{"self"}
	if !c.keeps("self", true) || c.keeps("Family", false) || c.keeps("Ana", false) {
		t.Fatal("self only")
	}
	c.cfg.Keep = []string{"self", "Family"}
	if !c.keeps("family", false) || c.keeps("Ana", false) {
		t.Fatal("self and a group")
	}
	c.cfg.Keep = []string{"self"}
	c.cfg.Show = []string{"Office"}
	if !c.keeps("office", false) || c.keeps("Ana", false) {
		t.Fatal("a group on her screen is kept")
	}
}
