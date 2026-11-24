package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ibnHatab/acasa/hass"
	"github.com/ibnHatab/nina-live/agenda"
	nlog "github.com/ibnHatab/nina-live/log"
)

// TestAReminderReachesTheGoogleCalendar: NINA_LIVE_GOOGLE="<summary>" puts that reminder on Nina's
// Google calendar (calendar.nina) through the agenda's rules and host, 15 minutes from now, as the
// voice's card would; a log of its own (the running Nina's is untouched).
func TestAReminderReachesTheGoogleCalendar(t *testing.T) {
	summary := os.Getenv("NINA_LIVE_GOOGLE")
	if summary == "" || os.Getenv("METISC") == "" {
		t.Skip("NINA_LIVE_GOOGLE=<summary> writes to the real Google calendar")
	}
	c, err := hass.New("http://127.0.0.1:8123", "~/.config/fsttm/hass.token")
	if err != nil {
		t.Fatal(err)
	}
	L, err := nlog.Open(filepath.Join(t.TempDir(), "nina.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer L.Close()
	var written []agenda.Write
	ag := &agenda.Host{Entities: agenda.Default, Save: func(w agenda.Write) { written = append(written, w) },
		Call: func(ctx context.Context, d, s string, data map[string]any) error { _, err := c.Service(ctx, d, s, data, false); return err }}
	root, _ := filepath.Abs("../catalog")
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	k, err := Open(ctx, Config{Log: L, Root: root, Metisc: os.Getenv("METISC"), Composed: true, Agenda: ag, Code: "google-check"})
	if err != nil {
		t.Fatal(err)
	}
	go k.Run(ctx, 200*time.Millisecond)
	start := time.Now().Add(15 * time.Minute).Truncate(time.Minute)
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	o := k.Home.Call(cctx, "home_agenda", map[string]any{"intent": "EVENT", "summary": summary, "start": start.Format("2006-01-02T15:04"), "minutes": 15.0})
	t.Logf("the rules: %s %s", o.Status, o.Text)
	for i := 0; i < 50 && len(written) == 0; i++ {
		time.Sleep(200 * time.Millisecond)
	}
	if o.Status != "done" || len(written) != 1 || written[0].Err != "" {
		t.Fatalf("written: %+v", written)
	}
	t.Logf("on %s: %+v", written[0].Entity, written[0])
}
