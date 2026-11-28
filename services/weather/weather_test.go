package weather

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetNoInHomeAssistantsWords(t *testing.T) {
	loc := time.Local
	start := time.Date(2026, 11, 28, 0, 0, 0, 0, loc)
	var ts []string
	for h := 0; h < 48; h++ { // two days, hourly: today rain in the morning, cloudy at noon; tomorrow sunny
		at := start.Add(time.Duration(h) * time.Hour)
		sym, rain, temp := "cloudy", 0.0, 2.0+float64(h%24)/4
		if h < 9 {
			sym, rain = "lightrain", 0.5
		}
		if h >= 24 {
			sym = "clearsky_day"
		}
		ts = append(ts, fmt.Sprintf(`{"time":%q,"data":{"instant":{"details":{"air_temperature":%.1f}},"next_1_hours":{"summary":{"symbol_code":%q},"details":{"precipitation_amount":%.1f}}}}`,
			at.UTC().Format(time.RFC3339), temp, sym, rain))
	}
	body := `{"properties":{"timeseries":[` + strings.Join(ts, ",") + `]}}`
	agent := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent = r.Header.Get("User-Agent")
		w.Write([]byte(body))
	}))
	defer srv.Close()
	s := &Source{Lat: 45.75, Lon: 21.23, URL: srv.URL, Now: func() time.Time { return start.Add(8 * time.Hour) }}
	f, err := s.Forecast(context.Background(), "daily", 5)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f, "today cloudy 2/8 °C 4.5 mm, ") || !strings.Contains(f, " sunny 2/8 °C") {
		t.Fatalf("daily: %s", f)
	}
	l, _ := s.Line(context.Background())
	if l != "weather (here): rainy, 4 °C" {
		t.Fatalf("now: %s", l)
	}
	if !strings.Contains(agent, "nina-live") {
		t.Fatalf("user agent: %q", agent)
	}
	for sym, want := range map[string]string{"clearsky_night": "clear-night", "fair_day": "partlycloudy", "heavyrain": "pouring",
		"rainshowersandthunder_day": "lightning-rainy", "lightsleet": "snowy-rainy", "snow": "snowy", "fog": "fog"} {
		if got := Condition(sym); got != want {
			t.Errorf("%s: %s, want %s", sym, got, want)
		}
	}
}
