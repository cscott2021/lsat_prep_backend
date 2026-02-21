# Question Quality & Validation Pipeline Spec

## Overview

This spec extends `QUESTION_GENERATION_SPEC.md` with a multi-stage validation pipeline that ensures every generated question meets LSAT authenticity standards and that the correct answer is provably right. The pipeline uses separate API calls for generation and validation — the same model that wrote a question cannot be the sole judge of its correctness.

---

## 1. Architecture — Three-Stage Pipeline

```
┌─────────────────┐     ┌──────────────────────┐     ┌────────────────────┐
│  STAGE 1         │     │  STAGE 2              │     │  STAGE 3            │
│  Generation      │────▶│  Self-Verification    │────▶│  Adversarial Check  │
│  (Opus, t=0.8)   │     │  (Sonnet, t=0.2)      │     │  (Sonnet, t=0.2)    │
│                  │     │                       │     │                     │
│  Produces batch  │     │  Solves each question │     │  Argues for every   │
│  of questions    │     │  WITHOUT seeing the   │     │  wrong answer —     │
│  with answers    │     │  answer key           │     │  flags if any       │
│  + explanations  │     │                       │     │  argument succeeds  │
└─────────────────┘     └──────────────────────┘     └────────────────────┘
         │                        │                           │
         ▼                        ▼                           ▼
   Store as "draft"        Compare answers.            Score each question.
                           If mismatch → flag          If wrong answer is
                           for manual review            defensible → reject
                           or reject.                   or flag.
```

### Model Allocation

| Stage | Model | Temperature | Extended Thinking | Why |
|---|---|---|---|---|
| Generation | `claude-opus-4-5-20251101` | 0.7–0.8 | Off | Creative diversity with controlled quality; **overrides** the 1.0 in the original spec — research shows 0.7–0.8 balances variety with coherence for structured outputs |
| Self-Verification | `claude-sonnet-4-5-20250929` | 0.2 | Off | Analytical precision; cheaper; acts as independent verifier |
| Adversarial Check | `claude-sonnet-4-5-20250929` | 0.2 | Off | Stress-test each wrong answer; should NOT find valid defenses |

### New Environment Variables

```
ANTHROPIC_VALIDATION_MODEL=claude-sonnet-4-5-20250929
VALIDATION_ENABLED=true              # Toggle validation pipeline (disable for dev)
ADVERSARIAL_ENABLED=true             # Toggle stage 3 (can run stage 2 only)
MAX_VALIDATION_RETRIES=1             # Retries per stage on transient failure
```

---

## 2. Stage 1 — Enhanced Generation Prompts

### 2.1 Per-Subtype Correct Answer Properties

The generation prompt must include explicit instructions for what makes the correct answer RIGHT for each question type. This prevents the model from generating vague or debatable correct answers.

Add to `internal/generator/prompts.go`:

```go
var subtypeCorrectAnswerRules = map[LRSubtype]string{

    SubtypeStrengthen: `
CORRECT ANSWER RULES (Strengthen):
- The correct answer must provide NEW information not stated in the stimulus
- It must make the conclusion MORE likely to be true
- It typically does one of: (a) rules out an alternative explanation, (b) provides an additional premise that supports the causal link, (c) shows the mechanism by which the conclusion follows
- It should NOT merely restate a premise or the conclusion
- It should NOT be so strong that it independently proves the conclusion`,

    SubtypeWeaken: `
