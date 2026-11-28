package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ibnHatab/nina-live/services/google"
	"github.com/ibnHatab/nina-live/services/weather"
	semhome "github.com/metis-lang-dev/metis-worlds/libs/semantic-world/go/home"
)

// Services is the config's services: the far ends Nina reaches directly instead of through a home's
// integration layer (the redesign's Google services, s-b478fe59). Each part is a switch: left out, the
// deployment's backend stays (Home Assistant's at home).
type Services struct {
	// Agenda: "google" puts the agenda's writes and the read tools' lists and calendars on Google's APIs
	// (the agenda: map's values are then Google calendar ids and task list titles or ids)
	Agenda string `yaml:"agenda"`
	// Photos: "google" lists her albums on Google Photos directly (the albums she made: Google's rule since 2025,
	// as for Home Assistant's integration) instead of through the deployment's backend
	Photos string `yaml:"photos"`
	Google struct {
		Token     string            `yaml:"token"`     // the credential file (chmod 600): client id, secret, refresh token
		Calendars map[string]string `yaml:"calendars"` // the calendars the read tools show: id → name
		// Endpoint: one base URL for every Google API (the SITL's fake: /token, /calendar, /tasks, /photos, /picker)
		Endpoint string `yaml:"endpoint"`
	} `yaml:"google"`
	// Weather: Met.no for this place (lat, lon), instead of the integration's
	Weather *struct {
		Lat float64 `yaml:"lat"`
		Lon float64 `yaml:"lon"`
		URL string  `yaml:"url"` // default Met.no's (the SITL: its fake)
	} `yaml:"weather"`
}

// Direct puts the config's direct services into the backends (a part not configured keeps the one given).
func Direct(c Config, b Backends) (Backends, error) {
	s := c.Services
	r := &mixed{base: b.Reader}
	var lines []func(ctx context.Context) []line
	var g *google.Client
	if s.Agenda == "google" || s.Photos == "google" {
		if s.Google.Token == "" {
			return b, fmt.Errorf("services: google needs services.google.token")
		}
		var err error
		if g, err = google.Load(expand(s.Google.Token)); err != nil {
			return b, err
		}
		if e := strings.TrimRight(s.Google.Endpoint, "/"); e != "" {
			g.TokenURL, g.CalendarURL, g.TasksURL, g.PhotosURL, g.PickerURL = e+"/token", e+"/calendar", e+"/tasks", e+"/photos", e+"/picker"
		}
	}
	if s.Photos == "google" {
		b.Albums, b.Photos = g.AlbumTitles, g
	}
	o := &observer{base: b.Observed, poke: make(chan struct{}, 1)}
	if s.Agenda == "google" {
		b.Agenda = poked{g, o}
		r.google, r.cals = g, s.Google.Calendars
		lines = append(lines, r.agendaLines, r.listLines)
	}
	if s.Weather != nil {
		r.weather = &weather.Source{Lat: s.Weather.Lat, Lon: s.Weather.Lon, URL: s.Weather.URL}
		lines = append(lines, r.weatherLines)
	}
	if len(lines) == 0 {
		return b, nil
	}
	b.Reader = r
	o.parts = lines
	b.Observed = o.Lines
	watch := b.Watch
	b.Watch = func(ctx context.Context, apply func(semhome.Event) (semhome.Out, error), line func(key, text string)) {
		go o.Run(ctx, line)
		if watch != nil {
			watch(ctx, apply, line)
		}
	}
	return b, nil
}

// mixed reads the lists and calendars from Google, the weather from Met.no, the rest from the base.
type mixed struct {
	base    Reader
	google  *google.Client
	cals    map[string]string
	weather *weather.Source
}

func short() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

func (m *mixed) Lists() []Named {
	if m.google == nil {
		return m.baseOr(func(r Reader) []Named { return r.Lists() })
	}
	ctx, cancel := short()
	defer cancel()
	ls, err := m.google.TaskLists(ctx)
	if err != nil {
		return nil
	}
	var out []Named
	for _, l := range ls {
		out = append(out, Named{ID: l.ID, Name: l.Title})
	}
	return out
}

func (m *mixed) Items(ctx context.Context, id, status string) ([]string, error) {
	if m.google == nil {
		return m.base.Items(ctx, id, status)
	}
	return m.google.Items(ctx, id)
}

func (m *mixed) Calendars() []Named {
	if m.google == nil {
		return m.baseOr(func(r Reader) []Named { return r.Calendars() })
	}
	var out []Named
	for id, n := range m.cals {
		out = append(out, Named{ID: id, Name: n})
	}
	return out
}

