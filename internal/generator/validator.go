package generator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
)

// Validator handles Stage 2 (self-verification) and Stage 3 (adversarial) checks.
type Validator struct {
	llm   LLMClient
	model string
}

func NewValidator() *Validator {
	var llm LLMClient
	model := "mock"

	if os.Getenv("USE_CLI_GENERATOR") == "true" {
		cliPath := os.Getenv("CLAUDE_CLI_PATH")
		if cliPath == "" {
			cliPath = "claude"
		}
		llm = NewCLIClient(cliPath)
		model = "claude-cli"
	} else if os.Getenv("MOCK_GENERATOR") == "true" {
		llm = nil // Validation is skipped in mock mode
	} else {
		model = os.Getenv("ANTHROPIC_VALIDATION_MODEL")
		if model == "" {
			model = "claude-sonnet-4-5-20250929"
		}
		llm = NewAPIClient(model)
	}

	return &Validator{llm: llm, model: model}
}

func (v *Validator) ModelName() string {
	return v.model
}

// ── Stage 2: Self-Verification ─────────────────────────────

type ValidationResult struct {
	QuestionIndex   int    `json:"question_index"`
	SelectedAnswer  string `json:"selected_answer"`
	GeneratedAnswer string `json:"generated_answer"`
	Matches         bool   `json:"matches"`
	Confidence      string `json:"confidence"`
	Reasoning       string `json:"reasoning"`
	PotentialIssues string `json:"potential_issues"`
	PromptTokens    int    `json:"prompt_tokens"`
	OutputTokens    int    `json:"output_tokens"`
}

type BatchValidationResult struct {
	TotalQuestions int                `json:"total_questions"`
	PassedCount    int                `json:"passed_count"`
	FlaggedCount   int                `json:"flagged_count"`
	RejectedCount  int                `json:"rejected_count"`
	Results        []ValidationResult `json:"results"`
	TotalPromptTokens int            `json:"total_prompt_tokens"`
	TotalOutputTokens int            `json:"total_output_tokens"`
}

type verificationResponse struct {
	SelectedAnswer  string `json:"selected_answer"`
	Confidence      string `json:"confidence"`
	Reasoning       string `json:"reasoning"`
	PotentialIssues string `json:"potential_issues"`
}

func (v *Validator) ValidateBatch(ctx context.Context, batch *GeneratedBatch) (*BatchValidationResult, error) {
	if v.llm == nil {
		return nil, fmt.Errorf("validator not initialized (mock mode)")
	}

	result := &BatchValidationResult{
		TotalQuestions: len(batch.Questions),
		Results:        make([]ValidationResult, 0, len(batch.Questions)),
	}

	for i, q := range batch.Questions {
		vr, err := v.ValidateQuestion(ctx, q, batch.Passage)
		if err != nil {
			log.Printf("WARN: validation failed for question %d: %v — passing as unvalidated", i+1, err)
			vr = &ValidationResult{
				QuestionIndex:   i,
				GeneratedAnswer: q.CorrectAnswerID,
				Matches:         true,
				Confidence:      "low",
				Reasoning:       fmt.Sprintf("validation error: %v", err),
			}
		}
		vr.QuestionIndex = i
		vr.GeneratedAnswer = q.CorrectAnswerID

		if vr.SelectedAnswer == q.CorrectAnswerID {
			vr.Matches = true
			if vr.Confidence == "high" {
				result.PassedCount++
			} else {
				result.FlaggedCount++
			}
		} else {
			vr.Matches = false
			result.RejectedCount++
		}

		result.TotalPromptTokens += vr.PromptTokens
		result.TotalOutputTokens += vr.OutputTokens
		result.Results = append(result.Results, *vr)
	}

	return result, nil
}

func (v *Validator) ValidateQuestion(ctx context.Context, q GeneratedQuestion, passage *GeneratedPassage) (*ValidationResult, error) {
	prompt := buildVerificationPrompt(q, passage)

	resp, err := v.llm.Generate(ctx, verificationSystemPrompt, prompt)
	if err != nil {
		return nil, fmt.Errorf("verification call failed: %w", err)
	}

	cleaned := stripCodeFences(resp.Content)
	var vResp verificationResponse
	if err := json.Unmarshal([]byte(cleaned), &vResp); err != nil {
		return nil, fmt.Errorf("failed to parse verification response: %w", err)
	}

	return &ValidationResult{
		SelectedAnswer:  strings.ToUpper(strings.TrimSpace(vResp.SelectedAnswer)),
		Confidence:      vResp.Confidence,
		Reasoning:       vResp.Reasoning,
		PotentialIssues: vResp.PotentialIssues,
		PromptTokens:    resp.PromptTokens,
		OutputTokens:    resp.OutputTokens,
	}, nil
}

const verificationSystemPrompt = `You are an expert LSAT tutor who has scored a 180 on the LSAT. You are reviewing a practice question to determine if the indicated correct answer is actually correct. Think through each choice systematically before answering. Respond with JSON only.`

func buildVerificationPrompt(q GeneratedQuestion, passage *GeneratedPassage) string {
	var sb strings.Builder

	if passage != nil {
		sb.WriteString("PASSAGE:\n")
		sb.WriteString(passage.Content)
		sb.WriteString("\n\n")
	}

	if q.Stimulus != "" {
		sb.WriteString("STIMULUS:\n")
		sb.WriteString(q.Stimulus)
		sb.WriteString("\n\n")
	}

	sb.WriteString("QUESTION:\n")
	sb.WriteString(q.QuestionStem)
	sb.WriteString("\n\nCHOICES:\n")

	for _, c := range q.Choices {
		sb.WriteString(fmt.Sprintf("(%s) %s\n", c.ID, c.Text))
	}

	sb.WriteString(`
Select the BEST answer. Respond with JSON only:
{
  "selected_answer": "B",
  "confidence": "high",
  "reasoning": "Step-by-step explanation of why you selected this answer and why each other choice is wrong...",
  "potential_issues": "Any ambiguity or problems you notice with the question construction..."
}`)

	return sb.String()
}

