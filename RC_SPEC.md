# Backend Reading Comprehension Spec

> Closes the gap between existing RC infrastructure (models, prompts, parser, generation) and production-ready RC question serving. The generation pipeline already handles RC — this spec focuses on **passage delivery to clients**, **RC-aware drill serving**, **comparative passage support**, and **subject area diversity**.

---

## 1. What Already Exists

| Component | Status | Notes |
|-----------|--------|-------|
| `RCSubtype` enum (10 values) | ✅ Coded | `models/question.go` |
| `RCPassage` model | ✅ Coded | Title, SubjectArea, Content, IsComparative, PassageB |
| `rc_passages` table | ✅ Coded | `database/database.go` |
| `questions.passage_id` FK | ✅ Coded | Links questions to passages |
| `RCSystemPrompt()` | ✅ Coded | 450-500 word passages, subject rotation, comparative support |
| `BuildRCUserPrompt()` | ✅ Coded | JSON structure for passage + questions |
| `GenerateRCBatch()` | ✅ Coded | Calls LLM, parses response |
| `GeneratedPassage` parser struct | ✅ Coded | Title, SubjectArea, Content, IsComparative, PassageB |
| Passage length validation | ✅ Coded | Warns if < 1500 or > 3000 chars |
| Passage INSERT in `SaveGeneratedBatch` | ✅ Coded | Links passage to batch, returns passageID |
| RC subtype filtering in adaptive serving | ✅ Coded | `GetOneAdaptiveQuestion`, `GetAdaptiveQuestions` |
| RC in Quick Drill + Subtype Drill | ✅ Coded | `allRCSubtypes` list, section filtering |
| RC in generation queue | ✅ Coded | `RCSubtype *string` on `GenerationQueueItem` |
| RC passage export/import | ✅ Coded | Passages bundled with exported questions |

### What's Missing

| Gap | Impact |
|-----|--------|
| `DrillQuestion` has no passage data | RC questions served without their passage — **unusable** |
| No passage endpoint (fetch by ID) | Frontend can't retrieve passage if not inline |
| No passage grouping in drill serving | RC drill could serve 6 questions from 6 different passages — bad UX |
| No subject area rotation enforcement | Could generate 5 consecutive law passages |
| No comparative passage drill mode | Comparative passages generate but can't be specifically requested |
| No passage word count stored | Can't filter by passage length for timed modes |
| Passage validation in 3-stage pipeline | Self-verification / adversarial checks don't evaluate passage quality |

---

## 2. Database Changes

### 2a. Add `word_count` to `rc_passages`

```sql
ALTER TABLE rc_passages ADD COLUMN IF NOT EXISTS word_count INT;

-- Backfill existing passages
UPDATE rc_passages SET word_count = array_length(regexp_split_to_array(trim(content), '\s+'), 1)
WHERE word_count IS NULL;
```

### 2b. Add index for passage-grouped serving

```sql
CREATE INDEX IF NOT EXISTS idx_questions_passage ON questions(passage_id) WHERE passage_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_passages_subject ON rc_passages(subject_area);
CREATE INDEX IF NOT EXISTS idx_passages_comparative ON rc_passages(is_comparative);
```

---

## 3. Model Changes

### 3a. Add passage to `DrillQuestion`

The key change: when serving RC questions, include the passage inline so the frontend has everything it needs in one response.

```go
// Update in models/question.go

type DrillQuestion struct {
    ID              int64          `json:"id"`
    Section         Section        `json:"section"`
    LRSubtype       *LRSubtype     `json:"lr_subtype,omitempty"`
    RCSubtype       *RCSubtype     `json:"rc_subtype,omitempty"`
    Difficulty      Difficulty     `json:"difficulty"`
    DifficultyScore int            `json:"difficulty_score"`
    Stimulus        string         `json:"stimulus"`
    QuestionStem    string         `json:"question_stem"`
    Choices         []DrillChoice  `json:"choices"`
    Passage         *DrillPassage  `json:"passage,omitempty"`  // NEW: nil for LR, populated for RC
}

// Backward Compatibility Note:
// - Passage field uses omitempty, so existing LR drill consumers see no change
// - Clients that ignore unknown JSON fields continue working
// - The ADAPTIVE_SYSTEM_SPEC DrillQuestion definition should be updated to include this field

// NEW: Passage data for drill serving (subset of RCPassage)
type DrillPassage struct {
    ID            int64  `json:"id"`
    Title         string `json:"title"`
    SubjectArea   string `json:"subject_area"`
    Content       string `json:"content"`
    IsComparative bool   `json:"is_comparative"`
    PassageB      string `json:"passage_b,omitempty"`
    WordCount     int    `json:"word_count"`
}
```

### 3b. New request type for RC drills

