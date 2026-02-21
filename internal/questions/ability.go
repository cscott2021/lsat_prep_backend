package questions

import "math"

// ExpectedAccuracy returns the probability a user with the given ability
// gets a question with the given difficulty correct.
// Uses a sigmoid centered on 0 with scaling factor 12.5.
func ExpectedAccuracy(userAbility, difficultyScore int) float64 {
	x := float64(userAbility-difficultyScore) / 12.5
	return 1.0 / (1.0 + math.Exp(-x))
}

// KFactor returns the adjustment strength based on how many questions
// the user has answered at this scope.
func KFactor(questionsAnswered int) float64 {
	if questionsAnswered < 20 {
		return 3.0 // New user: fast convergence
	}
	if questionsAnswered < 100 {
		return 2.0 // Intermediate: moderate adjustment
	}
	return 1.0 // Mature: stable, small adjustments
}

// ComputeNewAbility calculates the updated ability score after answering.
func ComputeNewAbility(currentAbility, difficultyScore int, correct bool, questionsAnswered int) int {
	expected := ExpectedAccuracy(currentAbility, difficultyScore)
	k := KFactor(questionsAnswered)

	var result float64
	if correct {
		result = 1.0
	}

	adjustment := (result - expected) * k
	newAbility := float64(currentAbility) + adjustment

	if newAbility < 0 {
		newAbility = 0
	}
	if newAbility > 100 {
		newAbility = 100
	}

	return int(math.Round(newAbility))
}

// TargetDifficulty computes the center of the difficulty window
// based on user ability and their slider preference.
//
// slider=0:   target = ability - 15  (all easier)
// slider=50:  target = ability       (centered on ability)
// slider=100: target = ability + 15  (all harder)
func TargetDifficulty(userAbility, slider int) int {
	offset := float64(slider-50) * 0.3
	target := float64(userAbility) + offset
	if target < 0 {
		target = 0
	}
	if target > 100 {
		target = 100
	}
	return int(math.Round(target))
}
