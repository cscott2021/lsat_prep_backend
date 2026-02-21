package questions

import (
	"math"
	"testing"
)

func TestExpectedAccuracy(t *testing.T) {
	// Equal ability and difficulty → ~50%
	got := ExpectedAccuracy(50, 50)
	if math.Abs(got-0.5) > 0.01 {
		t.Errorf("ExpectedAccuracy(50, 50) = %f, want ~0.5", got)
	}

	// User much better → ~88%
	got = ExpectedAccuracy(75, 50)
	if math.Abs(got-0.88) > 0.05 {
		t.Errorf("ExpectedAccuracy(75, 50) = %f, want ~0.88", got)
	}

	// User much worse → ~12%
	got = ExpectedAccuracy(25, 50)
	if math.Abs(got-0.12) > 0.05 {
		t.Errorf("ExpectedAccuracy(25, 50) = %f, want ~0.12", got)
	}

	// Extremes
	got = ExpectedAccuracy(100, 0)
	if got < 0.95 {
		t.Errorf("ExpectedAccuracy(100, 0) = %f, want >0.95", got)
	}

	got = ExpectedAccuracy(0, 100)
	if got > 0.05 {
		t.Errorf("ExpectedAccuracy(0, 100) = %f, want <0.05", got)
	}
}

func TestKFactor(t *testing.T) {
	tests := []struct {
		answered int
		want     float64
	}{
		{0, 3.0},
		{5, 3.0},
		{19, 3.0},
		{20, 2.0},
		{50, 2.0},
		{99, 2.0},
		{100, 1.0},
		{500, 1.0},
	}

	for _, tt := range tests {
		got := KFactor(tt.answered)
		if got != tt.want {
			t.Errorf("KFactor(%d) = %f, want %f", tt.answered, got, tt.want)
		}
	}
}

func TestComputeNewAbility(t *testing.T) {
	// Correct on equal difficulty → small increase
	got := ComputeNewAbility(50, 50, true, 50)
	if got != 51 {
		t.Errorf("ComputeNewAbility(50, 50, true, 50) = %d, want 51", got)
	}

	// Wrong on equal difficulty → small decrease
	got = ComputeNewAbility(50, 50, false, 50)
	if got != 49 {
		t.Errorf("ComputeNewAbility(50, 50, false, 50) = %d, want 49", got)
	}

	// Correct on hard question → bigger increase
	got = ComputeNewAbility(50, 70, true, 50)
	if got <= 51 {
		t.Errorf("ComputeNewAbility(50, 70, true, 50) = %d, want >51", got)
	}

	// Wrong on easy question → bigger decrease
	got = ComputeNewAbility(50, 30, false, 50)
	if got >= 49 {
		t.Errorf("ComputeNewAbility(50, 30, false, 50) = %d, want <49", got)
	}

	// New user (K=3) has bigger adjustments
	gotNew := ComputeNewAbility(50, 50, true, 5)
	gotMature := ComputeNewAbility(50, 50, true, 200)
	if gotNew <= gotMature {
		t.Errorf("New user adjustment (%d) should be larger than mature (%d)", gotNew, gotMature)
	}

	// Upper bound — question must be close enough for meaningful adjustment
	got = ComputeNewAbility(99, 80, true, 5)
	if got != 100 {
		t.Errorf("ComputeNewAbility(99, 80, true, 5) = %d, want 100", got)
	}

	// Lower bound
	got = ComputeNewAbility(1, 20, false, 5)
	if got != 0 {
		t.Errorf("ComputeNewAbility(1, 20, false, 5) = %d, want 0", got)
	}
}

func TestTargetDifficulty(t *testing.T) {
	// Centered: slider=50 means target equals ability
	got := TargetDifficulty(50, 50)
	if got != 50 {
		t.Errorf("TargetDifficulty(50, 50) = %d, want 50", got)
	}

	// Max harder: slider=100 → target = ability + 15
	got = TargetDifficulty(50, 100)
	if got != 65 {
		t.Errorf("TargetDifficulty(50, 100) = %d, want 65", got)
	}

	// Max easier: slider=0 → target = ability - 15
	got = TargetDifficulty(50, 0)
	if got != 35 {
		t.Errorf("TargetDifficulty(50, 0) = %d, want 35", got)
	}

	// Clamped at 0
	got = TargetDifficulty(5, 0)
	if got < 0 {
		t.Errorf("TargetDifficulty(5, 0) = %d, want >= 0", got)
	}

	// Clamped at 100
	got = TargetDifficulty(95, 100)
	if got > 100 {
		t.Errorf("TargetDifficulty(95, 100) = %d, want <= 100", got)
	}
}