```go
// RC drills serve questions grouped by passage, not individually
type RCDrillRequest struct {
    DifficultySlider int     `json:"difficulty_slider"`
    RCSubtype        *string `json:"rc_subtype,omitempty"`    // optional: filter to one subtype
    Comparative      *bool   `json:"comparative,omitempty"`   // optional: only comparative passages
    Count            int     `json:"count"`                   // questions per passage (default 5-8, max 8)
}

type RCDrillResponse struct {
    Passage   DrillPassage    `json:"passage"`
    Questions []DrillQuestion `json:"questions"`
    Total     int             `json:"total"`
    Page      int             `json:"page"`       // always 1 (no pagination for RC drills)
    PageSize  int             `json:"page_size"`  // matches Total
}
```

### 3c. Add passage quality fields to `RCPassage`

```go
type RCPassage struct {
    ID            int64     `json:"id"`
    BatchID       int64     `json:"batch_id"`
    Title         string    `json:"title"`
    SubjectArea   string    `json:"subject_area"`
    Content       string    `json:"content"`
    IsComparative bool      `json:"is_comparative"`
    PassageB      string    `json:"passage_b,omitempty"`
    WordCount     int       `json:"word_count"`              // NEW
    CreatedAt     time.Time `json:"created_at"`
}
```

---

## 4. Passage-Grouped Drill Serving

### 4a. Core Principle

RC drills are fundamentally different from LR drills:

- **LR**: Each question is independent. A drill of 6 = 6 unrelated questions.
- **RC**: 5-8 questions share one passage. A drill = 1 passage + its question set.

The frontend presents the passage on the left (or top) and cycles through questions on the right (or bottom). Users read the passage once and answer all its questions.

### 4b. RC Drill Flow

```
1. User requests RC drill (with optional subtype filter + difficulty slider)
2. Backend finds a passage whose questions match the user's ability window
3. Backend returns the passage + all its questions (5-8)
4. Frontend shows passage + first question
5. User answers each question in sequence (passage stays visible)
6. After all questions: drill complete screen
```

### 4c. Passage Selection Algorithm

```go
func (s *Service) GetRCDrill(ctx context.Context, userID int64, req models.RCDrillRequest) (*models.RCDrillResponse, error) {
    if req.Count <= 0 || req.Count > 8 {
        req.Count = 0 // 0 means "all questions for this passage"
    }

    // 1. Get user's RC section ability
    ability := s.getAbility(userID, models.ScopeSection, "reading_comprehension")
    target := TargetDifficulty(ability, req.DifficultySlider)
    minDiff := max(0, target-15)
    maxDiff := min(100, target+15)

    // 2. Find a passage with unseen questions in the difficulty window
    passage, questions, err := s.store.GetRCPassageWithQuestions(
        userID, minDiff, maxDiff, req.RCSubtype, req.Comparative, req.Count,
    )

    // 3. Widen window if needed
    if passage == nil {
        minDiff = max(0, target-35)
        maxDiff = min(100, target+35)
        passage, questions, err = s.store.GetRCPassageWithQuestions(
            userID, minDiff, maxDiff, req.RCSubtype, req.Comparative, req.Count,
        )
    }

    // 4. If STILL no passage, generate one synchronously
    if passage == nil {
        difficulty := mapScoreToDifficulty(target)
        genReq := models.GenerateBatchRequest{
            Section:    models.SectionRC,
            Difficulty: difficulty,
            Count:      6,
        }
        if req.RCSubtype != nil {
            sub := models.RCSubtype(*req.RCSubtype)
            genReq.RCSubtype = &sub
        }
        s.GenerateBatch(ctx, genReq)

        // Retry fetch after generation
        passage, questions, err = s.store.GetRCPassageWithQuestions(
            userID, minDiff, maxDiff, req.RCSubtype, req.Comparative, req.Count,
        )
    }

    if passage == nil {
        return nil, fmt.Errorf("no RC passages available")
    }

    // 5. Strip answer data from questions (drill mode)
    drillQuestions := make([]models.DrillQuestion, len(questions))
    for i, q := range questions {
        drillQuestions[i] = q.ToDrillQuestion()
        drillQuestions[i].Passage = nil // Don't duplicate passage in each question
    }

    // 6. Queue background generation if inventory is low
    go s.CheckRCInventory(minDiff, maxDiff, req.RCSubtype)

    return &models.RCDrillResponse{
        Passage:   passage.ToDrillPassage(),
        Questions: drillQuestions,
        Total:     len(drillQuestions),
    }, nil
}
```

### 4d. New Store Method: `GetRCPassageWithQuestions`

