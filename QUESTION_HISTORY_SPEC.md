# Backend Question History & Mistake Review Spec

> Adds endpoints to retrieve, filter, and review question history. Extends the existing `user_question_history` table with richer data (selected answer, time spent, attempt count) and adds a bookmarking system for targeted review.

---

## 1. Overview

### What Exists
- `user_question_history` table: records `(user_id, question_id, answered_at, correct)` with `UNIQUE(user_id, question_id)` — stores only the latest attempt via upsert
- `RecordAnswer()` store method: called from `SubmitAnswer()` on every answer submission
- `times_served` / `times_correct` on `questions` table: aggregate per-question stats
- `SubmitAnswerResponse`: returns correctness, explanation, choices, ability snapshot, XP — but does NOT include the user's selected choice for future reference
- No endpoint exists to **retrieve** history — only write path exists

### What This Spec Adds
- **Richer history records**: selected answer ID, time spent per question, attempt count
- **History retrieval endpoints**: paginated, filterable by section/subtype/correctness/date
- **Mistake review endpoint**: returns incorrect answers with full question data for re-study
- **Bookmark system**: users can flag questions for later review
- **Aggregate stats endpoint**: accuracy breakdowns by section, subtype, difficulty, and time period
- **Re-attempt tracking**: when a user retries a previously-answered question, the attempt count increments

---

## 2. Database Schema Changes

### 2a. Alter `user_question_history`

```sql
-- Add selected answer tracking
ALTER TABLE user_question_history
    ADD COLUMN IF NOT EXISTS selected_choice_id VARCHAR(5);

-- Add time spent per question (seconds, from frontend timer)
ALTER TABLE user_question_history
    ADD COLUMN IF NOT EXISTS time_spent_seconds REAL;

-- Track how many times the user has attempted this question
ALTER TABLE user_question_history
    ADD COLUMN IF NOT EXISTS attempt_count INT NOT NULL DEFAULT 1;

-- Index for filtering by correctness (mistake review)
CREATE INDEX IF NOT EXISTS idx_history_user_correct
    ON user_question_history(user_id, correct);

-- Index for date-range queries
CREATE INDEX IF NOT EXISTS idx_history_user_date
    ON user_question_history(user_id, answered_at DESC);
```

> The `UNIQUE(user_id, question_id)` constraint stays. Each question has one row per user — the upsert pattern updates `correct`, `selected_choice_id`, `time_spent_seconds`, `answered_at`, and increments `attempt_count` on re-attempt.

### 2b. New table: `user_bookmarks`

```sql
CREATE TABLE IF NOT EXISTS user_bookmarks (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    question_id BIGINT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    note        TEXT,                                    -- optional user note ("review flaw pattern")
    created_at  TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(user_id, question_id)
);

CREATE INDEX IF NOT EXISTS idx_bookmarks_user ON user_bookmarks(user_id, created_at DESC);
```

---

## 3. Model Changes

### 3a. Update existing model: `UserQuestionHistory` in `internal/models/ability.go`

The existing `UserQuestionHistory` struct must be extended with the new columns:

```go
type UserQuestionHistory struct {
    ID               int64      `json:"id"`
    UserID           int64      `json:"user_id"`
    QuestionID       int64      `json:"question_id"`
    AnsweredAt       time.Time  `json:"answered_at"`
    Correct          bool       `json:"correct"`
    SelectedChoiceID *string    `json:"selected_choice_id,omitempty"`
    TimeSpentSeconds *float64   `json:"time_spent_seconds,omitempty"`
    AttemptCount     int        `json:"attempt_count"`
}
```

### 3b. New file: `internal/models/history.go`

