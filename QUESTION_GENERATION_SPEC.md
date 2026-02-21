# Backend Spec — AI Question Generation System

## Overview

Add a question generation pipeline to the Go backend that calls Anthropic's Claude API to produce LSAT-quality questions in batches. Generated questions are stored in Postgres and served to the frontend via REST endpoints.

---

## 1. New Dependency

```
github.com/anthropics/anthropic-sdk-go  (Anthropic's official Go SDK)
```

Add to `go.mod`. The SDK handles auth, retries, and streaming natively.

---

## 2. Environment Variables

Add to `.env.example` and `docker-compose.yml`:

```
ANTHROPIC_API_KEY=sk-ant-...          # Required — Anthropic API key
ANTHROPIC_MODEL=claude-opus-4-5-20251101  # Claude Opus 4.5 — best for legal reasoning
QUESTION_BATCH_SIZE=6                 # Questions per generation batch (default 6)
```

`claude-opus-4-5-20251101` is Anthropic's most advanced model and the strongest choice for producing legally rigorous LSAT-caliber arguments. This model should be used for all question generation.

---

## 3. Database Schema — New Tables

Add to the `Migrate()` function in `internal/database/database.go`:

### `question_batches`

```sql
CREATE TABLE IF NOT EXISTS question_batches (
    id              BIGSERIAL PRIMARY KEY,
    section         VARCHAR(50) NOT NULL,          -- 'logical_reasoning' or 'reading_comprehension'
    lr_subtype      VARCHAR(50),                   -- null for RC; e.g. 'strengthen', 'flaw'
    difficulty      VARCHAR(20) NOT NULL,          -- 'easy', 'medium', 'hard'
    status          VARCHAR(20) NOT NULL DEFAULT 'pending',  -- 'pending', 'generating', 'completed', 'failed'
    question_count  INT NOT NULL DEFAULT 0,
    model_used      VARCHAR(100),                  -- e.g. 'claude-opus-4-5-20251101'
    prompt_tokens   INT,
    output_tokens   INT,
    generation_time_ms INT,
    error_message   TEXT,
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    completed_at    TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_batches_status ON question_batches(status);
CREATE INDEX IF NOT EXISTS idx_batches_section ON question_batches(section, lr_subtype);
```

### `questions`

```sql
CREATE TABLE IF NOT EXISTS questions (
    id                  BIGSERIAL PRIMARY KEY,
    batch_id            BIGINT NOT NULL REFERENCES question_batches(id),
    section             VARCHAR(50) NOT NULL,
    lr_subtype          VARCHAR(50),
    difficulty          VARCHAR(20) NOT NULL,
    stimulus            TEXT NOT NULL,                -- 4-7 sentences, the argument/scenario
    question_stem       TEXT NOT NULL,                -- "Which of the following..."
    correct_answer_id   VARCHAR(1) NOT NULL,          -- 'A', 'B', 'C', 'D', or 'E'
    explanation         TEXT NOT NULL,                -- Why the correct answer is correct
    passage_id          BIGINT REFERENCES rc_passages(id),  -- FK for RC questions, null for LR
    quality_score       DECIMAL(3,2),                -- Optional: 0.00-1.00 quality rating
    flagged             BOOLEAN DEFAULT FALSE,       -- Manual flag for review
    times_served        INT DEFAULT 0,
    times_correct       INT DEFAULT 0,
    created_at          TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_questions_batch ON questions(batch_id);
CREATE INDEX IF NOT EXISTS idx_questions_section ON questions(section, lr_subtype, difficulty);
CREATE INDEX IF NOT EXISTS idx_questions_serving ON questions(section, lr_subtype, difficulty, times_served);
```

### `answer_choices`

```sql
CREATE TABLE IF NOT EXISTS answer_choices (
    id              BIGSERIAL PRIMARY KEY,
    question_id     BIGINT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    choice_id       VARCHAR(1) NOT NULL,             -- 'A', 'B', 'C', 'D', 'E'
    choice_text     TEXT NOT NULL,                    -- 1-2 sentences
    explanation     TEXT NOT NULL,                    -- Why this choice is right/wrong
    is_correct      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(question_id, choice_id)
);

CREATE INDEX IF NOT EXISTS idx_choices_question ON answer_choices(question_id);
```

