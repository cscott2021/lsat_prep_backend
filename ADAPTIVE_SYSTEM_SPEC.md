# Backend Adaptive System Spec

> Replaces fixed difficulty with numeric scores, adds per-user ability tracking, adaptive question serving, two practice modes, and a smart generation queue.

---

## 1. Overview

The current system serves questions filtered by a fixed difficulty enum (`easy`/`medium`/`hard`) with no awareness of individual user performance. This spec introduces:

1. **Numeric difficulty scores** (0–100) on every question
2. **Per-user ability tracking** at three scopes (overall, section, subtype)
3. **Adaptive question serving** that matches questions to user ability
4. **Two practice modes**: Quick Questions (mixed subtypes) and Subtype Drill
5. **A difficulty slider** so users control how hard they want to be pushed
6. **A generation queue** that keeps question inventory stocked per difficulty bucket

---

## 2. Database Schema Changes

### 2a. Modify `questions` table

```sql
-- Add numeric difficulty score (0-100)
ALTER TABLE questions ADD COLUMN IF NOT EXISTS difficulty_score INT
    CHECK (difficulty_score >= 0 AND difficulty_score <= 100);

-- Backfill from existing enum
UPDATE questions SET difficulty_score = CASE
    WHEN difficulty = 'easy' THEN 25
    WHEN difficulty = 'medium' THEN 50
    WHEN difficulty = 'hard' THEN 75
END WHERE difficulty_score IS NULL;

-- Make NOT NULL after backfill
ALTER TABLE questions ALTER COLUMN difficulty_score SET NOT NULL;
ALTER TABLE questions ALTER COLUMN difficulty_score SET DEFAULT 50;

-- Index for adaptive serving
CREATE INDEX IF NOT EXISTS idx_questions_adaptive
    ON questions(section, lr_subtype, difficulty_score);
```

> The string `difficulty` column stays for backward compatibility and generation targeting. New questions get both: the string maps generation prompts, the score enables adaptive serving.

### 2b. Modify `users` table

```sql
ALTER TABLE users ADD COLUMN IF NOT EXISTS difficulty_slider INT NOT NULL DEFAULT 50
    CHECK (difficulty_slider >= 0 AND difficulty_slider <= 100);
```

### 2c. New table: `user_ability_scores`

```sql
CREATE TABLE IF NOT EXISTS user_ability_scores (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope           VARCHAR(20) NOT NULL,       -- 'overall', 'section', 'subtype'
    scope_value     VARCHAR(100),               -- NULL for overall; 'logical_reasoning' for section; 'strengthen' for subtype
    ability_score   INT NOT NULL DEFAULT 50     CHECK (ability_score >= 0 AND ability_score <= 100),
    questions_answered INT NOT NULL DEFAULT 0,
    questions_correct  INT NOT NULL DEFAULT 0,
    last_updated    TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(user_id, scope, scope_value)
);

CREATE INDEX IF NOT EXISTS idx_ability_user ON user_ability_scores(user_id);
CREATE INDEX IF NOT EXISTS idx_ability_lookup ON user_ability_scores(user_id, scope, scope_value);
```

### 2d. New table: `user_question_history`

```sql
CREATE TABLE IF NOT EXISTS user_question_history (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    question_id BIGINT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    answered_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    correct     BOOLEAN NOT NULL,
    UNIQUE(user_id, question_id)
);

CREATE INDEX IF NOT EXISTS idx_history_user ON user_question_history(user_id);
CREATE INDEX IF NOT EXISTS idx_history_user_question ON user_question_history(user_id, question_id);
```

### 2e. New table: `generation_queue`

```sql
CREATE TABLE IF NOT EXISTS generation_queue (
    id                   BIGSERIAL PRIMARY KEY,
    section              VARCHAR(50) NOT NULL,
    lr_subtype           VARCHAR(50),
    rc_subtype           VARCHAR(50),
    difficulty_bucket_min INT NOT NULL,
    difficulty_bucket_max INT NOT NULL,
    target_difficulty     VARCHAR(20) NOT NULL,  -- 'easy', 'medium', 'hard' for generation prompt
    status               VARCHAR(20) NOT NULL DEFAULT 'pending',  -- 'pending', 'generating', 'completed', 'failed'
    questions_needed     INT NOT NULL DEFAULT 6,
    error_message        TEXT,
    created_at           TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    completed_at         TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_genqueue_status ON generation_queue(status);
CREATE INDEX IF NOT EXISTS idx_genqueue_lookup ON generation_queue(section, lr_subtype, status);
```

---

## 3. RC Subtypes (New)

Add to `internal/models/question.go`:

```go
type RCSubtype string

const (
    RCSubtypeMainIdea          RCSubtype = "rc_main_idea"
    RCSubtypeDetail            RCSubtype = "rc_detail"
    RCSubtypeInference         RCSubtype = "rc_inference"
    RCSubtypeAttitude          RCSubtype = "rc_attitude"
    RCSubtypeFunction          RCSubtype = "rc_function"
    RCSubtypeOrganization      RCSubtype = "rc_organization"
    RCSubtypeStrengthenWeaken  RCSubtype = "rc_strengthen_weaken"
    RCSubtypeAnalogy           RCSubtype = "rc_analogy"
    RCSubtypeRelationship      RCSubtype = "rc_relationship"
    RCSubtypeAgreement         RCSubtype = "rc_agreement"
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
```