```go
func (s *Store) GetRCPassageWithQuestions(
    userID int64,
    minDiff, maxDiff int,
    rcSubtype *string,
    comparative *bool,
    maxQuestions int,
) (*models.RCPassage, []models.Question, error) {

    // Step 1: Find candidate passages that have unseen questions
    // in the difficulty window.
    //
    // Rank passages by how many unseen questions they have, pick the best one.
    candidateQuery := `
        SELECT p.id, p.title, p.subject_area, p.content, p.is_comparative,
               p.passage_b, p.word_count,
               COUNT(q.id) AS total_questions,
               COUNT(q.id) FILTER (WHERE h.id IS NULL) AS unseen_count
        FROM rc_passages p
        JOIN questions q ON q.passage_id = p.id
        LEFT JOIN user_question_history h ON h.question_id = q.id AND h.user_id = $1
        WHERE q.section = 'reading_comprehension'
          AND q.difficulty_score >= $2
          AND q.difficulty_score <= $3
          AND q.validation_status IN ('passed', 'unvalidated')
          AND (q.quality_score >= 0.50 OR q.quality_score IS NULL)
          FILTER_SUBTYPE
          FILTER_COMPARATIVE
        GROUP BY p.id
        HAVING COUNT(q.id) FILTER (WHERE h.id IS NULL) >= 3  -- at least 3 unseen
        ORDER BY unseen_count DESC, RANDOM()
        LIMIT 1
    `

    // Dynamically insert subtype and comparative filters
    if rcSubtype != nil {
        candidateQuery = strings.Replace(candidateQuery, "FILTER_SUBTYPE",
            fmt.Sprintf("AND q.rc_subtype = '%s'", *rcSubtype), 1)
    } else {
        candidateQuery = strings.Replace(candidateQuery, "FILTER_SUBTYPE", "", 1)
    }

    if comparative != nil && *comparative {
        candidateQuery = strings.Replace(candidateQuery, "FILTER_COMPARATIVE",
            "AND p.is_comparative = TRUE", 1)
    } else {
        candidateQuery = strings.Replace(candidateQuery, "FILTER_COMPARATIVE", "", 1)
    }

    // Step 2: Scan the passage
    var passage models.RCPassage
    var totalQuestions, unseenCount int
    err := s.db.QueryRow(candidateQuery, userID, minDiff, maxDiff).Scan(
        &passage.ID, &passage.Title, &passage.SubjectArea, &passage.Content,
        &passage.IsComparative, &passage.PassageB, &passage.WordCount,
        &totalQuestions, &unseenCount,
    )
    if err != nil {
        if err == sql.ErrNoRows {
            return nil, nil, nil
        }
        return nil, nil, fmt.Errorf("find RC passage: %w", err)
    }

    // Step 3: Fetch questions for this passage (unseen first)
    limit := maxQuestions
    if limit <= 0 {
        limit = 8
    }
    questionQuery := `
        SELECT q.id, q.section, q.lr_subtype, q.rc_subtype, q.difficulty,
               q.difficulty_score, q.stimulus, q.question_stem, q.correct_answer_id,
               q.explanation, q.passage_id
        FROM questions q
        LEFT JOIN user_question_history h ON h.question_id = q.id AND h.user_id = $1
        WHERE q.passage_id = $2
          AND q.validation_status IN ('passed', 'unvalidated')
          AND (q.quality_score >= 0.50 OR q.quality_score IS NULL)
        ORDER BY
            CASE WHEN h.id IS NULL THEN 0 ELSE 1 END,
            RANDOM()
        LIMIT $3
    `

    rows, err := s.db.Query(questionQuery, userID, passage.ID, limit)
    if err != nil {
        return nil, nil, fmt.Errorf("fetch passage questions: %w", err)
    }
    defer rows.Close()

    var questions []models.Question
    for rows.Next() {
        var q models.Question
        err := rows.Scan(
            &q.ID, &q.Section, &q.LRSubtype, &q.RCSubtype, &q.Difficulty,
            &q.DifficultyScore, &q.Stimulus, &q.QuestionStem, &q.CorrectAnswerID,
            &q.Explanation, &q.PassageID,
        )
        if err != nil {
            return nil, nil, err
        }

        // Fetch choices for each question
        choiceRows, err := s.db.Query(
            `SELECT id, choice_id, choice_text, explanation, is_correct, wrong_answer_type
             FROM answer_choices WHERE question_id = $1 ORDER BY choice_id`, q.ID)
        if err != nil {
            return nil, nil, err
        }
        for choiceRows.Next() {
            var c models.AnswerChoice
            choiceRows.Scan(&c.ID, &c.ChoiceID, &c.ChoiceText, &c.Explanation,
                &c.IsCorrect, &c.WrongAnswerType)
            q.Choices = append(q.Choices, c)
        }
        choiceRows.Close()

        questions = append(questions, q)
    }

    return &passage, questions, nil
}
```

---

## 5. Passage Delivery in Mixed Drills (Quick Questions)

When Quick Questions mode includes RC subtypes, the backend must also deliver the passage. Two approaches:

### Chosen Approach: Inline Passage on DrillQuestion

For Quick Questions that include RC questions, attach the passage to each RC `DrillQuestion`. This means the response may include the same passage text on multiple questions if they share a passage — but for Quick Questions (6 mixed questions, at most 1-2 RC), this is negligible overhead.