### `rc_passages` (for Reading Comprehension)

```sql
CREATE TABLE IF NOT EXISTS rc_passages (
    id              BIGSERIAL PRIMARY KEY,
    batch_id        BIGINT NOT NULL REFERENCES question_batches(id),
    title           VARCHAR(255) NOT NULL,           -- e.g. "Environmental Law and Policy"
    content         TEXT NOT NULL,                    -- ~450 words
    is_comparative  BOOLEAN DEFAULT FALSE,
    passage_b       TEXT,                             -- second passage if comparative
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
```

---

## 4. New File Structure

```
internal/
├── auth/           # Existing
├── database/       # Existing — add new migrations
├── middleware/      # Existing
├── models/
│   ├── user.go     # Existing
│   └── question.go # NEW — Question, Batch, AnswerChoice, Passage models
├── questions/
│   ├── handler.go  # NEW — HTTP handlers for question endpoints
│   ├── service.go  # NEW — business logic, batch orchestration
│   └── store.go    # NEW — Postgres CRUD operations
└── generator/
    ├── client.go   # NEW — Anthropic SDK wrapper
    ├── prompts.go  # NEW — system + user prompts by section/subtype
    └── parser.go   # NEW — parse Claude JSON response → Go structs
```

---

## 5. Models — `internal/models/question.go`

```go
package models

import "time"

// Enums as string constants for DB compatibility
type Section string
const (
    SectionLR Section = "logical_reasoning"
    SectionRC Section = "reading_comprehension"
)

type LRSubtype string
const (
    SubtypeStrengthen       LRSubtype = "strengthen"
    SubtypeWeaken           LRSubtype = "weaken"
    SubtypeAssumption       LRSubtype = "assumption"
    SubtypeFlaw             LRSubtype = "flaw"
    SubtypeMustBeTrue       LRSubtype = "must_be_true"
    SubtypeMostStrongly     LRSubtype = "most_strongly_supported"
    SubtypeMethodReasoning  LRSubtype = "method_of_reasoning"
    SubtypeParallelReasoning LRSubtype = "parallel_reasoning"
    SubtypeParallelFlaw     LRSubtype = "parallel_flaw"
    SubtypePrinciple        LRSubtype = "principle"
    SubtypeApplyPrinciple   LRSubtype = "apply_principle"
    SubtypeEvaluate         LRSubtype = "evaluate"
    SubtypeMainConclusion   LRSubtype = "main_conclusion"
    SubtypeRoleOfStatement  LRSubtype = "role_of_statement"
)

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
    BatchCompleted  BatchStatus = "completed"
    BatchFailed     BatchStatus = "failed"
)

type QuestionBatch struct {
    ID              int64       `json:"id"`
    Section         Section     `json:"section"`
    LRSubtype       *LRSubtype  `json:"lr_subtype,omitempty"`
    Difficulty      Difficulty  `json:"difficulty"`
    Status          BatchStatus `json:"status"`
    QuestionCount   int         `json:"question_count"`
    ModelUsed       string      `json:"model_used,omitempty"`
    PromptTokens    int         `json:"prompt_tokens,omitempty"`
    OutputTokens    int         `json:"output_tokens,omitempty"`
    GenerationTimeMs int        `json:"generation_time_ms,omitempty"`
    ErrorMessage    *string     `json:"error_message,omitempty"`
    CreatedAt       time.Time   `json:"created_at"`
    CompletedAt     *time.Time  `json:"completed_at,omitempty"`
}

type Question struct {
    ID              int64        `json:"id"`
    BatchID         int64        `json:"batch_id"`
    Section         Section      `json:"section"`
    LRSubtype       *LRSubtype   `json:"lr_subtype,omitempty"`
    Difficulty      Difficulty   `json:"difficulty"`
    Stimulus        string       `json:"stimulus"`
    QuestionStem    string       `json:"question_stem"`
    CorrectAnswerID string       `json:"correct_answer_id"`
    Explanation     string       `json:"explanation"`
    PassageID       *int64       `json:"passage_id,omitempty"`
    Choices         []AnswerChoice `json:"choices"`
    QualityScore    *float64     `json:"quality_score,omitempty"`
    Flagged         bool         `json:"flagged"`
    TimesServed     int          `json:"times_served"`
    TimesCorrect    int          `json:"times_correct"`
    CreatedAt       time.Time    `json:"created_at"`
}

type AnswerChoice struct {
    ID          int64  `json:"id"`
    QuestionID  int64  `json:"question_id"`
    ChoiceID    string `json:"choice_id"`      // "A" through "E"
    ChoiceText  string `json:"choice_text"`
    Explanation string `json:"explanation"`     // Why right or wrong
    IsCorrect   bool   `json:"is_correct"`
}

type RCPassage struct {
    ID            int64  `json:"id"`
    BatchID       int64  `json:"batch_id"`
    Title         string `json:"title"`
    Content       string `json:"content"`
    IsComparative bool   `json:"is_comparative"`
    PassageB      string `json:"passage_b,omitempty"`
}

// ── Request / Response Types ─────────────────────────────

type GenerateBatchRequest struct {
    Section    Section    `json:"section"`
    LRSubtype  *LRSubtype `json:"lr_subtype,omitempty"`   // Required for LR
    Difficulty Difficulty `json:"difficulty"`
    Count      int        `json:"count"`                  // Number of questions (default 6)
}

type GenerateBatchResponse struct {
    BatchID int64       `json:"batch_id"`
    Status  BatchStatus `json:"status"`
    Message string      `json:"message"`
}

type QuestionListResponse struct {
    Questions []Question `json:"questions"`
    Total     int        `json:"total"`
    Page      int        `json:"page"`
    PageSize  int        `json:"page_size"`
}
```

