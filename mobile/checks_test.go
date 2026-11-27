package nina

import (
	"strings"
	"testing"
)

func TestChecksOnTheDesktop(t *testing.T) {
	if r := piCheck(t.TempDir()); !strings.Contains(r, "clock answered") {
		t.Fatal(r)
	}
	t.Log(piCheck(t.TempDir()))
	t.Log(metisCheck("../catalog"))
}