```go
// In GetOneAdaptiveQuestion for RC, also fetch the passage:
if strings.HasPrefix(subtype, "rc_") && passageID != nil {
    passage, _ := s.GetPassage(*passageID)
    dq.Passage = &models.DrillPassage{
        ID:            passage.ID,
        Title:         passage.Title,
        SubjectArea:   passage.SubjectArea,
        Content:       passage.Content,
        IsComparative: passage.IsComparative,
        PassageB:      passage.PassageB,
        WordCount:     passage.WordCount,
    }
}
```

### Store Method: `GetPassage`

```go
func (s *Store) GetPassage(passageID int64) (*models.RCPassage, error) {
    var p models.RCPassage
    err := s.db.QueryRow(`
        SELECT id, batch_id, title, subject_area, content,
               is_comparative, passage_b, word_count, created_at
        FROM rc_passages WHERE id = $1`, passageID,
    ).Scan(&p.ID, &p.BatchID, &p.Title, &p.SubjectArea, &p.Content,
        &p.IsComparative, &p.PassageB, &p.WordCount, &p.CreatedAt)
    if err != nil {
        return nil, fmt.Errorf("get passage: %w", err)
    }
    return &p, nil
}
```

---

## 6. RC-Specific API Endpoints

### 6a. `POST /api/v1/questions/rc-drill` (Protected)

Serves a full RC drill: one passage + its questions.

```
Request:
{
    "difficulty_slider": 50,
    "rc_subtype": "rc_inference",      // optional filter
    "comparative": false,              // optional: only comparative passages
    "count": 0                         // 0 = all questions for the passage (up to 8)
}

Response 200:
{
    "passage": {
        "id": 12,
        "title": "The Evolution of Legal Personhood",
        "subject_area": "law",
        "content": "In recent decades, courts have increasingly grappled with... (450-500 words)",
        "is_comparative": false,
        "passage_b": null,
        "word_count": 478
    },
    "questions": [
        {
            "id": 201,
            "section": "reading_comprehension",
            "rc_subtype": "rc_main_idea",
            "difficulty": "medium",
            "difficulty_score": 52,
            "stimulus": "",
            "question_stem": "The primary purpose of the passage is to...",
            "choices": [
                {"choice_id": "A", "choice_text": "..."},
                {"choice_id": "B", "choice_text": "..."},
                {"choice_id": "C", "choice_text": "..."},
                {"choice_id": "D", "choice_text": "..."},
                {"choice_id": "E", "choice_text": "..."}
            ]
        },
        {
            "id": 202,
            "section": "reading_comprehension",
            "rc_subtype": "rc_detail",
            "difficulty_score": 48,
            "stimulus": "",
            "question_stem": "According to the passage, the author suggests that...",
            "choices": [...]
        },
        // ... 3-6 more questions tied to this passage
    ],
    "total": 6
}
```

Note: `stimulus` is empty for RC questions — the passage IS the stimulus. The `stimulus` field on `Question` was designed for LR where each question has its own argument paragraph.

### 6b. `GET /api/v1/passages/{id}` (Protected)

Standalone passage fetch (useful if frontend needs to re-fetch).

```
Response 200:
{
    "id": 12,
    "title": "The Evolution of Legal Personhood",
    "subject_area": "law",
    "content": "...",
    "is_comparative": false,
    "passage_b": null,
    "word_count": 478
}
```

### 6c. Modified Quick Drill / Subtype Drill Responses

When an RC question appears in a Quick Drill or Subtype Drill response, the `passage` field is populated:

```json
{
    "questions": [
        {
            "id": 42,
            "section": "logical_reasoning",
            "lr_subtype": "strengthen",
            "difficulty_score": 55,
            "stimulus": "Critics argue that the new policy...",
            "question_stem": "Which of the following strengthens...",
            "choices": [...],
            "passage": null
        },
        {
            "id": 203,
            "section": "reading_comprehension",
            "rc_subtype": "rc_inference",
            "difficulty_score": 50,
            "stimulus": "",
            "question_stem": "It can be inferred from the passage that...",
            "choices": [...],
            "passage": {
                "id": 12,
                "title": "The Evolution of Legal Personhood",
                "subject_area": "law",
                "content": "In recent decades...",
                "is_comparative": false,
                "word_count": 478
            }
        }
    ]
}
```

### 6d. Route Registration

Add to `cmd/server/main.go`:

```go
protected.HandleFunc("/questions/rc-drill", questionHandler.RCDrill).Methods("POST")
protected.HandleFunc("/passages/{id}", questionHandler.GetPassage).Methods("GET")
```

---

## 7. RC in Quick Questions — Passage Deduplication

When a Quick Drill returns 2+ RC questions that share the same passage, the passage data is included on each `DrillQuestion`. The frontend deduplicates by `passage.id` — if the current question and the previous question share the same `passage.id`, the passage panel doesn't re-render.

Backend does NOT attempt to deduplicate in the response. This keeps the serving logic simple and lets the frontend handle presentation.

However, the backend SHOULD prefer selecting RC questions from the same passage when filling a Quick Drill with multiple RC questions. This avoids the UX problem of asking the user to read two completely different passages in a 6-question drill:

