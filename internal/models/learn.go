package models

import "time"

// ── Guide ──────────────────────────────────────────────────

type LearnGuide struct {
	ID           int64           `json:"id"`
	Section      Section         `json:"section"`
	Subtype      string          `json:"subtype"`
	Overview     string          `json:"overview"`
	CommonStems  []string        `json:"common_stems"`
	Steps        []LearnStep     `json:"steps"`
	Tips         []string        `json:"tips"`
	Examples     []WorkedExample `json:"examples"`
	DisplayOrder int             `json:"display_order"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type LearnStep struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type WorkedExample struct {
	Stimulus     string          `json:"stimulus"`
	QuestionStem string          `json:"question_stem"`
	Choices      []ExampleChoice `json:"choices"`
	CorrectIndex int             `json:"correct_index"`
	Explanation  string          `json:"explanation"`
}

type ExampleChoice struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

// ── Progress ───────────────────────────────────────────────

type UserLearnProgress struct {
	UserID        int64     `json:"user_id"`
	GuideID       int64     `json:"guide_id"`
	FirstViewedAt time.Time `json:"first_viewed_at"`
	LastViewedAt  time.Time `json:"last_viewed_at"`
	ViewCount     int       `json:"view_count"`
}

// ── API Responses ──────────────────────────────────────────

type LearnGuideListItem struct {
	ID           int64   `json:"id"`
	Section      Section `json:"section"`
	Subtype      string  `json:"subtype"`
	Overview     string  `json:"overview"`
	DisplayOrder int     `json:"display_order"`
	Viewed       bool    `json:"viewed"`
	ViewCount    int     `json:"view_count"`
}

type LearnGuideListResponse struct {
	Guides []LearnGuideListItem `json:"guides"`
}

type LearnGuideDetailResponse struct {
	Guide     LearnGuide `json:"guide"`
	Viewed    bool       `json:"viewed"`
	ViewCount int        `json:"view_count"`
}
