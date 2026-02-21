package models

import "time"

type Section string

const (
	SectionLR Section = "logical_reasoning"
	SectionRC Section = "reading_comprehension"
)

type LRSubtype string

const (
	SubtypeStrengthen        LRSubtype = "strengthen"
	SubtypeWeaken            LRSubtype = "weaken"
	SubtypeAssumption        LRSubtype = "assumption"
	SubtypeFlaw              LRSubtype = "flaw"
	SubtypeMustBeTrue        LRSubtype = "must_be_true"
	SubtypeMostStrongly      LRSubtype = "most_strongly_supported"
	SubtypeMethodReasoning   LRSubtype = "method_of_reasoning"
	SubtypeParallelReasoning LRSubtype = "parallel_reasoning"
	SubtypeParallelFlaw      LRSubtype = "parallel_flaw"
	SubtypePrinciple         LRSubtype = "principle"
	SubtypeApplyPrinciple    LRSubtype = "apply_principle"
	SubtypeEvaluate          LRSubtype = "evaluate"
	SubtypeMainConclusion    LRSubtype = "main_conclusion"
	SubtypeRoleOfStatement   LRSubtype = "role_of_statement"
)

var ValidLRSubtypes = map[LRSubtype]bool{
	SubtypeStrengthen:        true,
	SubtypeWeaken:            true,
	SubtypeAssumption:        true,
	SubtypeFlaw:              true,
	SubtypeMustBeTrue:        true,
	SubtypeMostStrongly:      true,
	SubtypeMethodReasoning:   true,
	SubtypeParallelReasoning: true,
	SubtypeParallelFlaw:      true,
	SubtypePrinciple:         true,
	SubtypeApplyPrinciple:    true,
	SubtypeEvaluate:          true,
	SubtypeMainConclusion:    true,
	SubtypeRoleOfStatement:   true,
}

type RCSubtype string

const (
	RCSubtypeMainIdea         RCSubtype = "rc_main_idea"
	RCSubtypeDetail           RCSubtype = "rc_detail"
	RCSubtypeInference        RCSubtype = "rc_inference"
	RCSubtypeAttitude         RCSubtype = "rc_attitude"
	RCSubtypeFunction         RCSubtype = "rc_function"
	RCSubtypeOrganization     RCSubtype = "rc_organization"
	RCSubtypeStrengthenWeaken RCSubtype = "rc_strengthen_weaken"
	RCSubtypeAnalogy          RCSubtype = "rc_analogy"
	RCSubtypeRelationship     RCSubtype = "rc_relationship"
	RCSubtypeAgreement        RCSubtype = "rc_agreement"
)

var ValidRCSubtypes = map[RCSubtype]bool{
	RCSubtypeMainIdea:         true,
	RCSubtypeDetail:           true,
	RCSubtypeInference:        true,
	RCSubtypeAttitude:         true,
	RCSubtypeFunction:         true,
	RCSubtypeOrganization:     true,
	RCSubtypeStrengthenWeaken: true,
	RCSubtypeAnalogy:          true,
	RCSubtypeRelationship:     true,
	RCSubtypeAgreement:        true,
}

type Difficulty string

const (
	DifficultyEasy   Difficulty = "easy"
	DifficultyMedium Difficulty = "medium"
	DifficultyHard   Difficulty = "hard"
)

type BatchStatus string

const (
	BatchPending    BatchStatus = "pending"
	BatchGenerating BatchStatus = "generating"
	BatchValidating BatchStatus = "validating"
	BatchCompleted  BatchStatus = "completed"
	BatchFailed     BatchStatus = "failed"
)

type ValidationStatus string

const (
	ValidationUnvalidated ValidationStatus = "unvalidated"
	ValidationPassed      ValidationStatus = "passed"
	ValidationFlagged     ValidationStatus = "flagged"
	ValidationRejected    ValidationStatus = "rejected"
)