```go
// In GetQuickDrill, when section includes RC:
// After picking the first RC subtype's question, record its passage_id.
// For subsequent RC subtypes, prefer questions from the same passage.

var rcPassageID *int64
for _, st := range subtypes {
    if !strings.HasPrefix(st, "rc_") {
        // LR: fetch normally
        q, _ := s.store.GetOneAdaptiveQuestion(userID, querySection, st, minDiff, maxDiff)
        if q != nil { questions = append(questions, *q) }
        continue
    }

    // RC: try same passage first, then any passage
    if rcPassageID != nil {
        q, _ := s.store.GetOneAdaptiveQuestionFromPassage(userID, st, *rcPassageID, minDiff, maxDiff)
        if q != nil {
            questions = append(questions, *q)
            continue
        }
    }

    q, _ := s.store.GetOneAdaptiveQuestion(userID, querySection, st, minDiff, maxDiff)
    if q != nil {
        questions = append(questions, *q)
        if q.Passage != nil && rcPassageID == nil {
            rcPassageID = &q.Passage.ID
        }
    }
}
```

### New Store Method

```go
func (s *Store) GetOneAdaptiveQuestionFromPassage(
    userID int64, subtype string, passageID int64, minDiff, maxDiff int,
) (*models.DrillQuestion, error) {
    query := `
        SELECT q.id, q.section, q.lr_subtype, q.rc_subtype, q.difficulty,
               q.difficulty_score, q.stimulus, q.question_stem
        FROM questions q
        LEFT JOIN user_question_history h ON h.question_id = q.id AND h.user_id = $1
        WHERE q.passage_id = $2
          AND q.rc_subtype = $3
          AND q.difficulty_score >= $4
          AND q.difficulty_score <= $5
          AND q.validation_status IN ('passed', 'unvalidated')
          AND (q.quality_score >= 0.50 OR q.quality_score IS NULL)
        ORDER BY
            CASE WHEN h.id IS NULL THEN 0 ELSE 1 END,
            RANDOM()
        LIMIT 1
    `
    // ... scan and return with passage attached
}
```

---

## 8. Subject Area Diversity

### 8a. Tracking

The existing `rc_passages.subject_area` field already stores the subject. Valid values:

| Value | Description |
|-------|-------------|
| `law` | Legal theory, landmark cases, constitutional interpretation |
| `natural_science` | Biology, ecology, climate, geology, physics |
| `social_science` | Economics, psychology, sociology |
| `humanities` | Literary criticism, art history, philosophy, music theory |

### 8b. Generation Rotation

When the generation queue creates new RC passages, rotate subject areas to maintain diversity:

```go
func (s *Service) NextRCSubjectArea() string {
    subjects := []string{"law", "natural_science", "social_science", "humanities"}

    // Query: what subject was the most recently generated passage?
    var lastSubject string
    s.store.db.QueryRow(`
        SELECT subject_area FROM rc_passages ORDER BY created_at DESC LIMIT 1
    `).Scan(&lastSubject)

    // Pick the next one in rotation
    for i, s := range subjects {
        if s == lastSubject {
            return subjects[(i+1)%len(subjects)]
        }
    }
    return subjects[0]
}
```

Update `BuildRCUserPrompt` to accept subject area:

```go
func BuildRCUserPrompt(difficulty models.Difficulty, questionsPerPassage int, subjectArea string) string {
    return fmt.Sprintf(`Generate a Reading Comprehension passage with %d questions.

Difficulty: %s
Subject Area: %s

...`, questionsPerPassage, string(difficulty), subjectArea)
}
```

### 8c. Comparative Passage Ratio

Target: 1 in 4 RC passages should be comparative (matching real LSAT ratio).

```go
func (s *Service) ShouldGenerateComparative() bool {
    var total, comparative int
    s.store.db.QueryRow(`SELECT COUNT(*), COUNT(*) FILTER (WHERE is_comparative) FROM rc_passages`).
        Scan(&total, &comparative)

    if total == 0 { return false }
    ratio := float64(comparative) / float64(total)
    return ratio < 0.25
}
```

Update `BuildRCUserPrompt` to include comparative flag:

```go
func BuildRCUserPrompt(difficulty models.Difficulty, questionsPerPassage int, subjectArea string, comparative bool) string {
    compInstructions := ""
    if comparative {
        compInstructions = `
This should be a COMPARATIVE passage:
- Passage A: ~225 words presenting one perspective
- Passage B: ~225 words presenting a different perspective on the same topic
- Include at least 1 "Passage Relationship" question and 1 "Agreement/Disagreement" question
- Set "is_comparative": true and include "passage_b" in the JSON`
    }

    return fmt.Sprintf(`Generate a Reading Comprehension passage with %d questions.

Difficulty: %s
Subject Area: %s
%s

...`, questionsPerPassage, string(difficulty), subjectArea, compInstructions)
}
```

---

## 9. RC Passage Validation in Three-Stage Pipeline

