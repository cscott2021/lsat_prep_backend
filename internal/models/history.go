package models

import "time"

// ── History Types ────────────────────────────────────────

type HistoryQuestion struct {
	QuestionID      int64          `json:"question_id"`
	Section         Section        `json:"section"`
	LRSubtype       *LRSubtype     `json:"lr_subtype,omitempty"`
	RCSubtype       *RCSubtype     `json:"rc_subtype,omitempty"`
	Difficulty      Difficulty     `json:"difficulty"`
	DifficultyScore int            `json:"difficulty_score"`
	Stimulus        string         `json:"stimulus"`
	QuestionStem    string         `json:"question_stem"`
	CorrectAnswerID string         `json:"correct_answer_id"`
	Explanation     string         `json:"explanation"`
	Choices         []AnswerChoice `json:"choices"`
	Passage         *DrillPassage  `json:"passage,omitempty"`

	// User's history for this question
	SelectedChoiceID *string   `json:"selected_choice_id,omitempty"`
	Correct          bool      `json:"correct"`
	TimeSpentSeconds *float64  `json:"time_spent_seconds,omitempty"`
	AttemptCount     int       `json:"attempt_count"`
	AnsweredAt       time.Time `json:"answered_at"`
}

// ── Request Types ────────────────────────────────────────

type HistoryListRequest struct {
	Section   *string `json:"section"`
	Subtype   *string `json:"subtype"`
	Correct   *bool   `json:"correct"`
	DateFrom  *string `json:"date_from"`
	DateTo    *string `json:"date_to"`
	SortBy    string  `json:"sort_by"`
	SortOrder string  `json:"sort_order"`
	Page      int     `json:"page"`
	PageSize  int     `json:"page_size"`
}

type BookmarkRequest struct {
	Note string `json:"note,omitempty"`
}

type DrillReviewRequest struct {
	QuestionIDs []int64 `json:"question_ids"`
}

// ── Response Types ────────────────────────────────────────

type HistoryListResponse struct {
	Questions []HistoryQuestion `json:"questions"`
	Total     int               `json:"total"`
	Page      int               `json:"page"`
	PageSize  int               `json:"page_size"`
}

type HistoryStatsResponse struct {
	TotalAnswered   int                    `json:"total_answered"`
	TotalCorrect    int                    `json:"total_correct"`
	OverallAccuracy float64                `json:"overall_accuracy"`
	AvgTimeSeconds  float64                `json:"avg_time_seconds"`
	SectionStats    map[string]SectionStat `json:"section_stats"`
	SubtypeStats    map[string]SubtypeStat `json:"subtype_stats"`
	DifficultyStats DifficultyBreakdown    `json:"difficulty_stats"`
	RecentTrend     []DailyAccuracy        `json:"recent_trend"`
}

type SectionStat struct {
	Answered int     `json:"answered"`
	Correct  int     `json:"correct"`
	Accuracy float64 `json:"accuracy"`
	AvgTime  float64 `json:"avg_time_seconds"`
}

type SubtypeStat struct {
	Section  string  `json:"section"`
	Answered int     `json:"answered"`
	Correct  int     `json:"correct"`
	Accuracy float64 `json:"accuracy"`
	AvgTime  float64 `json:"avg_time_seconds"`
}

type DifficultyBreakdown struct {
	Easy   AccuracyStat `json:"easy"`
	Medium AccuracyStat `json:"medium"`
	Hard   AccuracyStat `json:"hard"`
}

type AccuracyStat struct {
	Answered int     `json:"answered"`
	Correct  int     `json:"correct"`
	Accuracy float64 `json:"accuracy"`
}

type DailyAccuracy struct {
	Date     string  `json:"date"`
	Answered int     `json:"answered"`
	Correct  int     `json:"correct"`
	Accuracy float64 `json:"accuracy"`
}

type BookmarkEntry struct {
	ID         int64     `json:"id"`
	QuestionID int64     `json:"question_id"`
	Note       *string   `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`

	Question *HistoryQuestion `json:"question,omitempty"`
}

type BookmarkListResponse struct {
	Bookmarks []BookmarkEntry `json:"bookmarks"`
	Total     int             `json:"total"`
	Page      int             `json:"page"`
	PageSize  int             `json:"page_size"`
}
