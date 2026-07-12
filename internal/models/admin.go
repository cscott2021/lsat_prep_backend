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
