package nina

import (
	"testing"

	"github.com/ibnHatab/nina-live/music"
)

func TestBestStation(t *testing.T) {
	sts := []music.Station{
		{Name: "Christmas FM Non-stop!", URL: "u1", Tags: []string{"christmas", "jazz", "pop"}},
		{Name: "Radio Swiss Classic", URL: "u2", Tags: []string{"classical"}},
		{Name: "Smooth Jazz Lounge", URL: "u3", Tags: []string{"lounge"}},
	}
	if st, _ := bestStation(sts, "jazz"); st.URL != "u3" {
		t.Fatalf("jazz: %s", st.Name)
	}
	if st, _ := bestStation(sts, "classical"); st.URL != "u2" {
		t.Fatalf("classical: %s", st.Name)
	}
	if st, _ := bestStation(sts[:1], "jazz"); st.URL != "u1" {
		t.Fatalf("tagged only: %s", st.Name)
	}
	if st, _ := bestStation(sts, ""); st.URL != "u1" {
		t.Fatalf("no genre: %s", st.Name)
	}
}