```go
package models

import "time"

// ── History Types ────────────────────────────────────────

type HistoryQuestion struct {
    // Full question data for review screens
    QuestionID       int64          `json:"question_id"`
    Section          Section        `json:"section"`
    LRSubtype        *LRSubtype     `json:"lr_subtype,omitempty"`
    RCSubtype        *RCSubtype     `json:"rc_subtype,omitempty"`
    Difficulty       Difficulty     `json:"difficulty"`
    DifficultyScore  int            `json:"difficulty_score"`
    Stimulus         string         `json:"stimulus"`
    QuestionStem     string         `json:"question_stem"`
    CorrectAnswerID  string         `json:"correct_answer_id"`
    Explanation      string         `json:"explanation"`
    Choices          []AnswerChoice `json:"choices"`
    Passage          *DrillPassage  `json:"passage,omitempty"`

    // User's history for this question
    SelectedChoiceID *string    `json:"selected_choice_id,omitempty"`
    Correct          bool       `json:"correct"`
    TimeSpentSeconds *float64   `json:"time_spent_seconds,omitempty"`
    AttemptCount     int        `json:"attempt_count"`
    AnsweredAt       time.Time  `json:"answered_at"`
}

// DrillPassage — reuse from question.go (defined in RC_SPEC.md).
// Do NOT redefine here. Import via: models.DrillPassage

// ── Request Types ────────────────────────────────────────

type HistoryListRequest struct {
    Section   *string `json:"section"`      // "logical_reasoning", "reading_comprehension", or nil for all
    Subtype   *string `json:"subtype"`      // lr or rc subtype string, or nil for all
    Correct   *bool   `json:"correct"`      // true = only correct, false = only incorrect, nil = all
    DateFrom  *string `json:"date_from"`    // ISO 8601 date, e.g. "2026-01-01"
    DateTo    *string `json:"date_to"`      // ISO 8601 date
    SortBy    string  `json:"sort_by"`      // "answered_at" (default), "difficulty_score", "time_spent"
    SortOrder string  `json:"sort_order"`   // "desc" (default), "asc"
    Page      int     `json:"page"`         // 1-indexed, default 1
    PageSize  int     `json:"page_size"`    // default 20, max 50
}

type BookmarkRequest struct {
    Note string `json:"note,omitempty"`
}

// ── Response Types ────────────────────────────────────────

type HistoryListResponse struct {
    Questions []HistoryQuestion `json:"questions"`
    Total     int               `json:"total"`
    Page      int               `json:"page"`
    PageSize  int               `json:"page_size"`
}

type HistoryStatsResponse struct {
    TotalAnswered    int                    `json:"total_answered"`
    TotalCorrect     int                    `json:"total_correct"`
    OverallAccuracy  float64                `json:"overall_accuracy"`
    AvgTimeSeconds   float64                `json:"avg_time_seconds"`
    SectionStats     map[string]SectionStat `json:"section_stats"`
    SubtypeStats     map[string]SubtypeStat `json:"subtype_stats"`
    DifficultyStats  DifficultyBreakdown    `json:"difficulty_stats"`
    RecentTrend      []DailyAccuracy        `json:"recent_trend"`      // last 30 days
}

type SectionStat struct {
    Answered   int     `json:"answered"`
    Correct    int     `json:"correct"`
    Accuracy   float64 `json:"accuracy"`
    AvgTime    float64 `json:"avg_time_seconds"`
}

type SubtypeStat struct {
    Section    string  `json:"section"`
    Answered   int     `json:"answered"`
    Correct    int     `json:"correct"`
    Accuracy   float64 `json:"accuracy"`
    AvgTime    float64 `json:"avg_time_seconds"`
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
    Date     string  `json:"date"`      // "2026-02-21"
    Answered int     `json:"answered"`
    Correct  int     `json:"correct"`
    Accuracy float64 `json:"accuracy"`
}

type BookmarkEntry struct {
    ID         int64     `json:"id"`
    QuestionID int64     `json:"question_id"`
    Note       *string   `json:"note,omitempty"`
    CreatedAt  time.Time `json:"created_at"`

    // Joined question data
    Question *HistoryQuestion `json:"question,omitempty"`
}

type BookmarkListResponse struct {
    Bookmarks []BookmarkEntry `json:"bookmarks"`
    Total     int             `json:"total"`
    Page      int             `json:"page"`
    PageSize  int             `json:"page_size"`
}
```

> Note: `DrillPassage` is defined in RC_SPEC.md. If the RC spec is implemented first, import from there. If not, define here and deduplicate later.

---

## 4. Updated Store Method: `RecordAnswer`

The existing upsert must be extended to store the new columns.

```go
// RecordAnswer upserts a user's answer for a question.
// On conflict (re-attempt), updates correctness, selection, time, and increments attempt_count.
func (s *Store) RecordAnswer(userID, questionID int64, correct bool, selectedChoiceID *string, timeSpentSeconds *float64) error {
    _, err := s.db.Exec(
        `INSERT INTO user_question_history
            (user_id, question_id, correct, selected_choice_id, time_spent_seconds, attempt_count)
         VALUES ($1, $2, $3, $4, $5, 1)
         ON CONFLICT (user_id, question_id)
         DO UPDATE SET
            correct = $3,
            selected_choice_id = $4,
            time_spent_seconds = $5,
            attempt_count = user_question_history.attempt_count + 1,
            answered_at = NOW()`,
        userID, questionID, correct, selectedChoiceID, timeSpentSeconds,
    )
    return err
}
```

### Update `SubmitAnswerRequest`

```go
type SubmitAnswerRequest struct {
    SelectedChoiceID string   `json:"selected_choice_id"`
    TimeSpentSeconds *float64 `json:"time_spent_seconds,omitempty"`
}
```

### Update `SubmitAnswer` service method

Pass the new fields through to `RecordAnswer`:

```go
func (s *Service) SubmitAnswer(userID int64, questionID int64, selectedChoiceID string, timeSpentSeconds *float64) (*models.SubmitAnswerResponse, error) {
    // ... existing logic ...

    // Record user history (now with selected answer and time)
    if err := s.store.RecordAnswer(userID, questionID, isCorrect, &selectedChoiceID, timeSpentSeconds); err != nil {
        log.Printf("WARN: failed to record answer history: %v", err)
    }

    // ... rest of existing logic unchanged ...
}
```

---

## 5. New Store Methods

### 5a. `GetUserHistory` — paginated, filtered history with joined question data

```go
func (s *Store) GetUserHistory(userID int64, req models.HistoryListRequest) ([]models.HistoryQuestion, int, error) {
    // Build WHERE clause from filters
    args := []interface{}{userID}
    paramIdx := 2
    var filters []string

    if req.Section != nil {
        filters = append(filters, fmt.Sprintf("q.section = $%d", paramIdx))
        args = append(args, *req.Section)
        paramIdx++
    }
    if req.Subtype != nil {
        // Detect LR vs RC subtype by prefix
        if strings.HasPrefix(*req.Subtype, "rc_") {
            filters = append(filters, fmt.Sprintf("q.rc_subtype = $%d", paramIdx))
        } else {
            filters = append(filters, fmt.Sprintf("q.lr_subtype = $%d", paramIdx))
        }
        args = append(args, *req.Subtype)
        paramIdx++
    }
    if req.Correct != nil {
        filters = append(filters, fmt.Sprintf("h.correct = $%d", paramIdx))
        args = append(args, *req.Correct)
        paramIdx++
    }
    if req.DateFrom != nil {
        filters = append(filters, fmt.Sprintf("h.answered_at >= $%d", paramIdx))
        args = append(args, *req.DateFrom)
        paramIdx++
    }
    if req.DateTo != nil {
        filters = append(filters, fmt.Sprintf("h.answered_at < $%d::date + 1", paramIdx))
        args = append(args, *req.DateTo)
        paramIdx++
    }

    filterSQL := ""
    if len(filters) > 0 {
        filterSQL = "AND " + strings.Join(filters, " AND ")
    }

    // Allowed sort columns (whitelist to prevent SQL injection)
    sortCol := "h.answered_at"
    switch req.SortBy {
    case "difficulty_score":
        sortCol = "q.difficulty_score"
    case "time_spent":
        sortCol = "h.time_spent_seconds"
    }
    sortDir := "DESC"
    if req.SortOrder == "asc" {
        sortDir = "ASC"
    }

    // Count total
    var total int
    countQuery := fmt.Sprintf(`
        SELECT COUNT(*)
        FROM user_question_history h
        JOIN questions q ON q.id = h.question_id
        WHERE h.user_id = $1 %s`, filterSQL)
    countArgs := args // same args
    err := s.db.QueryRow(countQuery, countArgs...).Scan(&total)
    if err != nil {
        return nil, 0, fmt.Errorf("count history: %w", err)
    }

    // Fetch page
    offset := (req.Page - 1) * req.PageSize
    args = append(args, req.PageSize, offset)

    dataQuery := fmt.Sprintf(`
        SELECT q.id, q.section, q.lr_subtype, q.rc_subtype, q.difficulty, q.difficulty_score,
               q.stimulus, q.question_stem, q.correct_answer_id, q.explanation, q.passage_id,
               h.correct, h.selected_choice_id, h.time_spent_seconds, h.attempt_count, h.answered_at
        FROM user_question_history h
        JOIN questions q ON q.id = h.question_id
        WHERE h.user_id = $1 %s
        ORDER BY %s %s
        LIMIT $%d OFFSET $%d`,
        filterSQL, sortCol, sortDir, paramIdx, paramIdx+1)

    rows, err := s.db.Query(dataQuery, args...)
    // ... scan into []HistoryQuestion, fetch choices + passage for each ...
}
```

> The full scan logic follows the same pattern as `scanQuestionsWithChoices` — fetch question rows, then batch-load choices and RC passages. For efficiency, collect all question IDs from the page, then run a single `WHERE question_id IN (...)` for choices and a single passage query for any RC questions.

### 5b. `GetUserMistakes` — shortcut for `correct = false`, most recent first

```go
func (s *Store) GetUserMistakes(userID int64, page, pageSize int) ([]models.HistoryQuestion, int, error) {
    req := models.HistoryListRequest{
        Correct:   boolPtr(false),
        SortBy:    "answered_at",
        SortOrder: "desc",
        Page:      page,
        PageSize:  pageSize,
    }
    return s.GetUserHistory(userID, req)
}
```

### 5c. `GetUserHistoryStats` — aggregate accuracy statistics

