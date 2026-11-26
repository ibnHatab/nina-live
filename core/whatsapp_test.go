package core

import (
	"context"
	"testing"
)

type fakeWA struct{ links, unlinks, refreshes int }

func (f *fakeWA) SetShow([]string)                  {}
func (f *fakeWA) Shown() []string                   { return nil }
func (f *fakeWA) Info() map[string]any              { return map[string]any{"linked": f.links > f.unlinks} }
func (f *fakeWA) StartLink(context.Context) error   { f.links++; return nil }
func (f *fakeWA) Unlink(context.Context) error      { f.unlinks++; return nil }
func (f *fakeWA) RefreshGroups(ctx context.Context) { f.refreshes++ }

// her screens link and unlink her WhatsApp through the panel's actions; none is a setting
func TestWhatsAppDeviceActions(t *testing.T) {
	k := &Core{}
	if ok, err := k.Action("whatsapp_link", ""); !ok || err == nil {
		t.Fatalf("no WhatsApp: %v %v", ok, err)
	}
	w := &fakeWA{}
	k.WhatsApp = w
	for _, a := range []string{"whatsapp_link", "whatsapp_refresh", "whatsapp_unlink"} {
		if ok, err := k.Action(a, ""); !ok || err != nil {
			t.Fatalf("%s: %v %v", a, ok, err)
		}
	}
	if w.links != 1 || w.refreshes != 1 || w.unlinks != 1 {
		t.Fatalf("calls: %+v", w)
	}
}