---

## 6. Generator — Anthropic Integration

### 6.1 `internal/generator/client.go`

Wraps the Anthropic Go SDK:

```go
type Generator struct {
    client *anthropic.Client
    model  string
}

func NewGenerator() *Generator {
    client := anthropic.NewClient()  // reads ANTHROPIC_API_KEY from env
    model := os.Getenv("ANTHROPIC_MODEL")
    if model == "" {
        model = "claude-opus-4-5-20251101"
    }
    return &Generator{client: client, model: model}
}

func (g *Generator) GenerateLRBatch(ctx context.Context, subtype LRSubtype, difficulty Difficulty, count int) (*GeneratedBatch, error)
func (g *Generator) GenerateRCBatch(ctx context.Context, difficulty Difficulty, questionsPerPassage int) (*GeneratedBatch, error)
```

Key implementation details:
- Set `max_tokens: 8192` — batches of 6 questions with full explanations need room
- Set `temperature: 1.0` — maximizes variety across batches; the system prompt constraints keep quality high
- Use structured JSON output via `response_format` or by instructing JSON in the prompt and parsing
- Include retry logic: 1 retry on 429/500, exponential backoff
- Track `prompt_tokens` and `output_tokens` from the response `usage` field for cost monitoring

### 6.2 `internal/generator/prompts.go`

The prompt architecture is the most critical piece. Two-part structure: **system prompt** (constant, sets the persona and quality bar) + **user prompt** (varies per batch request).

#### System Prompt (all LR questions)