```go
func (s *Store) GetUserHistoryStats(userID int64) (*models.HistoryStatsResponse, error) {
    stats := &models.HistoryStatsResponse{
        SectionStats:  make(map[string]models.SectionStat),
        SubtypeStats:  make(map[string]models.SubtypeStat),
    }

    // Overall totals + avg time
    err := s.db.QueryRow(`
        SELECT COUNT(*),
               COUNT(*) FILTER (WHERE h.correct = true),
               COALESCE(AVG(h.time_spent_seconds), 0)
        FROM user_question_history h
        WHERE h.user_id = $1`, userID,
    ).Scan(&stats.TotalAnswered, &stats.TotalCorrect, &stats.AvgTimeSeconds)
    if err != nil {
        return nil, fmt.Errorf("overall stats: %w", err)
    }
    if stats.TotalAnswered > 0 {
        stats.OverallAccuracy = float64(stats.TotalCorrect) / float64(stats.TotalAnswered)
    }

    // Per-section stats
    sectionRows, err := s.db.Query(`
        SELECT q.section,
               COUNT(*),
               COUNT(*) FILTER (WHERE h.correct = true),
               COALESCE(AVG(h.time_spent_seconds), 0)
        FROM user_question_history h
        JOIN questions q ON q.id = h.question_id
        WHERE h.user_id = $1
        GROUP BY q.section`, userID)
    // ... scan into stats.SectionStats ...

    // Per-subtype stats
    subtypeRows, err := s.db.Query(`
        SELECT q.section,
               COALESCE(q.lr_subtype, q.rc_subtype) as subtype,
               COUNT(*),
               COUNT(*) FILTER (WHERE h.correct = true),
               COALESCE(AVG(h.time_spent_seconds), 0)
        FROM user_question_history h
        JOIN questions q ON q.id = h.question_id
        WHERE h.user_id = $1
        GROUP BY q.section, COALESCE(q.lr_subtype, q.rc_subtype)`, userID)
    // ... scan into stats.SubtypeStats ...

    // Per-difficulty stats
    diffRows, err := s.db.Query(`
        SELECT q.difficulty,
               COUNT(*),
               COUNT(*) FILTER (WHERE h.correct = true)
        FROM user_question_history h
        JOIN questions q ON q.id = h.question_id
        WHERE h.user_id = $1
        GROUP BY q.difficulty`, userID)
    // ... scan into stats.DifficultyStats ...

    // Recent trend (last 30 days)
    trendRows, err := s.db.Query(`
        SELECT h.answered_at::date as day,
               COUNT(*),
               COUNT(*) FILTER (WHERE h.correct = true)
        FROM user_question_history h
        WHERE h.user_id = $1
          AND h.answered_at >= CURRENT_DATE - INTERVAL '30 days'
        GROUP BY day
        ORDER BY day ASC`, userID)
    // ... scan into stats.RecentTrend ...

    return stats, nil
}
```

### 5d. Bookmark CRUD

```go
func (s *Store) CreateBookmark(userID, questionID int64, note *string) error {
    _, err := s.db.Exec(
        `INSERT INTO user_bookmarks (user_id, question_id, note)
         VALUES ($1, $2, $3)
         ON CONFLICT (user_id, question_id)
         DO UPDATE SET note = COALESCE($3, user_bookmarks.note)`,
        userID, questionID, note,
    )
    return err
}

func (s *Store) DeleteBookmark(userID, questionID int64) error {
    result, err := s.db.Exec(
        `DELETE FROM user_bookmarks WHERE user_id = $1 AND question_id = $2`,
        userID, questionID,
    )
    if err != nil {
        return err
    }
    rows, _ := result.RowsAffected()
    if rows == 0 {
        return fmt.Errorf("bookmark not found")
    }
    return nil
}

func (s *Store) GetBookmarks(userID int64, page, pageSize int) ([]models.BookmarkEntry, int, error) {
    // Count
    var total int
    s.db.QueryRow(`SELECT COUNT(*) FROM user_bookmarks WHERE user_id = $1`, userID).Scan(&total)

    // Fetch bookmarks with joined question + history data
    offset := (page - 1) * pageSize
    rows, err := s.db.Query(`
        SELECT b.id, b.question_id, b.note, b.created_at,
               q.section, q.lr_subtype, q.rc_subtype, q.difficulty, q.difficulty_score,
               q.stimulus, q.question_stem, q.correct_answer_id, q.explanation, q.passage_id,
               h.correct, h.selected_choice_id, h.time_spent_seconds, h.attempt_count, h.answered_at
        FROM user_bookmarks b
        JOIN questions q ON q.id = b.question_id
        LEFT JOIN user_question_history h ON h.question_id = b.question_id AND h.user_id = $1
        WHERE b.user_id = $1
        ORDER BY b.created_at DESC
        LIMIT $2 OFFSET $3`, userID, pageSize, offset)
    // ... scan into []BookmarkEntry with nested HistoryQuestion ...

    return bookmarks, total, nil
}

func (s *Store) IsBookmarked(userID, questionID int64) (bool, error) {
    var exists bool
    err := s.db.QueryRow(
        `SELECT EXISTS(SELECT 1 FROM user_bookmarks WHERE user_id = $1 AND question_id = $2)`,
        userID, questionID,
    ).Scan(&exists)
    return exists, err
}
```

