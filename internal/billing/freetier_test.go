package billing

import (
	"testing"
	"time"
)

func TestParseFoundingLaunchDate(t *testing.T) {
	if got := parseFoundingLaunchDate(""); got != nil {
		t.Errorf("empty value should disable the feature (nil), got %v", got)
	}
	if got := parseFoundingLaunchDate("not-a-date"); got != nil {
		t.Errorf("unparseable value should degrade to nil, got %v", got)
	}

	dateOnly := parseFoundingLaunchDate("2026-07-01")
	if dateOnly == nil {
		t.Fatal("bare date should parse")
	}
	if want := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC); !dateOnly.Equal(want) {
		t.Errorf("date-only parsed to %v, want %v", dateOnly, want)
	}

	full := parseFoundingLaunchDate("2026-07-01T12:30:00Z")
	if full == nil {
		t.Fatal("RFC3339 timestamp should parse")
	}
	if want := time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC); !full.Equal(want) {
		t.Errorf("RFC3339 parsed to %v, want %v", full, want)
	}
}

func TestFoundingWindow(t *testing.T) {
	// Unconfigured: no window.
	if _, _, ok := (&Service{}).foundingWindow(); ok {
		t.Error("founding window should be disabled when FoundingLaunchDate is nil")
	}

	launch := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	svc := &Service{cfg: Config{FoundingLaunchDate: &launch}}
	gotLaunch, gotEnds, ok := svc.foundingWindow()
	if !ok {
		t.Fatal("founding window should be enabled when a launch date is set")
	}
	if !gotLaunch.Equal(launch) {
		t.Errorf("launch = %v, want %v", gotLaunch, launch)
	}
	if wantEnds := launch.AddDate(0, 0, foundingWindowDays); !gotEnds.Equal(wantEnds) {
		t.Errorf("ends = %v, want launch + %d days (%v)", gotEnds, foundingWindowDays, wantEnds)
	}
}
