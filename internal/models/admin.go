package models

import "time"

// ── Admin: question spread / distribution ──────────────────

// SpreadItem is one bucket of a distribution (e.g. section "logical_reasoning"
// with 240 questions).
type SpreadItem struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// QuestionSpread describes the composition of the question bank so admins can
// see coverage across sections, subtypes, difficulty, and validation status.
type QuestionSpread struct {
	Total        int          `json:"total"`
	Flagged      int          `json:"flagged"`
	BySection    []SpreadItem `json:"by_section"`
	BySubtype    []SpreadItem `json:"by_subtype"`
	ByDifficulty []SpreadItem `json:"by_difficulty"`
	ByStatus     []SpreadItem `json:"by_status"`
}

// ── Admin: user progress ──────────────────────────────────

type UserProgressAggregate struct {
	TotalUsers      int     `json:"total_users"`
	ActiveUsers     int     `json:"active_users"` // answered a question in the last 7 days
	TotalAnswered   int     `json:"total_answered"`
	TotalCorrect    int     `json:"total_correct"`
	AvgAccuracy     float64 `json:"avg_accuracy"`
	QuestionsInBank int     `json:"questions_in_bank"`
}

type UserProgressRow struct {
	UserID        int64      `json:"user_id"`
	Name          string     `json:"name"`
	Email         string     `json:"email"`
	Answered      int        `json:"answered"`
	Correct       int        `json:"correct"`
	Accuracy      float64    `json:"accuracy"`
	TotalXP       int64      `json:"total_xp"`
	CurrentStreak int        `json:"current_streak"`
	LastActive    *time.Time `json:"last_active,omitempty"`
}

type UserProgressResponse struct {
	Aggregate UserProgressAggregate `json:"aggregate"`
	Users     []UserProgressRow     `json:"users"`
	Total     int                   `json:"total"`
	Page      int                   `json:"page"`
	PageSize  int                   `json:"page_size"`
}

// ── Admin: growth metrics ─────────────────────────────────

// SignupPoint is one calendar day's signup count in the signups-over-time
// series. Date is a UTC calendar day formatted YYYY-MM-DD.
type SignupPoint struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// SubscriberMetrics breaks the user base down by subscription status and gives
// an internal estimate of monthly recurring revenue.
type SubscriberMetrics struct {
	Active   int `json:"active"`
	Trialing int `json:"trialing"`
	PastDue  int `json:"past_due"`
	Canceled int `json:"canceled"`
	// Free = total_users - (active + trialing). Per this definition, users with
	// no subscription row plus any comp/past_due/canceled/incomplete rows all fall
	// into "free"; it is a coarse "not currently paying" bucket, not a status.
	Free       int `json:"free"`
	TotalUsers int `json:"total_users"`
	// MRRCents is the summed monthly-equivalent amount of ACTIVE subscriptions.
	// Internal estimate only (see GetAdminMetrics) — not Stripe-authoritative.
	MRRCents int64  `json:"mrr_cents"`
	Currency string `json:"currency"`
}

// EngagementMetrics reports distinct users answering a question over rolling
// 1 / 7 / 30 day windows.
type EngagementMetrics struct {
	DAU int `json:"dau"`
	WAU int `json:"wau"`
	MAU int `json:"mau"`
}

// RetentionMetrics reports early-cohort return rates as fractions in [0,1].
type RetentionMetrics struct {
	D1Pct float64 `json:"d1_pct"`
	D7Pct float64 `json:"d7_pct"`
}

// AdminMetrics is the growth dashboard payload for GET /admin/metrics.
type AdminMetrics struct {
	SignupsOverTime []SignupPoint     `json:"signups_over_time"`
	Subscribers     SubscriberMetrics `json:"subscribers"`
	Engagement      EngagementMetrics `json:"engagement"`
	Retention       RetentionMetrics  `json:"retention"`
}

// ── Generation status (offramp) ───────────────────────────

// GenerationStatus reports how many unseen questions are ready for a user's
// current drill context and whether more are being generated. The client's
// "preparing your questions" offramp polls this and starts the drill once
// enough are ready.
type GenerationStatus struct {
	Ready      int  `json:"ready"`
	Generating bool `json:"generating"`
	Target     int  `json:"target"`
}