```
You are an expert LSAT question writer with 20 years of experience at the Law School Admission Council (LSAC). You write questions that are indistinguishable from real LSAT Logical Reasoning questions.

Your questions must follow these exact structural rules:

STIMULUS:
- 4-7 sentences presenting an argument, scenario, or set of facts
- Contains a clear logical structure: premises leading to a conclusion
- Uses formal but accessible language — the register of a newspaper editorial, academic summary, or public policy statement
- Covers diverse topics: science, law, history, business, ethics, environment, arts, social policy, technology
- Never references the LSAT itself or test-taking
- Each stimulus must present a self-contained argument — no external knowledge needed

QUESTION STEM:
- One sentence that clearly asks the student what to do
- Uses standard LSAT phrasing for the question type (provided below)

ANSWER CHOICES:
- Exactly 5 choices labeled A through E
- Each choice is 1-2 sentences
- Exactly ONE correct answer
- The 4 wrong answers must each be wrong for a specific, identifiable reason
- Wrong answers should be plausible — they must be genuinely tempting, not obviously dismissable
- At least one wrong answer should be a "close second" that tests the most common mistake for this question type
- Choices should vary in structure and length — not all the same sentence pattern

EXPLANATIONS:
- For the correct answer: 2-4 sentences explaining precisely WHY it is correct, referencing the logical structure of the stimulus
- For each wrong answer: 1-2 sentences explaining precisely WHY it is wrong — name the specific logical error (e.g., "irrelevant comparison," "out of scope," "reverses the relationship")

DIFFICULTY CALIBRATION:
- Easy: Straightforward argument with a clear flaw/assumption. The correct answer is noticeably stronger than alternatives. One strong distractor.
- Medium: More nuanced argument, possibly with multiple premises. Two strong distractors. Requires careful reading.
- Hard: Complex argument with subtle reasoning. The correct and "close second" answers require distinguishing between very similar logical moves. Three strong distractors.

You must respond with valid JSON only. No markdown, no explanation outside the JSON.
```

#### User Prompt Template (LR)

```
Generate exactly {count} LSAT Logical Reasoning questions.

Section: Logical Reasoning
Question type: {subtype}
Difficulty: {difficulty}

Standard question stems for this type:
{subtype_stems}

Respond with this exact JSON structure:
{
  "questions": [
    {
      "stimulus": "...",
      "question_stem": "...",
      "choices": [
        {"id": "A", "text": "...", "explanation": "..."},
        {"id": "B", "text": "...", "explanation": "..."},
        {"id": "C", "text": "...", "explanation": "..."},
        {"id": "D", "text": "...", "explanation": "..."},
        {"id": "E", "text": "...", "explanation": "..."}
      ],
      "correct_answer_id": "B",
      "explanation": "..."
    }
  ]
}

Requirements:
- Each question must cover a DIFFERENT topic — no two questions in the same batch about the same subject
- Vary the position of the correct answer across A-E — do not cluster correct answers
- The correct answer position distribution across the batch should be roughly uniform
```

#### Subtype Stem Bank (include in user prompt per subtype)

```go
var subtypeStems = map[LRSubtype][]string{
    SubtypeStrengthen: {
        "Which of the following, if true, most strengthens the argument?",
        "Which of the following, if true, most strongly supports the argument above?",
    },
    SubtypeWeaken: {
        "Which of the following, if true, most weakens the argument?",
        "Which of the following, if true, most seriously undermines the argument above?",
    },
    SubtypeAssumption: {
        "The argument relies on which of the following assumptions?",
        "Which of the following is an assumption on which the argument depends?",
        "The argument assumes which of the following?",
    },
    SubtypeFlaw: {
        "The reasoning in the argument is flawed because it",
        "The reasoning in the argument is most vulnerable to criticism because it",
        "Which of the following most accurately describes a flaw in the argument?",
    },
    SubtypeMustBeTrue: {
        "If the statements above are true, which of the following must also be true?",
        "Which of the following can be properly inferred from the statements above?",
    },
    SubtypeMainConclusion: {
        "Which of the following most accurately expresses the main conclusion of the argument?",
        "The main point of the argument is that",
    },
    SubtypeMethodReasoning: {
        "The argument proceeds by",
        "Which of the following most accurately describes the method of reasoning used in the argument?",
    },
    SubtypeEvaluate: {
        "Which of the following would be most useful to know in order to evaluate the argument?",
        "The answer to which of the following questions would most help in evaluating the argument?",
    },
    SubtypePrinciple: {
        "Which of the following principles, if valid, most helps to justify the reasoning above?",
    },
    SubtypeParallelReasoning: {
        "Which of the following arguments is most similar in its pattern of reasoning to the argument above?",
    },
    // ... remaining subtypes
}
```

#### RC System Prompt Addition

For Reading Comprehension batches, add to the system prompt:

```
PASSAGE:
- Approximately 400-500 words
- Written in the style of academic or professional writing
- Organized with a clear thesis/main idea
- Contains enough detail for 5-8 specific questions
- Topics: law, natural science, social science, humanities
- For comparative passages: two passages of ~200 words each with a shared topic but different perspectives

RC QUESTION TYPES:
- Main idea / primary purpose
- Specific detail / according to the passage
- Inference / the author would most likely agree
- Author's tone / attitude
- Function of a phrase or paragraph
- Strengthen/weaken (passage-based)
```

### 6.3 `internal/generator/parser.go`

Parses Claude's JSON response into Go structs:

```go
type GeneratedBatch struct {
    Questions []GeneratedQuestion `json:"questions"`
    Passage   *GeneratedPassage   `json:"passage,omitempty"`  // Only for RC
}

type GeneratedQuestion struct {
    Stimulus        string            `json:"stimulus"`
    QuestionStem    string            `json:"question_stem"`
    Choices         []GeneratedChoice `json:"choices"`
    CorrectAnswerID string            `json:"correct_answer_id"`
    Explanation     string            `json:"explanation"`
}

type GeneratedChoice struct {
    ID          string `json:"id"`
    Text        string `json:"text"`
    Explanation string `json:"explanation"`
}

func ParseLRResponse(responseBody string) (*GeneratedBatch, error)
func ParseRCResponse(responseBody string) (*GeneratedBatch, error)
```

Validation in the parser:
- Confirm exactly 5 choices per question (A–E)
- Confirm `correct_answer_id` matches one of the choice IDs
- Confirm stimulus length is 100–500 characters (roughly 4–7 sentences)
- Confirm each choice text is 20–300 characters (1–2 sentences)
- Confirm explanations are non-empty
- If validation fails, return a typed `ValidationError` — do NOT store the batch

---

## 7. Question Service — `internal/questions/service.go`

Orchestrates the generation flow:

```go
type Service struct {
    store     *Store
    generator *generator.Generator
}

func (s *Service) GenerateBatch(ctx context.Context, req models.GenerateBatchRequest) (*models.QuestionBatch, error) {
    // 1. Create batch record with status "pending"
    batch := s.store.CreateBatch(req)

    // 2. Update status to "generating"
    s.store.UpdateBatchStatus(batch.ID, models.BatchGenerating)

    // 3. Call Anthropic API
    startTime := time.Now()
    var generated *generator.GeneratedBatch
    var err error

    if req.Section == models.SectionLR {
        generated, err = s.generator.GenerateLRBatch(ctx, *req.LRSubtype, req.Difficulty, req.Count)
    } else {
        generated, err = s.generator.GenerateRCBatch(ctx, req.Difficulty, req.Count)
    }

    // 4. Handle failure
    if err != nil {
        s.store.FailBatch(batch.ID, err.Error())
        return batch, err
    }

    // 5. Store questions + choices in a transaction
    err = s.store.SaveGeneratedBatch(batch.ID, generated, req)

    // 6. Update batch to "completed" with token counts + timing
    elapsed := time.Since(startTime).Milliseconds()
    s.store.CompleteBatch(batch.ID, len(generated.Questions), elapsed, tokenCounts)

    return batch, nil
}
```

**Important:** Generation runs synchronously on the request. For a future iteration, move to async with a job queue, but for now keep it simple — the API caller waits for the response (typically 15–30 seconds for a 6-question batch with Opus).

---

## 8. Question Store — `internal/questions/store.go`

Postgres CRUD:

```go
type Store struct {
    db *sql.DB
}

func (s *Store) CreateBatch(req models.GenerateBatchRequest) (*models.QuestionBatch, error)
func (s *Store) UpdateBatchStatus(batchID int64, status models.BatchStatus) error
func (s *Store) FailBatch(batchID int64, errMsg string) error
func (s *Store) CompleteBatch(batchID int64, count int, timeMs int64, tokens TokenCounts) error
func (s *Store) SaveGeneratedBatch(batchID int64, batch *generator.GeneratedBatch, req models.GenerateBatchRequest) error

// Serving questions to users
func (s *Store) GetQuestionsBySubtype(section models.Section, subtype *models.LRSubtype, difficulty models.Difficulty, limit int, offset int) ([]models.Question, int, error)
func (s *Store) GetQuestionWithChoices(questionID int64) (*models.Question, error)
func (s *Store) GetDrillQuestions(section models.Section, subtype *models.LRSubtype, difficulty models.Difficulty, count int) ([]models.Question, error)
func (s *Store) IncrementServed(questionID int64) error
func (s *Store) IncrementCorrect(questionID int64) error

// Batch management
func (s *Store) GetBatch(batchID int64) (*models.QuestionBatch, error)
func (s *Store) ListBatches(status *models.BatchStatus, limit int, offset int) ([]models.QuestionBatch, error)
```