// ── Core Structs ───────────────────────────────────────

type QuestionBatch struct {
	ID                int64       `json:"id"`
	Section           Section     `json:"section"`
	LRSubtype         *LRSubtype  `json:"lr_subtype,omitempty"`
	Difficulty        Difficulty  `json:"difficulty"`
	Status            BatchStatus `json:"status"`
	QuestionCount     int         `json:"question_count"`
	QuestionsPassed   int         `json:"questions_passed"`
	QuestionsFlagged  int         `json:"questions_flagged"`
	QuestionsRejected int         `json:"questions_rejected"`
	ModelUsed         string      `json:"model_used,omitempty"`
	PromptTokens      int         `json:"prompt_tokens,omitempty"`
	OutputTokens      int         `json:"output_tokens,omitempty"`
	ValidationTokens  int         `json:"validation_tokens,omitempty"`
	GenerationTimeMs  int         `json:"generation_time_ms,omitempty"`
	TotalCostCents    int         `json:"total_cost_cents,omitempty"`
	ErrorMessage      *string     `json:"error_message,omitempty"`
	CreatedAt         time.Time   `json:"created_at"`
	CompletedAt       *time.Time  `json:"completed_at,omitempty"`
}

type Question struct {
	ID                  int64            `json:"id"`
	BatchID             int64            `json:"batch_id"`
	Section             Section          `json:"section"`
	LRSubtype           *LRSubtype       `json:"lr_subtype,omitempty"`
	RCSubtype           *RCSubtype       `json:"rc_subtype,omitempty"`
	Difficulty          Difficulty       `json:"difficulty"`
	DifficultyScore     int              `json:"difficulty_score"`
	Stimulus            string           `json:"stimulus"`
	QuestionStem        string           `json:"question_stem"`
	CorrectAnswerID     string           `json:"correct_answer_id"`
	Explanation         string           `json:"explanation"`
	PassageID           *int64           `json:"passage_id,omitempty"`
	Choices             []AnswerChoice   `json:"choices"`
	QualityScore        *float64         `json:"quality_score,omitempty"`
	ValidationStatus    ValidationStatus `json:"validation_status"`
	ValidationReasoning *string          `json:"validation_reasoning,omitempty"`
	AdversarialScore    *string          `json:"adversarial_score,omitempty"`
	Flagged             bool             `json:"flagged"`
	TimesServed         int              `json:"times_served"`
	TimesCorrect        int              `json:"times_correct"`
	CreatedAt           time.Time        `json:"created_at"`
}

type AnswerChoice struct {
	ID              int64  `json:"id"`
	QuestionID      int64  `json:"question_id"`
	ChoiceID        string `json:"choice_id"`
	ChoiceText      string `json:"choice_text"`
	Explanation     string `json:"explanation"`
	IsCorrect       bool   `json:"is_correct"`
	WrongAnswerType string `json:"wrong_answer_type,omitempty"`
}

type RCPassage struct {
	ID            int64     `json:"id"`
	BatchID       int64     `json:"batch_id"`
	Title         string    `json:"title"`
	SubjectArea   string    `json:"subject_area"`
	Content       string    `json:"content"`
	IsComparative bool      `json:"is_comparative"`
	PassageB      string    `json:"passage_b,omitempty"`
	WordCount     int       `json:"word_count"`
	CreatedAt     time.Time `json:"created_at"`
}

func (p *RCPassage) ToDrillPassage() DrillPassage {
	return DrillPassage{
		ID:            p.ID,
		Title:         p.Title,
		SubjectArea:   p.SubjectArea,
		Content:       p.Content,
		IsComparative: p.IsComparative,
		PassageB:      p.PassageB,
		WordCount:     p.WordCount,
	}
}