### 5e. `GetDrillReview` — questions from a specific set of IDs (for post-drill review)

```go
// GetDrillReview returns full question data + user history for a list of question IDs.
// Used by the frontend after a drill completes to show the "Review Mistakes" screen.
func (s *Store) GetDrillReview(userID int64, questionIDs []int64) ([]models.HistoryQuestion, error) {
    if len(questionIDs) == 0 {
        return nil, nil
    }

    // Build IN clause
    placeholders := make([]string, len(questionIDs))
    args := []interface{}{userID}
    for i, id := range questionIDs {
        placeholders[i] = fmt.Sprintf("$%d", i+2)
        args = append(args, id)
    }

    query := fmt.Sprintf(`
        SELECT q.id, q.section, q.lr_subtype, q.rc_subtype, q.difficulty, q.difficulty_score,
               q.stimulus, q.question_stem, q.correct_answer_id, q.explanation, q.passage_id,
               h.correct, h.selected_choice_id, h.time_spent_seconds, h.attempt_count, h.answered_at
        FROM questions q
        LEFT JOIN user_question_history h ON h.question_id = q.id AND h.user_id = $1
        WHERE q.id IN (%s)
        ORDER BY q.id`, strings.Join(placeholders, ","))

    rows, err := s.db.Query(query, args...)
    // ... scan + fetch choices + passages ...
}
```

---

## 6. New Service Methods

```go
// ── History Retrieval ───────────────────────────────────

func (s *Service) GetUserHistory(userID int64, req models.HistoryListRequest) (*models.HistoryListResponse, error) {
    // Validate and default request params
    if req.Page <= 0 {
        req.Page = 1
    }
    if req.PageSize <= 0 {
        req.PageSize = 20
    }
    if req.PageSize > 50 {
        req.PageSize = 50
    }
    if req.SortBy == "" {
        req.SortBy = "answered_at"
    }
    if req.SortOrder == "" {
        req.SortOrder = "desc"
    }

    questions, total, err := s.store.GetUserHistory(userID, req)
    if err != nil {
        return nil, err
    }
    return &models.HistoryListResponse{
        Questions: questions,
        Total:     total,
        Page:      req.Page,
        PageSize:  req.PageSize,
    }, nil
}

func (s *Service) GetUserMistakes(userID int64, page, pageSize int) (*models.HistoryListResponse, error) {
    if page <= 0 { page = 1 }
    if pageSize <= 0 { pageSize = 20 }
    if pageSize > 50 { pageSize = 50 }

    questions, total, err := s.store.GetUserMistakes(userID, page, pageSize)
    if err != nil {
        return nil, err
    }
    return &models.HistoryListResponse{
        Questions: questions,
        Total:     total,
        Page:      page,
        PageSize:  pageSize,
    }, nil
}

func (s *Service) GetUserHistoryStats(userID int64) (*models.HistoryStatsResponse, error) {
    return s.store.GetUserHistoryStats(userID)
}

func (s *Service) GetDrillReview(userID int64, questionIDs []int64) ([]models.HistoryQuestion, error) {
    return s.store.GetDrillReview(userID, questionIDs)
}

// ── Bookmarks ───────────────────────────────────────────

func (s *Service) CreateBookmark(userID, questionID int64, note *string) error {
    return s.store.CreateBookmark(userID, questionID, note)
}

func (s *Service) DeleteBookmark(userID, questionID int64) error {
    return s.store.DeleteBookmark(userID, questionID)
}

func (s *Service) GetBookmarks(userID int64, page, pageSize int) (*models.BookmarkListResponse, error) {
    if page <= 0 { page = 1 }
    if pageSize <= 0 { pageSize = 20 }
    if pageSize > 50 { pageSize = 50 }

    bookmarks, total, err := s.store.GetBookmarks(userID, page, pageSize)
    if err != nil {
        return nil, err
    }
    return &models.BookmarkListResponse{
        Bookmarks: bookmarks,
        Total:     total,
        Page:      page,
        PageSize:  pageSize,
    }, nil
}
```

---

## 7. API Endpoints

All endpoints require JWT authentication. `userID` is extracted from the token.

### 7a. `GET /api/v1/history`

Paginated list of all answered questions with full question data.

**Query Parameters:**

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `section` | string | all | `logical_reasoning` or `reading_comprehension` |
| `subtype` | string | all | LR or RC subtype string (e.g. `strengthen`, `rc_inference`) |
| `correct` | bool | all | `true` for correct only, `false` for mistakes only |
| `date_from` | string | — | ISO date lower bound |
| `date_to` | string | — | ISO date upper bound |
| `sort_by` | string | `answered_at` | `answered_at`, `difficulty_score`, `time_spent` |
| `sort_order` | string | `desc` | `asc` or `desc` |
| `page` | int | 1 | 1-indexed page number |
| `page_size` | int | 20 | Items per page (max 50) |