Also add `rc_subtype VARCHAR(50)` column to `questions` table and corresponding model field.

---

## 4. Go Model Additions

### 4a. `internal/models/ability.go` (new file)

```go
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
    ID         int64     `json:"id"`
    UserID     int64     `json:"user_id"`
    QuestionID int64     `json:"question_id"`
    AnsweredAt time.Time `json:"answered_at"`
    Correct    bool      `json:"correct"`
}

// API request/response types

type AbilityResponse struct {
    OverallAbility    int            `json:"overall_ability"`
    SectionAbilities  map[string]int `json:"section_abilities"`
    SubtypeAbilities  map[string]int `json:"subtype_abilities"`
}

// ── CHANGED: Unified per-question serving request ──────────────────────
type NextQuestionRequest struct {
    Section    string  `json:"section"`              // "logical_reasoning", "reading_comprehension", "both"
    LRSubtype  *string `json:"lr_subtype,omitempty"`  // for subtype focus mode
    RCSubtype  *string `json:"rc_subtype,omitempty"`  // for subtype focus mode
    SessionID  *string `json:"session_id,omitempty"`  // track questions served in this session
}

// DEPRECATED: Use NextQuestionRequest instead
type QuickDrillRequest struct {
    Section          string `json:"section"`           // "logical_reasoning", "reading_comprehension", "both"
    DifficultySlider int    `json:"difficulty_slider"`  // 0-100
    Count            int    `json:"count"`              // default 6
}

// DEPRECATED: Use NextQuestionRequest instead
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

type StartSessionRequest struct {
    Mode       string  `json:"mode"`                 // "quick" or "subtype"
    Section    string  `json:"section"`              // "logical_reasoning", "reading_comprehension", "both"
    LRSubtype  *string `json:"lr_subtype,omitempty"`
    RCSubtype  *string `json:"rc_subtype,omitempty"`
}

type StartSessionResponse struct {
    SessionID string    `json:"session_id"`
    StartedAt time.Time `json:"started_at"`
}

type EndSessionRequest struct {
    SessionID string `json:"session_id"`
}

type SessionSummary struct {
    SessionID        string    `json:"session_id"`
    TotalQuestions   int       `json:"total_questions"`
    CorrectAnswers   int       `json:"correct_answers"`
    AbilityBefore    int       `json:"ability_before"`
    AbilityAfter     int       `json:"ability_after"`
    StartedAt        time.Time `json:"started_at"`
    EndedAt          time.Time `json:"ended_at"`
    DurationSeconds  int       `json:"duration_seconds"`
}

// In-memory session tracking
type StudySession struct {
    ID                string
    UserID            int64
    QuestionsServed   []int64     // question IDs
    SubtypesServed    []string    // for dedup/weighting
    StartTime         time.Time
    CorrectCount      int
    AbilityBefore     int
}

type GenerationQueueItem struct {
    ID                int64      `json:"id"`
    Section           string     `json:"section"`
    LRSubtype         *string    `json:"lr_subtype,omitempty"`
    RCSubtype         *string    `json:"rc_subtype,omitempty"`
    DifficultyBucketMin int     `json:"difficulty_bucket_min"`
    DifficultyBucketMax int     `json:"difficulty_bucket_max"`
    TargetDifficulty    string  `json:"target_difficulty"`
    Status            string     `json:"status"`
    QuestionsNeeded   int        `json:"questions_needed"`
    ErrorMessage      *string    `json:"error_message,omitempty"`
    CreatedAt         time.Time  `json:"created_at"`
    CompletedAt       *time.Time `json:"completed_at,omitempty"`
}
```

### 4b. Update `internal/models/question.go`

Add to the `Question` struct:

```go
DifficultyScore int        `json:"difficulty_score"` // 0-100
RCSubtype       *RCSubtype `json:"rc_subtype,omitempty"`
```

Add to `DrillQuestion` struct:

```go
DifficultyScore int        `json:"difficulty_score"`
RCSubtype       *RCSubtype `json:"rc_subtype,omitempty"`
```

Update `SubmitAnswerResponse` to include ability updates:

```go
type SubmitAnswerResponse struct {
    Correct         bool             `json:"correct"`
    CorrectAnswerID string           `json:"correct_answer_id"`
    Explanation     string           `json:"explanation"`
    Choices         []AnswerChoice   `json:"choices"`
    AbilityUpdated  *AbilitySnapshot `json:"ability_updated,omitempty"`
}

type AbilitySnapshot struct {
    OverallAbility  int `json:"overall_ability"`
    SectionAbility  int `json:"section_ability"`
    SubtypeAbility  int `json:"subtype_ability"`
}
```

---

## 5. Ability Score Algorithm