CORRECT ANSWER RULES (Weaken):
- The correct answer must provide NEW information not stated in the stimulus
- It must make the conclusion LESS likely to be true
- It typically does one of: (a) introduces a plausible alternative explanation, (b) breaks the causal link, (c) shows a flaw in the evidence, (d) provides a counterexample
- It should NOT merely contradict the conclusion — it must attack the REASONING
- It should NOT be irrelevant to the argument's logical structure`,

    SubtypeAssumption: `
CORRECT ANSWER RULES (Assumption — Necessary):
- The correct answer states something that MUST be true for the argument to work
- Apply the Negation Test: if you negate the correct answer, the argument should fall apart
- It fills a gap between the premises and conclusion
- It should NOT be a mere restatement of a premise
- It should NOT be something that strengthens but is not required
- It should NOT be the conclusion itself`,

    SubtypeFlaw: `
CORRECT ANSWER RULES (Flaw):
- The correct answer must accurately DESCRIBE the logical error in the argument
- It must be phrased in abstract/general terms describing the reasoning error
- Common flaws: confusing necessary/sufficient conditions, correlation vs causation, ad hominem, hasty generalization, equivocation, part-whole fallacy, appeal to authority, false dichotomy
- The description must match what actually happens in the stimulus — not just name a flaw that sounds plausible
- It should NOT describe the argument's conclusion as wrong — it should describe HOW the reasoning fails`,

    SubtypeMustBeTrue: `
CORRECT ANSWER RULES (Must Be True / Inference):
- The correct answer must be LOGICALLY ENTAILED by the stimulus — not merely likely
- If the stimulus premises are true, the correct answer cannot be false
- It is typically a logical consequence of combining two or more premises
- It should NOT require any assumptions beyond what is stated
- It should NOT go beyond the scope of the stimulus
- It should NOT be merely consistent with the stimulus — it must be required by it`,

    SubtypeMostStrongly: `
CORRECT ANSWER RULES (Most Strongly Supported):
- The correct answer is the claim most supported by the stimulus evidence
- Unlike Must Be True, it need not be logically entailed — just most probable given the evidence
- It should follow naturally from the information provided
- It should NOT require significant additional assumptions`,

    SubtypeMethodReasoning: `
CORRECT ANSWER RULES (Method of Reasoning):
- The correct answer abstractly describes the argumentative technique used
- It must accurately map onto the stimulus structure (premises → conclusion)
- Common methods: analogy, counterexample, reductio ad absurdum, appeal to evidence, elimination of alternatives, establishing a general principle
- The description must match the actual logical moves in the stimulus`,

    SubtypeParallelReasoning: `
CORRECT ANSWER RULES (Parallel Reasoning):
- The correct answer must replicate the EXACT logical structure of the stimulus
- Match: (a) the type of premises (conditional, causal, statistical), (b) the validity/invalidity of the reasoning, (c) the conclusion type
- Topic should differ but structure should be identical
- If the stimulus has a flaw, the correct answer must have the same flaw`,

    SubtypePrinciple: `
CORRECT ANSWER RULES (Principle):
- The correct answer states a general rule that, if true, justifies the specific argument in the stimulus
- It must be broad enough to be a principle (not just a restatement) but specific enough to actually support this argument
- The argument's conclusion should follow from the principle + the premises`,

    SubtypeEvaluate: `
CORRECT ANSWER RULES (Evaluate):
- The correct answer identifies information that would help determine if the argument is sound
- Both possible answers to the question (yes/no) should have different implications for the argument
- One answer should strengthen, the other should weaken — that's what makes it useful for evaluation`,

    SubtypeMainConclusion: `
CORRECT ANSWER RULES (Main Conclusion):
- The correct answer is a near-paraphrase of the argument's main point
- It is what the other statements in the stimulus are trying to prove
- It should NOT be a premise, intermediate conclusion, or background information`,

    SubtypeRoleOfStatement: `
CORRECT ANSWER RULES (Role of a Statement):
- The correct answer describes the function a specific claim plays in the argument
- Functions: main conclusion, intermediate conclusion, premise, evidence, counterexample, background, concession
- The description must accurately characterize the relationship between the statement and the rest of the argument`,

    SubtypeParallelFlaw: `
CORRECT ANSWER RULES (Parallel Flaw):
- The correct answer contains an argument with the SAME logical flaw as the stimulus
- Both the structure AND the error type must match
- The topic must be completely different from the stimulus
- If the stimulus confuses necessary/sufficient, the correct answer must too
- If the stimulus makes a causal error, the correct answer must make the same kind`,

    SubtypeApplyPrinciple: `
CORRECT ANSWER RULES (Apply Principle):
- The stimulus states a general principle or rule
- The correct answer presents a specific situation where the principle applies correctly
- The application must follow logically from the principle's conditions
- The correct answer should not require any additional principles or unstated assumptions
- All conditions of the principle must be met in the specific case`,
}
```

### 2.2 Per-Subtype Wrong Answer Archetypes

Each question type has characteristic wrong answer patterns. The generation prompt must instruct the model to use these specific archetypes to create plausible but definitively wrong distractors.

```go
var subtypeWrongAnswerRules = map[LRSubtype]string{

    SubtypeStrengthen: `
WRONG ANSWER CONSTRUCTION (Strengthen):
Each wrong answer must fall into one of these categories — label each in your explanation:
1. IRRELEVANT: True-sounding but does not connect to the argument's logical gap
2. WEAKENER: Actually undermines the argument (common trap for test-takers who confuse the task)
3. OUT OF SCOPE: Addresses a topic related to but distinct from the argument's core claim
4. RESTATES PREMISE: Merely repeats information already in the stimulus without adding support
At least one wrong answer should be a WEAKENER (the most common student error on strengthen questions).`,

    SubtypeWeaken: `
WRONG ANSWER CONSTRUCTION (Weaken):
1. IRRELEVANT: Sounds related but doesn't attack the reasoning
2. STRENGTHENER: Actually supports the argument (reversal trap)
3. OUT OF SCOPE: About a related but different issue
4. TOO EXTREME: Addresses an extreme version of the claim not actually made
At least one wrong answer should be a STRENGTHENER.`,

    SubtypeAssumption: `
WRONG ANSWER CONSTRUCTION (Assumption):
1. HELPS BUT NOT REQUIRED: Strengthens the argument but isn't necessary (fails negation test)
2. RESTATES PREMISE: Already stated in the stimulus
3. RESTATES CONCLUSION: Says what the argument concludes, not what it assumes
4. OUT OF SCOPE: Irrelevant to the argument's logical structure
The "HELPS BUT NOT REQUIRED" distractor is the hardest — it must be clearly not required when negated.`,

    SubtypeFlaw: `
WRONG ANSWER CONSTRUCTION (Flaw):
1. WRONG FLAW: Accurately describes a real logical flaw, but NOT the one in this argument
2. DESCRIBES THE ARGUMENT CORRECTLY: States what the argument does without identifying an error
3. TOO BROAD: Describes a flaw in vague terms that could apply to almost any argument
4. MISCHARACTERIZES: Describes something the argument doesn't actually do
The "WRONG FLAW" distractor is the classic trap — it's a valid flaw type but doesn't match this stimulus.`,

    SubtypeMustBeTrue: `
WRONG ANSWER CONSTRUCTION (Must Be True):
1. COULD BE TRUE: Consistent with the stimulus but not required by it
2. GOES BEYOND: Requires assumptions not in the stimulus
3. PARTIAL INFERENCE: True of some cases mentioned but overgeneralizes
4. REVERSAL: Gets a conditional relationship backwards
The key distinction: must be true vs. could be true.`,

    SubtypeMethodReasoning: `
WRONG ANSWER CONSTRUCTION (Method of Reasoning):
1. WRONG METHOD: Accurately describes a reasoning method, but not the one used here
2. PARTIAL DESCRIPTION: Captures one aspect of the argument but misses the main technique
3. MISCHARACTERIZES: Describes the argument doing something it doesn't do
4. TOO SPECIFIC/GENERAL: Either over- or under-describes the method`,

    SubtypeMostStrongly: `
WRONG ANSWER CONSTRUCTION (Most Strongly Supported):
1. COULD BE TRUE: Consistent with the stimulus but not particularly supported by it
2. GOES BEYOND: Requires significant assumptions not in the stimulus
3. REVERSAL: Gets the direction of a relationship backwards
4. EXTREME LANGUAGE: Uses "always," "never," "all" when the stimulus hedges`,

    SubtypeParallelReasoning: `
WRONG ANSWER CONSTRUCTION (Parallel Reasoning):
1. SAME TOPIC, WRONG STRUCTURE: Similar subject matter but different logical form
2. FLAWED WHEN ORIGINAL IS VALID: Introduces a logical error not present in the original
3. VALID WHEN ORIGINAL IS FLAWED: Fixes the error, making the reasoning valid
4. PARTIALLY PARALLEL: Matches some but not all structural elements`,

    SubtypeParallelFlaw: `
WRONG ANSWER CONSTRUCTION (Parallel Flaw):
1. DIFFERENT FLAW: Contains a logical error, but a different type than the stimulus
2. NO FLAW: The reasoning is actually valid (no parallel error)
3. SAME TOPIC: Mimics the stimulus subject but with a different logical structure
4. PARTIALLY PARALLEL: Matches the structure but the flaw manifests differently`,

    SubtypePrinciple: `
WRONG ANSWER CONSTRUCTION (Principle):
1. TOO NARROW: A principle that only covers part of the argument
2. TOO BROAD: A principle that's true but doesn't specifically justify THIS argument
3. WRONG DIRECTION: A principle that would justify the opposite conclusion
4. IRRELEVANT PRINCIPLE: A valid principle that doesn't connect to the argument's reasoning`,

    SubtypeApplyPrinciple: `
WRONG ANSWER CONSTRUCTION (Apply Principle):
1. VIOLATES A CONDITION: The scenario doesn't meet all conditions of the principle
2. WRONG OUTCOME: Meets the conditions but draws the wrong conclusion
3. SUPERFICIALLY SIMILAR: Shares surface features with the principle but doesn't actually apply
4. REVERSES APPLICATION: Applies the principle backwards`,

    SubtypeEvaluate: `
WRONG ANSWER CONSTRUCTION (Evaluate):
1. ONE-DIRECTIONAL: The answer to the question only strengthens OR weakens, not both depending on the answer
2. IRRELEVANT QUESTION: The answer wouldn't affect the argument either way
3. ALREADY ANSWERED: The stimulus already provides the information asked about
4. WRONG SCOPE: Asks about something adjacent but not central to the argument's logic`,

    SubtypeMainConclusion: `
WRONG ANSWER CONSTRUCTION (Main Conclusion):
1. PREMISE MASQUERADING: A premise stated in the stimulus, not the conclusion
2. INTERMEDIATE CONCLUSION: A sub-conclusion that supports the main conclusion
3. BACKGROUND INFO: Context from the stimulus that isn't argued for or against
4. OVERSTATED CONCLUSION: Exaggerates what the argument actually claims`,

    SubtypeRoleOfStatement: `
WRONG ANSWER CONSTRUCTION (Role of a Statement):
1. WRONG ROLE: Correctly identifies that the statement is present but mischaracterizes its function
2. CONFUSES PREMISE/CONCLUSION: Calls a premise a conclusion or vice versa
3. INVENTS A ROLE: Describes a function (like "counterexample") that the statement doesn't serve
4. RIGHT ROLE, WRONG RELATIONSHIP: Correctly names the role type but misstates what it supports or opposes`,
}
```

### 2.3 Enhanced System Prompt — Stimulus Construction Rules

Add to the system prompt in `prompts.go` to ensure stimuli mirror real LSAT construction:

```
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
```

### 2.4 Reading Comprehension Passage Construction

```go
var rcPassageRules = `
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

Comparative passages (when is_comparative = true):
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

6. Strengthen/Weaken (passage-based) (0-1 per passage)
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
`
```

---

## 3. Stage 2 — Self-Verification (Independent Solve)

### Purpose

A separate model instance reads each question (stimulus + stem + choices) and selects the best answer **without seeing the answer key**. If its answer disagrees with the generated correct answer, the question is flagged.

### New File: `internal/generator/validator.go`

```go
type Validator struct {
    client *anthropic.Client
    model  string  // Sonnet by default
}

