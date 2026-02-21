package models

import "time"

// ── Core Gamification Structs ─────────────────────────────

type UserGamification struct {
	UserID                int64      `json:"user_id"`
	TotalXP               int64      `json:"total_xp"`
	WeeklyXP              int64      `json:"weekly_xp"`
	WeeklyXPResetAt       time.Time  `json:"weekly_xp_reset_at"`
	CurrentStreak         int        `json:"current_streak"`
	LongestStreak         int        `json:"longest_streak"`
	LastActiveDate        *time.Time `json:"last_active_date"`
	StreakFreezeActive    bool       `json:"streak_freeze_active"`
	StreakFreezesOwned    int        `json:"streak_freezes_owned"`
	Gems                  int        `json:"gems"`
	DailyGoalTarget       int        `json:"daily_goal_target"`
	DailyGoalProgress     int        `json:"daily_goal_progress"`
	DailyGoalDate         time.Time  `json:"daily_goal_date"`
	LeagueTier            string     `json:"league_tier"`
	QuestionsAnsweredTotal int       `json:"questions_answered_total"`
	QuestionsCorrectTotal  int       `json:"questions_correct_total"`
	DrillsCompletedTotal   int       `json:"drills_completed_total"`
	PerfectDrillsTotal     int       `json:"perfect_drills_total"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

type XPEvent struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	EventType string    `json:"event_type"`
	XPAmount  int       `json:"xp_amount"`
	Metadata  string    `json:"metadata,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Friendship struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"user_id"`
	FriendID   int64      `json:"friend_id"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
}

type Nudge struct {
	ID         int64     `json:"id"`
	SenderID   int64     `json:"sender_id"`
	ReceiverID int64     `json:"receiver_id"`
	Message    string    `json:"message,omitempty"`
	NudgeType  string    `json:"nudge_type"`
	Read       bool      `json:"read"`
	CreatedAt  time.Time `json:"created_at"`
}

type Achievement struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"user_id"`
	Achievement string    `json:"achievement"`
	EarnedAt    time.Time `json:"earned_at"`
}

// ── Request Types ─────────────────────────────────────────

type CompleteDrillRequest struct {
	QuestionIDs    []int64 `json:"question_ids"`
	CorrectIDs     []int64 `json:"correct_ids"`
	AvgTimeSeconds float64 `json:"avg_time_seconds"`
	ComboMax       int     `json:"combo_max"`
}

type SetDailyGoalRequest struct {
	Target int `json:"target"`
}

type FriendRequestReq struct {
	ToUserID int64 `json:"to_user_id"`
}

type FriendRespondReq struct {
	FriendshipID int64  `json:"friendship_id"`
	Action       string `json:"action"` // "accept" or "reject"
}

type SendNudgeRequest struct {
	ReceiverID int64  `json:"receiver_id"`
	NudgeType  string `json:"nudge_type"`
	Message    string `json:"message,omitempty"`
}

// ── Response Types ────────────────────────────────────────

type GamificationResponse struct {
	TotalXP               int64    `json:"total_xp"`
	WeeklyXP              int64    `json:"weekly_xp"`
	CurrentStreak         int      `json:"current_streak"`
	LongestStreak         int      `json:"longest_streak"`
	StreakFreezeActive    bool     `json:"streak_freeze_active"`
	StreakFreezesOwned    int      `json:"streak_freezes_owned"`
	Gems                  int      `json:"gems"`
	DailyGoalTarget       int      `json:"daily_goal_target"`
	DailyGoalProgress     int      `json:"daily_goal_progress"`
	LeagueTier            string   `json:"league_tier"`
	QuestionsAnsweredTotal int     `json:"questions_answered_total"`
	QuestionsCorrectTotal  int     `json:"questions_correct_total"`
	DrillsCompletedTotal   int     `json:"drills_completed_total"`
	PerfectDrillsTotal     int     `json:"perfect_drills_total"`
	Achievements          []string `json:"achievements"`
	UnreadNudges          int      `json:"unread_nudges"`
}