### 5a. Core Formula (Elo-Inspired)

```go
// File: internal/questions/ability.go

import "math"

// ExpectedAccuracy returns the probability a user with the given ability
// gets a question with the given difficulty correct.
// Uses a sigmoid centered on 0 with scaling factor 25.
func ExpectedAccuracy(userAbility, difficultyScore int) float64 {
    x := float64(userAbility-difficultyScore) / 25.0
    return 1.0 / (1.0 + math.Exp(-x))
}

// KFactor returns the adjustment strength based on how many questions
// the user has answered at this scope.
func KFactor(questionsAnswered int) float64 {
    if questionsAnswered < 20 {
        return 3.0  // New user: fast convergence
    }
    if questionsAnswered < 100 {
        return 2.0  // Intermediate: moderate adjustment
    }
    return 1.0      // Mature: stable, small adjustments
}

// ComputeNewAbility calculates the updated ability score after answering.
func ComputeNewAbility(currentAbility, difficultyScore int, correct bool, questionsAnswered int) int {
    expected := ExpectedAccuracy(currentAbility, difficultyScore)
    k := KFactor(questionsAnswered)

    var result float64
    if correct {
        result = 1.0
    } else {
        result = 0.0
    }

    adjustment := (result - expected) * k
    newAbility := float64(currentAbility) + adjustment

    // Clamp to [0, 100]
    if newAbility < 0 {
        newAbility = 0
    }
    if newAbility > 100 {
        newAbility = 100
    }

    return int(math.Round(newAbility))
}
```

### 5b. Example Scenarios

| User Ability | Question Difficulty | Correct? | Expected | Adjustment | New Ability |
|:---:|:---:|:---:|:---:|:---:|:---:|
| 50 | 50 | Yes | 0.50 | +1.0 | 51 |
| 50 | 50 | No | 0.50 | -1.0 | 49 |
| 50 | 70 | Yes | 0.31 | +1.4 | 51 |
| 50 | 70 | No | 0.31 | -0.6 | 49 |
| 50 | 30 | Yes | 0.69 | +0.6 | 51 |
| 50 | 30 | No | 0.69 | -1.4 | 49 |
| 80 | 60 | Yes | 0.69 | +0.6 | 81 |
| 80 | 90 | No | 0.40 | -0.8 | 79 |

**Key properties:**
- Correct on a hard question → bigger ability increase
- Wrong on an easy question → bigger ability decrease
- Symmetric around 50% expected accuracy at equal ability/difficulty
- Converges faster for new users (K=3), stabilizes for experienced users (K=1)

### 5c. Three-Scope Update

On every answer submission, update three ability records:

```go
func (s *Service) UpdateAbilityScores(
    ctx context.Context,
    userID int64,
    question *Question,
    correct bool,
) (*AbilitySnapshot, error) {
    section := string(question.Section)
    subtype := ""
    if question.LRSubtype != nil {
        subtype = string(*question.LRSubtype)
    } else if question.RCSubtype != nil {
        subtype = string(*question.RCSubtype)
    }

    // 1. Update overall
    overall, err := s.store.GetOrCreateAbility(userID, ScopeOverall, nil)
    newOverall := ComputeNewAbility(overall.AbilityScore, question.DifficultyScore, correct, overall.QuestionsAnswered)
    s.store.UpdateAbility(userID, ScopeOverall, nil, newOverall, correct)

    // 2. Update section
    sectionAbility, err := s.store.GetOrCreateAbility(userID, ScopeSection, &section)
    newSection := ComputeNewAbility(sectionAbility.AbilityScore, question.DifficultyScore, correct, sectionAbility.QuestionsAnswered)
    s.store.UpdateAbility(userID, ScopeSection, &section, newSection, correct)

    // 3. Update subtype
    subtypeAbility, err := s.store.GetOrCreateAbility(userID, ScopeSubtype, &subtype)
    newSubtype := ComputeNewAbility(subtypeAbility.AbilityScore, question.DifficultyScore, correct, subtypeAbility.QuestionsAnswered)
    s.store.UpdateAbility(userID, ScopeSubtype, &subtype, newSubtype, correct)

    return &AbilitySnapshot{
        OverallAbility: newOverall,
        SectionAbility: newSection,
        SubtypeAbility: newSubtype,
    }, nil
}
```

---

## 6. Adaptive Question Serving

### 6a. Target Difficulty Calculation

```go
// TargetDifficulty computes the center of the difficulty window
// based on user ability and their slider preference.
//
// slider=0:   target = ability - 15  (all easier)
// slider=50:  target = ability       (centered on ability)
// slider=100: target = ability + 15  (all harder)
func TargetDifficulty(userAbility, slider int) int {
    offset := float64(slider-50) * 0.3
    target := float64(userAbility) + offset
    if target < 0 {
        target = 0
    }
    if target > 100 {
        target = 100
    }
    return int(math.Round(target))
}
```

### 6b. Adaptive Serving Query