**Response:** `200 OK`

```json
{
  "questions": [
    {
      "question_id": 42,
      "section": "logical_reasoning",
      "lr_subtype": "flaw",
      "difficulty": "medium",
      "difficulty_score": 55,
      "stimulus": "The committee argued that...",
      "question_stem": "The reasoning in the argument is flawed because it...",
      "correct_answer_id": "C",
      "explanation": "The argument commits a...",
      "choices": [
        { "id": 1, "choice_id": "A", "choice_text": "...", "explanation": "...", "is_correct": false, "wrong_answer_type": "opposite" },
        { "id": 2, "choice_id": "B", "choice_text": "...", "explanation": "...", "is_correct": false, "wrong_answer_type": "partial" },
        { "id": 3, "choice_id": "C", "choice_text": "...", "explanation": "...", "is_correct": true },
        { "id": 4, "choice_id": "D", "choice_text": "...", "explanation": "...", "is_correct": false, "wrong_answer_type": "irrelevant" },
        { "id": 5, "choice_id": "E", "choice_text": "...", "explanation": "...", "is_correct": false, "wrong_answer_type": "scope_shift" }
      ],
      "selected_choice_id": "A",
      "correct": false,
      "time_spent_seconds": 45.2,
      "attempt_count": 1,
      "answered_at": "2026-02-21T14:30:00Z"
    }
  ],
  "total": 156,
  "page": 1,
  "page_size": 20
}
```

### 7b. `GET /api/v1/history/mistakes`

Shortcut for `GET /api/v1/history?correct=false`. Returns only incorrect answers, most recent first.

**Query Parameters:** `page`, `page_size` only.

**Response:** Same shape as `GET /api/v1/history`.

### 7c. `GET /api/v1/history/stats`

Aggregate accuracy and performance statistics.

**Response:** `200 OK`

```json
{
  "total_answered": 312,
  "total_correct": 234,
  "overall_accuracy": 0.75,
  "avg_time_seconds": 42.5,
  "section_stats": {
    "logical_reasoning": {
      "answered": 240,
      "correct": 192,
      "accuracy": 0.80,
      "avg_time_seconds": 38.2
    },
    "reading_comprehension": {
      "answered": 72,
      "correct": 42,
      "accuracy": 0.58,
      "avg_time_seconds": 55.1
    }
  },
  "subtype_stats": {
    "strengthen": { "section": "logical_reasoning", "answered": 30, "correct": 26, "accuracy": 0.87, "avg_time_seconds": 35.0 },
    "flaw": { "section": "logical_reasoning", "answered": 28, "correct": 18, "accuracy": 0.64, "avg_time_seconds": 40.5 }
  },
  "difficulty_stats": {
    "easy": { "answered": 100, "correct": 90, "accuracy": 0.90 },
    "medium": { "answered": 150, "correct": 108, "accuracy": 0.72 },
    "hard": { "answered": 62, "correct": 36, "accuracy": 0.58 }
  },
  "recent_trend": [
    { "date": "2026-02-19", "answered": 12, "correct": 9, "accuracy": 0.75 },
    { "date": "2026-02-20", "answered": 18, "correct": 15, "accuracy": 0.83 },
    { "date": "2026-02-21", "answered": 6, "correct": 5, "accuracy": 0.83 }
  ]
}
```

### 7d. `POST /api/v1/history/drill-review`

Returns full question data for a set of question IDs (for post-drill review). The frontend sends the IDs of questions from the just-completed drill.

**Request Body:**

```json
{
  "question_ids": [42, 55, 63, 78, 91, 104]
}
```

**Response:** `200 OK`

```json
{
  "questions": [
    {
      "question_id": 42,
      "section": "logical_reasoning",
      "lr_subtype": "flaw",
      "stimulus": "...",
      "question_stem": "...",
      "correct_answer_id": "C",
      "explanation": "...",
      "choices": [...],
      "passage": null,
      "selected_choice_id": "A",
      "correct": false,
      "time_spent_seconds": 45.2,
      "attempt_count": 1,
      "answered_at": "2026-02-21T14:30:00Z"
    }
  ]
}
```

### 7e. `POST /api/v1/bookmarks/{questionID}`

Bookmark a question for later review.

**Request Body (optional):**

```json
{ "note": "Review flaw reasoning pattern" }
```

**Response:** `201 Created`

```json
{ "message": "bookmarked" }
```

### 7f. `DELETE /api/v1/bookmarks/{questionID}`

Remove a bookmark.

**Response:** `200 OK`

```json
{ "message": "unbookmarked" }
```

### 7g. `GET /api/v1/bookmarks`

List all bookmarked questions with full question data and user history.

**Query Parameters:** `page` (default 1), `page_size` (default 20, max 50).

**Response:** `200 OK`

