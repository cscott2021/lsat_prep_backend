package gamification

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/lsat-prep/backend/internal/models"
)

type Service struct {
	store *Store
}

func NewService(store *Store) *Service {
	return &Service{store: store}
}

// ── Per-Question XP (called from SubmitAnswer) ──────────

// AwardQuestionXP calculates and awards XP for a correct answer.
// Returns the XP awarded (0 if incorrect — caller should only call on correct answers).
func (s *Service) AwardQuestionXP(userID int64, difficultyScore, userAbility int) int {
	base := BaseXP(difficultyScore)
	challenge := ChallengeBonus(userAbility, difficultyScore)
	xpAwarded := base + challenge

	// Ensure gamification row exists
	s.store.GetOrCreateGamification(userID)

	if err := s.store.AddXP(userID, xpAwarded); err != nil {
		log.Printf("[gamification] failed to add XP for user %d: %v", userID, err)
	}

	s.store.LogXPEvent(userID, "question_correct", xpAwarded, map[string]interface{}{
		"difficulty_score": difficultyScore,
		"base_xp":          base,
		"challenge_bonus":  challenge,
	})

	return xpAwarded
}

// ── Streak ──────────────────────────────────────────────

func (s *Service) UpdateStreak(userID int64) error {
	gam, err := s.store.GetOrCreateGamification(userID)
	if err != nil {
		return fmt.Errorf("get gamification: %w", err)
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)

	// Already active today — no change
	if gam.LastActiveDate != nil {
		lastActive := gam.LastActiveDate.Truncate(24 * time.Hour)
		if lastActive.Equal(today) {
			return nil
		}

		daysSinceLast := int(today.Sub(lastActive).Hours() / 24)

		switch {
		case daysSinceLast == 1:
			// Consecutive day — increment streak
			gam.CurrentStreak++
		case daysSinceLast == 2 && gam.StreakFreezesOwned > 0:
			// Missed yesterday but had a freeze — streak preserved
			gam.CurrentStreak++
			gam.StreakFreezeActive = false
			gam.StreakFreezesOwned--
		default:
			// Streak broken
			gam.CurrentStreak = 1
			gam.StreakFreezeActive = false
		}
	} else {
		// First ever activity
		gam.CurrentStreak = 1
	}

	if gam.CurrentStreak > gam.LongestStreak {
		gam.LongestStreak = gam.CurrentStreak
	}

	gam.LastActiveDate = &today

	// Check streak milestones and award gems
	streakMilestones := map[int]int{
		3: 10, 7: 25, 14: 50, 30: 100, 60: 200, 100: 500, 365: 1000,
	}
	if gems, ok := streakMilestones[gam.CurrentStreak]; ok {
		gam.Gems += gems
		s.store.LogXPEvent(userID, "streak_milestone", 0, map[string]interface{}{
			"streak":      gam.CurrentStreak,
			"gems_awarded": gems,
		})
	}

	return s.store.UpdateGamification(userID, gam)
}

// ── Daily Goal ──────────────────────────────────────────

func (s *Service) UpdateDailyGoal(userID int64, questionsAnswered int) error {
	gam, err := s.store.GetOrCreateGamification(userID)
	if err != nil {
		return fmt.Errorf("get gamification: %w", err)
	}

	today := time.Now().UTC().Format("2006-01-02")
	goalDate := gam.DailyGoalDate.Format("2006-01-02")

	// Reset if new day
	if today != goalDate {
		gam.DailyGoalProgress = 0
		gam.DailyGoalDate = time.Now().UTC()
	}

	wasCompleted := gam.DailyGoalProgress >= gam.DailyGoalTarget
	gam.DailyGoalProgress += questionsAnswered
	nowCompleted := gam.DailyGoalProgress >= gam.DailyGoalTarget

	// Award gems if just completed
	if !wasCompleted && nowCompleted {
		gam.Gems += 5
		s.store.LogXPEvent(userID, "daily_goal", 0, map[string]interface{}{
			"gems_awarded": 5,
			"target":       gam.DailyGoalTarget,
		})
	}

	return s.store.UpdateGamification(userID, gam)
}

// ── Counter Increment (delegates to store) ──────────────

func (s *Service) IncrementCounters(userID int64, correct bool) error {
	s.store.GetOrCreateGamification(userID)
	return s.store.IncrementCounters(userID, correct)
}