type ValidationResult struct {
    QuestionIndex     int      `json:"question_index"`
    SelectedAnswer    string   `json:"selected_answer"`     // A-E
    GeneratedAnswer   string   `json:"generated_answer"`    // A-E from generation
    Matches           bool     `json:"matches"`
    Confidence        string   `json:"confidence"`          // "high", "medium", "low"
    Reasoning         string   `json:"reasoning"`           // Why the validator chose this answer
    FlagReason        string   `json:"flag_reason,omitempty"` // If mismatch, why
}

type BatchValidationResult struct {
    TotalQuestions   int                `json:"total_questions"`
    PassedCount      int                `json:"passed_count"`
    FlaggedCount     int                `json:"flagged_count"`
    RejectedCount    int                `json:"rejected_count"`
    Results          []ValidationResult `json:"results"`
}

func (v *Validator) ValidateBatch(ctx context.Context, batch *GeneratedBatch) (*BatchValidationResult, error)
func (v *Validator) ValidateQuestion(ctx context.Context, q GeneratedQuestion) (*ValidationResult, error)
```

### API Call Parameters for Validation Stages

| Parameter | Stage 2 (Verification) | Stage 3 (Adversarial) |
|---|---|---|
| `max_tokens` | 2048 (single question analysis) | 4096 (all 4 wrong answers in one call) |
| `temperature` | 0.2 | 0.2 |
| `timeout` | 30 seconds per question | 45 seconds per question |
| Retry logic | 1 retry on 429/500, exponential backoff | Same |
| Fallback on failure | Pass question without validation (log warning) | Pass question without adversarial (log warning) |

### Verification Prompt

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

### Decision Logic

| Verification Outcome | Confidence | Action |
|---|---|---|
| Answers match | High | ✅ Pass — store question |
| Answers match | Medium/Low | ⚠️ Pass with flag — store but mark for review |
| Answers disagree | Any | ❌ Reject — do not store; log both reasonings |

### Important Implementation Detail

The verification prompt must NOT include the answer key, the correct answer explanation, or any hint about which answer is intended to be correct. The validator sees only what a test-taker would see.

---

## 4. Stage 3 — Adversarial Validation

### Purpose

For each wrong answer choice, attempt to construct a logical argument for why it could be correct. If a compelling argument can be made for a wrong answer, the question has a defensibility problem.

### Adversarial Prompt

```
You are a devil's advocate reviewing an LSAT question. Your job is to argue that a specific answer choice is actually the BEST answer, even though it has been marked as incorrect.

