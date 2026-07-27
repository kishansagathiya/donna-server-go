package google

import (
	"testing"
	"time"
)

func TestLoadLocationAsiaKolkata(t *testing.T) {
	loc, err := loadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 28, 16, 0, 0, 0, loc)
	if _, offset := now.Zone(); offset != 5*3600+30*60 {
		t.Fatalf("offset=%d want IST", offset)
	}
}

func TestLoadLocationFallsBackForProfileZones(t *testing.T) {
	for name := range fixedZones {
		loc, err := loadLocation(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if loc == nil {
			t.Fatalf("%s: nil location", name)
		}
	}
}

func TestCreateEventWithKolkataTimezone(t *testing.T) {
	loc, err := loadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 27, 11, 0, 0, 0, loc)
	start, end, err := resolveEventWindow(map[string]any{
		"when":    "28 July, 2026 on 4PM",
		"summary": "Meet Radhika on 28 July, 2026 on 4PM",
	}, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 7, 28, 16, 0, 0, 0, loc)
	if !start.Equal(want) {
		t.Fatalf("start=%s want=%s", start, want)
	}
	if !end.Equal(want.Add(time.Hour)) {
		t.Fatalf("end=%s", end)
	}
}