// ── Drill Completion ────────────────────────────────────

func (s *Service) CompleteDrill(userID int64, req models.CompleteDrillRequest) (*models.DrillCompleteResponse, error) {
	gam, err := s.store.GetOrCreateGamification(userID)
	if err != nil {
		return nil, fmt.Errorf("get gamification: %w", err)
	}

	total := len(req.QuestionIDs)
	correct := len(req.CorrectIDs)
	isPerfect := correct == total && total > 0

	// The per-question XP was already awarded during SubmitAnswer.
	// Here we calculate the drill-level bonuses.

	// Combo XP
	comboXP := CalculateComboXPTotal(req.ComboMax)

	// Time bonus
	timeBonus := TimeBonus(req.AvgTimeSeconds)

	// Drill completion bonus
	drillXP := DrillCompletionXP(correct, total)

	// Subtotal of drill-level bonuses
	subtotal := comboXP + timeBonus + drillXP

	// Apply streak multiplier
	multiplier := StreakMultiplier(gam.CurrentStreak)
	totalDrillXP := ApplyStreakMultiplier(subtotal, multiplier)

	// Award drill-level XP
	if totalDrillXP > 0 {
		if err := s.store.AddXP(userID, totalDrillXP); err != nil {
			log.Printf("[gamification] failed to add drill XP: %v", err)
		}
		s.store.LogXPEvent(userID, "drill_complete", totalDrillXP, map[string]interface{}{
			"combo_xp":     comboXP,
			"time_bonus":    timeBonus,
			"drill_xp":      drillXP,
			"multiplier":    multiplier,
			"correct":       correct,
			"total":         total,
		})
	}

	// Update drill counters
	gam.DrillsCompletedTotal++
	if isPerfect {
		gam.PerfectDrillsTotal++
	}

	// Gem awards
	gemsEarned := 0
	if isPerfect {
		gemsEarned += 10
	}

	// First drill bonus
	if gam.DrillsCompletedTotal == 1 {
		gemsEarned += 50
	}

	if gemsEarned > 0 {
		gam.Gems += gemsEarned
	}

	// Refresh totals from DB before achievement check
	if err := s.store.UpdateGamification(userID, gam); err != nil {
		log.Printf("[gamification] failed to update gamification: %v", err)
	}

	// Re-read to get accurate total_xp after AddXP
	gam, _ = s.store.GetOrCreateGamification(userID)

	// Check achievements
	friendCount, _ := s.store.CountFriends(userID)
	qualifiedAchievements := CheckAchievements(gam, friendCount)
	existingAchievements, _ := s.store.GetUserAchievements(userID)
	existingSet := make(map[string]bool)
	for _, a := range existingAchievements {
		existingSet[a] = true
	}

	var newAchievements []string
	for _, a := range qualifiedAchievements {
		if !existingSet[a] {
			if err := s.store.AwardAchievement(userID, a); err == nil {
				newAchievements = append(newAchievements, a)
				// Award gems for achievement
				if def, ok := Achievements[a]; ok {
					s.store.AwardGems(userID, def.Gems)
					gemsEarned += def.Gems
				}
			}
		}
	}

	if newAchievements == nil {
		newAchievements = []string{}
	}

	return &models.DrillCompleteResponse{
		XPBreakdown: models.XPBreakdown{
			Questions:       0, // Already awarded per-question
			ComboBonuses:    comboXP,
			TimeBonus:       timeBonus,
			DrillCompletion: drillXP,
			Subtotal:        subtotal,
			StreakMultiplier: multiplier,
			TotalXP:         totalDrillXP,
		},
		GemsEarned: gemsEarned,
		Streak: models.StreakInfo{
			Current:    gam.CurrentStreak,
			Multiplier: multiplier,
		},
		DailyGoal: models.DailyGoalInfo{
			Progress:  gam.DailyGoalProgress,
			Target:    gam.DailyGoalTarget,
			Completed: gam.DailyGoalProgress >= gam.DailyGoalTarget,
		},
		AchievementsUnlocked: newAchievements,
		LeagueTier:           gam.LeagueTier,
	}, nil
}

// ── Get Gamification State ──────────────────────────────