STIMULUS:
{stimulus}

QUESTION:
{question_stem}

The following answer has been marked as INCORRECT:
({choice_id}) {choice_text}

The answer marked as CORRECT is:
({correct_id}) {correct_text}

Your task:
1. Make the STRONGEST possible case that ({choice_id}) is actually the better answer
2. Identify any weakness in the argument for ({correct_id})
3. Rate how defensible ({choice_id}) is as the correct answer

Respond with JSON only:
{
  "defense_strength": "strong" | "moderate" | "weak" | "none",
  "defense_argument": "The strongest case for this being correct...",
  "correct_answer_weakness": "Any weakness in the marked correct answer...",
  "recommendation": "accept" | "flag" | "reject"
}
```

### Decision Logic

Run adversarial check on all 4 wrong answers per question:

| Defense Strength (any wrong answer) | Action |
|---|---|
| All "none" or "weak" | ✅ Pass — correct answer is clearly best |
| Any "moderate" | ⚠️ Flag for review — store but mark `flagged = true` |
| Any "strong" | ❌ Reject — the question is ambiguous |

### Cost Optimization

Adversarial checks run 4 API calls per question (one per wrong answer). For a 6-question batch, that's 24 calls. To manage cost:

- Batch the 4 wrong-answer checks into a single prompt per question (include all 4 in one call)
- Use Sonnet (cheaper, still excellent at argumentation)
- Skip adversarial for "easy" difficulty questions (simpler arguments, lower ambiguity risk)
- Make stage 3 toggleable via `ADVERSARIAL_ENABLED` env var

### Optimized Single-Call Adversarial Prompt

```
You are reviewing an LSAT question for quality. For each incorrect answer choice, attempt to argue it could be the correct answer.

