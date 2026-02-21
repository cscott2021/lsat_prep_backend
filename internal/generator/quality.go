package generator

// StructuralScore holds the individual structural compliance checks.
type StructuralScore struct {
	StimulusLengthOK       bool
	AllChoicesInRange       bool
	AllExplanationsPresent  bool
	CorrectAnswerDistribOK bool
}

// ComputeStructuralScore evaluates structural compliance for a single question.
func ComputeStructuralScore(q GeneratedQuestion, isRC bool) StructuralScore {
	stimOK := true
	if !isRC {
		stimLen := len(q.Stimulus)
		stimOK = stimLen >= 100 && stimLen <= 600
	}

	choicesOK := true
	explOK := true
	for _, c := range q.Choices {
		textLen := len(c.Text)
		if textLen < 20 || textLen > 400 {
			choicesOK = false
		}
		if c.Explanation == "" {
			explOK = false
		}
	}

	return StructuralScore{
		StimulusLengthOK:       stimOK,
		AllChoicesInRange:       choicesOK,
		AllExplanationsPresent:  explOK,
		CorrectAnswerDistribOK: true, // Set externally based on batch-level analysis
	}
}

// ComputeQualityScore calculates a composite quality score (0.0-1.0).
//
// Formula: verification_confidence * 0.40 + adversarial_cleanliness * 0.35 + structural * 0.25
func ComputeQualityScore(vr *ValidationResult, ar *AdversarialResult, structural StructuralScore) float64 {
	// Verification confidence score
	verificationScore := 0.4 // default low if no validation
	if vr != nil {
		switch vr.Confidence {
		case "high":
			verificationScore = 1.0
		case "medium":
			verificationScore = 0.7
		case "low":
			verificationScore = 0.4
		}
	}

	// Adversarial cleanliness score
	adversarialScore := 1.0 // default clean if no adversarial check
	if ar != nil && len(ar.Challenges) > 0 {
		moderateCount := 0
		for _, c := range ar.Challenges {
			switch c.DefenseStrength {
			case "strong":
				adversarialScore = 0.0
			case "moderate":
				moderateCount++
			}
		}
		if adversarialScore > 0 {
			if moderateCount > 1 {
				adversarialScore = 0.3
			} else if moderateCount == 1 {
				adversarialScore = 0.6
			}
		}
	}

	// Structural compliance score (4 checks, each worth 0.25)
	structuralScore := 0.0
	if structural.StimulusLengthOK {
		structuralScore += 0.25
	}
	if structural.AllChoicesInRange {
		structuralScore += 0.25
	}
	if structural.AllExplanationsPresent {
		structuralScore += 0.25
	}
	if structural.CorrectAnswerDistribOK {
		structuralScore += 0.25
	}

	return verificationScore*0.40 + adversarialScore*0.35 + structuralScore*0.25
}

// ClassifyQuality returns a classification based on the quality score.
// Returns: "reject" (< 0.50), "flagged" (0.50-0.70), "passed" (> 0.70)
func ClassifyQuality(score float64) string {
	if score < 0.50 {
		return "reject"
	}
	if score <= 0.70 {
		return "flagged"
	}
	return "passed"
}