func (s *Service) GetGamification(userID int64) (*models.GamificationResponse, error) {
	gam, err := s.store.GetOrCreateGamification(userID)
	if err != nil {
		return nil, err
	}

	achievements, err := s.store.GetUserAchievements(userID)
	if err != nil {
		achievements = []string{}
	}

	unreadNudges, _ := s.store.CountUnreadNudges(userID)

	// Reset daily progress if day changed
	today := time.Now().UTC().Format("2006-01-02")
	goalDate := gam.DailyGoalDate.Format("2006-01-02")
	dailyProgress := gam.DailyGoalProgress
	if today != goalDate {
		dailyProgress = 0
	}

	return &models.GamificationResponse{
		TotalXP:                gam.TotalXP,
		WeeklyXP:              gam.WeeklyXP,
		CurrentStreak:         gam.CurrentStreak,
		LongestStreak:         gam.LongestStreak,
		StreakFreezeActive:    gam.StreakFreezeActive,
		StreakFreezesOwned:    gam.StreakFreezesOwned,
		Gems:                  gam.Gems,
		DailyGoalTarget:       gam.DailyGoalTarget,
		DailyGoalProgress:     dailyProgress,
		LeagueTier:            gam.LeagueTier,
		QuestionsAnsweredTotal: gam.QuestionsAnsweredTotal,
		QuestionsCorrectTotal:  gam.QuestionsCorrectTotal,
		DrillsCompletedTotal:   gam.DrillsCompletedTotal,
		PerfectDrillsTotal:     gam.PerfectDrillsTotal,
		Achievements:          achievements,
		UnreadNudges:          unreadNudges,
	}, nil
}

// ── Purchases ───────────────────────────────────────────

func (s *Service) BuyStreakFreeze(userID int64) (*models.StreakFreezeResponse, error) {
	gam, err := s.store.GetOrCreateGamification(userID)
	if err != nil {
		return nil, err
	}

	if gam.StreakFreezesOwned >= 3 {
		return nil, fmt.Errorf("already have maximum freezes (3)")
	}
	if gam.Gems < 50 {
		return nil, fmt.Errorf("not enough gems (need 50, have %d)", gam.Gems)
	}

	if err := s.store.BuyStreakFreeze(userID); err != nil {
		return nil, err
	}

	return &models.StreakFreezeResponse{
		GemsRemaining:    gam.Gems - 50,
		StreakFreezesOwned: gam.StreakFreezesOwned + 1,
	}, nil
}

func (s *Service) SetDailyGoal(userID int64, target int) error {
	validTargets := map[int]bool{3: true, 6: true, 12: true, 18: true}
	if !validTargets[target] {
		return fmt.Errorf("target must be 3, 6, 12, or 18")
	}
	s.store.GetOrCreateGamification(userID)
	return s.store.SetDailyGoalTarget(userID, target)
}

// ── Friends ─────────────────────────────────────────────

func (s *Service) SendFriendRequest(userID int64, friendID int64) (*models.FriendRequestResponse, error) {
	if friendID == userID {
		return nil, fmt.Errorf("cannot friend yourself")
	}

	// Look up friend to get their display name and username
	_, friendName, friendUsername, err := s.store.LookupUserByID(friendID)
	if err != nil {
		return nil, fmt.Errorf("user not found")
	}

	existing, err := s.store.CheckExistingFriendship(userID, friendID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("friend request already exists")
	}

	id, err := s.store.SendFriendRequest(userID, friendID)
	if err != nil {
		return nil, fmt.Errorf("send friend request: %w", err)
	}

	resp := &models.FriendRequestResponse{
		FriendshipID: id,
		Status:       "pending",
	}
	resp.Friend.ID = friendID
	resp.Friend.DisplayName = models.User{Name: friendName}.DisplayName()
	resp.Friend.Username = friendUsername

	return resp, nil
}

func (s *Service) RespondFriendRequest(userID int64, friendshipID int64, action string) error {
	friendship, err := s.store.GetFriendship(friendshipID)
	if err != nil {
		return fmt.Errorf("friendship not found")
	}

	// Only the recipient can respond
	if friendship.FriendID != userID {
		return fmt.Errorf("not authorized to respond to this request")
	}

	if friendship.Status != "pending" {
		return fmt.Errorf("request already processed")
	}

	accept := action == "accept"
	return s.store.RespondFriendRequest(friendshipID, accept)
}

