package questions

import (
	"testing"
	"time"
)

// zeroFillSignups must return a continuous, oldest-first series of exactly
// `days` points ending on the UTC calendar day of `now`, using UTC dates
// regardless of the wall-clock timezone passed in.
func TestZeroFillSignups_ContinuousAndUTC(t *testing.T) {
	// 14:30 in a +05:00 zone on 2026-07-28 is still 2026-07-28 in UTC (09:30Z),
	// so "today" must be 2026-07-28.
	loc := time.FixedZone("plus5", 5*60*60)
	now := time.Date(2026, 7, 28, 14, 30, 0, 0, loc)

	counts := map[string]int{
		"2026-07-28": 5,  // today
		"2026-07-27": 2,  // yesterday
		"2026-07-20": 9,  // mid-window
		"2026-06-01": 99, // outside the 30-day window: must be ignored
	}

	got := zeroFillSignups(counts, now, signupDays)

	if len(got) != signupDays {
		t.Fatalf("series length = %d, want %d", len(got), signupDays)
	}

	// Oldest first, ending today; the last point is today.
	if last := got[len(got)-1]; last.Date != "2026-07-28" || last.Count != 5 {
		t.Errorf("last point = %+v, want {2026-07-28 5}", last)
	}
	if first := got[0]; first.Date != "2026-06-29" { // 2026-07-28 minus 29 days
		t.Errorf("first date = %s, want 2026-06-29", first.Date)
	}

	// Dates must be strictly consecutive with no gaps or duplicates.
	prev, _ := time.Parse("2006-01-02", got[0].Date)
	for i := 1; i < len(got); i++ {
		cur, err := time.Parse("2006-01-02", got[i].Date)
		if err != nil {
			t.Fatalf("unparseable date %q: %v", got[i].Date, err)
		}
		if !cur.Equal(prev.AddDate(0, 0, 1)) {
			t.Errorf("gap at index %d: %s follows %s", i, got[i].Date, got[i-1].Date)
		}
		prev = cur
	}

	// Present days carry their count; absent days are zero-filled.
	byDate := map[string]int{}
	for _, p := range got {
		byDate[p.Date] = p.Count
	}
	if byDate["2026-07-27"] != 2 {
		t.Errorf("2026-07-27 count = %d, want 2", byDate["2026-07-27"])
	}
	if byDate["2026-07-20"] != 9 {
		t.Errorf("2026-07-20 count = %d, want 9", byDate["2026-07-20"])
	}
	if byDate["2026-07-25"] != 0 { // no signups that day -> zero-filled
		t.Errorf("2026-07-25 count = %d, want 0 (zero-filled)", byDate["2026-07-25"])
	}
	// The out-of-window key must never appear in the series.
	if _, ok := byDate["2026-06-01"]; ok {
		t.Errorf("out-of-window date 2026-06-01 leaked into the series")
	}
}

// An empty counts map must still yield a full, all-zero series.
func TestZeroFillSignups_EmptyIsAllZero(t *testing.T) {
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	got := zeroFillSignups(map[string]int{}, now, signupDays)
	if len(got) != signupDays {
		t.Fatalf("series length = %d, want %d", len(got), signupDays)
	}
	for _, p := range got {
		if p.Count != 0 {
			t.Errorf("date %s count = %d, want 0", p.Date, p.Count)
		}
	}
}