```sql
-- Fetch questions for a user, preferring unseen, within difficulty window, random order
SELECT q.id, q.section, q.lr_subtype, q.rc_subtype, q.difficulty, q.difficulty_score,
       q.stimulus, q.question_stem, q.correct_answer_id, q.explanation,
       q.passage_id, q.times_served, q.times_correct,
       ac.id AS ac_id, ac.choice_id, ac.choice_text, ac.explanation AS ac_explanation,
       ac.is_correct, COALESCE(ac.wrong_answer_type, ''),
       CASE WHEN h.id IS NULL THEN 0 ELSE 1 END AS seen
FROM questions q
JOIN answer_choices ac ON ac.question_id = q.id
LEFT JOIN user_question_history h ON h.question_id = q.id AND h.user_id = $1
WHERE q.section = $2
  AND q.difficulty_score >= $3        -- minDifficulty
  AND q.difficulty_score <= $4        -- maxDifficulty
  AND q.validation_status IN ('passed', 'flagged', 'unvalidated')
  AND (q.quality_score >= 0.50 OR q.quality_score IS NULL)
  [AND q.lr_subtype = $5]            -- for subtype drill only
ORDER BY
    CASE WHEN h.id IS NULL THEN 0 ELSE 1 END,  -- unseen first
    RANDOM()
LIMIT $6
```

**Window size:** ±15 points from target. If the window yields fewer than `count` questions, widen by 10 and retry (up to ±35).

### 6c. Get Next Question (Per-Question Serving) — CHANGED

Replaces GetQuickDrill and GetSubtypeDrill with a unified per-question endpoint:

```go
// POST /api/v1/questions/next
// Serves ONE question adapted to the user's current ability.
// For Quick Study: section="both"|"logical_reasoning"|"reading_comprehension"
// For Subtype Focus: section + lr_subtype or rc_subtype
// Frontend calls this repeatedly, one question at a time after each answer.
func (s *Service) GetNextQuestion(ctx context.Context, userID int64, req NextQuestionRequest) (*DrillQuestion, error) {
    // 1. Retrieve the session to track served questions
    var session *StudySession
    if req.SessionID != nil {
        session = s.sessions[*req.SessionID] // in-memory map
    }

    // 2. Determine ability scope and target difficulty
    var ability int
    var subtype *string // selected subtype for this question
    var sliderValue int  // read from user's saved setting

    if req.LRSubtype != nil || req.RCSubtype != nil {
        // Subtype focus mode: use subtype ability
        subtypeKey := ""
        if req.LRSubtype != nil {
            subtypeKey = *req.LRSubtype
            ability = s.getSubtypeAbility(userID, subtypeKey)
        } else {
            subtypeKey = *req.RCSubtype
            ability = s.getSubtypeAbility(userID, subtypeKey)
        }
        subtype = &subtypeKey
    } else {
        // Quick Study mode: use section or overall ability
        ability = s.getSectionAbility(userID, req.Section)
    }

    sliderValue = s.getUserSlider(userID)
    target := TargetDifficulty(ability, sliderValue)
    minDiff := max(0, target-15)
    maxDiff := min(100, target+15)

    // 3. For Quick Study ("both" or section-only, no subtype):
    //    Pick a random subtype, biased toward user's weakest subtypes
    if subtype == nil && req.LRSubtype == nil && req.RCSubtype == nil {
        subtype = s.selectWeakSubtype(userID, req.Section, session)
    }

    // 4. Fetch 1 unseen question in the difficulty window for that subtype
    excludeIDs := []int64{}
    if session != nil {
        excludeIDs = session.QuestionsServed
    }

    q, err := s.store.GetOneAdaptiveQuestion(userID, *subtype, minDiff, maxDiff, excludeIDs)
    if err != nil || q == nil {
        // 5. If none found, widen window
        minDiff = max(0, target-35)
        maxDiff = min(100, target+35)
        q, err = s.store.GetOneAdaptiveQuestion(userID, *subtype, minDiff, maxDiff, excludeIDs)
    }

    // 6. If STILL none, try another subtype (only in Quick Study)
    if (err != nil || q == nil) && req.LRSubtype == nil && req.RCSubtype == nil {
        subtype = s.selectWeakSubtype(userID, req.Section, session)
        q, err = s.store.GetOneAdaptiveQuestion(userID, *subtype, minDiff, maxDiff, excludeIDs)
    }

    // 7. If STILL no questions, generate synchronously as fallback
    if err != nil || q == nil {
        difficulty := mapScoreToDifficulty(target)
        genReq := GenerateBatchRequest{
            Section:   Section(req.Section),
            LRSubtype: (*LRSubtype)(subtype) if isLRSubtype(*subtype),
            RCSubtype: (*RCSubtype)(subtype) if isRCSubtype(*subtype),
            Difficulty: difficulty,
            Count:      1,
        }
        _, err := s.GenerateBatch(ctx, genReq)
        if err == nil {
            q, _ = s.store.GetOneAdaptiveQuestion(userID, *subtype, minDiff, maxDiff, excludeIDs)
        }
    }

    if q == nil {
        return nil, errors.New("unable to fetch question after retries")
    }

    // 8. Track in session
    if session != nil {
        session.QuestionsServed = append(session.QuestionsServed, q.ID)
        session.SubtypesServed = append(session.SubtypesServed, *subtype)
    }

    // 9. Queue background generation if inventory low
    go s.CheckAndQueueGeneration(req.Section, subtype, minDiff, maxDiff)

    return q, nil
}

// selectWeakSubtype picks a random subtype, weighted toward weaker ones
func (s *Service) selectWeakSubtype(userID int64, section string, session *StudySession) *string {
    // Get all subtypes for section
    var allSubtypes []string
    var abilities map[string]int // subtype -> ability

    if section == "logical_reasoning" || section == "both" {
        allSubtypes = append(allSubtypes, allLRSubtypes...)
    }
    if section == "reading_comprehension" || section == "both" {
        allSubtypes = append(allSubtypes, allRCSubtypes...)
    }

    // Fetch ability for each subtype
    abilities = make(map[string]int)
    for _, st := range allSubtypes {
        abilities[st] = s.getSubtypeAbility(userID, st)
    }

    // Weight by inverse ability (weaker subtypes get higher weight)
    // maxAbility = 100; weight = (maxAbility + 1 - ability)
    var weights []float64
    for _, st := range allSubtypes {
        w := float64(101 - abilities[st])
        // Decay if this subtype appeared in last 2-3 questions (avoid repetition)
        if session != nil && len(session.SubtypesServed) >= 3 {
            lastThree := session.SubtypesServed[len(session.SubtypesServed)-3:]
            for _, prev := range lastThree {
                if prev == st {
                    w *= 0.5  // Halve weight if recently served
                }
            }
        }
        weights = append(weights, w)
    }

    // Weighted random selection
    selected := weightedRandomSelect(allSubtypes, weights)
    return &selected
}
```