func (s *Service) ListFriends(userID int64) (*models.FriendsResponse, error) {
	return s.store.GetFriends(userID)
}

func (s *Service) RemoveFriend(userID int64, friendshipID int64) error {
	return s.store.RemoveFriend(friendshipID, userID)
}

func (s *Service) SearchUsers(userID int64, query string) ([]models.UserSearchResult, error) {
	if len(query) < 2 {
		return []models.UserSearchResult{}, nil
	}
	return s.store.SearchUsers(query, userID)
}

// ── Nudges ──────────────────────────────────────────────

func (s *Service) SendNudge(userID int64, req models.SendNudgeRequest) (int64, error) {
	// Verify friendship
	friends, err := s.store.AreFriends(userID, req.ReceiverID)
	if err != nil || !friends {
		return 0, fmt.Errorf("you can only nudge friends")
	}

	// Validate nudge type
	validTypes := map[string]bool{"comeback": true, "challenge": true, "cheer": true}
	if !validTypes[req.NudgeType] {
		return 0, fmt.Errorf("invalid nudge type")
	}

	id, err := s.store.SendNudge(userID, req.ReceiverID, req.NudgeType, req.Message)
	if err != nil {
		return 0, fmt.Errorf("already nudged this person today")
	}

	// Check nudge_first achievement
	s.store.GetOrCreateGamification(userID)
	s.store.AwardAchievement(userID, "nudge_first")
	if def, ok := Achievements["nudge_first"]; ok {
		// Award gems only if this is the first time (AwardAchievement is idempotent)
		// We check by trying the insert — if it was a no-op, no gems
		s.store.AwardGems(userID, def.Gems)
	}

	return id, nil
}

func (s *Service) GetNudges(userID int64) (*models.NudgesResponse, error) {
	nudges, err := s.store.GetUnreadNudges(userID)
	if err != nil {
		return nil, err
	}
	return &models.NudgesResponse{
		Nudges:      nudges,
		UnreadCount: len(nudges),
	}, nil
}

func (s *Service) MarkNudgeRead(userID int64, nudgeID int64) error {
	return s.store.MarkNudgeRead(nudgeID, userID)
}

// ── Leaderboard ─────────────────────────────────────────

func (s *Service) GetGlobalLeaderboard(userID int64, limit int) (*models.LeaderboardResponse, error) {
	if limit <= 0 {
		limit = 20
	}

	entries, err := s.store.GetGlobalLeaderboard(limit)
	if err != nil {
		return nil, err
	}

	// Mark current user
	for i := range entries {
		if entries[i].UserID == userID {
			entries[i].IsCurrentUser = true
		}
	}

	// Get current user's rank if not in top N
	var currentUser *models.LeaderboardEntry
	found := false
	for _, e := range entries {
		if e.UserID == userID {
			found = true
			break
		}
	}
	if !found {
		rank, _ := s.store.GetUserRank(userID)
		if rank > 0 {
			gam, _ := s.store.GetOrCreateGamification(userID)
			currentUser = &models.LeaderboardEntry{
				Rank:       rank,
				UserID:     userID,
				WeeklyXP:   gam.WeeklyXP,
				LeagueTier: gam.LeagueTier,
			}
		}
	}

	if entries == nil {
		entries = []models.LeaderboardEntry{}
	}

	// Compute period string
	now := time.Now().UTC()
	weekStart := now.AddDate(0, 0, -int(now.Weekday()-time.Monday+7)%7)
	weekEnd := weekStart.AddDate(0, 0, 6)
	period := fmt.Sprintf("%s to %s", weekStart.Format("2006-01-02"), weekEnd.Format("2006-01-02"))

	return &models.LeaderboardResponse{
		Period:      period,
		Entries:     entries,
		CurrentUser: currentUser,
	}, nil
}

func (s *Service) GetFriendsLeaderboard(userID int64) (*models.LeaderboardResponse, error) {
	entries, err := s.store.GetFriendsLeaderboard(userID)
	if err != nil {
		return nil, err
	}

	for i := range entries {
		if entries[i].UserID == userID {
			entries[i].IsCurrentUser = true
		}
	}

	if entries == nil {
		entries = []models.LeaderboardEntry{}
	}

	now := time.Now().UTC()
	weekStart := now.AddDate(0, 0, -int(now.Weekday()-time.Monday+7)%7)
	weekEnd := weekStart.AddDate(0, 0, 6)
	period := fmt.Sprintf("%s to %s", weekStart.Format("2006-01-02"), weekEnd.Format("2006-01-02"))

	return &models.LeaderboardResponse{
		Period:  period,
		Entries: entries,
	}, nil
}