STIMULUS:
{stimulus}

QUESTION:
{question_stem}

MARKED CORRECT: ({correct_id}) {correct_text}

INCORRECT CHOICES TO CHALLENGE:
(A) {choice_a_text}
(B) {choice_b_text}
... (excluding the correct answer)

For each incorrect choice, respond with JSON:
{
  "challenges": [
    {
      "choice_id": "A",
      "defense_strength": "weak",
      "defense_argument": "...",
      "recommendation": "accept"
    },
    ...
  ],
  "overall_quality": "high" | "medium" | "low",
  "overall_recommendation": "accept" | "flag" | "reject"
}
```

---

## 5. Updated Service Orchestration

### Changes to `internal/questions/service.go`

```go
func (s *Service) GenerateBatch(ctx context.Context, req models.GenerateBatchRequest) (*models.QuestionBatch, error) {
    batch := s.store.CreateBatch(req)
    s.store.UpdateBatchStatus(batch.ID, models.BatchGenerating)

    startTime := time.Now()

    // ── Stage 1: Generation ──────────────────────────────
    var generated *generator.GeneratedBatch
    if req.Section == models.SectionLR {
        generated, err = s.generator.GenerateLRBatch(ctx, *req.LRSubtype, req.Difficulty, req.Count)
    } else {
        generated, err = s.generator.GenerateRCBatch(ctx, req.Difficulty, req.Count)
    }
    if err != nil {
        s.store.FailBatch(batch.ID, err.Error())
        return batch, err
    }

    // ── Stage 2: Self-Verification ───────────────────────
    if s.validationEnabled {
        validationResult, err := s.validator.ValidateBatch(ctx, generated)
        if err != nil {
            // Validation service failure — log but don't block
            log.Printf("WARN: validation failed for batch %d: %v", batch.ID, err)
        } else {
            generated = s.filterByValidation(generated, validationResult)
        }
    }

    // ── Stage 3: Adversarial Check ───────────────────────
    if s.adversarialEnabled && req.Difficulty != models.DifficultyEasy {
        advResult, err := s.validator.AdversarialCheck(ctx, generated)
        if err != nil {
            log.Printf("WARN: adversarial check failed for batch %d: %v", batch.ID, err)
        } else {
            generated = s.filterByAdversarial(generated, advResult)
        }
    }

    // ── Store surviving questions ─────────────────────────
    if len(generated.Questions) == 0 {
        s.store.FailBatch(batch.ID, "all questions rejected by validation")
        return batch, fmt.Errorf("all questions rejected")
    }

    err = s.store.SaveGeneratedBatch(batch.ID, generated, req)
    elapsed := time.Since(startTime).Milliseconds()
    s.store.CompleteBatch(batch.ID, len(generated.Questions), elapsed, tokenCounts)

    return batch, nil
}

