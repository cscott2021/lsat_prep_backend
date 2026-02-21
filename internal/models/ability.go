package models

import "time"

type AbilityScope string

const (
	ScopeOverall AbilityScope = "overall"
	ScopeSection AbilityScope = "section"
	ScopeSubtype AbilityScope = "subtype"
)

type UserAbilityScore struct {
	ID                int64        `json:"id"`
	UserID            int64        `json:"user_id"`
	Scope             AbilityScope `json:"scope"`
	ScopeValue        *string      `json:"scope_value,omitempty"`
	AbilityScore      int          `json:"ability_score"`
	QuestionsAnswered int          `json:"questions_answered"`
	QuestionsCorrect  int          `json:"questions_correct"`
	LastUpdated       time.Time    `json:"last_updated"`
}

type UserQuestionHistory struct {
	ID               int64     `json:"id"`
	UserID           int64     `json:"user_id"`
	QuestionID       int64     `json:"question_id"`
	AnsweredAt       time.Time `json:"answered_at"`
	Correct          bool      `json:"correct"`
	SelectedChoiceID *string   `json:"selected_choice_id,omitempty"`
	TimeSpentSeconds *float64  `json:"time_spent_seconds,omitempty"`
	AttemptCount     int       `json:"attempt_count"`
}

// ── API Request/Response Types ────────────────────────────

type AbilityResponse struct {
	OverallAbility   int            `json:"overall_ability"`
	SectionAbilities map[string]int `json:"section_abilities"`
	SubtypeAbilities map[string]int `json:"subtype_abilities"`
	DifficultySlider int            `json:"difficulty_slider"`
}

type QuickDrillRequest struct {
	Section          string `json:"section"`
	DifficultySlider int    `json:"difficulty_slider"`
	Count            int    `json:"count"`
}

type SubtypeDrillRequest struct {
	Section          string  `json:"section"`
	LRSubtype        *string `json:"lr_subtype,omitempty"`
	RCSubtype        *string `json:"rc_subtype,omitempty"`
	DifficultySlider int     `json:"difficulty_slider"`
	Count            int     `json:"count"`
}

type DifficultySliderRequest struct {
	SliderValue int `json:"slider_value"`
}

type AbilitySnapshot struct {
	OverallAbility int `json:"overall_ability"`
	SectionAbility int `json:"section_ability"`
	SubtypeAbility int `json:"subtype_ability"`
}

type GenerationQueueItem struct {
	ID                  int64      `json:"id"`
	Section             string     `json:"section"`
	LRSubtype           *string    `json:"lr_subtype,omitempty"`
	RCSubtype           *string    `json:"rc_subtype,omitempty"`
	DifficultyBucketMin int        `json:"difficulty_bucket_min"`
	DifficultyBucketMax int        `json:"difficulty_bucket_max"`
	TargetDifficulty    string     `json:"target_difficulty"`
	Status              string     `json:"status"`
	QuestionsNeeded     int        `json:"questions_needed"`
	SubjectArea         *string    `json:"subject_area,omitempty"`
	IsComparative       bool       `json:"is_comparative"`
	ErrorMessage        *string    `json:"error_message,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	CompletedAt         *time.Time `json:"completed_at,omitempty"`
}
