package questions

import "testing"

func TestRCPassageTarget(t *testing.T) {
	cases := []struct {
		name        string
		maxComplete int
		want        int
	}{
		{"no users yet", 0, rcPassageFloor},
		{"below floor", 5, rcPassageFloor},
		{"floor boundary", 7, rcPassageFloor},
		{"gap drives target past floor", 8, 11},
		{"far ahead", 20, 23},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rcPassageTarget(tc.maxComplete); got != tc.want {
				t.Errorf("rcPassageTarget(%d) = %d, want %d", tc.maxComplete, got, tc.want)
			}
		})
	}
}

func TestBatchCostCents(t *testing.T) {
	// Real numbers from prod batch 21: 1525 Opus in, 3509 Opus out, 8041
	// Sonnet validation (blended). 0.7625 + 8.7725 + 7.2369 = 16.7719 -> 17.
	if got := batchCostCents(1525, 3509, 8041); got != 17 {
		t.Errorf("batchCostCents(1525, 3509, 8041) = %d, want 17", got)
	}
	if got := batchCostCents(0, 0, 0); got != 0 {
		t.Errorf("batchCostCents(0, 0, 0) = %d, want 0", got)
	}
}