func (m *mixed) Events(ctx context.Context, id string, window time.Duration, n int) ([]string, error) {
	if m.google == nil {
		return m.base.Events(ctx, id, window, n)
	}
	return m.google.Events(ctx, id, window, n)
}

func (m *mixed) Weathers() []Named {
	if m.weather == nil {
		return m.baseOr(func(r Reader) []Named { return r.Weathers() })
	}
	return []Named{{ID: "metno", Name: "here"}}
}

func (m *mixed) Forecast(ctx context.Context, id, kind string, n int) (string, error) {
	if m.weather == nil {
		return m.base.Forecast(ctx, id, kind, n)
	}
	return m.weather.Forecast(ctx, kind, n)
}

func (m *mixed) baseOr(f func(Reader) []Named) []Named {
	if m.base == nil {
		return nil
	}
	return f(m.base)
}

// line is one keyed line of her silent context.
type line struct{ key, text string }

func (m *mixed) agendaLines(ctx context.Context) []line {
	var out []line
	for id, name := range m.cals { // as Home Assistant's observer words a calendar (acasa hass Observer.Line)
		evs, err := m.google.Events(ctx, id, 7*24*time.Hour, 5)
		if err != nil {
			continue
		}
		t := name + ": nothing in the coming week"
		if len(evs) > 0 {
			t = name + ", the coming week: " + strings.Join(evs, "; ")
		}
		out = append(out, line{"calendar:" + id, t})
	}
	return out
}

// listLines: every task list with its open items, as Home Assistant's observer words a to-do list.
func (m *mixed) listLines(ctx context.Context) []line {
	ls, err := m.google.TaskLists(ctx)
	if err != nil {
		return nil
	}
	var out []line
	for _, l := range ls {
		items, err := m.google.Items(ctx, l.ID)
		if err != nil {
			continue
		}
		t := l.Title + ": empty"
		if len(items) > 0 {
			t = l.Title + ": " + strings.Join(items, ", ")
		}
		out = append(out, line{"list:" + l.ID, t})
	}
	return out
}

func (m *mixed) weatherLines(ctx context.Context) []line {
	l, err := m.weather.Line(ctx)
	if err != nil {
		return nil
	}
	if f, err := m.weather.Forecast(ctx, "daily", 3); err == nil && f != "" { // the next days too, as Home Assistant's line
		l += "; " + f
	}
	return []line{{"weather", l}}
}

// observer polls the direct services' lines (five minutes; the weather's own cache is half an hour) and
// gives the changed ones to her context, as Home Assistant's observer does for its entities.
type observer struct {
	parts []func(ctx context.Context) []line
	base  func() []string
	poke  chan struct{} // a write to the agenda: read again now (Home Assistant's line followed the list's state)
	mu    sync.Mutex
	now   map[string]string
}

// poked is the agenda's backend whose writes make the observer read again at once.
type poked struct {
	*google.Client
	o *observer
}

func (p poked) after(err error) error {
	if err == nil {
		select {
		case p.o.poke <- struct{}{}:
		default:
		}
	}
	return err
}

func (p poked) CreateEvent(ctx context.Context, id, summary, start, end, note string) error {
	return p.after(p.Client.CreateEvent(ctx, id, summary, start, end, note))
}
func (p poked) AddItem(ctx context.Context, id, summary, due, note string) error {
	return p.after(p.Client.AddItem(ctx, id, summary, due, note))
}
func (p poked) RemoveItem(ctx context.Context, id, item string) error {
	return p.after(p.Client.RemoveItem(ctx, id, item))
}

func (o *observer) Lines() []string {
	var out []string
	if o.base != nil {
		out = append(out, o.base()...)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, t := range o.now {
		out = append(out, t)
	}
	return out
}

func (o *observer) Run(ctx context.Context, changed func(key, text string)) {
	tk := time.NewTicker(5 * time.Minute)
	defer tk.Stop()
	for {
		for _, p := range o.parts {
			c, cancel := context.WithTimeout(ctx, 20*time.Second)
			ls := p(c)
			cancel()
			for _, l := range ls {
				o.mu.Lock()
				if o.now == nil {
					o.now = map[string]string{}
				}
				was := o.now[l.key]
				o.now[l.key] = l.text
				o.mu.Unlock()
				if was != l.text && changed != nil {
					changed(l.key, l.text)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		case <-o.poke:
		}
	}
}
