package gamification

import "math"

// BaseXP returns XP for a correct answer based on difficulty score (0-100).
func BaseXP(difficultyScore int) int {
	if difficultyScore <= 20 {
		return 5
	}
	if difficultyScore <= 40 {
		return 8
	}
	if difficultyScore <= 60 {
		return 10
	}
	if difficultyScore <= 80 {
		return 13
	}
	return 16
}

// ChallengeBonus adds XP when you answer a question above your ability level.
func ChallengeBonus(userAbility, difficultyScore int) int {
	gap := difficultyScore - userAbility
	if gap <= 0 {
		return 0
	}
	if gap <= 10 {
		return 2
	}
	if gap <= 20 {
		return 5
	}
	return 8
}

// ComboXP returns bonus XP for consecutive correct answers in a drill.
func ComboXP(consecutiveCorrect int) int {
	switch {
	case consecutiveCorrect < 3:
		return 0
	case consecutiveCorrect == 3:
		return 3
	case consecutiveCorrect == 4:
		return 5
	case consecutiveCorrect == 5:
		return 8
	default:
		return 10
	}
}

// TimeBonus rewards fast correct answers. Based on avg time per question in drill.
func TimeBonus(avgSecondsPerQuestion float64) int {
	if avgSecondsPerQuestion <= 45 {
		return 10
	}
	if avgSecondsPerQuestion <= 75 {
		return 5
	}
	if avgSecondsPerQuestion <= 120 {
		return 2
	}
	return 0
}

// StreakMultiplier returns the XP multiplier for a daily streak.
func StreakMultiplier(currentStreak int) float64 {
	if currentStreak < 3 {
		return 1.0
	}
	if currentStreak < 7 {
		return 1.15
	}
	if currentStreak < 14 {
		return 1.25
	}
	if currentStreak < 30 {
		return 1.5
	}
	return 2.0
}

// DrillCompletionXP returns bonus XP for completing a drill.
func DrillCompletionXP(correct, total int) int {
	if total == 0 {
		return 0
	}
	accuracy := float64(correct) / float64(total)

	if correct == total {
		return 25 // Perfect drill bonus
	}
	if accuracy >= 0.8 {
		return 10 // Great drill bonus
	}
	return 0
}

// CalculateComboXPTotal computes total combo XP from the max combo streak in a drill.
func CalculateComboXPTotal(comboMax int) int {
	total := 0
	for i := 3; i <= comboMax; i++ {
		total += ComboXP(i)
	}
	return total
}

// ApplyStreakMultiplier rounds the multiplied XP to the nearest integer.
func ApplyStreakMultiplier(xp int, multiplier float64) int {
	return int(math.Round(float64(xp) * multiplier))
}