// filterByValidation removes or flags questions based on verification results
func (s *Service) filterByValidation(batch *generator.GeneratedBatch, result *generator.BatchValidationResult) *generator.GeneratedBatch {
    var surviving []generator.GeneratedQuestion
    for i, q := range batch.Questions {
        vr := result.Results[i]
        if vr.Matches {
            surviving = append(surviving, q)
        } else {
            log.Printf("REJECTED question %d: validator chose %s, generated answer was %s. Reasoning: %s",
                i, vr.SelectedAnswer, vr.GeneratedAnswer, vr.Reasoning)
        }
    }
    batch.Questions = surviving
    return batch
}
```

---

## 6. Database Schema Updates

### New columns on `questions` table

```sql
ALTER TABLE questions ADD COLUMN IF NOT EXISTS
    validation_status VARCHAR(20) DEFAULT 'unvalidated';  -- 'unvalidated', 'passed', 'flagged', 'rejected'

ALTER TABLE questions ADD COLUMN IF NOT EXISTS
    validation_reasoning TEXT;  -- Validator's reasoning for its answer choice

ALTER TABLE questions ADD COLUMN IF NOT EXISTS
    adversarial_score VARCHAR(20);  -- 'clean', 'minor_concern', 'ambiguous'
```

### New table: `validation_logs`

```sql
CREATE TABLE IF NOT EXISTS validation_logs (
    id                  BIGSERIAL PRIMARY KEY,
    question_id         BIGINT REFERENCES questions(id),
    batch_id            BIGINT REFERENCES question_batches(id),
    stage               VARCHAR(20) NOT NULL,           -- 'verification', 'adversarial'
    model_used          VARCHAR(100),
    generated_answer    VARCHAR(1),
    validator_answer    VARCHAR(1),
    matches             BOOLEAN,
    confidence          VARCHAR(20),
    reasoning           TEXT,
    adversarial_details JSONB,                          -- Full adversarial results per wrong answer
    prompt_tokens       INT,
    output_tokens       INT,
    created_at          TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_validation_question ON validation_logs(question_id);
CREATE INDEX IF NOT EXISTS idx_validation_batch ON validation_logs(batch_id, stage);
```

---

## 7. Quality Scoring Formula

Each question that passes the pipeline gets a composite quality score (0.0–1.0) stored in `quality_score`:

```
quality_score = (
    verification_confidence_score   * 0.40   +
    adversarial_cleanliness_score   * 0.35   +
    structural_compliance_score     * 0.25
)
```

Where:

| Component | Score |
|---|---|
| **Verification confidence** | high = 1.0, medium = 0.7, low = 0.4 |
| **Adversarial cleanliness** | all "none"/"weak" = 1.0, one "moderate" = 0.6, multiple "moderate" = 0.3 |
| **Structural compliance** | Stimulus length in range = +0.25, all choices in range = +0.25, all explanations present = +0.25, correct answer distribution OK = +0.25 |

Questions scoring below **0.50** are auto-rejected. Questions scoring **0.50–0.70** are stored but flagged. Questions scoring above **0.70** are served normally.

---

## 8. Runtime Difficulty Calibration

Over time, actual student performance data refines difficulty labels:

```go
// Run periodically (daily cron or on-demand endpoint)
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
    // Log all recalibrations to a separate audit table
}
```

### New endpoint

```
POST /api/v1/admin/recalibrate
```

Returns a report of questions whose labeled difficulty doesn't match empirical difficulty.

---

## 9. Updated File Structure

```
internal/
├── generator/
│   ├── client.go        # Anthropic SDK wrapper (generation)
│   ├── prompts.go       # System + user prompts (UPDATED with per-subtype rules)
│   ├── parser.go        # JSON → Go struct + structural validation
│   ├── validator.go     # NEW — Stage 2 + Stage 3 validation
│   └── quality.go       # NEW — Quality scoring formula
├── questions/
│   ├── handler.go       # HTTP handlers (UPDATED — new admin endpoints)
│   ├── service.go       # UPDATED — 3-stage pipeline orchestration
│   └── store.go         # UPDATED — new columns, validation_logs table
└── ...
```

---

## 10. Cost Estimation

Per 6-question batch with full pipeline:

| Stage | Model | Input Tokens | Output Tokens | Estimated Cost |
|---|---|---|---|---|
| Generation | Opus | ~2,500 | ~5,000 | ~$0.13 |
| Verification (6 questions) | Sonnet | ~6,000 | ~3,000 | ~$0.04 |
| Adversarial (6 questions, batched) | Sonnet | ~8,000 | ~4,000 | ~$0.05 |
| **Total per batch** | | | | **~$0.22** |

For 1,000 questions (~167 batches): **~$37**

This is roughly 2x the generation-only cost but dramatically reduces the rate of incorrect or ambiguous questions reaching students.

### Cost Controls

- `MAX_DAILY_GENERATION_COST` env var (default: $10.00)
- Track cumulative daily cost in memory (reset at midnight UTC)
- When limit reached, return 429 with "Daily generation limit reached"
- Dashboard endpoint: `GET /api/v1/admin/generation-stats` returns daily/weekly/monthly cost + quality metrics

---

## 11. Quality Metrics Dashboard Data

### Endpoint: `GET /api/v1/admin/quality-stats`

Returns:

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
  },
  "cost": {
    "today": 2.45,
    "this_week": 12.30,
    "this_month": 37.80
  }
}
```