### 6d. Deprecated: GetQuickDrill and GetSubtypeDrill

These methods are replaced by GetNextQuestion. Keep them for backward compatibility if needed, but mark as deprecated in code comments. The frontend should use the new per-question endpoint.

---

## 7. API Endpoints

### 7a. `GET /api/v1/users/ability` (Protected)

Returns all ability scores for the authenticated user.

```
Response 200:
{
    "overall_ability": 52,
    "section_abilities": {
        "logical_reasoning": 55,
        "reading_comprehension": 48
    },
    "subtype_abilities": {
        "strengthen": 60,
        "weaken": 52,
        "assumption": 48,
        "flaw": 55,
        "must_be_true": 50,
        "most_strongly_supported": 50,
        "method_of_reasoning": 50,
        "parallel_reasoning": 50,
        "parallel_flaw": 50,
        "principle": 50,
        "apply_principle": 50,
        "evaluate": 50,
        "main_conclusion": 50,
        "role_of_statement": 50,
        "rc_main_idea": 50,
        "rc_detail": 50,
        "rc_inference": 50,
        "rc_attitude": 50,
        "rc_function": 50,
        "rc_organization": 50,
        "rc_strengthen_weaken": 50,
        "rc_analogy": 50,
        "rc_relationship": 50,
        "rc_agreement": 50
    }
}
```

### 7b. `PUT /api/v1/users/difficulty-slider` (Protected)

```
Request:
{
    "slider_value": 65
}

Response 200:
{
    "slider_value": 65
}
```

### 7c. `POST /api/v1/questions/start-session` (Protected) — CHANGED

Starts a new study session and returns a session ID for tracking.

```
Request:
{
    "mode": "quick",                       // "quick" or "subtype"
    "section": "logical_reasoning",        // or "reading_comprehension" or "both"
    "lr_subtype": "strengthen",            // required if mode="subtype"
    "rc_subtype": null                     // or required if mode="subtype"
}

Response 200:
{
    "session_id": "sess_abc123def456",
    "started_at": "2026-02-21T10:30:00Z"
}
```

### 7d. `POST /api/v1/questions/next` (Protected) — CHANGED (New)

Serves ONE question adapted to the user's current ability. Replaces quick-drill and subtype-drill with per-question serving.

```
Request:
{
    "section": "logical_reasoning",     // or "reading_comprehension" or "both"
    "lr_subtype": "strengthen",         // optional, for subtype focus mode
    "rc_subtype": null,                 // optional, for subtype focus mode
    "session_id": "sess_abc123def456"   // optional, for tracking in session
}

Response 200 (single DrillQuestion, not array):
{
    "id": 42,
    "section": "logical_reasoning",
    "lr_subtype": "strengthen",
    "difficulty_score": 55,
    "stimulus": "...",
    "question_stem": "...",
    "choices": [
        {"choice_id": "A", "choice_text": "..."},
        {"choice_id": "B", "choice_text": "..."},
        {"choice_id": "C", "choice_text": "..."},
        {"choice_id": "D", "choice_text": "..."},
        {"choice_id": "E", "choice_text": "..."}
    ]
}
```

Timeout: 120s (to accommodate synchronous generation on cold start).