The `GetDrillQuestions` function should:
1. Select questions matching section/subtype/difficulty
2. Order by `times_served ASC` (prioritize least-served questions)
3. Limit to `count`
4. Eagerly load `answer_choices` for each question
5. Return fully hydrated `Question` structs with `Choices` populated

---

## 9. HTTP Handler — `internal/questions/handler.go`

```go
type Handler struct {
    service *Service
}

func NewHandler(service *Service) *Handler
```

### Endpoints to register in `main.go`:

```go
// Admin / generation endpoints (protected)
protected.HandleFunc("/questions/generate", questionHandler.GenerateBatch).Methods("POST")
protected.HandleFunc("/questions/batches", questionHandler.ListBatches).Methods("GET")
protected.HandleFunc("/questions/batches/{id}", questionHandler.GetBatch).Methods("GET")

// Question serving endpoints (protected)
protected.HandleFunc("/questions/drill", questionHandler.GetDrill).Methods("GET")
protected.HandleFunc("/questions/{id}", questionHandler.GetQuestion).Methods("GET")
protected.HandleFunc("/questions/{id}/answer", questionHandler.SubmitAnswer).Methods("POST")
```

### Endpoint Details

#### `POST /api/v1/questions/generate`

Triggers a new batch generation. Admin-only in production (add role check later).

Request:
```json
{
  "section": "logical_reasoning",
  "lr_subtype": "strengthen",
  "difficulty": "medium",
  "count": 6
}
```

Response (201):
```json
{
  "batch_id": 42,
  "status": "completed",
  "message": "Generated 6 questions"
}
```

#### `GET /api/v1/questions/drill?section=logical_reasoning&subtype=strengthen&difficulty=medium&count=6`

Returns a set of questions for a drill session. This is the primary frontend-facing endpoint.

Response (200):
```json
{
  "questions": [
    {
      "id": 101,
      "section": "logical_reasoning",
      "lr_subtype": "strengthen",
      "difficulty": "medium",
      "stimulus": "A recent study...",
      "question_stem": "Which of the following...",
      "choices": [
        {"choice_id": "A", "choice_text": "The commuters..."},
        {"choice_id": "B", "choice_text": "The study..."},
        {"choice_id": "C", "choice_text": "Some of..."},
        {"choice_id": "D", "choice_text": "Public transit..."},
        {"choice_id": "E", "choice_text": "Stress levels..."}
      ]
    }
  ],
  "total": 6,
  "page": 1,
  "page_size": 6
}
```

**Note:** The drill endpoint does NOT include `correct_answer_id`, `explanation`, or choice-level explanations. Those are only returned after the user submits an answer.

#### `POST /api/v1/questions/{id}/answer`

Submit an answer and get feedback.

Request:
```json
{
  "selected_choice_id": "B"
}
```

Response (200):
```json
{
  "correct": true,
  "correct_answer_id": "B",
  "explanation": "The argument concludes that...",
  "choices": [
    {"choice_id": "A", "choice_text": "...", "explanation": "Incorrect: This is irrelevant because...", "is_correct": false},
    {"choice_id": "B", "choice_text": "...", "explanation": "Correct: This strengthens by...", "is_correct": true},
    {"choice_id": "C", "choice_text": "...", "explanation": "Incorrect: This actually weakens...", "is_correct": false},
    {"choice_id": "D", "choice_text": "...", "explanation": "Incorrect: Out of scope...", "is_correct": false},
    {"choice_id": "E", "choice_text": "...", "explanation": "Incorrect: This is about measurement...", "is_correct": false}
  ]
}
```