### 9a. Stage 2 (Self-Verification) — Add Passage Checks

Currently self-verification only validates individual questions. For RC, also verify:

```go
func BuildRCVerificationPrompt(passage *GeneratedPassage, questions []GeneratedQuestion) string {
    return fmt.Sprintf(`You are verifying a Reading Comprehension passage and its questions.

PASSAGE:
Title: %s
Subject: %s
Content: %s

QUESTIONS:
%s

Verify ALL of the following:

PASSAGE QUALITY:
1. Is the passage 450-500 words? (Count approximately)
2. Does it have a clear main thesis that can be identified?
3. Does it contain at least 3 specific, factual details?
4. Does it include author attitude signals (approval, skepticism, etc.)?
5. Does it have structural transitions between paragraphs?
6. Is the writing style academic and LSAT-appropriate?
7. Is the subject area correct: %s?

QUESTION-PASSAGE FIT:
8. Can each question be answered SOLELY from the passage content?
9. Does the main idea question correctly identify the passage's main point?
10. Do detail questions reference specific claims actually in the passage?
11. Are inference questions supported by passage evidence (not requiring outside knowledge)?
12. Do wrong answers contain common LSAT traps (distortion, too broad, out of scope)?

QUESTION VARIETY:
13. Is there at least 1 Main Idea question?
14. Is there at least 1 Inference question?
15. Are correct answers distributed across A-E (not all the same letter)?

Respond with JSON:
{
    "passage_valid": true/false,
    "passage_issues": ["issue1", "issue2"],
    "questions_valid": [true, false, true, ...],
    "question_issues": ["q1: fine", "q2: answer B is too similar to A", ...],
    "overall_confidence": 0.0-1.0
}`, passage.Title, passage.SubjectArea, passage.Content,
        formatQuestionsForVerification(questions), passage.SubjectArea)
}
```

### 9b. Stage 3 (Adversarial) — Passage-Specific Attacks

```go
func BuildRCAdversarialPrompt(passage *GeneratedPassage, questions []GeneratedQuestion) string {
    return fmt.Sprintf(`You are an adversarial reviewer attacking a Reading Comprehension passage and question set.

PASSAGE: %s

QUESTIONS: %s

For EACH question, attempt these attacks:
1. Can you find a DIFFERENT answer that is equally supported by the passage?
2. Is the "correct" answer actually stated or inferable from the passage?
3. Could a careful reader reasonably eliminate the correct answer?
4. Are any wrong answers accidentally correct based on passage content?

For the PASSAGE:
5. Does the passage contain any factual errors?
6. Are there ambiguities that make questions unanswerable?
7. Is the passage internally consistent?

Respond with JSON:
{
    "passage_clean": true/false,
    "passage_attacks": ["attack1", ...],
    "question_attacks": [
        {"question_index": 0, "attack": "...", "severity": "low/medium/high"},
        ...
    ],
    "overall_score": "clean/minor_issues/major_issues"
}`, passage.Content, formatQuestionsForAdversarial(questions))
}
```

### 9c. Integration into `GenerateBatch`

Update the RC path in `service.go`'s `GenerateBatch`:

```go
case models.SectionRC:
    genBatch, llmResp, err = s.generator.GenerateRCBatch(ctx, req.Difficulty, req.Count)
    if err != nil { return nil, err }

    // Stage 2: Verify passage + questions together
    if genBatch.Passage != nil {
        verifyResp, err := s.generator.VerifyRCBatch(ctx, genBatch.Passage, genBatch.Questions)
        if err != nil {
            log.Printf("[validation] RC verification failed: %v", err)
        } else {
            // Apply verification results
            for i, valid := range verifyResp.QuestionsValid {
                if !valid && i < len(genBatch.Questions) {
                    genBatch.Questions[i].Flagged = true
                }
            }
            if !verifyResp.PassageValid {
                log.Printf("[validation] RC passage failed: %v", verifyResp.PassageIssues)
                // Flag all questions in this passage
                for i := range genBatch.Questions {
                    genBatch.Questions[i].Flagged = true
                }
            }
        }

        // Stage 3: Adversarial check
        advResp, err := s.generator.AdversarialCheckRC(ctx, genBatch.Passage, genBatch.Questions)
        if err != nil {
            log.Printf("[validation] RC adversarial check failed: %v", err)
        } else {
            for _, attack := range advResp.QuestionAttacks {
                if attack.Severity == "high" && attack.QuestionIndex < len(genBatch.Questions) {
                    genBatch.Questions[attack.QuestionIndex].Flagged = true
                }
            }
        }
    }
```

---

## 10. RC Generation Queue Enhancements

### 10a. Passage-Level Inventory Check

Instead of checking question count per subtype (like LR), RC inventory should be checked at the **passage level**: how many passages exist in each difficulty bucket?