**Usage pattern:**
1. `POST /start-session` → get session_id
2. `POST /next` (session_id, section, optional subtype) → get 1 question
3. `POST /questions/{id}/answer` (with choice) → answer + ability update
4. Repeat step 2-3 for each subsequent question
5. `POST /end-session` (session_id) → session complete, get summary

### 7e. `POST /api/v1/questions/end-session` (Protected) — CHANGED (New)

Ends a study session and returns a summary with ability changes.

```
Request:
{
    "session_id": "sess_abc123def456"
}

Response 200:
{
    "session_id": "sess_abc123def456",
    "total_questions": 6,
    "correct_answers": 4,
    "ability_before": 50,
    "ability_after": 53,
    "started_at": "2026-02-21T10:30:00Z",
    "ended_at": "2026-02-21T10:35:00Z",
    "duration_seconds": 300
}
```

### 7f. `POST /api/v1/questions/{id}/answer` (Modified)

Now also records history and updates ability scores. Same as before, but now used in per-question flow.

```
Request:
{
    "selected_choice_id": "B"
}

Response 200:
{
    "correct": true,
    "correct_answer_id": "B",
    "explanation": "The argument's conclusion is that...",
    "choices": [...],
    "ability_updated": {
        "overall_ability": 53,
        "section_ability": 56,
        "subtype_ability": 61
    }
}
```

Side effects on each answer submission:
1. Increment `times_served` on question
2. Increment `times_correct` if correct
3. INSERT into `user_question_history`
4. UPDATE three `user_ability_scores` rows (overall, section, subtype)
5. Async: check generation queue for this subtype's difficulty range

### 7g. Deprecated Endpoints

The following endpoints are deprecated but may be kept for backward compatibility:

- `POST /api/v1/questions/quick-drill` (replaced by start-session + next)
- `POST /api/v1/questions/subtype-drill` (replaced by start-session + next)

### 7h. Route Registration — CHANGED

Remove from `main.go`:
```go
// REMOVE: protected.HandleFunc("/questions/quick-drill", ...)
// REMOVE: protected.HandleFunc("/questions/subtype-drill", ...)
```

Add to `main.go`:
```go
protected.HandleFunc("/users/ability", questionHandler.GetAbility).Methods("GET")
protected.HandleFunc("/users/difficulty-slider", questionHandler.SetDifficultySlider).Methods("PUT")
protected.HandleFunc("/questions/start-session", questionHandler.StartSession).Methods("POST")
protected.HandleFunc("/questions/next", questionHandler.NextQuestion).Methods("POST")
protected.HandleFunc("/questions/end-session", questionHandler.EndSession).Methods("POST")
```

Keep:
```go
protected.HandleFunc("/questions/{id}/answer", questionHandler.SubmitAnswer).Methods("POST")
protected.HandleFunc("/questions/{id}", questionHandler.GetQuestion).Methods("GET")
protected.HandleFunc("/questions/batches", questionHandler.ListBatches).Methods("GET")
protected.HandleFunc("/questions/batches/{id}", questionHandler.GetBatch).Methods("GET")
```

---

## 8. Smart Generation Queue

### 8a. Difficulty Buckets

| Bucket | Score Range | Generation Difficulty | Description |
|:---:|:---:|:---:|:---|
| 1 | 0–20 | `easy` | Fundamentals |
| 2 | 21–40 | `easy` | Below average |
| 3 | 41–60 | `medium` | Average |
| 4 | 61–80 | `hard` | Above average |
| 5 | 81–100 | `hard` | Expert |

### 8b. Inventory Check

After every drill completion (all 6 answered), and after every answer submission, run asynchronously:

```go
func (s *Service) CheckAndQueueGeneration(section string, subtype *string, minDiff, maxDiff int) {
    // Determine which bucket(s) overlap with the requested range
    buckets := []struct{ min, max int; difficulty string }{
        {0, 20, "easy"}, {21, 40, "easy"}, {41, 60, "medium"},
        {61, 80, "hard"}, {81, 100, "hard"},
    }

    for _, bucket := range buckets {
        if bucket.max < minDiff || bucket.min > maxDiff {
            continue // Bucket doesn't overlap with requested range
        }

        count := s.store.CountQuestionsInBucket(section, subtype, bucket.min, bucket.max)
        if count < 6 {
            needed := 6 - count
            // Upsert into generation_queue (avoid duplicates)
            s.store.UpsertGenerationQueue(section, subtype, bucket.min, bucket.max, bucket.difficulty, needed)
        }
    }
}
```

### 8c. Background Processor

A goroutine started in `main.go` that processes the queue every 30 seconds:

```go
func (s *Service) StartGenerationWorker(ctx context.Context) {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            s.processGenerationQueue(ctx)
        }
    }
}

func (s *Service) processGenerationQueue(ctx context.Context) {
    items, _ := s.store.GetPendingGenerations(5) // Process up to 5 at a time

    for _, item := range items {
        s.store.UpdateGenerationStatus(item.ID, "generating", nil)

        genReq := GenerateBatchRequest{
            Section:    Section(item.Section),
            Difficulty: Difficulty(item.TargetDifficulty),
            Count:      item.QuestionsNeeded,
        }
        if item.LRSubtype != nil {
            sub := LRSubtype(*item.LRSubtype)
            genReq.LRSubtype = &sub
        }
        if item.RCSubtype != nil {
            sub := RCSubtype(*item.RCSubtype)
            genReq.RCSubtype = &sub
        }

        _, err := s.GenerateBatch(ctx, genReq)
        if err != nil {
            errMsg := err.Error()
            s.store.UpdateGenerationStatus(item.ID, "failed", &errMsg)
            log.Printf("[gen-queue] failed: section=%s subtype=%v bucket=%d-%d err=%v",
                item.Section, item.LRSubtype, item.DifficultyBucketMin, item.DifficultyBucketMax, err)
        } else {
            s.store.UpdateGenerationStatus(item.ID, "completed", nil)
            log.Printf("[gen-queue] completed: section=%s subtype=%v bucket=%d-%d generated=%d",
                item.Section, item.LRSubtype, item.DifficultyBucketMin, item.DifficultyBucketMax, item.QuestionsNeeded)
        }
    }
}
```

### 8d. Synchronous Generation (Cold Start)

If a drill request finds 0 questions in the difficulty window AND the generation queue has nothing ready, the endpoint generates synchronously:

```
User requests drill → 0 questions found → Generate 6 inline (5-15s wait) → Return questions
```

This only happens on the very first request for a new subtype/difficulty combination. After that, the queue keeps inventory stocked.

---

## 9. Difficulty Score Assignment for New Questions

When the generator creates new questions, assign `difficulty_score` based on the generation prompt's difficulty parameter:

| Generation Difficulty | Score Range | Assignment |
|:---:|:---:|:---|
| `easy` | 10–35 | Random within range per question |
| `medium` | 40–65 | Random within range per question |
| `hard` | 70–95 | Random within range per question |

```go
func AssignDifficultyScore(difficulty Difficulty) int {
    switch difficulty {
    case DifficultyEasy:
        return 10 + rand.Intn(26)   // 10-35
    case DifficultyMedium:
        return 40 + rand.Intn(26)   // 40-65
    case DifficultyHard:
        return 70 + rand.Intn(26)   // 70-95
    default:
        return 50
    }
}
```

Over time, the recalibration system (existing `POST /admin/recalibrate`) can adjust `difficulty_score` based on actual user accuracy data.

---

## 10. New Store Methods — CHANGED

Add to `internal/questions/store.go`:

```go
// ── Ability Scores ──────────────────────────────────────────

func (s *Store) GetOrCreateAbility(userID int64, scope AbilityScope, scopeValue *string) (*UserAbilityScore, error)
// INSERT ... ON CONFLICT (user_id, scope, scope_value) DO NOTHING
// Then SELECT

func (s *Store) UpdateAbility(userID int64, scope AbilityScope, scopeValue *string, newScore int, correct bool) error
// UPDATE user_ability_scores
// SET ability_score = $1, questions_answered = questions_answered + 1,
//     questions_correct = questions_correct + CASE WHEN $2 THEN 1 ELSE 0 END,
//     last_updated = NOW()
// WHERE user_id = $3 AND scope = $4 AND scope_value = $5

func (s *Store) GetAllAbilities(userID int64) (*AbilityResponse, error)
// SELECT * FROM user_ability_scores WHERE user_id = $1
// Group into overall / section / subtype maps

// ── Question History ────────────────────────────────────────

func (s *Store) RecordAnswer(userID, questionID int64, correct bool) error
// INSERT INTO user_question_history (user_id, question_id, correct)
// ON CONFLICT (user_id, question_id) DO UPDATE SET correct = $3, answered_at = NOW()

// ── Adaptive Serving ────────────────────────────────────────

// CHANGED: Added excludeIDs parameter for tracking questions already served in session
func (s *Store) GetOneAdaptiveQuestion(userID int64, subtype string, minDiff, maxDiff int, excludeIDs []int64) (*DrillQuestion, error)
// Single question, unseen preferred, random, in difficulty window
// Excludes questions in excludeIDs list (questions already served in current session)

func (s *Store) GetAdaptiveQuestions(userID int64, section string, subtype *string, minDiff, maxDiff, count int, excludeIDs []int64) ([]DrillQuestion, error)
// Multiple questions, unseen preferred, random, in difficulty window
// Excludes questions in excludeIDs list

func (s *Store) CountQuestionsInBucket(section string, subtype *string, minDiff, maxDiff int) (int, error)
// SELECT COUNT(*) FROM questions WHERE section = $1 [AND lr_subtype = $2]
//   AND difficulty_score >= $3 AND difficulty_score <= $4
//   AND validation_status IN ('passed', 'flagged', 'unvalidated')

// ── Generation Queue ────────────────────────────────────────

func (s *Store) UpsertGenerationQueue(section string, subtype *string, minDiff, maxDiff int, targetDiff string, needed int) error
func (s *Store) GetPendingGenerations(limit int) ([]GenerationQueueItem, error)
func (s *Store) UpdateGenerationStatus(id int64, status string, errMsg *string) error

// ── User Settings ───────────────────────────────────────────

func (s *Store) GetDifficultySlider(userID int64) (int, error)
func (s *Store) SetDifficultySlider(userID int64, value int) error
// UPDATE users SET difficulty_slider = $1 WHERE id = $2

// ── Session Management ──────────────────────────────────────
// NEW: In-memory session tracking (can be upgraded to DB table later)

func (s *Service) CreateSession(userID int64, mode string, section string, subtype *string) (string, error)
// Generate session ID, create StudySession, store in s.sessions map
// Return session ID

func (s *Service) GetSession(sessionID string) (*StudySession, error)
// Retrieve session from s.sessions map

func (s *Service) UpdateSession(sessionID string, questionID int64, correct bool) error
// Update session: append to QuestionsServed, increment CorrectCount if correct

func (s *Service) EndSession(sessionID string) (*SessionSummary, error)
// Close session, compute summary (total, correct, ability delta, duration)
// Can optionally persist to DB for audit trail
```