type DrillCompleteResponse struct {
	XPBreakdown          XPBreakdown    `json:"xp_breakdown"`
	GemsEarned           int            `json:"gems_earned"`
	Streak               StreakInfo     `json:"streak"`
	DailyGoal            DailyGoalInfo  `json:"daily_goal"`
	AchievementsUnlocked []string       `json:"achievements_unlocked"`
	LeagueTier           string         `json:"league_tier"`
}

type XPBreakdown struct {
	Questions        int     `json:"questions"`
	ComboBonuses     int     `json:"combo_bonuses"`
	TimeBonus        int     `json:"time_bonus"`
	DrillCompletion  int     `json:"drill_completion"`
	Subtotal         int     `json:"subtotal"`
	StreakMultiplier float64 `json:"streak_multiplier"`
	TotalXP          int     `json:"total_xp"`
}

type StreakInfo struct {
	Current    int     `json:"current"`
	Multiplier float64 `json:"multiplier"`
}

type DailyGoalInfo struct {
	Progress  int  `json:"progress"`
	Target    int  `json:"target"`
	Completed bool `json:"completed"`
}

type LeaderboardResponse struct {
	Period      string             `json:"period"`
	Entries     []LeaderboardEntry `json:"entries"`
	CurrentUser *LeaderboardEntry  `json:"current_user,omitempty"`
}

type LeaderboardEntry struct {
	Rank          int    `json:"rank"`
	UserID        int64  `json:"user_id"`
	DisplayName   string `json:"display_name"`
	Username      string `json:"username"`
	WeeklyXP      int64  `json:"weekly_xp"`
	LeagueTier    string `json:"league_tier"`
	CurrentStreak int    `json:"current_streak"`
	IsCurrentUser bool   `json:"is_current_user"`
}

type FriendsResponse struct {
	Friends         []FriendEntry        `json:"friends"`
	PendingReceived []PendingFriendEntry `json:"pending_received"`
	PendingSent     []PendingFriendEntry `json:"pending_sent"`
}

type FriendEntry struct {
	FriendshipID   int64  `json:"id"`
	UserID         int64  `json:"user_id"`
	DisplayName    string `json:"display_name"`
	Username       string `json:"username"`
	WeeklyXP       int64  `json:"weekly_xp"`
	CurrentStreak  int    `json:"current_streak"`
	LeagueTier     string `json:"league_tier"`
	LastActiveDate string `json:"last_active_date,omitempty"`
	IsOnlineToday  bool   `json:"is_online_today"`
}

type PendingFriendEntry struct {
	FriendshipID int64     `json:"friendship_id"`
	UserID       int64     `json:"user_id"`
	DisplayName  string    `json:"display_name"`
	Username     string    `json:"username"`
	CreatedAt    time.Time `json:"created_at"`
}

type FriendRequestResponse struct {
	FriendshipID int64  `json:"friendship_id"`
	Status       string `json:"status"`
	Friend       struct {
		ID          int64  `json:"id"`
		DisplayName string `json:"display_name"`
		Username    string `json:"username"`
	} `json:"friend"`
}

type UserSearchResult struct {
	UserID             int64  `json:"user_id"`
	DisplayName        string `json:"display_name"`
	Username           string `json:"username"`
	LeagueTier         string `json:"league_tier"`
	RelationshipStatus string `json:"relationship_status"`
}

type NudgesResponse struct {
	Nudges      []NudgeEntry `json:"nudges"`
	UnreadCount int          `json:"unread_count"`
}

type NudgeEntry struct {
	ID         int64     `json:"id"`
	SenderName string    `json:"sender_name"`
	SenderID   int64     `json:"sender_id"`
	NudgeType  string    `json:"nudge_type"`
	Message    string    `json:"message,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type StreakFreezeResponse struct {
	GemsRemaining    int `json:"gems_remaining"`
	StreakFreezesOwned int `json:"streak_freezes_owned"`
}

// ── League Tier Constants ─────────────────────────────────

const (
	LeagueBronze   = "bronze"
	LeagueSilver   = "silver"
	LeagueGold     = "gold"
	LeagueDiamond  = "diamond"
	LeagueObsidian = "obsidian"
)