```go
func (s *Service) CheckRCInventory(minDiff, maxDiff int, rcSubtype *string) {
    // Count distinct passages with questions in the difficulty range
    count := s.store.CountRCPassagesInBucket(minDiff, maxDiff)

    if count < 3 { // Want at least 3 passages per bucket
        needed := 3 - count
        for i := 0; i < needed; i++ {
            subjectArea := s.NextRCSubjectArea()
            comparative := s.ShouldGenerateComparative()
            s.store.UpsertRCGenerationQueue(
                minDiff, maxDiff,
                mapScoreToDifficulty(avg(minDiff, maxDiff)),
                subjectArea, comparative,
            )
        }
    }
}
```

### 10b. Modified Generation Queue Table

Add RC-specific fields:

```sql
ALTER TABLE generation_queue ADD COLUMN IF NOT EXISTS subject_area VARCHAR(50);
ALTER TABLE generation_queue ADD COLUMN IF NOT EXISTS is_comparative BOOLEAN DEFAULT FALSE;
```

### 10c. RC Queue Processing

```go
func (s *Service) processRCGeneration(ctx context.Context, item models.GenerationQueueItem) error {
    questionsPerPassage := 6
    if item.IsComparative {
        questionsPerPassage = 7 // Comparative passages get extra relationship questions
    }

    genBatch, _, err := s.generator.GenerateRCBatch(ctx, models.Difficulty(item.TargetDifficulty), questionsPerPassage)
    if err != nil {
        return err
    }

    // Override subject area if specified
    if item.SubjectArea != "" && genBatch.Passage != nil {
        genBatch.Passage.SubjectArea = item.SubjectArea
    }

    // Save with validation
    return s.store.SaveGeneratedBatch(batchID, models.SectionRC, nil, nil,
        models.Difficulty(item.TargetDifficulty), genBatch)
}
```

---

## 11. Word Count Computation

Calculate and store word count when saving passages:

```go
func wordCount(text string) int {
    return len(strings.Fields(text))
}

// In SaveGeneratedBatch, when inserting passage:
wc := wordCount(batch.Passage.Content)
if batch.Passage.IsComparative && batch.Passage.PassageB != "" {
    wc += wordCount(batch.Passage.PassageB)
}

err := tx.QueryRow(
    `INSERT INTO rc_passages (batch_id, title, subject_area, content, is_comparative, passage_b, word_count)
     VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
    batchID, batch.Passage.Title, subjectArea, batch.Passage.Content,
    batch.Passage.IsComparative, nullString(batch.Passage.PassageB), wc,
).Scan(&pid)
```

---

## 12. Handler Methods

### 12a. `RCDrill`

```go
func (h *Handler) RCDrill(w http.ResponseWriter, r *http.Request) {
    userID := middleware.GetUserID(r)

    var req models.RCDrillRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid request"})
        return
    }

    // Validate optional RC subtype
    if req.RCSubtype != nil {
        if !models.ValidRCSubtypes[models.RCSubtype(*req.RCSubtype)] {
            writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid RC subtype"})
            return
        }
    }

    resp, err := h.service.GetRCDrill(r.Context(), userID, req)
    if err != nil {
        writeJSON(w, http.StatusInternalServerError, models.ErrorResponse{Error: err.Error()})
        return
    }

    writeJSON(w, http.StatusOK, resp)
}
```

### 12b. `GetPassage`

```go
func (h *Handler) GetPassage(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    passageID, err := strconv.ParseInt(vars["id"], 10, 64)
    if err != nil {
        writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid passage ID"})
        return
    }

    passage, err := h.service.store.GetPassage(passageID)
    if err != nil {
        writeJSON(w, http.StatusNotFound, models.ErrorResponse{Error: "passage not found"})
        return
    }

    writeJSON(w, http.StatusOK, passage.ToDrillPassage())
}
```

---

## 13. Gamification Integration

RC drills integrate with the existing gamification system (GAMIFICATION_SPEC.md) identically to LR drills:

### 13a. XP Rewards

RC questions award XP using the **same formula** as LR questions. No passage-level bonus — XP is per-question:

- BaseXP by difficulty score (same scale)
- ChallengeBonus (ability gap)
- ComboXP (consecutive correct across the passage's questions)
- TimeBonus (average time per question)
- StreakMultiplier (current daily streak)

### 13b. Drill Completion

An RC drill is "complete" when the user has answered all questions for the passage (5-8 questions). This triggers:

- `POST /drills/complete` with the full XP breakdown
- Daily goal progress increments by number of questions answered
- Streak is maintained (at least 1 question answered today)
- Combo tracking spans all questions in the passage (max possible combo = total questions)

### 13c. Ability Updates

Each RC answer updates three ability scores (same as LR):

1. **Overall** ability
2. **Section** ability (`reading_comprehension`)
3. **Subtype** ability (e.g., `rc_inference`)

The Elo-based algorithm is identical. RC and LR ability scores are tracked independently at the section and subtype levels but contribute to the same overall score.

---

## 14. Unified Inventory Check

The existing `CheckAndQueueGeneration()` from ADAPTIVE_SYSTEM_SPEC should be updated to handle both LR and RC:

```go
func (s *Service) CheckAndQueueGeneration(section string, subtype *string, minDiff, maxDiff int) {
    if section == "reading_comprehension" || (subtype != nil && strings.HasPrefix(*subtype, "rc_")) {
        // RC: Check at passage level
        s.CheckRCInventory(minDiff, maxDiff, subtype)
    } else {
        // LR: Check at question level (existing logic)
        s.checkLRInventory(section, subtype, minDiff, maxDiff)
    }
}
```

This ensures a single entry point for inventory management regardless of section.

---

## 15. Constants

Centralized thresholds referenced across this spec and ADAPTIVE_SYSTEM_SPEC:

```go
const (
    MinQualityScore          = 0.50  // Minimum quality_score to serve a question
    MinPassagesPerBucket     = 3     // RC: minimum passages per difficulty bucket
    MinQuestionsPerBucket    = 6     // LR: minimum questions per difficulty bucket
    MinUnseenPassageQuestions = 3    // RC: minimum unseen questions to select a passage
    DifficultyWindowDefault  = 15    // ± points from target difficulty
    DifficultyWindowMax      = 35    // Maximum window expansion
)
```

---

## 16. Testing

### Unit Tests

```go
func TestGetRCPassageWithQuestions(t *testing.T) {
    // Setup: Insert a passage with 6 questions
    // Assert: Returns passage + all 6 questions
    // Assert: Unseen questions come first
}