---

## 11. Environment Variables

Add to `docker-compose.yml`:

```yaml
DIFFICULTY_WINDOW_SIZE: "15"        # ±N points from target difficulty
DIFFICULTY_WINDOW_MAX: "35"         # Maximum window expansion
MIN_BUCKET_INVENTORY: "6"           # Minimum questions per difficulty bucket
GENERATION_QUEUE_INTERVAL: "30"     # Seconds between queue processing runs
GENERATION_QUEUE_BATCH_SIZE: "5"    # Max items to process per run
```

---

## 12. Testing

### Unit Tests

```go
func TestExpectedAccuracy(t *testing.T) {
    // Equal ability and difficulty → ~50%
    assert.InDelta(t, 0.5, ExpectedAccuracy(50, 50), 0.01)
    // User much better → ~88%
    assert.InDelta(t, 0.88, ExpectedAccuracy(75, 50), 0.05)
    // User much worse → ~12%
    assert.InDelta(t, 0.12, ExpectedAccuracy(25, 50), 0.05)
}

func TestComputeNewAbility(t *testing.T) {
    // Correct on equal difficulty → small increase
    assert.Equal(t, 51, ComputeNewAbility(50, 50, true, 50))
    // Wrong on equal difficulty → small decrease
    assert.Equal(t, 49, ComputeNewAbility(50, 50, false, 50))
    // Correct on hard question → bigger increase
    assert.Greater(t, ComputeNewAbility(50, 70, true, 50), 51)
    // Bounds
    assert.Equal(t, 100, ComputeNewAbility(99, 10, true, 5))
    assert.Equal(t, 0, ComputeNewAbility(1, 90, false, 5))
}

func TestTargetDifficulty(t *testing.T) {
    assert.Equal(t, 50, TargetDifficulty(50, 50))  // Centered
    assert.Equal(t, 65, TargetDifficulty(50, 100)) // Max harder
    assert.Equal(t, 35, TargetDifficulty(50, 0))   // Max easier
}

func TestQuickDrill_MixedSubtypes(t *testing.T) {
    // Setup: 14 LR subtypes with questions at difficulty 45-55
    // Request: Quick drill, section=LR, count=6
    // Assert: 6 questions returned, all different subtypes
}

func TestSubtypeDrill_WindowExpansion(t *testing.T) {
    // Setup: Few questions at target, more at wider range
    // Request: Subtype drill with tight window returning < count
    // Assert: Window expands, returns full count
}
```

### Integration Tests

```go
func TestAdaptiveFlow_EndToEnd(t *testing.T) {
    // 1. Create user → ability starts at 50
    // 2. Request quick drill → questions near difficulty 50
    // 3. Answer all correct → ability increases
    // 4. Request another drill → questions are slightly harder
    // 5. Move slider to 80 → questions shift harder still
}
```

---

## 13. Implementation Order — CHANGED

1. Database migrations (new tables + ALTER existing)
2. Go models (`ability.go`, update `question.go`, add session models)
3. Store methods (ability, history, adaptive serving, session management)
4. Ability algorithm (`ability.go` functions)
5. Session management (in-memory map or DB table)
6. Service methods:
   - `GetNextQuestion` (replaces GetQuickDrill and GetSubtypeDrill)
   - `selectWeakSubtype` helper
   - `CreateSession`, `GetSession`, `UpdateSession`, `EndSession`
   - `UpdateAbilityScores`
7. Handler methods:
   - `StartSession`
   - `NextQuestion`
   - `EndSession`
   - Modify `SubmitAnswer` to update session
8. Route registration (remove quick-drill/subtype-drill, add new endpoints)
9. Generation queue (table, store, background worker)
10. Update `GenerateBatch` to assign `difficulty_score`
11. Environment variables
12. Tests
13. Update frontend (see ADAPTIVE_FRONTEND_SPEC.md)
