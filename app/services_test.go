package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ibnHatab/nina-live/creds"
)

type baseReader struct{}

func (baseReader) Lists() []Named { return []Named{{"todo.shopping_list", "Shopping list"}} }
func (baseReader) Items(context.Context, string, string) ([]string, error) {
	return []string{"Milk"}, nil
}
func (baseReader) Calendars() []Named { return []Named{{"calendar.nina", "Nina"}} }
func (baseReader) Events(context.Context, string, time.Duration, int) ([]string, error) {
	return nil, nil
}
func (baseReader) Weathers() []Named { return []Named{{"weather.home", "Home"}} }
func (baseReader) Forecast(context.Context, string, string, int) (string, error) {
	return "today cloudy", nil
}

// TestServicesAreSwitchesPartByPart: no services, the deployment's backends as given; the weather on
// Met.no leaves the lists and calendars to the deployment's reader; the agenda on Google needs a token.
func TestServicesAreSwitchesPartByPart(t *testing.T) {
	b := Backends{Reader: baseReader{}}
	got, err := Direct(Config{}, b)
	if err != nil || got.Reader != b.Reader || got.Observed != nil {
		t.Fatalf("no services: %v %v", got, err)
	}
	var c Config
	c.Services.Weather = &struct {
		Lat float64 `yaml:"lat"`
		Lon float64 `yaml:"lon"`
		URL string  `yaml:"url"`
	}{45.75, 21.23, ""}
	got, err = Direct(c, b)
	if err != nil {
		t.Fatal(err)
	}
	if w := got.Reader.Weathers(); len(w) != 1 || w[0].ID != "metno" {
		t.Fatalf("weathers: %v", w)
	}
	if l := got.Reader.Lists(); len(l) != 1 || l[0].ID != "todo.shopping_list" {
		t.Fatalf("lists: %v", l)
	}
	c.Services.Agenda = "google"
	if _, err := Direct(c, b); err == nil {
		t.Fatal("the agenda on Google without a token")
	}
	c.Services.Agenda, c.Services.Photos = "", "google"
	if _, err := Direct(c, b); err == nil {
		t.Fatal("the photos on Google without a token")
	}
}

// TestChoiceTurnsGoogleOn: the services chosen in her setup put the agenda and the albums on Google at her
// start, only with a sign-in kept and only where her config names no backend; nothing chosen: as before.
func TestChoiceTurnsGoogleOn(t *testing.T) {
	dir := t.TempDir()
	st := creds.Open(filepath.Join(dir, "google-token.json"))
	var c Config
	if got := withChoice(c, []string{"calendar", "photos"}, st); got.Services.Agenda != "" || got.Services.Photos != "" {
		t.Fatalf("no sign-in: %+v", got.Services)
	}
	if err := st.Save(creds.Token{ClientID: "id", ClientSecret: "s", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	got := withChoice(c, []string{"calendar", "photos"}, st)
	if got.Services.Agenda != "google" || got.Services.Photos != "google" || got.Services.Google.Token != st.Path ||
		got.Agenda["my_tasks"] != "@default" || got.Services.Google.Calendars["primary"] == "" {
		t.Fatalf("chosen: %+v %v", got.Services, got.Agenda)
	}
	if got := withChoice(c, []string{"tasks"}, st); got.Services.Photos != "" || got.Services.Agenda != "google" {
		t.Fatalf("tasks only: %+v", got.Services)
	}
	c.Services.Agenda = "ha" // her config names one: kept
	if got := withChoice(c, []string{"calendar"}, st); got.Services.Agenda != "ha" {
		t.Fatalf("config's own kept: %+v", got.Services)
	}
	if got := withChoice(Config{}, nil, st); got.Services.Agenda != "" {
		t.Fatalf("nothing chosen: %+v", got.Services)
	}
	b, err := DirectWith(withChoice(Config{}, []string{"photos"}, st), Backends{}, st)
	if err != nil || b.Photos == nil || b.Albums == nil {
		t.Fatalf("photos on Google: %v %v", b.Photos, err)
	}
}