This endpoint also:
- Calls `store.IncrementServed(questionID)`
- If correct, calls `store.IncrementCorrect(questionID)`
- Returns all 5 choice explanations so the user can learn from every option

#### `GET /api/v1/questions/batches`

List all generation batches. Query params: `?status=completed&limit=20&offset=0`

#### `GET /api/v1/questions/batches/{id}`

Get a single batch with its questions.

---

## 10. Quality Guardrails

### 10.1 Post-Generation Validation

After parsing Claude's response, run these checks before storing:

| Check | Rule | On Fail |
|---|---|---|
| Choice count | Exactly 5 per question | Reject entire batch |
| Correct answer | `correct_answer_id` ∈ {A,B,C,D,E} | Reject question |
| Stimulus length | 100–600 chars | Reject question |
| Choice text length | 20–400 chars each | Reject question |
| Explanation present | Non-empty for correct + all wrong | Reject question |
| Unique correct answers | No more than 2 questions with same correct letter in a 6-question batch | Log warning |
| Topic diversity | No two stimuli sharing >60% keyword overlap | Log warning |

### 10.2 Difficulty Consistency

Track per-question accuracy over time (`times_correct / times_served`). If a "hard" question has >80% accuracy after 50+ serves, auto-flag it for review. If an "easy" question has <40% accuracy, same.

### 10.3 Cost Monitoring

Log token usage per batch. At current Opus pricing:
- ~2K prompt tokens per batch
- ~4K output tokens per 6-question batch
- Estimated ~$0.10–0.15 per batch

Include a daily cost cap check in the service layer (configurable via `MAX_DAILY_GENERATION_COST` env var).

---

## 11. Router Registration — Changes to `main.go`

```go
// Add to imports
"github.com/lsat-prep/backend/internal/generator"
"github.com/lsat-prep/backend/internal/questions"

// Add after authHandler initialization
gen := generator.NewGenerator()
questionStore := questions.NewStore(db)
questionService := questions.NewService(questionStore, gen)
questionHandler := questions.NewHandler(questionService)

// Add routes
protected.HandleFunc("/questions/generate", questionHandler.GenerateBatch).Methods("POST")
protected.HandleFunc("/questions/batches", questionHandler.ListBatches).Methods("GET")
protected.HandleFunc("/questions/batches/{id}", questionHandler.GetBatch).Methods("GET")
protected.HandleFunc("/questions/drill", questionHandler.GetDrill).Methods("GET")
protected.HandleFunc("/questions/{id}", questionHandler.GetQuestion).Methods("GET")
protected.HandleFunc("/questions/{id}/answer", questionHandler.SubmitAnswer).Methods("POST")
```

---

## 12. Testing Strategy

### Unit Tests
- `generator/parser_test.go` — test JSON parsing with valid and malformed inputs
- `generator/prompts_test.go` — test prompt construction for each subtype
- `questions/store_test.go` — test DB operations with a test database

### Integration Tests
- `questions/handler_test.go` — HTTP handler tests with mocked service
- `generator/client_test.go` — test against Anthropic API with a small (1-question) batch, gated behind `INTEGRATION_TEST=true` env flag

### Mock for Local Dev
- Add a `MOCK_GENERATOR=true` env var
- When set, the generator returns hardcoded questions instead of calling Claude
- Uses the same question format the frontend already has in `mock_questions.dart`

---

## 13. Summary

| Component | File | Purpose |
|---|---|---|
| Models | `internal/models/question.go` | All question/batch/choice/passage structs |
| Generator client | `internal/generator/client.go` | Anthropic SDK wrapper, API calls |
| Prompts | `internal/generator/prompts.go` | System + user prompts per subtype |
| Parser | `internal/generator/parser.go` | JSON → Go struct validation |
| Service | `internal/questions/service.go` | Batch orchestration logic |
| Store | `internal/questions/store.go` | Postgres CRUD |
| Handler | `internal/questions/handler.go` | HTTP endpoints |
| Migration | `internal/database/database.go` | 4 new tables |
| Router | `cmd/server/main.go` | Wire up new routes |