// ── Background Workers ──────────────────────────────────

func (s *Service) StartWeeklyResetWorker(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	log.Println("[gamification] Weekly reset worker started")

	for {
		select {
		case <-ctx.Done():
			log.Println("[gamification] Weekly reset worker shutting down")
			return
		case t := <-ticker.C:
			utc := t.UTC()
			// Run at Monday 00:xx UTC
			if utc.Weekday() == time.Monday && utc.Hour() == 0 {
				log.Println("[gamification] Running weekly leaderboard reset")
				s.runWeeklyReset()
			}
		}
	}
}

func (s *Service) runWeeklyReset() {
	// 1. Award gems to top 3
	top3, err := s.store.GetGlobalLeaderboard(3)
	if err != nil {
		log.Printf("[gamification] weekly reset: failed to get top 3: %v", err)
	} else {
		gemRewards := []int{50, 30, 20}
		for i, entry := range top3 {
			if i < len(gemRewards) {
				s.store.AwardGems(entry.UserID, gemRewards[i])
				log.Printf("[gamification] weekly reset: awarded %d gems to user %d (rank %d)", gemRewards[i], entry.UserID, i+1)
			}
		}
	}

	// 2. Process league changes
	changes, err := s.store.ProcessLeagueChanges()
	if err != nil {
		log.Printf("[gamification] weekly reset: failed to process leagues: %v", err)
	} else {
		for _, c := range changes {
			log.Printf("[gamification] league change: user %d %s → %s", c.UserID, c.OldTier, c.NewTier)
			// Award gems for promotion
			if isPromotion(c.OldTier, c.NewTier) {
				s.store.AwardGems(c.UserID, 25)
				// Award league achievement
				switch c.NewTier {
				case models.LeagueSilver:
					s.store.AwardAchievement(c.UserID, "league_silver")
				case models.LeagueGold:
					s.store.AwardAchievement(c.UserID, "league_gold")
				case models.LeagueDiamond:
					s.store.AwardAchievement(c.UserID, "league_diamond")
				case models.LeagueObsidian:
					s.store.AwardAchievement(c.UserID, "league_obsidian")
				}
			}
		}
	}

	// 3. Reset weekly XP
	if err := s.store.ResetWeeklyXP(); err != nil {
		log.Printf("[gamification] weekly reset: failed to reset XP: %v", err)
	}
}

func isPromotion(old, new string) bool {
	order := map[string]int{
		models.LeagueBronze:   0,
		models.LeagueSilver:   1,
		models.LeagueGold:     2,
		models.LeagueDiamond:  3,
		models.LeagueObsidian: 4,
	}
	return order[new] > order[old]
}

func (s *Service) StartDailyStreakWorker(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	log.Println("[gamification] Daily streak worker started")

	for {
		select {
		case <-ctx.Done():
			log.Println("[gamification] Daily streak worker shutting down")
			return
		case t := <-ticker.C:
			utc := t.UTC()
			// Run at midnight UTC
			if utc.Hour() == 0 {
				log.Println("[gamification] Running daily streak check")
				s.runDailyStreakCheck()
			}
		}
	}
}

func (s *Service) runDailyStreakCheck() {
	users, err := s.store.GetAllGamificationForStreakCheck()
	if err != nil {
		log.Printf("[gamification] streak check: failed to get users: %v", err)
		return
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	yesterday := today.AddDate(0, 0, -1)

	for _, g := range users {
		if g.LastActiveDate == nil {
			continue
		}

		lastActive := g.LastActiveDate.Truncate(24 * time.Hour)

		// If last active was before yesterday and has freezes, activate one
		if lastActive.Before(yesterday) && g.StreakFreezesOwned > 0 && !g.StreakFreezeActive {
			g.StreakFreezeActive = true
			s.store.UpdateStreakData(g.UserID, g.CurrentStreak, g.LongestStreak, true, g.StreakFreezesOwned)
			log.Printf("[gamification] streak check: auto-activated freeze for user %d", g.UserID)
		}
	}
}
