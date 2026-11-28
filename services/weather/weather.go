// Package weather is the forecast without Home Assistant: Met.no's locationforecast (the source Home
// Assistant's own met integration reads), its symbols named with Home Assistant's conditions so the lines
// are the same words (the redesign's Google services, s-b478fe59). No key; Met.no asks for an identifying
// User-Agent (a generic one: no personal data).
package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Source is one place's forecast.
type Source struct {
	Lat, Lon float64
	URL      string // default Met.no's compact locationforecast 2.0
	HTTP     *http.Client
	Now      func() time.Time
	Agent    string // the User-Agent

	mu    sync.Mutex
	cache *forecast
	at    time.Time
}

type point struct {
	Time time.Time
	Temp float64
	Sym  string  // the next hours' symbol
	Rain float64 // the next hour's precipitation (mm)
}

type forecast struct{ points []point }

func (s *Source) get(ctx context.Context) (*forecast, error) {
	now := s.now()
	s.mu.Lock()
	if s.cache != nil && now.Sub(s.at) < 30*time.Minute {
		c := s.cache
		s.mu.Unlock()
		return c, nil
	}
	s.mu.Unlock()
	u := s.URL
	if u == "" {
		u = "https://api.met.no/weatherapi/locationforecast/2.0/compact"
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s?lat=%.4f&lon=%.4f", u, s.Lat, s.Lon), nil)
	ag := s.Agent
	if ag == "" {
		ag = "nina-live/1 github.com/ibnHatab/nina-live"
	}
	req.Header.Set("User-Agent", ag)
	hc := s.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("weather: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("weather: Met.no %s", resp.Status)
	}
	var r struct {
		Properties struct {
			Timeseries []struct {
				Time time.Time
				Data struct {
					Instant struct {
						Details struct {
							Temp float64 `json:"air_temperature"`
						}
					}
					Next1 *struct {
						Summary struct {
							Symbol string `json:"symbol_code"`
						}
						Details struct {
							Rain float64 `json:"precipitation_amount"`
						}
					} `json:"next_1_hours"`
					Next6 *struct {
						Summary struct {
							Symbol string `json:"symbol_code"`
						}
						Details struct {
							Rain float64 `json:"precipitation_amount"`
						}
					} `json:"next_6_hours"`
				}
			}
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("weather: %w", err)
	}
	f := &forecast{}
	for _, t := range r.Properties.Timeseries {
		p := point{Time: t.Time, Temp: t.Data.Instant.Details.Temp}
		switch {
		case t.Data.Next1 != nil:
			p.Sym, p.Rain = t.Data.Next1.Summary.Symbol, t.Data.Next1.Details.Rain
		case t.Data.Next6 != nil:
			p.Sym, p.Rain = t.Data.Next6.Summary.Symbol, t.Data.Next6.Details.Rain/6
		}
		f.points = append(f.points, p)
	}
	if len(f.points) == 0 {
		return nil, fmt.Errorf("weather: Met.no gave no forecast")
	}
	s.mu.Lock()
	s.cache, s.at = f, now
	s.mu.Unlock()
	return f, nil
}

func (s *Source) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Condition is Home Assistant's name for a Met.no symbol (its met integration's table).
func Condition(sym string) string {
	base := sym
	night := strings.HasSuffix(sym, "_night")
	if i := strings.IndexByte(sym, '_'); i > 0 {
		base = sym[:i]
	}
	switch {
	case base == "clearsky" && night:
		return "clear-night"
	case base == "clearsky":
		return "sunny"
	case base == "fair" || base == "partlycloudy":
		return "partlycloudy"
	case base == "cloudy":
		return "cloudy"
	case base == "fog":
		return "fog"
	case strings.Contains(base, "thunder"):
		return "lightning-rainy"
	case strings.Contains(base, "sleet"):
		return "snowy-rainy"
	case strings.Contains(base, "snow"):
		return "snowy"
	case base == "heavyrain" || base == "heavyrainshowers":
		return "pouring"
	case strings.Contains(base, "rain"):
		return "rainy"
	}
	return base
}

// Now is the weather now, as a line for her silent context ("the weather: cloudy, 6 °C").
func (s *Source) Line(ctx context.Context) (string, error) {
	f, err := s.get(ctx)
	if err != nil {
		return "", err
	}
	now := s.now()
	p := f.points[0]
	for _, q := range f.points {
		if !q.Time.After(now) {
			p = q
		}
	}
	return fmt.Sprintf("weather (here): %s, %.0f °C", strings.ReplaceAll(Condition(p.Sym), "-", " "), p.Temp), nil
}

// Forecast is the next days (kind daily) or hours (hourly), in the words of Home Assistant's forecast
// lines (acasa's hass Observer.Forecast): "today cloudy 2/7 °C, Sat rainy 3/8 °C 4.2 mm".
func (s *Source) Forecast(ctx context.Context, kind string, n int) (string, error) {
	f, err := s.get(ctx)
	if err != nil {
		return "", err
	}
	now := s.now()
	if kind == "hourly" {
		var parts []string
		for _, p := range f.points {
			if p.Time.Before(now.Truncate(time.Hour)) {
				continue
			}
			if len(parts) == n {
				break
			}
			parts = append(parts, fmt.Sprintf("%s %s %.0f °C", p.Time.Local().Format("15:04"), strings.ReplaceAll(Condition(p.Sym), "-", " "), p.Temp))
		}
		return strings.Join(parts, ", "), nil
	}
	type day struct {
		lo, hi, rain float64
		syms         map[string]int
		noon         string
	}
	days := map[string]*day{}
	var order []string
	for _, p := range f.points {
		k := p.Time.Local().Format("2006-01-02")
		if k < now.Local().Format("2006-01-02") {
			continue
		}
		d := days[k]
		if d == nil {
			d = &day{lo: math.Inf(1), hi: math.Inf(-1), syms: map[string]int{}}
			days[k] = d
			order = append(order, k)
		}
		d.lo, d.hi, d.rain = math.Min(d.lo, p.Temp), math.Max(d.hi, p.Temp), d.rain+p.Rain
		if p.Sym != "" {
			d.syms[Condition(p.Sym)]++
			if h := p.Time.Local().Hour(); h >= 11 && h <= 13 {
				d.noon = Condition(p.Sym)
			}
		}
	}
	sort.Strings(order)
	var parts []string
	for i, k := range order {
		if i >= n {
			break
		}
		d := days[k]
		cond := d.noon
		if cond == "" { // the day's most frequent condition
			best := 0
			for c, m := range d.syms {
				if m > best || (m == best && c < cond) {
					cond, best = c, m
				}
			}
		}
		t, _ := time.ParseInLocation("2006-01-02", k, time.Local)
		name := t.Format("Mon")
		if i == 0 && k == now.Local().Format("2006-01-02") {
			name = "today"
		}
		s := fmt.Sprintf("%s %s %.0f/%.0f °C", name, strings.ReplaceAll(cond, "-", " "), d.lo, d.hi)
		if d.rain > 0.05 {
			s += fmt.Sprintf(" %.1f mm", d.rain)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", "), nil
}
