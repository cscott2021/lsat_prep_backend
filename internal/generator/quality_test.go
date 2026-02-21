package generator

import (
	"math"
	"testing"
)

func TestComputeQualityScore_AllPerfect(t *testing.T) {
	vr := &ValidationResult{Confidence: "high", Matches: true}
	ar := &AdversarialResult{Challenges: nil} // no challenges = clean
	structural := StructuralScore{
		StimulusLengthOK:      true,
		AllChoicesInRange:      true,
		AllExplanationsPresent: true,
		CorrectAnswerDistribOK: true,
	}

	score := ComputeQualityScore(vr, ar, structural)
	// verification: 1.0*0.40 + adversarial: 1.0*0.35 + structural: 1.0*0.25 = 1.0
	if !almostEqual(score, 1.0) {
		t.Errorf("expected score ~1.0, got %f", score)
	}
}

func TestComputeQualityScore_LowVerification(t *testing.T) {
	vr := &ValidationResult{Confidence: "low", Matches: true}
	ar := &AdversarialResult{} // clean
	structural := StructuralScore{
		StimulusLengthOK:      true,
		AllChoicesInRange:      true,
		AllExplanationsPresent: true,
		CorrectAnswerDistribOK: true,
	}

	score := ComputeQualityScore(vr, ar, structural)
	// verification: 0.4*0.40 + adversarial: 1.0*0.35 + structural: 1.0*0.25 = 0.16 + 0.35 + 0.25 = 0.76
	if !almostEqual(score, 0.76) {
		t.Errorf("expected score ~0.76, got %f", score)
	}
}

func TestComputeQualityScore_StrongAdversarial(t *testing.T) {
	vr := &ValidationResult{Confidence: "high", Matches: true}
	ar := &AdversarialResult{
		Challenges: []AdversarialChallenge{
			{ChoiceID: "B", DefenseStrength: "strong"},
		},
	}
	structural := StructuralScore{
		StimulusLengthOK:      true,
		AllChoicesInRange:      true,
		AllExplanationsPresent: true,
		CorrectAnswerDistribOK: true,
	}

	score := ComputeQualityScore(vr, ar, structural)
	// verification: 1.0*0.40 + adversarial: 0.0*0.35 + structural: 1.0*0.25 = 0.40 + 0.0 + 0.25 = 0.65
	if !almostEqual(score, 0.65) {
		t.Errorf("expected score ~0.65, got %f", score)
	}
}

func TestComputeQualityScore_PartialStructural(t *testing.T) {
	vr := &ValidationResult{Confidence: "medium", Matches: true}
	ar := &AdversarialResult{} // clean
	structural := StructuralScore{
		StimulusLengthOK:      true,
		AllChoicesInRange:      false,
		AllExplanationsPresent: true,
		CorrectAnswerDistribOK: false,
	}

	score := ComputeQualityScore(vr, ar, structural)
	// verification: 0.7*0.40 + adversarial: 1.0*0.35 + structural: 0.50*0.25 = 0.28 + 0.35 + 0.125 = 0.755
	if !almostEqual(score, 0.755) {
		t.Errorf("expected score ~0.755, got %f", score)
	}
}

func TestComputeQualityScore_NilInputs(t *testing.T) {
	structural := StructuralScore{
		StimulusLengthOK:      true,
		AllChoicesInRange:      true,
		AllExplanationsPresent: true,
		CorrectAnswerDistribOK: true,
	}

	score := ComputeQualityScore(nil, nil, structural)
	// verification: 0.4*0.40 + adversarial: 1.0*0.35 + structural: 1.0*0.25 = 0.16 + 0.35 + 0.25 = 0.76
	if !almostEqual(score, 0.76) {
		t.Errorf("expected score ~0.76, got %f", score)
	}
}

func TestClassifyQuality_Reject(t *testing.T) {
	if got := ClassifyQuality(0.49); got != "reject" {
		t.Errorf("expected 'reject' for 0.49, got %q", got)
	}
	if got := ClassifyQuality(0.0); got != "reject" {
		t.Errorf("expected 'reject' for 0.0, got %q", got)
	}
}

func TestClassifyQuality_Flagged(t *testing.T) {
	if got := ClassifyQuality(0.50); got != "flagged" {
		t.Errorf("expected 'flagged' for 0.50, got %q", got)
	}
	if got := ClassifyQuality(0.70); got != "flagged" {
		t.Errorf("expected 'flagged' for 0.70, got %q", got)
	}
}

func TestClassifyQuality_Passed(t *testing.T) {
	if got := ClassifyQuality(0.71); got != "passed" {
		t.Errorf("expected 'passed' for 0.71, got %q", got)
	}
	if got := ClassifyQuality(1.0); got != "passed" {
		t.Errorf("expected 'passed' for 1.0, got %q", got)
	}
}

func TestComputeStructuralScore(t *testing.T) {
	q := GeneratedQuestion{
		Stimulus: string(make([]byte, 200)),
	}
	q.Choices = []GeneratedChoice{
		{ID: "A", Text: string(make([]byte, 30)), Explanation: "expl"},
		{ID: "B", Text: string(make([]byte, 30)), Explanation: "expl"},
		{ID: "C", Text: string(make([]byte, 30)), Explanation: "expl"},
		{ID: "D", Text: string(make([]byte, 30)), Explanation: "expl"},
		{ID: "E", Text: string(make([]byte, 30)), Explanation: "expl"},
	}

	score := ComputeStructuralScore(q, false)
	if !score.StimulusLengthOK {
		t.Error("expected StimulusLengthOK = true for 200-char stimulus")
	}
	if !score.AllChoicesInRange {
		t.Error("expected AllChoicesInRange = true")
	}
	if !score.AllExplanationsPresent {
		t.Error("expected AllExplanationsPresent = true")
	}
}

func TestComputeStructuralScore_RC(t *testing.T) {
	q := GeneratedQuestion{
		Stimulus: "", // RC questions have empty stimulus
		Choices: []GeneratedChoice{
			{ID: "A", Text: string(make([]byte, 30)), Explanation: "expl"},
			{ID: "B", Text: string(make([]byte, 30)), Explanation: "expl"},
			{ID: "C", Text: string(make([]byte, 30)), Explanation: "expl"},
			{ID: "D", Text: string(make([]byte, 30)), Explanation: "expl"},
			{ID: "E", Text: string(make([]byte, 30)), Explanation: "expl"},
		},
	}

	score := ComputeStructuralScore(q, true)
	if !score.StimulusLengthOK {
		t.Error("expected StimulusLengthOK = true for RC question (stimulus check skipped)")
	}
}

func TestDetermineAdversarialScore(t *testing.T) {
	tests := []struct {
		name       string
		challenges []AdversarialChallenge
		expected   string
	}{
		{"no challenges", nil, "clean"},
		{"all weak", []AdversarialChallenge{
			{DefenseStrength: "weak"}, {DefenseStrength: "none"},
		}, "clean"},
		{"one moderate", []AdversarialChallenge{
			{DefenseStrength: "moderate"}, {DefenseStrength: "weak"},
		}, "minor_concern"},
		{"any strong", []AdversarialChallenge{
			{DefenseStrength: "weak"}, {DefenseStrength: "strong"},
		}, "ambiguous"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetermineAdversarialScore(tt.challenges)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 0.001
}