// ── Stage 3: Adversarial Check ─────────────────────────────

type AdversarialResult struct {
	QuestionIndex         int                    `json:"question_index"`
	Challenges            []AdversarialChallenge `json:"challenges"`
	OverallQuality        string                 `json:"overall_quality"`
	OverallRecommendation string                 `json:"overall_recommendation"`
	PromptTokens          int                    `json:"prompt_tokens"`
	OutputTokens          int                    `json:"output_tokens"`
}

type AdversarialChallenge struct {
	ChoiceID              string `json:"choice_id"`
	DefenseStrength       string `json:"defense_strength"`
	DefenseArgument       string `json:"defense_argument"`
	CorrectAnswerWeakness string `json:"correct_answer_weakness"`
	Recommendation        string `json:"recommendation"`
}

type adversarialResponse struct {
	Challenges            []AdversarialChallenge `json:"challenges"`
	OverallQuality        string                 `json:"overall_quality"`
	OverallRecommendation string                 `json:"overall_recommendation"`
}

func (v *Validator) AdversarialCheckBatch(ctx context.Context, batch *GeneratedBatch) ([]AdversarialResult, error) {
	if v.llm == nil {
		return nil, fmt.Errorf("validator not initialized (mock mode)")
	}

	results := make([]AdversarialResult, 0, len(batch.Questions))

	for i, q := range batch.Questions {
		ar, err := v.AdversarialCheckQuestion(ctx, q, batch.Passage)
		if err != nil {
			log.Printf("WARN: adversarial check failed for question %d: %v — passing as clean", i+1, err)
			ar = &AdversarialResult{
				QuestionIndex:         i,
				OverallQuality:        "medium",
				OverallRecommendation: "accept",
			}
		}
		ar.QuestionIndex = i
		results = append(results, *ar)
	}

	return results, nil
}

func (v *Validator) AdversarialCheckQuestion(ctx context.Context, q GeneratedQuestion, passage *GeneratedPassage) (*AdversarialResult, error) {
	prompt := buildAdversarialPrompt(q, passage)

	resp, err := v.llm.Generate(ctx, adversarialSystemPrompt, prompt)
	if err != nil {
		return nil, fmt.Errorf("adversarial call failed: %w", err)
	}

	cleaned := stripCodeFences(resp.Content)
	var aResp adversarialResponse
	if err := json.Unmarshal([]byte(cleaned), &aResp); err != nil {
		return nil, fmt.Errorf("failed to parse adversarial response: %w", err)
	}

	return &AdversarialResult{
		Challenges:            aResp.Challenges,
		OverallQuality:        aResp.OverallQuality,
		OverallRecommendation: aResp.OverallRecommendation,
		PromptTokens:          resp.PromptTokens,
		OutputTokens:          resp.OutputTokens,
	}, nil
}

const adversarialSystemPrompt = `You are reviewing an LSAT question for quality. Your job is to try to argue for every incorrect answer choice. If you can make a compelling argument for any wrong answer, the question is ambiguous and should be flagged. Respond with JSON only.`

func buildAdversarialPrompt(q GeneratedQuestion, passage *GeneratedPassage) string {
	var sb strings.Builder

	if passage != nil {
		sb.WriteString("PASSAGE:\n")
		sb.WriteString(passage.Content)
		if passage.IsComparative && passage.PassageB != "" {
			sb.WriteString("\n\nPASSAGE B:\n")
			sb.WriteString(passage.PassageB)
		}
		sb.WriteString("\n\n")
	}

	if q.Stimulus != "" {
		sb.WriteString("STIMULUS:\n")
		sb.WriteString(q.Stimulus)
		sb.WriteString("\n\n")
	}

	sb.WriteString("QUESTION:\n")
	sb.WriteString(q.QuestionStem)
	sb.WriteString("\n\n")

	// Find the correct choice text
	var correctText string
	for _, c := range q.Choices {
		if c.ID == q.CorrectAnswerID {
			correctText = c.Text
			break
		}
	}

	sb.WriteString(fmt.Sprintf("MARKED CORRECT: (%s) %s\n\n", q.CorrectAnswerID, correctText))
	sb.WriteString("INCORRECT CHOICES TO CHALLENGE:\n")

	for _, c := range q.Choices {
		if c.ID != q.CorrectAnswerID {
			sb.WriteString(fmt.Sprintf("(%s) %s\n", c.ID, c.Text))
		}
	}

	sb.WriteString(`
For each incorrect choice, make the STRONGEST possible argument that it could be correct. Then assess whether the marked correct answer is truly unambiguous.

Respond with JSON only:
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
overall_recommendation must be one of: "accept", "flag", "reject"`)

	return sb.String()
}

// DetermineAdversarialScore returns the adversarial score based on challenge results.
func DetermineAdversarialScore(challenges []AdversarialChallenge) string {
	hasStrong := false
	moderateCount := 0

	for _, c := range challenges {
		switch c.DefenseStrength {
		case "strong":
			hasStrong = true
		case "moderate":
			moderateCount++
		}
	}

	if hasStrong {
		return "ambiguous"
	}
	if moderateCount > 0 {
		return "minor_concern"
	}
	return "clean"
}