type ValidationLog struct {
	ID                  int64     `json:"id"`
	QuestionID          *int64    `json:"question_id,omitempty"`
	BatchID             *int64    `json:"batch_id,omitempty"`
	Stage               string    `json:"stage"`
	ModelUsed           string    `json:"model_used,omitempty"`
	GeneratedAnswer     string    `json:"generated_answer,omitempty"`
	ValidatorAnswer     string    `json:"validator_answer,omitempty"`
	Matches             *bool     `json:"matches,omitempty"`
	Confidence          string    `json:"confidence,omitempty"`
	Reasoning           string    `json:"reasoning,omitempty"`
	AdversarialDetails  string    `json:"adversarial_details,omitempty"`
	PromptTokens        int       `json:"prompt_tokens,omitempty"`
	OutputTokens        int       `json:"output_tokens,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

// ── Request Types ─────────────────────────────────────

type GenerateBatchRequest struct {
	Section       Section    `json:"section"`
	LRSubtype     *LRSubtype `json:"lr_subtype,omitempty"`
	RCSubtype     *RCSubtype `json:"rc_subtype,omitempty"`
	Difficulty    Difficulty `json:"difficulty"`
	Count         int        `json:"count"`
	SubjectArea   string     `json:"subject_area,omitempty"`
	IsComparative bool       `json:"is_comparative,omitempty"`
}

type SubmitAnswerRequest struct {
	SelectedChoiceID string   `json:"selected_choice_id"`
	TimeSpentSeconds *float64 `json:"time_spent_seconds,omitempty"`
}

type RCDrillRequest struct {
	DifficultySlider int     `json:"difficulty_slider"`
	RCSubtype        *string `json:"rc_subtype,omitempty"`
	Comparative      *bool   `json:"comparative,omitempty"`
	Count            int     `json:"count"`
}

type RCDrillResponse struct {
	Passage   DrillPassage    `json:"passage"`
	Questions []DrillQuestion `json:"questions"`
	Total     int             `json:"total"`
	Page      int             `json:"page"`
	PageSize  int             `json:"page_size"`
}

// ── Response Types ────────────────────────────────────

type GenerateBatchResponse struct {
	BatchID           int64       `json:"batch_id"`
	Status            BatchStatus `json:"status"`
	QuestionsPassed   int         `json:"questions_passed"`
	QuestionsFlagged  int         `json:"questions_flagged"`
	QuestionsRejected int         `json:"questions_rejected"`
	Message           string      `json:"message"`
}

type SubmitAnswerResponse struct {
	Correct         bool              `json:"correct"`
	CorrectAnswerID string            `json:"correct_answer_id"`
	Explanation     string            `json:"explanation"`
	Choices         []AnswerChoice    `json:"choices"`
	AbilityUpdated  *AbilitySnapshot  `json:"ability_updated,omitempty"`
	XPAwarded       int               `json:"xp_awarded"`
}

type QuestionListResponse struct {
	Questions []Question `json:"questions"`
	Total     int        `json:"total"`
	Page      int        `json:"page"`
	PageSize  int        `json:"page_size"`
}

// ── Drill Types (strip answers for serving) ───────────

type DrillQuestion struct {
	ID              int64         `json:"id"`
	Section         Section       `json:"section"`
	LRSubtype       *LRSubtype    `json:"lr_subtype,omitempty"`
	RCSubtype       *RCSubtype    `json:"rc_subtype,omitempty"`
	Difficulty      Difficulty    `json:"difficulty"`
	DifficultyScore int           `json:"difficulty_score"`
	Stimulus        string        `json:"stimulus"`
	QuestionStem    string        `json:"question_stem"`
	Choices         []DrillChoice `json:"choices"`
	Passage         *DrillPassage `json:"passage,omitempty"`
}

type DrillPassage struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	SubjectArea   string `json:"subject_area"`
	Content       string `json:"content"`
	IsComparative bool   `json:"is_comparative"`
	PassageB      string `json:"passage_b,omitempty"`
	WordCount     int    `json:"word_count"`
}

type DrillChoice struct {
	ChoiceID   string `json:"choice_id"`
	ChoiceText string `json:"choice_text"`
}

type DrillListResponse struct {
	Questions []DrillQuestion `json:"questions"`
	Total     int             `json:"total"`
	Page      int             `json:"page"`
	PageSize  int             `json:"page_size"`
}

// ── Admin Types ───────────────────────────────────────

type QualityStats struct {
	TotalGenerated   int                `json:"total_generated"`
	TotalPassed      int                `json:"total_passed"`
	TotalFlagged     int                `json:"total_flagged"`
	TotalRejected    int                `json:"total_rejected"`
	PassRate         float64            `json:"pass_rate"`
	QualityDistribution map[string]int  `json:"quality_score_distribution"`
}

type GenerationStats struct {
	Cost   CostStats   `json:"cost"`
	Batches BatchStats `json:"batches"`
	Tokens TokenStats  `json:"tokens"`
}

type CostStats struct {
	TodayCents     int `json:"today_cents"`
	ThisWeekCents  int `json:"this_week_cents"`
	ThisMonthCents int `json:"this_month_cents"`
	DailyLimitCents int `json:"daily_limit_cents"`
}

type BatchStats struct {
	Today     int `json:"today"`
	ThisWeek  int `json:"this_week"`
	ThisMonth int `json:"this_month"`
}

type TokenStats struct {
	GenerationTotal  int `json:"generation_total"`
	ValidationTotal  int `json:"validation_total"`
}

type RecalibrationCandidate struct {
	QuestionID          int64   `json:"question_id"`
	LabeledDifficulty   string  `json:"labeled_difficulty"`
	ActualAccuracy       float64 `json:"actual_accuracy"`
	SuggestedDifficulty  string  `json:"suggested_difficulty"`
	TimesServed          int     `json:"times_served"`
	TimesCorrect         int     `json:"times_correct"`
}

type RecalibrationReport struct {
	TotalEvaluated int                      `json:"total_evaluated"`
	Recalibrated   int                      `json:"recalibrated"`
	Details        []RecalibrationCandidate `json:"details"`
}

// ── Export/Import Types ──────────────────────────────────

type ExportEnvelope struct {
	Version    int              `json:"version"`
	ExportedAt time.Time       `json:"exported_at"`
	Questions  []ExportQuestion `json:"questions"`
}

type ExportQuestion struct {
	Section          Section          `json:"section"`
	LRSubtype        *LRSubtype       `json:"lr_subtype"`
	RCSubtype        *RCSubtype       `json:"rc_subtype"`
	Difficulty       Difficulty       `json:"difficulty"`
	DifficultyScore  int              `json:"difficulty_score"`
	Stimulus         string           `json:"stimulus"`
	QuestionStem     string           `json:"question_stem"`
	CorrectAnswerID  string           `json:"correct_answer_id"`
	Explanation      string           `json:"explanation"`
	QualityScore     *float64         `json:"quality_score"`
	ValidationStatus ValidationStatus `json:"validation_status"`
	Passage          *ExportPassage   `json:"passage"`
	Choices          []ExportChoice   `json:"choices"`
}

type ExportPassage struct {
	Title         string `json:"title"`
	SubjectArea   string `json:"subject_area"`
	Content       string `json:"content"`
	IsComparative bool   `json:"is_comparative"`
	PassageB      string `json:"passage_b,omitempty"`
}

type ExportChoice struct {
	ChoiceID        string `json:"choice_id"`
	ChoiceText      string `json:"choice_text"`
	Explanation     string `json:"explanation"`
	IsCorrect       bool   `json:"is_correct"`
	WrongAnswerType string `json:"wrong_answer_type,omitempty"`
}

type ImportResult struct {
	TotalInPayload int `json:"total_in_payload"`
	Imported       int `json:"imported"`
	Skipped        int `json:"skipped"`
	BatchesCreated int `json:"batches_created"`
}
