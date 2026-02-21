package gamification

import "github.com/lsat-prep/backend/internal/models"

// AchievementDef defines a single achievement.
type AchievementDef struct {
	Name        string
	Description string
	Gems        int
}

// Achievements maps achievement keys to their definitions.
var Achievements = map[string]AchievementDef{
	"first_drill":     {Name: "First Steps", Description: "Complete your first drill", Gems: 50},
	"streak_3":        {Name: "Getting Started", Description: "3-day streak", Gems: 10},
	"streak_7":        {Name: "Week Warrior", Description: "7-day streak", Gems: 25},
	"streak_14":       {Name: "Dedicated", Description: "14-day streak", Gems: 50},
	"streak_30":       {Name: "Monthly Master", Description: "30-day streak", Gems: 100},
	"streak_100":      {Name: "Centurion", Description: "100-day streak", Gems: 500},
	"perfect_1":       {Name: "Flawless", Description: "First perfect drill", Gems: 10},
	"perfect_10":      {Name: "Perfectionist", Description: "10 perfect drills", Gems: 50},
	"perfect_50":      {Name: "Machine", Description: "50 perfect drills", Gems: 200},
	"questions_100":   {Name: "Century", Description: "Answer 100 questions", Gems: 25},
	"questions_500":   {Name: "Scholar", Description: "Answer 500 questions", Gems: 50},
	"questions_1000":  {Name: "Expert", Description: "Answer 1000 questions", Gems: 100},
	"xp_1000":         {Name: "Rising Star", Description: "Earn 1,000 total XP", Gems: 10},
	"xp_10000":        {Name: "Powerhouse", Description: "Earn 10,000 total XP", Gems: 50},
	"xp_50000":        {Name: "Legend", Description: "Earn 50,000 total XP", Gems: 200},
	"all_lr_subtypes": {Name: "LR Complete", Description: "Practice all 14 LR subtypes", Gems: 50},
	"all_rc_subtypes": {Name: "RC Complete", Description: "Practice all 10 RC subtypes", Gems: 50},
	"friend_5":        {Name: "Social Butterfly", Description: "Add 5 friends", Gems: 25},
	"nudge_first":     {Name: "Motivator", Description: "Send your first nudge", Gems: 5},
	"league_silver":   {Name: "Silver League", Description: "Reach Silver league", Gems: 25},
	"league_gold":     {Name: "Gold League", Description: "Reach Gold league", Gems: 50},
	"league_diamond":  {Name: "Diamond League", Description: "Reach Diamond league", Gems: 100},
	"league_obsidian": {Name: "Obsidian League", Description: "Reach Obsidian league", Gems: 250},
}

// CheckAchievements returns achievement keys the user has newly qualified for
// based on their current gamification state. The caller is responsible for
// checking which ones are already earned and only awarding new ones.
func CheckAchievements(gam *models.UserGamification, friendCount int) []string {
	var earned []string

	// Drill milestones
	if gam.DrillsCompletedTotal >= 1 {
		earned = append(earned, "first_drill")
	}

	// Streak milestones
	if gam.CurrentStreak >= 3 {
		earned = append(earned, "streak_3")
	}
	if gam.CurrentStreak >= 7 {
		earned = append(earned, "streak_7")
	}
	if gam.CurrentStreak >= 14 {
		earned = append(earned, "streak_14")
	}
	if gam.CurrentStreak >= 30 {
		earned = append(earned, "streak_30")
	}
	if gam.CurrentStreak >= 100 {
		earned = append(earned, "streak_100")
	}

	// Perfect drill milestones
	if gam.PerfectDrillsTotal >= 1 {
		earned = append(earned, "perfect_1")
	}
	if gam.PerfectDrillsTotal >= 10 {
		earned = append(earned, "perfect_10")
	}
	if gam.PerfectDrillsTotal >= 50 {
		earned = append(earned, "perfect_50")
	}

	// Question milestones
	if gam.QuestionsAnsweredTotal >= 100 {
		earned = append(earned, "questions_100")
	}
	if gam.QuestionsAnsweredTotal >= 500 {
		earned = append(earned, "questions_500")
	}
	if gam.QuestionsAnsweredTotal >= 1000 {
		earned = append(earned, "questions_1000")
	}

	// XP milestones
	if gam.TotalXP >= 1000 {
		earned = append(earned, "xp_1000")
	}
	if gam.TotalXP >= 10000 {
		earned = append(earned, "xp_10000")
	}
	if gam.TotalXP >= 50000 {
		earned = append(earned, "xp_50000")
	}

	// Friend milestones
	if friendCount >= 5 {
		earned = append(earned, "friend_5")
	}

	// League milestones
	switch gam.LeagueTier {
	case models.LeagueObsidian:
		earned = append(earned, "league_obsidian", "league_diamond", "league_gold", "league_silver")
	case models.LeagueDiamond:
		earned = append(earned, "league_diamond", "league_gold", "league_silver")
	case models.LeagueGold:
		earned = append(earned, "league_gold", "league_silver")
	case models.LeagueSilver:
		earned = append(earned, "league_silver")
	}

	return earned
}
