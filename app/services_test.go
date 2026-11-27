package app

import (
	"context"
	"testing"
	"time"
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
	}{45.75, 21.23}
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
}
