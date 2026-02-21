# LSAT Prep — Backend Spec: AI Question Generation & Validation Pipeline

> **Audience:** Backend engineering team
> **Codebase:** `lsat_prep_backend/` — Go 1.22, Gorilla Mux, PostgreSQL 16, JWT auth
> **Supersedes:** `QUESTION_GENERATION_SPEC.md` and `QUESTION_QUALITY_SPEC.md`

---

## Table of Contents

1. [Overview](#1-overview)
2. [New Dependencies](#2-new-dependencies)
3. [Environment Variables](#3-environment-variables)
4. [File Structure](#4-file-structure)
5. [Database Schema](#5-database-schema)
6. [Models](#6-models)
7. [Generator — Anthropic Integration](#7-generator--anthropic-integration)
8. [Prompt Architecture](#8-prompt-architecture)
9. [Response Parser](#9-response-parser)
10. [Validation Pipeline](#10-validation-pipeline)
11. [Quality Scoring](#11-quality-scoring)
12. [Question Service — Orchestration](#12-question-service--orchestration)
13. [Question Store — Postgres CRUD](#13-question-store--postgres-crud)
14. [HTTP Endpoints](#14-http-endpoints)
15. [Router Registration](#15-router-registration)
16. [Cost Controls](#16-cost-controls)
17. [Testing Strategy](#17-testing-strategy)
18. [Implementation Phases](#18-implementation-phases)

---

## 1. Overview

Add a three-stage question generation and validation pipeline to the existing Go backend. The pipeline:

1. **Generates** LSAT-quality questions in batches by calling Anthropic's Claude API with subtype-specific prompt engineering
2. **Verifies** each question's correct answer via an independent model solve (no answer key visible)
3. **Stress-tests** each wrong answer via adversarial argumentation to ensure the correct answer is unambiguous

Generated questions are scored, stored in Postgres, and served to the Flutter frontend via REST endpoints. The drill endpoint withholds answers until the user submits — answers and explanations are returned only after submission.

### Pipeline Architecture

```
┌──────────────────┐     ┌───────────────────────┐     ┌─────────────────────┐
│  STAGE 1          │     │  STAGE 2               │     │  STAGE 3             │
│  Generation       │────▶│  Self-Verification     │────▶│  Adversarial Check   │
│  (Opus, t=0.7-0.8)│     │  (Sonnet, t=0.2)       │     │  (Sonnet, t=0.2)     │
│                   │     │                        │     │                      │
│  Produces batch   │     │  Solves each question  │     │  Argues for every    │
│  of questions     │     │  WITHOUT seeing the    │     │  wrong answer —      │
│  with answers     │     │  answer key            │     │  flags if any        │
│  + explanations   │     │                        │     │  argument succeeds   │
└──────────────────┘     └───────────────────────┘     └─────────────────────┘
         │                         │                            │
         ▼                         ▼                            ▼
   Structural validation    Compare answers.              Score each question.
   (parser checks)          If mismatch → reject.         If wrong answer is
                            If low confidence → flag.     defensible → reject.
```

### Model Allocation

| Stage | Model | Temperature | Max Tokens | Why |
|---|---|---|---|---|
| Generation | `claude-opus-4-5-20251101` | 0.7–0.8 | 8192 | Creative diversity with controlled quality; Opus produces the most nuanced legally rigorous arguments |
| Self-Verification | `claude-sonnet-4-5-20250929` | 0.2 | 2048 | Analytical precision; cheaper; acts as independent verifier |
| Adversarial Check | `claude-sonnet-4-5-20250929` | 0.2 | 4096 | Stress-test wrong answers; should NOT find valid defenses |

---

## 2. New Dependencies

Add to `go.mod`:

```
github.com/anthropics/anthropic-sdk-go  // Anthropic's official Go SDK — handles auth, retries, streaming
```

The SDK reads `ANTHROPIC_API_KEY` from the environment automatically.

---

## 3. Environment Variables

Add to `.env.example` and `docker-compose.yml` backend service:

```bash
# ── Anthropic API ─────────────────────────────────────
ANTHROPIC_API_KEY=sk-ant-...                    # Required — Anthropic API key
ANTHROPIC_MODEL=claude-opus-4-5-20251101        # Generation model (Opus — best for legal reasoning)
ANTHROPIC_VALIDATION_MODEL=claude-sonnet-4-5-20250929  # Validation model (Sonnet — cheaper, precise)

# ── Generation Settings ───────────────────────────────
QUESTION_BATCH_SIZE=6                           # Questions per generation batch (default 6)
MAX_DAILY_GENERATION_COST=10.00                 # Daily cost cap in USD (default $10)

# ── Validation Pipeline ──────────────────────────────
VALIDATION_ENABLED=true                         # Toggle Stage 2 (disable for dev/testing)
ADVERSARIAL_ENABLED=true                        # Toggle Stage 3 (can run Stage 2 only)
MAX_VALIDATION_RETRIES=1                        # Retries per validation stage on transient failure

# ── Development ───────────────────────────────────────
MOCK_GENERATOR=false                            # When true, return hardcoded questions (no API calls)
USE_CLI_GENERATOR=true                          # When true, use `claude` CLI instead of Anthropic SDK (local dev — uses your Claude plan)
CLAUDE_CLI_PATH=claude                          # Path to claude CLI binary (default: "claude" on PATH)
```

Update `docker-compose.yml` backend service environment block to include all of the above.

---

## 4. File Structure

```
internal/
├── auth/                   # EXISTING — unchanged
│   └── handler.go
├── database/               # EXISTING — add migrations
│   └── database.go
├── middleware/              # EXISTING — unchanged
│   └── auth.go
├── models/
│   ├── user.go             # EXISTING — unchanged
│   └── question.go         # NEW — Question, Batch, AnswerChoice, Passage, Validation models
├── generator/
│   ├── client.go           # NEW — Anthropic SDK wrapper for generation calls (production)
│   ├── cli_client.go       # NEW — Claude CLI wrapper for local dev (uses your plan)
│   ├── prompts.go          # NEW — System + user prompts, per-subtype rules, stem banks
│   ├── parser.go           # NEW — JSON response → Go structs + structural validation
│   ├── validator.go        # NEW — Stage 2 (verification) + Stage 3 (adversarial) logic
│   └── quality.go          # NEW — Composite quality scoring formula
└── questions/
    ├── handler.go           # NEW — HTTP handlers for all question endpoints
    ├── service.go           # NEW — 3-stage pipeline orchestration, batch management
    └── store.go             # NEW — Postgres CRUD for questions, batches, validation logs
```

---

## 5. Database Schema

Add all of the following to the `Migrate()` function in `internal/database/database.go`, after the existing `users` table creation.

### 5.1 `question_batches`

```sql
CREATE TABLE IF NOT EXISTS question_batches (
    id                BIGSERIAL PRIMARY KEY,
    section           VARCHAR(50) NOT NULL,              -- 'logical_reasoning' | 'reading_comprehension'
    lr_subtype        VARCHAR(50),                       -- null for RC; e.g. 'strengthen', 'flaw'
    difficulty        VARCHAR(20) NOT NULL,              -- 'easy' | 'medium' | 'hard'
    status            VARCHAR(20) NOT NULL DEFAULT 'pending',  -- 'pending' | 'generating' | 'validating' | 'completed' | 'failed'
    question_count    INT NOT NULL DEFAULT 0,
    questions_passed  INT NOT NULL DEFAULT 0,            -- Questions surviving validation
    questions_flagged INT NOT NULL DEFAULT 0,
    questions_rejected INT NOT NULL DEFAULT 0,
    model_used        VARCHAR(100),                      -- e.g. 'claude-opus-4-5-20251101'
    prompt_tokens     INT,
    output_tokens     INT,
    validation_tokens INT,                               -- Total tokens used across Stage 2 + 3
    generation_time_ms INT,
    total_cost_cents  INT,                               -- Total cost in cents for this batch
    error_message     TEXT,
    created_at        TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    completed_at      TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_batches_status ON question_batches(status);
CREATE INDEX IF NOT EXISTS idx_batches_section ON question_batches(section, lr_subtype);
```

### 5.2 `rc_passages`

```sql
CREATE TABLE IF NOT EXISTS rc_passages (
    id              BIGSERIAL PRIMARY KEY,
    batch_id        BIGINT NOT NULL REFERENCES question_batches(id),
    title           VARCHAR(255) NOT NULL,               -- e.g. "Environmental Law and Policy"
    subject_area    VARCHAR(50) NOT NULL,                -- 'law' | 'natural_science' | 'social_science' | 'humanities'
    content         TEXT NOT NULL,                        -- ~450 words
    is_comparative  BOOLEAN DEFAULT FALSE,
    passage_b       TEXT,                                 -- Second passage if comparative
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
```

### 5.3 `questions`

```sql
CREATE TABLE IF NOT EXISTS questions (
    id                    BIGSERIAL PRIMARY KEY,
    batch_id              BIGINT NOT NULL REFERENCES question_batches(id),
    section               VARCHAR(50) NOT NULL,
    lr_subtype            VARCHAR(50),
    difficulty            VARCHAR(20) NOT NULL,
    stimulus              TEXT NOT NULL,                  -- 4-7 sentences, the argument/scenario
    question_stem         TEXT NOT NULL,                  -- "Which of the following..."
    correct_answer_id     VARCHAR(1) NOT NULL,            -- 'A' | 'B' | 'C' | 'D' | 'E'
    explanation           TEXT NOT NULL,                  -- Why the correct answer is correct
    passage_id            BIGINT REFERENCES rc_passages(id),  -- FK for RC questions, null for LR
    quality_score         DECIMAL(3,2),                   -- 0.00-1.00 composite quality rating
    validation_status     VARCHAR(20) DEFAULT 'unvalidated',  -- 'unvalidated' | 'passed' | 'flagged' | 'rejected'
    validation_reasoning  TEXT,                           -- Validator's reasoning for its answer choice
    adversarial_score     VARCHAR(20),                    -- 'clean' | 'minor_concern' | 'ambiguous'
    flagged               BOOLEAN DEFAULT FALSE,         -- Manual/auto flag for review
    times_served          INT DEFAULT 0,
    times_correct         INT DEFAULT 0,
    created_at            TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_questions_batch ON questions(batch_id);
CREATE INDEX IF NOT EXISTS idx_questions_section ON questions(section, lr_subtype, difficulty);
CREATE INDEX IF NOT EXISTS idx_questions_serving ON questions(section, lr_subtype, difficulty, times_served);
CREATE INDEX IF NOT EXISTS idx_questions_validation ON questions(validation_status);
CREATE INDEX IF NOT EXISTS idx_questions_quality ON questions(quality_score) WHERE quality_score IS NOT NULL;
```

### 5.4 `answer_choices`

```sql
CREATE TABLE IF NOT EXISTS answer_choices (
    id              BIGSERIAL PRIMARY KEY,
    question_id     BIGINT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    choice_id       VARCHAR(1) NOT NULL,                 -- 'A' | 'B' | 'C' | 'D' | 'E'
    choice_text     TEXT NOT NULL,                        -- 1-2 sentences
    explanation     TEXT NOT NULL,                        -- Why this choice is right/wrong
    is_correct      BOOLEAN NOT NULL DEFAULT FALSE,
    wrong_answer_type VARCHAR(50),                       -- e.g. 'irrelevant', 'weakener', 'out_of_scope' — from generation
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(question_id, choice_id)
);

CREATE INDEX IF NOT EXISTS idx_choices_question ON answer_choices(question_id);
```

### 5.5 `validation_logs`

```sql
CREATE TABLE IF NOT EXISTS validation_logs (
    id                    BIGSERIAL PRIMARY KEY,
    question_id           BIGINT REFERENCES questions(id),
    batch_id              BIGINT REFERENCES question_batches(id),
    stage                 VARCHAR(20) NOT NULL,           -- 'verification' | 'adversarial'
    model_used            VARCHAR(100),
    generated_answer      VARCHAR(1),                     -- What generation said was correct
    validator_answer      VARCHAR(1),                     -- What verification picked (null for adversarial)
    matches               BOOLEAN,                        -- Did verification agree?
    confidence            VARCHAR(20),                    -- 'high' | 'medium' | 'low'
    reasoning             TEXT,                           -- Full reasoning from the validator
    adversarial_details   JSONB,                          -- Per-wrong-answer results: [{choice_id, defense_strength, defense_argument, recommendation}]
    prompt_tokens         INT,
    output_tokens         INT,
    created_at            TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_validation_question ON validation_logs(question_id);
CREATE INDEX IF NOT EXISTS idx_validation_batch ON validation_logs(batch_id, stage);
```

---

## 6. Models

### `internal/models/question.go`

```go
package models

import "time"

// ── Section & Subtype Enums ────────────────────────────

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
    Difficulty          Difficulty       `json:"difficulty"`
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
    ChoiceID        string `json:"choice_id"`        // "A" through "E"
    ChoiceText      string `json:"choice_text"`
    Explanation     string `json:"explanation"`       // Why right or wrong
    IsCorrect       bool   `json:"is_correct"`
    WrongAnswerType string `json:"wrong_answer_type,omitempty"` // e.g. "irrelevant", "weakener"
}

type RCPassage struct {
    ID            int64  `json:"id"`
    BatchID       int64  `json:"batch_id"`
    Title         string `json:"title"`
    SubjectArea   string `json:"subject_area"`
    Content       string `json:"content"`
    IsComparative bool   `json:"is_comparative"`
    PassageB      string `json:"passage_b,omitempty"`
}

// ── Request / Response Types ───────────────────────────

type GenerateBatchRequest struct {
    Section    Section    `json:"section"`
    LRSubtype  *LRSubtype `json:"lr_subtype,omitempty"`   // Required for LR
    Difficulty Difficulty `json:"difficulty"`
    Count      int        `json:"count"`                  // Number of questions (default 6)
}

type GenerateBatchResponse struct {
    BatchID           int64       `json:"batch_id"`
    Status            BatchStatus `json:"status"`
    QuestionsPassed   int         `json:"questions_passed"`
    QuestionsFlagged  int         `json:"questions_flagged"`
    QuestionsRejected int         `json:"questions_rejected"`
    Message           string      `json:"message"`
}

type SubmitAnswerRequest struct {
    SelectedChoiceID string `json:"selected_choice_id"` // "A" through "E"
}

type SubmitAnswerResponse struct {
    Correct         bool           `json:"correct"`
    CorrectAnswerID string         `json:"correct_answer_id"`
    Explanation     string         `json:"explanation"`
    Choices         []AnswerChoice `json:"choices"` // All 5 with explanations
}

type QuestionListResponse struct {
    Questions []Question `json:"questions"`
    Total     int        `json:"total"`
    Page      int        `json:"page"`
    PageSize  int        `json:"page_size"`
}
```

---

## 7. Generator — Dual-Mode Architecture

The generator uses an interface so the service layer doesn't care whether questions come from the Anthropic SDK (production) or the Claude CLI (local dev using your plan). Both implementations share the same prompts, parser, and validation pipeline.

### 7.1 Generator Interface

```go
// LLMClient is the interface both generator implementations satisfy.
// The service layer depends on this — not on a concrete struct.
type LLMClient interface {
    Generate(ctx context.Context, systemPrompt string, userPrompt string) (*LLMResponse, error)
}

type LLMResponse struct {
    Content      string `json:"content"`       // Raw response body (JSON string)
    PromptTokens int    `json:"prompt_tokens"`  // 0 for CLI mode (not tracked)
    OutputTokens int    `json:"output_tokens"`  // 0 for CLI mode
}

// Generator wraps an LLMClient and adds LSAT-specific batch methods
type Generator struct {
    llm   LLMClient
    model string
}

func NewGenerator() *Generator {
    var llm LLMClient

    if os.Getenv("USE_CLI_GENERATOR") == "true" {
        cliPath := os.Getenv("CLAUDE_CLI_PATH")
        if cliPath == "" {
            cliPath = "claude"
        }
        llm = NewCLIClient(cliPath)
        log.Println("Generator using Claude CLI (local plan)")
    } else if os.Getenv("MOCK_GENERATOR") == "true" {
        llm = NewMockClient()
        log.Println("Generator using mock data")
    } else {
        model := os.Getenv("ANTHROPIC_MODEL")
        if model == "" {
            model = "claude-opus-4-5-20251101"
        }
        llm = NewAPIClient(model)
        log.Println("Generator using Anthropic API:", model)
    }

    return &Generator{llm: llm}
}

func (g *Generator) GenerateLRBatch(ctx context.Context, subtype models.LRSubtype, difficulty models.Difficulty, count int) (*GeneratedBatch, error)
func (g *Generator) GenerateRCBatch(ctx context.Context, difficulty models.Difficulty, questionsPerPassage int) (*GeneratedBatch, error)
```

Both `GenerateLRBatch` and `GenerateRCBatch` build prompts via `prompts.go`, call `g.llm.Generate(...)`, then parse the response via `parser.go`. The LLM backend is invisible to the batch methods.

### 7.2 `internal/generator/client.go` — Anthropic SDK (Production)

```go
type APIClient struct {
    client *anthropic.Client
    model  string
}

func NewAPIClient(model string) *APIClient {
    return &APIClient{
        client: anthropic.NewClient(),  // reads ANTHROPIC_API_KEY from env
        model:  model,
    }
}

func (c *APIClient) Generate(ctx context.Context, systemPrompt string, userPrompt string) (*LLMResponse, error) {
    resp, err := c.client.Messages.New(ctx, anthropic.MessageNewParams{
        Model:     c.model,
        MaxTokens: 8192,
        System:    anthropic.SystemPrompt(systemPrompt),
        Messages:  []anthropic.MessageParam{
            anthropic.NewUserMessage(anthropic.NewTextBlock(userPrompt)),
        },
        Temperature: anthropic.Float(0.8),
    })
    if err != nil {
        return nil, fmt.Errorf("anthropic API error: %w", err)
    }

    return &LLMResponse{
        Content:      resp.Content[0].Text,
        PromptTokens: int(resp.Usage.InputTokens),
        OutputTokens: int(resp.Usage.OutputTokens),
    }, nil
}
```

### 7.3 `internal/generator/cli_client.go` — Claude CLI (Local Dev)

This is the key addition. Instead of calling the Anthropic API, it shells out to the `claude` CLI which runs against your existing Claude plan. No API key needed, no per-token charges — it just uses your plan's allowance.

```go
type CLIClient struct {
    cliPath string
}

func NewCLIClient(cliPath string) *CLIClient {
    return &CLIClient{cliPath: cliPath}
}

func (c *CLIClient) Generate(ctx context.Context, systemPrompt string, userPrompt string) (*LLMResponse, error) {
    // Build the full prompt as a single string for the CLI
    // The CLI's --system-prompt flag passes the system prompt
    // The user prompt goes via stdin with --print flag for non-interactive output

    combinedPrompt := userPrompt

    // Use claude CLI in non-interactive print mode
    // --output-format json gives us structured output
    cmd := exec.CommandContext(ctx,
        c.cliPath,
        "--print",                          // Non-interactive: print response and exit
        "--output-format", "text",          // Raw text output (our prompt already asks for JSON)
        "--system-prompt", systemPrompt,    // System prompt passed as flag
        "--max-turns", "1",                 // Single turn, no back-and-forth
    )

    // Pass the user prompt via stdin
    cmd.Stdin = strings.NewReader(combinedPrompt)

    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr

    // Set a generous timeout — generation can take 30-60s
    if ctx.Err() != nil {
        return nil, ctx.Err()
    }

    err := cmd.Run()
    if err != nil {
        return nil, fmt.Errorf("claude CLI error: %w\nstderr: %s", err, stderr.String())
    }

    responseText := strings.TrimSpace(stdout.String())
    if responseText == "" {
        return nil, fmt.Errorf("claude CLI returned empty response")
    }

    // CLI doesn't report token usage — set to 0
    return &LLMResponse{
        Content:      responseText,
        PromptTokens: 0,
        OutputTokens: 0,
    }, nil
}
```

**How it works:**
- Runs `claude --print --system-prompt "..." --max-turns 1` with the user prompt piped to stdin
- The `--print` flag makes it non-interactive — it outputs the response and exits
- The `--output-format text` gives raw text (our prompts already instruct JSON output)
- No API key needed — uses whatever Claude plan your local `claude` CLI is authenticated with
- Token usage is not tracked (reports 0) — cost controls are skipped in CLI mode

**Requirements:**
- `claude` CLI must be installed and authenticated (`claude` on PATH, or set `CLAUDE_CLI_PATH`)
- Your Claude plan must have available usage
- Works with any model your plan has access to (the CLI uses its default model)

### 7.4 Validator in CLI Mode

The same dual-mode pattern applies to the validator. Update `validator.go`:

```go
func NewValidator() *Validator {
    var llm LLMClient

    if os.Getenv("USE_CLI_GENERATOR") == "true" {
        cliPath := os.Getenv("CLAUDE_CLI_PATH")
        if cliPath == "" {
            cliPath = "claude"
        }
        llm = NewCLIClient(cliPath)
    } else {
        model := os.Getenv("ANTHROPIC_VALIDATION_MODEL")
        if model == "" {
            model = "claude-sonnet-4-5-20250929"
        }
        llm = NewAPIClient(model)
    }

    return &Validator{llm: llm}
}
```

In CLI mode, both generation and validation use the same CLI — you don't get the Opus/Sonnet split, but for local dev and prompt iteration that's fine. The full model separation kicks in when you switch to `USE_CLI_GENERATOR=false` for production.

**API call parameters for production (SDK mode):**

| Parameter | Value |
|---|---|
| `model` | `ANTHROPIC_MODEL` env var (default: `claude-opus-4-5-20251101`) |
| `max_tokens` | 8192 |
| `temperature` | 0.7–0.8 (use 0.7 for hard, 0.8 for easy/medium) |
| `system` | System prompt from `prompts.go` |
| Retry | 1 retry on 429/500, exponential backoff (1s → 3s) |
| Timeout | 60 seconds |

Track `prompt_tokens` and `output_tokens` from the response `usage` field for cost monitoring.

---

## 8. Prompt Architecture

### `internal/generator/prompts.go`

The prompt architecture is the most critical piece. Each generation call uses a **system prompt** (persona + structural rules) and a **user prompt** (subtype-specific instructions + JSON schema).

### 8.1 System Prompt — Logical Reasoning

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

STIMULUS CONSTRUCTION RULES:

Reasoning patterns to use (vary across questions):
- Conditional logic: "If X then Y" chains, with conclusions drawn from contrapositives or affirming the consequent
- Causal reasoning: "X caused Y" claims with evidence that may have confounds
- Analogy: "X is like Y, so what's true of X is true of Y"
- Statistical/survey: "A study found..." or "In a survey of..."
- Appeal to evidence: Expert testimony, historical precedent, experimental results
- Principle application: General rule applied to a specific case

Language register:
- Use the voice of newspaper editorials, academic summaries, policy memos, or scientific abstracts
- No first person ("I believe")
- No slang, no contractions
- Varied sentence structures: some complex with subordinate clauses, some short and declarative
- Signal words that mark argument structure: "therefore," "however," "since," "although," "consequently," "nevertheless"

Topic diversity requirements:
- Law: constitutional issues, legal theory, court decisions, legislation
- Natural science: biology, ecology, geology, physics, chemistry, medicine
- Social science: economics, psychology, sociology, political science, anthropology
- Humanities: art, literature, philosophy, history, music, architecture
- Business/policy: regulation, urban planning, education, public health, technology
- NO questions about the LSAT itself, test prep, or academic admissions

QUESTION STEM:
- One sentence that clearly asks the student what to do
- Uses standard LSAT phrasing for the question type (provided in user prompt)

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
- For each wrong answer: 1-2 sentences explaining precisely WHY it is wrong — name the specific logical error (e.g., "irrelevant comparison," "out of scope," "reverses the relationship") and label its wrong answer archetype

DIFFICULTY CALIBRATION:
- Easy: Straightforward argument with a clear flaw/assumption. The correct answer is noticeably stronger than alternatives. One strong distractor.
- Medium: More nuanced argument, possibly with multiple premises. Two strong distractors. Requires careful reading.
- Hard: Complex argument with subtle reasoning. The correct and "close second" answers require distinguishing between very similar logical moves. Three strong distractors.

You must respond with valid JSON only. No markdown, no explanation outside the JSON.
```

### 8.2 User Prompt Template — LR

```
Generate exactly {count} LSAT Logical Reasoning questions.

Section: Logical Reasoning
Question type: {subtype}
Difficulty: {difficulty}

Standard question stems for this type:
{subtype_stems}

{correct_answer_rules}

{wrong_answer_rules}

Respond with this exact JSON structure:
{
  "questions": [
    {
      "stimulus": "...",
      "question_stem": "...",
      "choices": [
        {"id": "A", "text": "...", "explanation": "...", "wrong_answer_type": "irrelevant"},
        {"id": "B", "text": "...", "explanation": "...", "wrong_answer_type": null},
        {"id": "C", "text": "...", "explanation": "...", "wrong_answer_type": "out_of_scope"},
        {"id": "D", "text": "...", "explanation": "...", "wrong_answer_type": "weakener"},
        {"id": "E", "text": "...", "explanation": "...", "wrong_answer_type": "restates_premise"}
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
- For the correct answer choice, set "wrong_answer_type" to null
- For each wrong answer choice, set "wrong_answer_type" to one of the archetype labels specified in the wrong answer rules above
```

### 8.3 Subtype Stem Bank

Include the appropriate stems in the user prompt per subtype:

```go
var subtypeStems = map[models.LRSubtype][]string{
    models.SubtypeStrengthen: {
        "Which of the following, if true, most strengthens the argument?",
        "Which of the following, if true, most strongly supports the argument above?",
    },
    models.SubtypeWeaken: {
        "Which of the following, if true, most weakens the argument?",
        "Which of the following, if true, most seriously undermines the argument above?",
    },
    models.SubtypeAssumption: {
        "The argument relies on which of the following assumptions?",
        "Which of the following is an assumption on which the argument depends?",
        "The argument assumes which of the following?",
    },
    models.SubtypeFlaw: {
        "The reasoning in the argument is flawed because it",
        "The reasoning in the argument is most vulnerable to criticism because it",
        "Which of the following most accurately describes a flaw in the argument?",
    },
    models.SubtypeMustBeTrue: {
        "If the statements above are true, which of the following must also be true?",
        "Which of the following can be properly inferred from the statements above?",
    },
    models.SubtypeMostStrongly: {
        "Which of the following is most strongly supported by the information above?",
        "The statements above, if true, most strongly support which of the following?",
    },
    models.SubtypeMainConclusion: {
        "Which of the following most accurately expresses the main conclusion of the argument?",
        "The main point of the argument is that",
    },
    models.SubtypeMethodReasoning: {
        "The argument proceeds by",
        "Which of the following most accurately describes the method of reasoning used in the argument?",
    },
    models.SubtypeEvaluate: {
        "Which of the following would be most useful to know in order to evaluate the argument?",
        "The answer to which of the following questions would most help in evaluating the argument?",
    },
    models.SubtypePrinciple: {
        "Which of the following principles, if valid, most helps to justify the reasoning above?",
    },
    models.SubtypeApplyPrinciple: {
        "The principle stated above, if valid, most helps to justify which of the following judgments?",
        "Which of the following judgments best illustrates the principle stated above?",
    },
    models.SubtypeParallelReasoning: {
        "Which of the following arguments is most similar in its pattern of reasoning to the argument above?",
        "The pattern of reasoning in which of the following is most similar to that in the argument above?",
    },
    models.SubtypeParallelFlaw: {
        "The flawed pattern of reasoning in which of the following is most similar to that in the argument above?",
        "Which of the following exhibits a pattern of flawed reasoning most similar to that exhibited by the argument above?",
    },
    models.SubtypeRoleOfStatement: {
        "The claim that [quoted claim] plays which of the following roles in the argument?",
        "The statement that [quoted claim] figures in the argument in which of the following ways?",
    },
}
```

### 8.4 Per-Subtype Correct Answer Rules

Injected into the user prompt based on the requested subtype. These define exactly what makes the correct answer RIGHT.

```go
var subtypeCorrectAnswerRules = map[models.LRSubtype]string{

    models.SubtypeStrengthen: `
CORRECT ANSWER RULES (Strengthen):
- The correct answer must provide NEW information not stated in the stimulus
- It must make the conclusion MORE likely to be true
- It typically does one of: (a) rules out an alternative explanation, (b) provides an additional premise that supports the causal link, (c) shows the mechanism by which the conclusion follows
- It should NOT merely restate a premise or the conclusion
- It should NOT be so strong that it independently proves the conclusion`,

    models.SubtypeWeaken: `
CORRECT ANSWER RULES (Weaken):
- The correct answer must provide NEW information not stated in the stimulus
- It must make the conclusion LESS likely to be true
- It typically does one of: (a) introduces a plausible alternative explanation, (b) breaks the causal link, (c) shows a flaw in the evidence, (d) provides a counterexample
- It should NOT merely contradict the conclusion — it must attack the REASONING
- It should NOT be irrelevant to the argument's logical structure`,

    models.SubtypeAssumption: `
CORRECT ANSWER RULES (Assumption — Necessary):
- The correct answer states something that MUST be true for the argument to work
- Apply the Negation Test: if you negate the correct answer, the argument should fall apart
- It fills a gap between the premises and conclusion
- It should NOT be a mere restatement of a premise
- It should NOT be something that strengthens but is not required
- It should NOT be the conclusion itself`,

    models.SubtypeFlaw: `
CORRECT ANSWER RULES (Flaw):
- The correct answer must accurately DESCRIBE the logical error in the argument
- It must be phrased in abstract/general terms describing the reasoning error
- Common flaws: confusing necessary/sufficient conditions, correlation vs causation, ad hominem, hasty generalization, equivocation, part-whole fallacy, appeal to authority, false dichotomy
- The description must match what actually happens in the stimulus — not just name a flaw that sounds plausible
- It should NOT describe the argument's conclusion as wrong — it should describe HOW the reasoning fails`,

    models.SubtypeMustBeTrue: `
CORRECT ANSWER RULES (Must Be True / Inference):
- The correct answer must be LOGICALLY ENTAILED by the stimulus — not merely likely
- If the stimulus premises are true, the correct answer cannot be false
- It is typically a logical consequence of combining two or more premises
- It should NOT require any assumptions beyond what is stated
- It should NOT go beyond the scope of the stimulus
- It should NOT be merely consistent with the stimulus — it must be required by it`,

    models.SubtypeMostStrongly: `
CORRECT ANSWER RULES (Most Strongly Supported):
- The correct answer is the claim most supported by the stimulus evidence
- Unlike Must Be True, it need not be logically entailed — just most probable given the evidence
- It should follow naturally from the information provided
- It should NOT require significant additional assumptions`,

    models.SubtypeMethodReasoning: `
CORRECT ANSWER RULES (Method of Reasoning):
- The correct answer abstractly describes the argumentative technique used
- It must accurately map onto the stimulus structure (premises → conclusion)
- Common methods: analogy, counterexample, reductio ad absurdum, appeal to evidence, elimination of alternatives, establishing a general principle
- The description must match the actual logical moves in the stimulus`,

    models.SubtypeParallelReasoning: `
CORRECT ANSWER RULES (Parallel Reasoning):
- The correct answer must replicate the EXACT logical structure of the stimulus
- Match: (a) the type of premises (conditional, causal, statistical), (b) the validity/invalidity of the reasoning, (c) the conclusion type
- Topic should differ but structure should be identical
- If the stimulus has a flaw, the correct answer must have the same flaw`,

    models.SubtypeParallelFlaw: `
CORRECT ANSWER RULES (Parallel Flaw):
- The correct answer contains an argument with the SAME logical flaw as the stimulus
- Both the structure AND the error type must match
- The topic must be completely different from the stimulus
- If the stimulus confuses necessary/sufficient, the correct answer must too
- If the stimulus makes a causal error, the correct answer must make the same kind`,

    models.SubtypePrinciple: `
CORRECT ANSWER RULES (Principle):
- The correct answer states a general rule that, if true, justifies the specific argument in the stimulus
- It must be broad enough to be a principle (not just a restatement) but specific enough to actually support this argument
- The argument's conclusion should follow from the principle + the premises`,

    models.SubtypeApplyPrinciple: `
CORRECT ANSWER RULES (Apply Principle):
- The stimulus states a general principle or rule
- The correct answer presents a specific situation where the principle applies correctly
- The application must follow logically from the principle's conditions
- The correct answer should not require any additional principles or unstated assumptions
- All conditions of the principle must be met in the specific case`,

    models.SubtypeEvaluate: `
CORRECT ANSWER RULES (Evaluate):
- The correct answer identifies information that would help determine if the argument is sound
- Both possible answers to the question (yes/no) should have different implications for the argument
- One answer should strengthen, the other should weaken — that's what makes it useful for evaluation`,

    models.SubtypeMainConclusion: `
CORRECT ANSWER RULES (Main Conclusion):
- The correct answer is a near-paraphrase of the argument's main point
- It is what the other statements in the stimulus are trying to prove
- It should NOT be a premise, intermediate conclusion, or background information`,

    models.SubtypeRoleOfStatement: `
CORRECT ANSWER RULES (Role of a Statement):
- The correct answer describes the function a specific claim plays in the argument
- Functions: main conclusion, intermediate conclusion, premise, evidence, counterexample, background, concession
- The description must accurately characterize the relationship between the statement and the rest of the argument`,
}
```

### 8.5 Per-Subtype Wrong Answer Archetypes

Each wrong answer must fall into a named archetype so the explanation can cite the specific error. These are also injected into the user prompt.

```go
var subtypeWrongAnswerRules = map[models.LRSubtype]string{

    models.SubtypeStrengthen: `
WRONG ANSWER CONSTRUCTION (Strengthen):
Each wrong answer must fall into one of these categories — label each in your explanation:
1. IRRELEVANT: True-sounding but does not connect to the argument's logical gap
2. WEAKENER: Actually undermines the argument (common trap for test-takers who confuse the task)
3. OUT OF SCOPE: Addresses a topic related to but distinct from the argument's core claim
4. RESTATES PREMISE: Merely repeats information already in the stimulus without adding support
At least one wrong answer should be a WEAKENER (the most common student error on strengthen questions).`,

    models.SubtypeWeaken: `
WRONG ANSWER CONSTRUCTION (Weaken):
1. IRRELEVANT: Sounds related but doesn't attack the reasoning
2. STRENGTHENER: Actually supports the argument (reversal trap)
3. OUT OF SCOPE: About a related but different issue
4. TOO EXTREME: Addresses an extreme version of the claim not actually made
At least one wrong answer should be a STRENGTHENER.`,

    models.SubtypeAssumption: `
WRONG ANSWER CONSTRUCTION (Assumption):
1. HELPS BUT NOT REQUIRED: Strengthens the argument but isn't necessary (fails negation test)
2. RESTATES PREMISE: Already stated in the stimulus
3. RESTATES CONCLUSION: Says what the argument concludes, not what it assumes
4. OUT OF SCOPE: Irrelevant to the argument's logical structure
The "HELPS BUT NOT REQUIRED" distractor is the hardest — it must be clearly not required when negated.`,

    models.SubtypeFlaw: `
WRONG ANSWER CONSTRUCTION (Flaw):
1. WRONG FLAW: Accurately describes a real logical flaw, but NOT the one in this argument
2. DESCRIBES THE ARGUMENT CORRECTLY: States what the argument does without identifying an error
3. TOO BROAD: Describes a flaw in vague terms that could apply to almost any argument
4. MISCHARACTERIZES: Describes something the argument doesn't actually do
The "WRONG FLAW" distractor is the classic trap — it's a valid flaw type but doesn't match this stimulus.`,

    models.SubtypeMustBeTrue: `
WRONG ANSWER CONSTRUCTION (Must Be True):
1. COULD BE TRUE: Consistent with the stimulus but not required by it
2. GOES BEYOND: Requires assumptions not in the stimulus
3. PARTIAL INFERENCE: True of some cases mentioned but overgeneralizes
4. REVERSAL: Gets a conditional relationship backwards
The key distinction: must be true vs. could be true.`,

    models.SubtypeMostStrongly: `
WRONG ANSWER CONSTRUCTION (Most Strongly Supported):
1. COULD BE TRUE: Consistent with the stimulus but not particularly supported by it
2. GOES BEYOND: Requires significant assumptions not in the stimulus
3. REVERSAL: Gets the direction of a relationship backwards
4. EXTREME LANGUAGE: Uses "always," "never," "all" when the stimulus hedges`,

    models.SubtypeMethodReasoning: `
WRONG ANSWER CONSTRUCTION (Method of Reasoning):
1. WRONG METHOD: Accurately describes a reasoning method, but not the one used here
2. PARTIAL DESCRIPTION: Captures one aspect of the argument but misses the main technique
3. MISCHARACTERIZES: Describes the argument doing something it doesn't do
4. TOO SPECIFIC/GENERAL: Either over- or under-describes the method`,

    models.SubtypeParallelReasoning: `
WRONG ANSWER CONSTRUCTION (Parallel Reasoning):
1. SAME TOPIC, WRONG STRUCTURE: Similar subject matter but different logical form
2. FLAWED WHEN ORIGINAL IS VALID: Introduces a logical error not present in the original
3. VALID WHEN ORIGINAL IS FLAWED: Fixes the error, making the reasoning valid
4. PARTIALLY PARALLEL: Matches some but not all structural elements`,

    models.SubtypeParallelFlaw: `
WRONG ANSWER CONSTRUCTION (Parallel Flaw):
1. DIFFERENT FLAW: Contains a logical error, but a different type than the stimulus
2. NO FLAW: The reasoning is actually valid (no parallel error)
3. SAME TOPIC: Mimics the stimulus subject but with a different logical structure
4. PARTIALLY PARALLEL: Matches the structure but the flaw manifests differently`,

    models.SubtypePrinciple: `
WRONG ANSWER CONSTRUCTION (Principle):
1. TOO NARROW: A principle that only covers part of the argument
2. TOO BROAD: A principle that's true but doesn't specifically justify THIS argument
3. WRONG DIRECTION: A principle that would justify the opposite conclusion
4. IRRELEVANT PRINCIPLE: A valid principle that doesn't connect to the argument's reasoning`,

    models.SubtypeApplyPrinciple: `
WRONG ANSWER CONSTRUCTION (Apply Principle):
1. VIOLATES A CONDITION: The scenario doesn't meet all conditions of the principle
2. WRONG OUTCOME: Meets the conditions but draws the wrong conclusion
3. SUPERFICIALLY SIMILAR: Shares surface features with the principle but doesn't actually apply
4. REVERSES APPLICATION: Applies the principle backwards`,

    models.SubtypeEvaluate: `
WRONG ANSWER CONSTRUCTION (Evaluate):
1. ONE-DIRECTIONAL: The answer to the question only strengthens OR weakens, not both depending on the answer
2. IRRELEVANT QUESTION: The answer wouldn't affect the argument either way
3. ALREADY ANSWERED: The stimulus already provides the information asked about
4. WRONG SCOPE: Asks about something adjacent but not central to the argument's logic`,

    models.SubtypeMainConclusion: `
WRONG ANSWER CONSTRUCTION (Main Conclusion):
1. PREMISE MASQUERADING: A premise stated in the stimulus, not the conclusion
2. INTERMEDIATE CONCLUSION: A sub-conclusion that supports the main conclusion
3. BACKGROUND INFO: Context from the stimulus that isn't argued for or against
4. OVERSTATED CONCLUSION: Exaggerates what the argument actually claims`,

    models.SubtypeRoleOfStatement: `
WRONG ANSWER CONSTRUCTION (Role of a Statement):
1. WRONG ROLE: Correctly identifies that the statement is present but mischaracterizes its function
2. CONFUSES PREMISE/CONCLUSION: Calls a premise a conclusion or vice versa
3. INVENTS A ROLE: Describes a function (like "counterexample") that the statement doesn't serve
4. RIGHT ROLE, WRONG RELATIONSHIP: Correctly names the role type but misstates what it supports or opposes`,
}
```

### 8.6 System Prompt — Reading Comprehension

For RC generation, use this system prompt instead of (not in addition to) the LR system prompt:

```
You are an expert LSAT question writer with 20 years of experience at the Law School Admission Council (LSAC). You write Reading Comprehension passages and questions that are indistinguishable from real LSAT material.

RC PASSAGE CONSTRUCTION:

Structure (450-500 words, 3-4 paragraphs):
- Paragraph 1: Introduce the topic and the main thesis or debate
- Paragraph 2: Develop the main argument with evidence, examples, or analysis
- Paragraph 3: Present a counterargument, complication, or alternative perspective
- Paragraph 4 (optional): Resolve, synthesize, or state implications

Required elements:
- A clear MAIN POINT that can be identified (for main idea questions)
- At least 3 SPECIFIC DETAILS that questions can reference (dates, names, percentages, specific claims)
- At least 1 AUTHOR'S ATTITUDE signal (words revealing agreement, skepticism, enthusiasm, caution)
- At least 2 STRUCTURAL TRANSITIONS between ideas ("however," "in contrast," "furthermore")
- Language that supports INFERENCE but doesn't state everything explicitly

Subject areas (rotate across batches):
1. Law: legal theory, landmark cases, constitutional interpretation, comparative law
2. Natural science: evolutionary biology, ecology, climate, geology, physics
3. Social science: economic theory, psychological research, sociological analysis
4. Humanities: literary criticism, art history, philosophical debates, music theory

Comparative passages (when requested):
- Passage A: ~225 words presenting one perspective
- Passage B: ~225 words presenting a different perspective on the same topic
- Both passages must be self-contained but share enough overlap for comparison questions

RC QUESTION TYPES (generate 5-8 per passage):
1. Main Point / Primary Purpose (1 per passage)
   - Correct: captures the central thesis
   - Wrong: too narrow (one paragraph only), too broad, misidentifies the purpose

2. Specific Detail (1-2 per passage)
   - Correct: accurately restates a specific claim from the passage
   - Wrong: distorts the detail, confuses which paragraph, attributes to wrong source

3. Inference (1-2 per passage)
   - Correct: logically follows from passage content without being explicitly stated
   - Wrong: requires outside knowledge, overgeneralizes, reverses a relationship

4. Author's Attitude/Tone (0-1 per passage)
   - Correct: matches the attitude signals in the text
   - Wrong: too extreme, opposite tone, confuses attitude toward topic vs. attitude toward a cited source

5. Function of a Phrase/Paragraph (1 per passage)
   - Correct: accurately describes the rhetorical role
   - Wrong: identifies the content but mischaracterizes its purpose

6. Strengthen/Weaken passage-based (0-1 per passage)
   - Same rules as LR strengthen/weaken but grounded in passage claims

7. Comparative Passage Questions (for comparative passages only)
   - "Both authors would agree..."
   - "Unlike Passage A, Passage B..."
   - Correct: accurately reflects the relationship between the two perspectives

RC WRONG ANSWER PRINCIPLES:
- The "conservative and boring" principle: correct RC answers tend to be carefully hedged and understated
- Wrong answers are often MORE interesting or specific than the correct answer
- Common wrong answer types:
  * DISTORTION: Takes a passage idea and subtly changes it
  * TOO BROAD: Overgeneralizes beyond what the passage supports
  * TOO NARROW: Captures only one detail, missing the bigger picture
  * OUT OF SCOPE: Introduces ideas not discussed in the passage
  * REVERSED RELATIONSHIP: Gets the causal or comparative direction wrong
  * WRONG PARAGRAPH: Attributes information to the wrong part of the passage

ANSWER CHOICES:
- Exactly 5 choices labeled A through E per question
- Each choice is 1-2 sentences
- Exactly ONE correct answer per question

EXPLANATIONS:
- For the correct answer: 2-4 sentences referencing specific passage content
- For each wrong answer: 1-2 sentences naming the specific error type from the list above

DIFFICULTY CALIBRATION:
- Easy: Main idea and detail questions with clear passage support. One strong distractor.
- Medium: Inference and function questions requiring synthesis. Two strong distractors.
- Hard: Subtle inference, author tone, and comparative questions. Three strong distractors.

You must respond with valid JSON only. No markdown, no explanation outside the JSON.
```

### 8.7 RC User Prompt Template

```
Generate a Reading Comprehension passage with {count} questions.

Difficulty: {difficulty}
Comparative: {is_comparative}
Subject area: {subject_area}

Respond with this exact JSON structure:
{
  "passage": {
    "title": "...",
    "subject_area": "law",
    "content": "... (450-500 words) ...",
    "is_comparative": false,
    "passage_b": null
  },
  "questions": [
    {
      "stimulus": "",
      "question_stem": "The main purpose of the passage is to...",
      "choices": [
        {"id": "A", "text": "...", "explanation": "...", "wrong_answer_type": "too_broad"},
        {"id": "B", "text": "...", "explanation": "...", "wrong_answer_type": null},
        {"id": "C", "text": "...", "explanation": "...", "wrong_answer_type": "too_narrow"},
        {"id": "D", "text": "...", "explanation": "...", "wrong_answer_type": "distortion"},
        {"id": "E", "text": "...", "explanation": "...", "wrong_answer_type": "out_of_scope"}
      ],
      "correct_answer_id": "B",
      "explanation": "..."
    }
  ]
}

Requirements:
- For RC questions, the "stimulus" field should be empty (the passage IS the stimulus)
- Vary question types across the set as specified in the system prompt
- Vary the position of correct answers across A-E
- Include at least one Main Point question and at least one Inference question
```

---

## 9. Response Parser

### `internal/generator/parser.go`

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
    ID              string  `json:"id"`
    Text            string  `json:"text"`
    Explanation     string  `json:"explanation"`
    WrongAnswerType *string `json:"wrong_answer_type"` // null for correct answer
}

type GeneratedPassage struct {
    Title         string `json:"title"`
    SubjectArea   string `json:"subject_area"`
    Content       string `json:"content"`
    IsComparative bool   `json:"is_comparative"`
    PassageB      string `json:"passage_b,omitempty"`
}

func ParseLRResponse(responseBody string) (*GeneratedBatch, error)
func ParseRCResponse(responseBody string) (*GeneratedBatch, error)
```

### Structural Validation Rules (run in parser before returning)

| Check | Rule | On Fail |
|---|---|---|
| Valid JSON | Response parses as valid JSON | Reject entire batch |
| Choice count | Exactly 5 per question (A–E) | Reject entire batch |
| Correct answer | `correct_answer_id` ∈ {A, B, C, D, E} | Reject question |
| Correct answer match | Exactly one choice has same ID as `correct_answer_id` | Reject question |
| Stimulus length (LR) | 100–600 characters | Reject question |
| Passage length (RC) | 1500–3000 characters (~450-500 words) | Reject batch |
| Choice text length | 20–400 characters each | Reject question |
| Explanation present | Non-empty for correct + all 4 wrong | Reject question |
| Wrong answer types | All 4 wrong answers have a `wrong_answer_type` value | Log warning |
| Unique correct answers | No more than 2 questions with same correct letter in a 6-question batch | Log warning |
| Topic diversity | No two stimuli sharing >60% keyword overlap (jaccard) | Log warning |

If validation fails, return a typed `ValidationError` — do NOT store the batch.

---

## 10. Validation Pipeline

### `internal/generator/validator.go`

```go
type Validator struct {
    client *anthropic.Client
    model  string  // From ANTHROPIC_VALIDATION_MODEL env var
}

func NewValidator() *Validator {
    client := anthropic.NewClient()
    model := os.Getenv("ANTHROPIC_VALIDATION_MODEL")
    if model == "" {
        model = "claude-sonnet-4-5-20250929"
    }
    return &Validator{client: client, model: model}
}
```

### 10.1 Stage 2 — Self-Verification (Independent Solve)

A separate model instance reads each question (stimulus + stem + choices) and selects the best answer **without seeing the answer key**. If its answer disagrees with the generated correct answer, the question is rejected.

**Verification prompt** (sent once per question):

```
You are an expert LSAT tutor who has scored a 180 on the LSAT. You are reviewing a practice question to determine if the indicated correct answer is actually correct.

Read the following LSAT question carefully. Select the BEST answer from choices A through E. Think through each choice systematically before answering.

STIMULUS:
{stimulus}

QUESTION:
{question_stem}

CHOICES:
(A) {choice_a}
(B) {choice_b}
(C) {choice_c}
(D) {choice_d}
(E) {choice_e}

Respond with JSON only:
{
  "selected_answer": "B",
  "confidence": "high",
  "reasoning": "Step-by-step explanation of why you selected this answer and why each other choice is wrong...",
  "potential_issues": "Any ambiguity or problems you notice with the question construction..."
}
```

**Critical:** The verification prompt must NOT include the answer key, the correct answer explanation, or any hint about which answer is intended to be correct. The validator sees only what a test-taker would see.

**For RC questions:** Include the passage content before the stimulus/question.

**API parameters:** `max_tokens: 2048`, `temperature: 0.2`, timeout 30s, 1 retry on 429/500.

**Decision logic:**

| Verification Outcome | Confidence | Action |
|---|---|---|
| Answers match | High | Pass — store question, `validation_status = 'passed'` |
| Answers match | Medium or Low | Pass with flag — store but `validation_status = 'flagged'` |
| Answers disagree | Any | Reject — do not store; `validation_status = 'rejected'`; log both reasonings |

**Structs:**

```go
type ValidationResult struct {
    QuestionIndex   int    `json:"question_index"`
    SelectedAnswer  string `json:"selected_answer"`
    GeneratedAnswer string `json:"generated_answer"`
    Matches         bool   `json:"matches"`
    Confidence      string `json:"confidence"`
    Reasoning       string `json:"reasoning"`
    PotentialIssues string `json:"potential_issues"`
}

type BatchValidationResult struct {
    TotalQuestions int                `json:"total_questions"`
    PassedCount   int                `json:"passed_count"`
    FlaggedCount  int                `json:"flagged_count"`
    RejectedCount int                `json:"rejected_count"`
    Results       []ValidationResult `json:"results"`
}

func (v *Validator) ValidateBatch(ctx context.Context, batch *GeneratedBatch) (*BatchValidationResult, error)
func (v *Validator) ValidateQuestion(ctx context.Context, q GeneratedQuestion) (*ValidationResult, error)
```

### 10.2 Stage 3 — Adversarial Validation

For each question that passed Stage 2, attempt to argue for every wrong answer. If a compelling defense can be made, the question is ambiguous.

**Optimized single-call adversarial prompt** (one call per question, all 4 wrong answers):

```
You are reviewing an LSAT question for quality. For each incorrect answer choice, make the STRONGEST possible argument that it could be the correct answer. Then assess whether the marked correct answer is truly and unambiguously the best choice.

STIMULUS:
{stimulus}

QUESTION:
{question_stem}

MARKED CORRECT: ({correct_id}) {correct_text}

INCORRECT CHOICES TO CHALLENGE:
{for each wrong answer: ({choice_id}) {choice_text}}

For each incorrect choice, respond with JSON:
{
  "challenges": [
    {
      "choice_id": "A",
      "defense_strength": "weak",
      "defense_argument": "The strongest case for this answer...",
      "correct_answer_weakness": "Any weakness in the marked correct answer...",
      "recommendation": "accept"
    }
  ],
  "overall_quality": "high",
  "overall_recommendation": "accept"
}

defense_strength must be one of: "strong", "moderate", "weak", "none"
recommendation must be one of: "accept", "flag", "reject"
overall_quality must be one of: "high", "medium", "low"
overall_recommendation must be one of: "accept", "flag", "reject"
```

**API parameters:** `max_tokens: 4096`, `temperature: 0.2`, timeout 45s, 1 retry on 429/500.

**Decision logic:**

| Defense Strength (any wrong answer) | Action |
|---|---|
| All "none" or "weak" | Pass — `adversarial_score = 'clean'` |
| Any "moderate" | Flag — `adversarial_score = 'minor_concern'`, `flagged = true` |
| Any "strong" | Reject — `adversarial_score = 'ambiguous'`, do not store |

**Cost optimization:**
- Skip adversarial for "easy" difficulty questions (lower ambiguity risk)
- Toggleable via `ADVERSARIAL_ENABLED` env var
- All 4 wrong answers checked in a single API call per question

**Struct:**

```go
type AdversarialResult struct {
    QuestionIndex         int                    `json:"question_index"`
    Challenges            []AdversarialChallenge `json:"challenges"`
    OverallQuality        string                 `json:"overall_quality"`
    OverallRecommendation string                 `json:"overall_recommendation"`
}

type AdversarialChallenge struct {
    ChoiceID              string `json:"choice_id"`
    DefenseStrength       string `json:"defense_strength"`
    DefenseArgument       string `json:"defense_argument"`
    CorrectAnswerWeakness string `json:"correct_answer_weakness"`
    Recommendation        string `json:"recommendation"`
}

func (v *Validator) AdversarialCheckBatch(ctx context.Context, batch *GeneratedBatch) ([]AdversarialResult, error)
func (v *Validator) AdversarialCheckQuestion(ctx context.Context, q GeneratedQuestion) (*AdversarialResult, error)
```

### 10.3 Fallback Behavior

If a validation stage fails due to API errors (not logical disagreement):
- Log a warning with the batch ID and error
- Pass the question through **without** validation (do not block the pipeline)
- Set `validation_status = 'unvalidated'` — these should be reviewed manually
- Track unvalidated questions in quality metrics

---

## 11. Quality Scoring

### `internal/generator/quality.go`

Each question that survives the pipeline gets a composite quality score (0.0–1.0):

```
quality_score = (
    verification_confidence_score   * 0.40   +
    adversarial_cleanliness_score   * 0.35   +
    structural_compliance_score     * 0.25
)
```

| Component | Score |
|---|---|
| **Verification confidence** | high = 1.0, medium = 0.7, low = 0.4 |
| **Adversarial cleanliness** | all "none"/"weak" = 1.0, one "moderate" = 0.6, multiple "moderate" = 0.3 |
| **Structural compliance** | Stimulus length in range = +0.25, all choices in range = +0.25, all explanations present = +0.25, correct answer distribution OK = +0.25 |

**Thresholds:**
- Below **0.50** → auto-reject (do not store)
- **0.50–0.70** → store but set `flagged = true`
- Above **0.70** → serve normally

**Runtime difficulty recalibration** (run via admin endpoint or daily cron):

```go
func (s *Service) RecalibrateDifficulty() {
    // Query questions with 50+ responses
    // actual_difficulty = 1.0 - (times_correct / times_served)
    //
    // Thresholds:
    //   actual_difficulty < 0.30  → easy
    //   actual_difficulty 0.30-0.65 → medium
    //   actual_difficulty > 0.65  → hard
    //
    // If labeled difficulty != actual difficulty → flag for review
}
```

---

## 12. Question Service — Orchestration

### `internal/questions/service.go`

```go
type Service struct {
    store               *Store
    generator           *generator.Generator
    validator           *generator.Validator
    validationEnabled   bool
    adversarialEnabled  bool
}

func NewService(store *Store, gen *generator.Generator, val *generator.Validator) *Service {
    return &Service{
        store:              store,
        generator:          gen,
        validator:          val,
        validationEnabled:  os.Getenv("VALIDATION_ENABLED") != "false",
        adversarialEnabled: os.Getenv("ADVERSARIAL_ENABLED") != "false",
    }
}

func (s *Service) GenerateBatch(ctx context.Context, req models.GenerateBatchRequest) (*models.GenerateBatchResponse, error) {
    // 1. Create batch record with status "pending"
    batch := s.store.CreateBatch(req)

    // 2. Update status to "generating"
    s.store.UpdateBatchStatus(batch.ID, models.BatchGenerating)

    startTime := time.Now()

    // ── Stage 1: Generation ──────────────────────────────
    var generated *generator.GeneratedBatch
    var err error

    if req.Section == models.SectionLR {
        generated, err = s.generator.GenerateLRBatch(ctx, *req.LRSubtype, req.Difficulty, req.Count)
    } else {
        generated, err = s.generator.GenerateRCBatch(ctx, req.Difficulty, req.Count)
    }
    if err != nil {
        s.store.FailBatch(batch.ID, err.Error())
        return nil, err
    }

    // Parser structural validation happens inside Generate*Batch
    // If we get here, the batch passed structural checks

    passed := len(generated.Questions)
    flagged := 0
    rejected := 0

    // ── Stage 2: Self-Verification ───────────────────────
    s.store.UpdateBatchStatus(batch.ID, models.BatchValidating)

    if s.validationEnabled {
        validationResult, err := s.validator.ValidateBatch(ctx, generated)
        if err != nil {
            log.Printf("WARN: validation failed for batch %d: %v", batch.ID, err)
        } else {
            generated, flagged, rejected = s.filterByValidation(generated, validationResult)
            passed = len(generated.Questions)
        }
    }

    // ── Stage 3: Adversarial Check ───────────────────────
    if s.adversarialEnabled && req.Difficulty != models.DifficultyEasy {
        advResults, err := s.validator.AdversarialCheckBatch(ctx, generated)
        if err != nil {
            log.Printf("WARN: adversarial check failed for batch %d: %v", batch.ID, err)
        } else {
            generated, advFlagged, advRejected := s.filterByAdversarial(generated, advResults)
            flagged += advFlagged
            rejected += advRejected
            passed = len(generated.Questions)
        }
    }

    // ── Store surviving questions ─────────────────────────
    if len(generated.Questions) == 0 {
        s.store.FailBatch(batch.ID, "all questions rejected by validation")
        return nil, fmt.Errorf("all questions rejected by validation pipeline")
    }

    err = s.store.SaveGeneratedBatch(batch.ID, generated, req)
    if err != nil {
        s.store.FailBatch(batch.ID, err.Error())
        return nil, err
    }

    // Accumulate token counts from all stages
    tokenCounts := TokenCounts{
        PromptTokens:     generated.Usage.PromptTokens,
        OutputTokens:     generated.Usage.OutputTokens,
        ValidationTokens: 0, // Added by validation/adversarial stages if they ran
    }
    // Note: ValidateBatch and AdversarialCheckBatch should return token usage
    // which gets added to tokenCounts.ValidationTokens as each stage completes

    elapsed := time.Since(startTime).Milliseconds()
    s.store.CompleteBatch(batch.ID, passed, flagged, rejected, elapsed, tokenCounts)

    return &models.GenerateBatchResponse{
        BatchID:           batch.ID,
        Status:            models.BatchCompleted,
        QuestionsPassed:   passed,
        QuestionsFlagged:  flagged,
        QuestionsRejected: rejected,
        Message:           fmt.Sprintf("Generated %d questions (%d flagged, %d rejected)", passed, flagged, rejected),
    }, nil
}
```

**Important:** Generation runs synchronously. The API caller waits for the response (typically 30–60 seconds for a 6-question batch with full pipeline). For a future iteration, move to async with a job queue.

---

## 13. Question Store — Postgres CRUD

### `internal/questions/store.go`

```go
type Store struct {
    db *sql.DB
}

func NewStore(db *sql.DB) *Store

// ── Batch Management ────────────────────────────────────
func (s *Store) CreateBatch(req models.GenerateBatchRequest) (*models.QuestionBatch, error)
func (s *Store) UpdateBatchStatus(batchID int64, status models.BatchStatus) error
func (s *Store) FailBatch(batchID int64, errMsg string) error
func (s *Store) CompleteBatch(batchID int64, passed, flagged, rejected int, timeMs int64, tokens TokenCounts) error
func (s *Store) GetBatch(batchID int64) (*models.QuestionBatch, error)
func (s *Store) ListBatches(status *models.BatchStatus, limit int, offset int) ([]models.QuestionBatch, int, error)

// ── Question Storage ────────────────────────────────────
func (s *Store) SaveGeneratedBatch(batchID int64, batch *generator.GeneratedBatch, req models.GenerateBatchRequest) error
// Saves in a DB transaction: passage (if RC) → questions → answer_choices

// ── Validation Logging ──────────────────────────────────
func (s *Store) LogValidation(log ValidationLog) error
// Writes to validation_logs table for audit trail

// ── Serving Questions to Users ──────────────────────────
func (s *Store) GetDrillQuestions(section models.Section, subtype *models.LRSubtype, difficulty models.Difficulty, count int) ([]models.Question, error)
// 1. SELECT questions WHERE section/subtype/difficulty match AND validation_status IN ('passed', 'flagged') AND quality_score >= 0.50
// 2. ORDER BY times_served ASC (prioritize least-served)
// 3. LIMIT count
// 4. Eagerly load answer_choices for each question
// 5. Return fully hydrated Question structs with Choices populated

func (s *Store) GetQuestionWithChoices(questionID int64) (*models.Question, error)
func (s *Store) IncrementServed(questionID int64) error
func (s *Store) IncrementCorrect(questionID int64) error

// ── Admin Queries ───────────────────────────────────────
func (s *Store) GetQualityStats() (*QualityStats, error)
func (s *Store) GetGenerationStats() (*GenerationStats, error)
func (s *Store) GetFlaggedQuestions(limit int, offset int) ([]models.Question, int, error)
func (s *Store) GetRecalibrationCandidates(minResponses int) ([]RecalibrationCandidate, error)
```

---

## 14. HTTP Endpoints

### `internal/questions/handler.go`

```go
type Handler struct {
    service *Service
}

func NewHandler(service *Service) *Handler
```

### 14.1 Question Generation (Admin)

#### `POST /api/v1/questions/generate`

Triggers a new batch generation with full validation pipeline.

**Request:**
```json
{
  "section": "logical_reasoning",
  "lr_subtype": "strengthen",
  "difficulty": "medium",
  "count": 6
}
```

**Response (201):**
```json
{
  "batch_id": 42,
  "status": "completed",
  "questions_passed": 5,
  "questions_flagged": 1,
  "questions_rejected": 0,
  "message": "Generated 5 questions (1 flagged, 0 rejected)"
}
```

### 14.2 Drill (Frontend-Facing)

#### `GET /api/v1/questions/drill?section=logical_reasoning&subtype=strengthen&difficulty=medium&count=6`

Returns questions for a drill session. **Does NOT include answers or explanations.**

**Response (200):**
```json
{
  "questions": [
    {
      "id": 101,
      "section": "logical_reasoning",
      "lr_subtype": "strengthen",
      "difficulty": "medium",
      "stimulus": "A recent study of commuters in metropolitan areas...",
      "question_stem": "Which of the following, if true, most strengthens the argument?",
      "choices": [
        {"choice_id": "A", "choice_text": "The commuters surveyed..."},
        {"choice_id": "B", "choice_text": "The study controlled for..."},
        {"choice_id": "C", "choice_text": "Some metropolitan areas..."},
        {"choice_id": "D", "choice_text": "Public transit ridership..."},
        {"choice_id": "E", "choice_text": "Stress levels among..."}
      ]
    }
  ],
  "total": 6,
  "page": 1,
  "page_size": 6
}
```

### 14.3 Submit Answer

#### `POST /api/v1/questions/{id}/answer`

Submit an answer and get full feedback with explanations for all 5 choices.

**Request:**
```json
{
  "selected_choice_id": "B"
}
```

**Response (200):**
```json
{
  "correct": true,
  "correct_answer_id": "B",
  "explanation": "The argument concludes that public transit reduces stress. Choice B strengthens this by showing the study controlled for confounding variables...",
  "choices": [
    {"choice_id": "A", "choice_text": "...", "explanation": "Incorrect (IRRELEVANT): This information about commuter demographics doesn't address...", "is_correct": false},
    {"choice_id": "B", "choice_text": "...", "explanation": "Correct: This strengthens by ruling out the alternative explanation that...", "is_correct": true},
    {"choice_id": "C", "choice_text": "...", "explanation": "Incorrect (OUT OF SCOPE): This is about other metropolitan areas, not the study's...", "is_correct": false},
    {"choice_id": "D", "choice_text": "...", "explanation": "Incorrect (WEAKENER): This actually undermines the argument by suggesting...", "is_correct": false},
    {"choice_id": "E", "choice_text": "...", "explanation": "Incorrect (RESTATES PREMISE): This merely repeats what the stimulus already states...", "is_correct": false}
  ]
}
```

This endpoint also calls `store.IncrementServed(questionID)` and, if correct, `store.IncrementCorrect(questionID)`.

### 14.4 Individual Question

#### `GET /api/v1/questions/{id}`

Get a single question with choices (without answers — same format as drill).

### 14.5 Batch Management (Admin)

#### `GET /api/v1/questions/batches?status=completed&limit=20&offset=0`

List all generation batches with pagination.

#### `GET /api/v1/questions/batches/{id}`

Get a single batch with its questions.

### 14.6 Quality & Admin Endpoints

#### `GET /api/v1/admin/quality-stats`

Returns quality pipeline metrics:

```json
{
  "total_generated": 1200,
  "total_passed": 980,
  "total_flagged": 150,
  "total_rejected": 70,
  "pass_rate": 0.817,
  "rejection_reasons": {
    "verification_mismatch": 45,
    "adversarial_ambiguous": 18,
    "structural_validation": 7
  },
  "quality_score_distribution": {
    "0.9-1.0": 420,
    "0.8-0.9": 310,
    "0.7-0.8": 250,
    "below_0.7": 150
  },
  "difficulty_calibration": {
    "easy_accurate": 85,
    "easy_mismatched": 12,
    "medium_accurate": 210,
    "medium_mismatched": 30,
    "hard_accurate": 180,
    "hard_mismatched": 25
  }
}
```

#### `GET /api/v1/admin/generation-stats`

Returns cost and usage data:

```json
{
  "cost": {
    "today_cents": 245,
    "this_week_cents": 1230,
    "this_month_cents": 3780,
    "daily_limit_cents": 1000
  },
  "batches": {
    "today": 12,
    "this_week": 56,
    "this_month": 167
  },
  "tokens": {
    "generation_total": 420000,
    "validation_total": 180000
  }
}
```

#### `POST /api/v1/admin/recalibrate`

Triggers difficulty recalibration for all questions with 50+ student responses. Returns a report of mismatches.

**Response (200):**
```json
{
  "total_evaluated": 475,
  "recalibrated": 67,
  "details": [
    {
      "question_id": 203,
      "labeled_difficulty": "hard",
      "actual_accuracy": 0.82,
      "suggested_difficulty": "easy",
      "times_served": 120,
      "times_correct": 98
    }
  ]
}
```

#### `GET /api/v1/admin/flagged?limit=20&offset=0`

Returns flagged questions for manual review (questions where `flagged = true` or `validation_status = 'flagged'`).

**Response (200):**
```json
{
  "questions": [
    {
      "id": 305,
      "section": "logical_reasoning",
      "lr_subtype": "assumption",
      "difficulty": "hard",
      "stimulus": "...",
      "question_stem": "...",
      "validation_status": "flagged",
      "adversarial_score": "minor_concern",
      "quality_score": 0.62,
      "validation_reasoning": "Selected B with medium confidence. The argument for C is also plausible...",
      "choices": [...]
    }
  ],
  "total": 150,
  "page": 1,
  "page_size": 20
}
```

---

## 15. Router Registration

### Changes to `cmd/server/main.go`

The current `main.go` already imports `generator` and `questions` and wires up the generator and question routes. Update it to also initialize the validator:

```go
// Update the existing initialization block:
gen := generator.NewGenerator()       // Already present — auto-selects CLI, SDK, or mock based on env
val := generator.NewValidator()       // NEW — add this line
questionStore := questions.NewStore(db)
questionService := questions.NewService(questionStore, gen, val)  // UPDATE — add val parameter
questionHandler := questions.NewHandler(questionService)

// Question generation & batch management (protected)
protected.HandleFunc("/questions/generate", questionHandler.GenerateBatch).Methods("POST")
protected.HandleFunc("/questions/batches", questionHandler.ListBatches).Methods("GET")
protected.HandleFunc("/questions/batches/{id}", questionHandler.GetBatch).Methods("GET")

// Question serving (protected)
protected.HandleFunc("/questions/drill", questionHandler.GetDrill).Methods("GET")
protected.HandleFunc("/questions/{id}", questionHandler.GetQuestion).Methods("GET")
protected.HandleFunc("/questions/{id}/answer", questionHandler.SubmitAnswer).Methods("POST")

// Admin endpoints (protected — add admin role check later)
protected.HandleFunc("/admin/quality-stats", questionHandler.GetQualityStats).Methods("GET")
protected.HandleFunc("/admin/generation-stats", questionHandler.GetGenerationStats).Methods("GET")
protected.HandleFunc("/admin/recalibrate", questionHandler.Recalibrate).Methods("POST")
protected.HandleFunc("/admin/flagged", questionHandler.GetFlaggedQuestions).Methods("GET")
```

---

## 16. Cost Controls

### Per-Batch Estimated Cost (6 questions, full pipeline)

| Stage | Model | Input Tokens | Output Tokens | Est. Cost |
|---|---|---|---|---|
| Generation | Opus | ~2,500 | ~5,000 | ~$0.13 |
| Verification (×6) | Sonnet | ~6,000 | ~3,000 | ~$0.04 |
| Adversarial (×6, batched) | Sonnet | ~8,000 | ~4,000 | ~$0.05 |
| **Total** | | | | **~$0.22** |

For 1,000 questions (~167 batches): **~$37**

### Safeguards

- `MAX_DAILY_GENERATION_COST` env var (default: $10.00 = 1000 cents)
- Track cumulative daily cost in memory (reset at midnight UTC)
- When limit reached, return HTTP 429 with `"Daily generation limit reached"`
- Log all token usage per batch in `question_batches.prompt_tokens`, `output_tokens`, `validation_tokens`, `total_cost_cents`

---

## 17. Testing Strategy

### Unit Tests

| File | Tests |
|---|---|
| `generator/parser_test.go` | JSON parsing with valid inputs, malformed JSON, missing fields, boundary lengths |
| `generator/prompts_test.go` | Prompt construction for every subtype; verify stems, correct/wrong rules injected |
| `generator/quality_test.go` | Quality score formula with various inputs, threshold behavior |
| `questions/store_test.go` | DB operations with test database — CRUD, drill query ordering, increment counters |

### Integration Tests

| File | Tests |
|---|---|
| `questions/handler_test.go` | HTTP handler tests with mocked service |
| `generator/client_test.go` | Live API test with 1-question batch; gated behind `INTEGRATION_TEST=true` env flag |
| `generator/validator_test.go` | Live validation + adversarial with known-good/known-bad questions; gated behind `INTEGRATION_TEST=true` |

### Mock for Local Dev (no LLM at all)

- `MOCK_GENERATOR=true` env var
- When set, `Generator` returns hardcoded questions instead of calling any LLM
- Mock questions match the same JSON schema the frontend already consumes
- Validation stages are skipped when `MOCK_GENERATOR=true`
- Use this for frontend development and handler/store unit testing

### Claude CLI Mode (local dev with real LLM)

- `USE_CLI_GENERATOR=true` env var
- When set, `Generator` shells out to the `claude` CLI instead of the Anthropic SDK
- Uses your existing Claude plan — no API key needed, no per-token charges
- Same prompts, same parser, same validation pipeline — just a different transport
- Token usage is not tracked (cost controls are skipped)
- Best for: **iterating on prompts, testing the parser, building up a real question bank locally, and validating the full pipeline end-to-end before connecting the production API**
- Requires `claude` CLI installed and authenticated on the machine running the backend
- Set `CLAUDE_CLI_PATH` if the binary isn't on your PATH

---

## 18. Implementation Phases

### Phase A — Enhanced Prompts + Generation (no validation cost)

**Files:** `models/question.go`, `generator/client.go`, `generator/cli_client.go`, `generator/prompts.go`, `generator/parser.go`, `questions/store.go`, `questions/service.go`, `questions/handler.go`, `database/database.go`, `cmd/server/main.go`

**What ships:** Full generation pipeline with per-subtype correct/wrong answer rules, structural validation in the parser, question serving endpoints, answer submission. No validation stages yet — `VALIDATION_ENABLED=false`.

**Start with `USE_CLI_GENERATOR=true`** — use the Claude CLI against your plan to iterate on prompts, test the parser, and build up a question bank locally at no API cost. Switch to `USE_CLI_GENERATOR=false` once prompts are stable and you're ready for production model selection (Opus generation / Sonnet validation).

**Expected answer accuracy:** ~85-90%

### Phase B — Self-Verification (Stage 2)

**Files:** `generator/validator.go` (verification only), update `questions/service.go`

**What ships:** Independent model solve for every generated question. Questions with mismatched answers are rejected. `VALIDATION_ENABLED=true`.

**Expected answer accuracy:** ~93-96%

### Phase C — Adversarial Check (Stage 3)

**Files:** Add adversarial logic to `generator/validator.go`, update `questions/service.go`

**What ships:** Devil's advocate pass on every wrong answer. Ambiguous questions caught and rejected. `ADVERSARIAL_ENABLED=true`.

**Expected answer accuracy:** ~97%+

### Phase D — Quality Scoring + Admin Dashboard

**Files:** `generator/quality.go`, admin handler methods, `validation_logs` table usage

**What ships:** Composite quality scores, auto-flagging, difficulty recalibration, quality and cost dashboards.

Each phase is independently deployable. Phase A should ship with the initial feature. Phase B should follow immediately. Phases C and D can be added once question volume justifies the cost.

---

## Appendix: File-to-Component Map

| Component | File | Purpose |
|---|---|---|
| Models | `internal/models/question.go` | All question/batch/choice/passage/validation structs + enums |
| Generator (SDK) | `internal/generator/client.go` | Anthropic SDK wrapper for production API calls |
| Generator (CLI) | `internal/generator/cli_client.go` | Claude CLI wrapper for local dev (uses your plan) |
| Prompts | `internal/generator/prompts.go` | System + user prompts, per-subtype rules, stem banks |
| Parser | `internal/generator/parser.go` | JSON → Go structs + structural validation |
| Validator | `internal/generator/validator.go` | Stage 2 (verification) + Stage 3 (adversarial) |
| Quality | `internal/generator/quality.go` | Composite quality scoring formula |
| Service | `internal/questions/service.go` | 3-stage pipeline orchestration |
| Store | `internal/questions/store.go` | Postgres CRUD for all tables |
| Handler | `internal/questions/handler.go` | HTTP endpoints (generation, serving, admin) |
| Migration | `internal/database/database.go` | 5 new tables added to Migrate() |
| Router | `cmd/server/main.go` | Wire up new routes + initialize generator/validator/service |