```json
{
  "bookmarks": [
    {
      "id": 7,
      "question_id": 42,
      "note": "Review flaw reasoning pattern",
      "created_at": "2026-02-20T10:00:00Z",
      "question": {
        "question_id": 42,
        "section": "logical_reasoning",
        "stimulus": "...",
        "question_stem": "...",
        "correct_answer_id": "C",
        "explanation": "...",
        "choices": [...],
        "selected_choice_id": "A",
        "correct": false,
        "time_spent_seconds": 45.2,
        "attempt_count": 1,
        "answered_at": "2026-02-21T14:30:00Z"
      }
    }
  ],
  "total": 5,
  "page": 1,
  "page_size": 20
}
```

---

## 8. Handler Registration

### New file: `internal/questions/history_handler.go`

```go
func (h *Handler) RegisterHistoryRoutes(r *mux.Router, authMiddleware mux.MiddlewareFunc) {
    history := r.PathPrefix("/api/v1").Subrouter()
    history.Use(authMiddleware)

    history.HandleFunc("/history", h.GetHistory).Methods("GET")
    history.HandleFunc("/history/mistakes", h.GetMistakes).Methods("GET")
    history.HandleFunc("/history/stats", h.GetHistoryStats).Methods("GET")
    history.HandleFunc("/history/drill-review", h.GetDrillReview).Methods("POST")

    history.HandleFunc("/bookmarks", h.GetBookmarks).Methods("GET")
    history.HandleFunc("/bookmarks/{questionID}", h.CreateBookmark).Methods("POST")
    history.HandleFunc("/bookmarks/{questionID}", h.DeleteBookmark).Methods("DELETE")
}
```

### Handler method signatures

```go
func (h *Handler) GetHistory(w http.ResponseWriter, r *http.Request) {
    userID := getUserID(r)
    req := models.HistoryListRequest{
        Section:   queryStringPtr(r, "section"),
        Subtype:   queryStringPtr(r, "subtype"),
        Correct:   queryBoolPtr(r, "correct"),
        DateFrom:  queryStringPtr(r, "date_from"),
        DateTo:    queryStringPtr(r, "date_to"),
        SortBy:    queryString(r, "sort_by", "answered_at"),
        SortOrder: queryString(r, "sort_order", "desc"),
        Page:      queryInt(r, "page", 1),
        PageSize:  queryInt(r, "page_size", 20),
    }
    resp, err := h.service.GetUserHistory(userID, req)
    // ... json.Encode(resp) ...
}

func (h *Handler) GetMistakes(w http.ResponseWriter, r *http.Request) {
    userID := getUserID(r)
    page := queryInt(r, "page", 1)
    pageSize := queryInt(r, "page_size", 20)
    resp, err := h.service.GetUserMistakes(userID, page, pageSize)
    // ...
}

func (h *Handler) GetHistoryStats(w http.ResponseWriter, r *http.Request) {
    userID := getUserID(r)
    resp, err := h.service.GetUserHistoryStats(userID)
    // ...
}

func (h *Handler) GetDrillReview(w http.ResponseWriter, r *http.Request) {
    userID := getUserID(r)
    var body struct {
        QuestionIDs []int64 `json:"question_ids"`
    }
    json.NewDecoder(r.Body).Decode(&body)
    if len(body.QuestionIDs) > 50 {
        body.QuestionIDs = body.QuestionIDs[:50]
    }
    questions, err := h.service.GetDrillReview(userID, body.QuestionIDs)
    // ...
}

func (h *Handler) CreateBookmark(w http.ResponseWriter, r *http.Request) {
    userID := getUserID(r)
    questionID := muxVarInt64(r, "questionID")
    var body models.BookmarkRequest
    json.NewDecoder(r.Body).Decode(&body)
    err := h.service.CreateBookmark(userID, questionID, nilIfEmpty(body.Note))
    // ... 201 Created ...
}

func (h *Handler) DeleteBookmark(w http.ResponseWriter, r *http.Request) {
    userID := getUserID(r)
    questionID := muxVarInt64(r, "questionID")
    err := h.service.DeleteBookmark(userID, questionID)
    // ... 200 OK or 404 ...
}

func (h *Handler) GetBookmarks(w http.ResponseWriter, r *http.Request) {
    userID := getUserID(r)
    page := queryInt(r, "page", 1)
    pageSize := queryInt(r, "page_size", 20)
    resp, err := h.service.GetBookmarks(userID, page, pageSize)
    // ...
}
```

---

## 9. Route Registration in `cmd/server/main.go`

```go
// Add after existing route registrations:
questionHandler.RegisterHistoryRoutes(router, authMiddleware)
```

---

## 10. Migration in `internal/database/database.go`

Add to the `Migrate()` function:

```sql
-- Extend user_question_history
ALTER TABLE user_question_history
    ADD COLUMN IF NOT EXISTS selected_choice_id VARCHAR(5);
ALTER TABLE user_question_history
    ADD COLUMN IF NOT EXISTS time_spent_seconds REAL;
ALTER TABLE user_question_history
    ADD COLUMN IF NOT EXISTS attempt_count INT NOT NULL DEFAULT 1;

CREATE INDEX IF NOT EXISTS idx_history_user_correct
    ON user_question_history(user_id, correct);
CREATE INDEX IF NOT EXISTS idx_history_user_date
    ON user_question_history(user_id, answered_at DESC);

-- Bookmarks table
CREATE TABLE IF NOT EXISTS user_bookmarks (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    question_id BIGINT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    note        TEXT,
    created_at  TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(user_id, question_id)
);

CREATE INDEX IF NOT EXISTS idx_bookmarks_user
    ON user_bookmarks(user_id, created_at DESC);
```

---

## 11. Backward Compatibility

- **`RecordAnswer` signature change**: The method gains two new parameters (`selectedChoiceID`, `timeSpentSeconds`). All existing callers in `SubmitAnswer()` must be updated. Both new params accept `nil`, so passing `nil` preserves old behavior.
- **`SubmitAnswerRequest` gains optional field**: `time_spent_seconds` is `omitempty`, so existing clients that don't send it will not break.
- **New columns use defaults**: `selected_choice_id` and `time_spent_seconds` default to `NULL`, `attempt_count` defaults to `1`. Existing rows remain valid.
- **New tables**: `user_bookmarks` is additive.
- **New endpoints**: All are new routes — no existing routes are modified.

---

## 12. Performance Considerations

- **Index coverage**: The new indexes (`idx_history_user_correct`, `idx_history_user_date`) cover the two most common query patterns (mistake filtering and chronological listing).
- **Batch choice loading**: When fetching a page of history questions, load all choices in one query (`WHERE question_id IN (...)`) rather than N+1 individual fetches.
- **Passage loading**: Similarly, batch-load RC passages for any questions with `passage_id IS NOT NULL` in a single query.
- **Stats caching**: `GetUserHistoryStats` runs multiple aggregate queries. If performance becomes an issue, cache the result per user with a 5-minute TTL (invalidated on new answer submission).
- **Pagination limits**: `page_size` capped at 50 to prevent unbounded result sets.
- **Drill review limit**: `POST /history/drill-review` caps `question_ids` at 50 entries.

---

## 13. Integration with Existing Specs

### Adaptive System (ADAPTIVE_SYSTEM_SPEC.md)
- `user_question_history` table defined in Section 2d of that spec. This spec extends it with three new columns. The `UNIQUE(user_id, question_id)` constraint and indexes from that spec remain unchanged.
- `RecordAnswer()` store method defined in that spec is extended here with new parameters. The upsert pattern is preserved.

### Gamification (GAMIFICATION_SPEC.md)
- No changes to XP, streaks, or daily goals. History retrieval is read-only and does not affect gamification state.
- The `user_gamification.questions_answered_total` / `questions_correct_total` fields provide fast aggregate counts without querying history. The history stats endpoint provides richer breakdowns.

### Reading Comprehension (RC_SPEC.md)
- `DrillPassage` struct is defined in RC_SPEC. History endpoints must include passage data for RC questions. The `GetUserHistory`, `GetDrillReview`, and `GetBookmarks` store methods JOIN on `rc_passages` when `passage_id IS NOT NULL`.
- If RC_SPEC is not yet implemented, history endpoints will simply return `passage: null` for all questions (since no RC questions will have been answered yet).

### Question Quality (QUESTION_QUALITY_SPEC.md)
- No changes. Quality scores are internal to generation/validation and not exposed in history endpoints.

---

## 14. File Changes Summary

| File | Action | Description |
|------|--------|-------------|
| `internal/models/history.go` | **New** | HistoryEntry, HistoryQuestion, stats/bookmark types, request/response types |
| `internal/models/question.go` | **Modify** | Add `TimeSpentSeconds *float64` to `SubmitAnswerRequest` |
| `internal/questions/store.go` | **Modify** | Update `RecordAnswer` signature; add `GetUserHistory`, `GetUserMistakes`, `GetUserHistoryStats`, `GetDrillReview`, bookmark CRUD methods |
| `internal/questions/service.go` | **Modify** | Update `SubmitAnswer` to pass new fields; add history/bookmark service methods |
| `internal/questions/history_handler.go` | **New** | 7 handler methods + `RegisterHistoryRoutes` |
| `internal/database/database.go` | **Modify** | Add ALTER TABLE + CREATE TABLE to `Migrate()` |
| `cmd/server/main.go` | **Modify** | Register history routes |

---

## 15. Constants

```go
const (
    HistoryDefaultPageSize = 20
    HistoryMaxPageSize     = 50
    DrillReviewMaxIDs      = 50
    RecentTrendDays        = 30
)
```