---

## 12. Implementation Order

1. **Phase A — Enhanced prompts** (no API cost increase): Add per-subtype correct/wrong answer rules to `prompts.go`. This alone significantly improves generation quality.

2. **Phase B — Self-verification** (Stage 2): Implement `validator.go` with the independent solve. This catches ~80% of answer-correctness issues.

3. **Phase C — Adversarial check** (Stage 3): Add the devil's advocate pass. Catches subtle ambiguity issues that verification misses.

4. **Phase D — Quality scoring + metrics**: Implement composite scoring, auto-flagging, difficulty recalibration, and admin dashboard.

Each phase is independently deployable. Phase A should ship with the initial question generation feature. Phase B should follow immediately. Phases C and D can be added once the question volume justifies the additional cost.

---

## 13. Summary of Changes to Existing Spec

| Original Spec Section | Change |
|---|---|
| §2 Environment Variables | Add `ANTHROPIC_VALIDATION_MODEL`, `VALIDATION_ENABLED`, `ADVERSARIAL_ENABLED` |
| §3 Database Schema | Add `validation_status`, `validation_reasoning`, `adversarial_score` columns to `questions`; add `validation_logs` table |
| §4 File Structure | Add `generator/validator.go`, `generator/quality.go` |
| §6.1 client.go | Change temperature from 1.0 to 0.7–0.8 for generation |
| §6.2 prompts.go | Add per-subtype correct answer rules, wrong answer archetypes, stimulus construction rules, RC passage rules |
| §7 service.go | Replace single-stage flow with 3-stage pipeline |
| §9 Endpoints | Add `POST /admin/recalibrate`, `GET /admin/quality-stats`, `GET /admin/generation-stats` |
| §10 Quality Guardrails | Expand from structural checks to full validation pipeline |