func TestGetRCPassageWithQuestions_SubtypeFilter(t *testing.T) {
    // Setup: Passage with 6 questions of mixed subtypes
    // Request: Filter to rc_inference only
    // Assert: Returns only inference questions for this passage
}

func TestGetRCPassageWithQuestions_NoResults(t *testing.T) {
    // Setup: No passages in difficulty window
    // Assert: Returns nil, nil, nil (triggers synchronous generation)
}

func TestRCDrill_ComparativePassage(t *testing.T) {
    // Setup: Comparative passage with passage_b
    // Assert: Response includes both passage content and passage_b
}

func TestQuickDrill_RCPassageInline(t *testing.T) {
    // Setup: Quick drill with section="both"
    // Request: Get 6 mixed questions
    // Assert: RC questions have passage populated, LR questions have passage=null
}

func TestQuickDrill_RCPassageDedup(t *testing.T) {
    // Setup: Quick drill picks 2 RC subtypes
    // Assert: Both RC questions come from the same passage when possible
}

func TestNextRCSubjectArea_Rotation(t *testing.T) {
    // Insert passages: law, law, law
    // Assert: Next subject is natural_science
}

func TestShouldGenerateComparative_Below25(t *testing.T) {
    // Insert 10 passages, 1 comparative
    // Assert: ShouldGenerateComparative() returns true (10% < 25%)
}

func TestRCVerification_PassageFails(t *testing.T) {
    // Mock verification response with passage_valid=false
    // Assert: All questions in batch are flagged
}

func TestWordCount(t *testing.T) {
    assert.Equal(t, 5, wordCount("one two three four five"))
    assert.Equal(t, 0, wordCount(""))
    assert.Equal(t, 1, wordCount("hello"))
}
```

### Integration Tests

```go
func TestRCDrill_EndToEnd(t *testing.T) {
    // 1. Generate an RC batch
    // 2. Request RC drill
    // 3. Assert: passage + questions returned
    // 4. Answer all questions
    // 5. Assert: ability scores updated for RC subtypes
    // 6. Request another RC drill
    // 7. Assert: previously-seen questions ranked lower
}

func TestRCDrill_ColdStart(t *testing.T) {
    // 1. Empty database
    // 2. Request RC drill
    // 3. Assert: synchronous generation triggers
    // 4. Assert: passage + questions returned (within 120s timeout)
}
```

---

## 17. Implementation Order

1. Add `word_count` column to `rc_passages` + backfill migration
2. Add `DrillPassage` and update `DrillQuestion` model with `Passage` field
3. Add `RCDrillRequest` / `RCDrillResponse` models
4. Implement `GetRCPassageWithQuestions` store method
5. Implement `GetPassage` store method
6. Implement `GetOneAdaptiveQuestionFromPassage` store method
7. Implement `GetRCDrill` service method (passage selection + serving)
8. Update `GetOneAdaptiveQuestion` to attach passage for RC questions
9. Update `GetQuickDrill` for passage deduplication across RC questions
10. Add `subject_area` and `is_comparative` to generation queue
11. Implement `NextRCSubjectArea()` and `ShouldGenerateComparative()`
12. Update `BuildRCUserPrompt` with subject area + comparative params
13. Add RC-specific verification prompt (`BuildRCVerificationPrompt`)
14. Add RC-specific adversarial prompt (`BuildRCAdversarialPrompt`)
15. Wire RC validation into `GenerateBatch`
16. Implement `CheckRCInventory` and RC queue processing
17. Add handler methods + route registration
18. Compute `word_count` on passage save
19. Tests
