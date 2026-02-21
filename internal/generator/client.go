package generator

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/lsat-prep/backend/internal/models"
)

// LLMClient is the interface both generator implementations satisfy.
type LLMClient interface {
	Generate(ctx context.Context, systemPrompt string, userPrompt string) (*LLMResponse, error)
}

// LLMResponse holds the raw response content and token usage.
type LLMResponse struct {
	Content      string
	PromptTokens int
	OutputTokens int
}

// Generator wraps an LLMClient and adds LSAT-specific batch methods.
type Generator struct {
	llm   LLMClient
	model string
}

func NewGenerator() *Generator {
	var llm LLMClient
	model := "mock"

	if os.Getenv("USE_CLI_GENERATOR") == "true" {
		cliPath := os.Getenv("CLAUDE_CLI_PATH")
		if cliPath == "" {
			cliPath = "claude"
		}
		llm = NewCLIClient(cliPath)
		model = "claude-cli"
		log.Println("Generator using Claude CLI (local plan)")
	} else if os.Getenv("MOCK_GENERATOR") == "true" {
		llm = NewMockClient()
		log.Println("Generator using mock data")
	} else {
		model = os.Getenv("ANTHROPIC_MODEL")
		if model == "" {
			model = "claude-opus-4-5-20251101"
		}
		llm = NewAPIClient(model)
		log.Println("Generator using Anthropic API:", model)
	}

	return &Generator{llm: llm, model: model}
}

func (g *Generator) ModelName() string {
	return g.model
}

func (g *Generator) GenerateLRBatch(ctx context.Context, subtype models.LRSubtype, difficulty models.Difficulty, count int) (*GeneratedBatch, *LLMResponse, error) {
	systemPrompt := LRSystemPrompt()
	userPrompt := BuildLRUserPrompt(subtype, difficulty, count)

	resp, err := g.llm.Generate(ctx, systemPrompt, userPrompt)
	if err != nil {
		return nil, nil, fmt.Errorf("generate LR batch: %w", err)
	}

	batch, err := ParseResponse(resp.Content)
	if err != nil {
		return nil, resp, fmt.Errorf("parse LR response: %w", err)
	}

	return batch, resp, nil
}

func (g *Generator) GenerateRCBatch(ctx context.Context, difficulty models.Difficulty, questionsPerPassage int, subjectArea string, comparative bool) (*GeneratedBatch, *LLMResponse, error) {
	systemPrompt := RCSystemPrompt()
	userPrompt := BuildRCUserPrompt(difficulty, questionsPerPassage, subjectArea, comparative)

	resp, err := g.llm.Generate(ctx, systemPrompt, userPrompt)
	if err != nil {
		return nil, nil, fmt.Errorf("generate RC batch: %w", err)
	}

	batch, err := ParseResponse(resp.Content)
	if err != nil {
		return nil, resp, fmt.Errorf("parse RC response: %w", err)
	}

	return batch, resp, nil
}

// AssignDifficultyScore maps a generation difficulty enum to a numeric score (0-100).
// Each difficulty band gets a random score within its range.
func AssignDifficultyScore(difficulty models.Difficulty) int {
	switch difficulty {
	case models.DifficultyEasy:
		return 10 + rand.Intn(26) // 10-35
	case models.DifficultyMedium:
		return 40 + rand.Intn(26) // 40-65
	case models.DifficultyHard:
		return 70 + rand.Intn(26) // 70-95
	default:
		return 50
	}
}

// ── APIClient — Anthropic SDK (Production) ─────────────────

type APIClient struct {
	client *anthropic.Client
	model  string
}

func NewAPIClient(model string) *APIClient {
	client := anthropic.NewClient(
		option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
	)
	return &APIClient{client: &client, model: model}
}

func (c *APIClient) Generate(ctx context.Context, systemPrompt string, userPrompt string) (*LLMResponse, error) {
	params := anthropic.MessageNewParams{
		Model:       anthropic.Model(c.model),
		MaxTokens:   8192,
		Temperature: param.NewOpt(0.8),
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(userPrompt)),
		},
	}

	message, err := c.callWithRetry(ctx, params)
	if err != nil {
		return nil, err
	}

	var responseText string
	for _, block := range message.Content {
		if block.Type == "text" {
			responseText = block.Text
			break
		}
	}

	if responseText == "" {
		return nil, fmt.Errorf("no text content in API response")
	}

	return &LLMResponse{
		Content:      responseText,
		PromptTokens: int(message.Usage.InputTokens),
		OutputTokens: int(message.Usage.OutputTokens),
	}, nil
}

func (c *APIClient) callWithRetry(ctx context.Context, params anthropic.MessageNewParams) (*anthropic.Message, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			sleepDuration := time.Duration(1<<uint(attempt)) * time.Second
			log.Printf("Retrying Anthropic API call in %v (attempt %d)", sleepDuration, attempt+1)
			time.Sleep(sleepDuration)
		}

		message, err := c.client.Messages.New(ctx, params)
		if err == nil {
			return message, nil
		}
		lastErr = err
		log.Printf("Anthropic API attempt %d failed: %v", attempt+1, err)
	}
	return nil, fmt.Errorf("anthropic API failed after retries: %w", lastErr)
}

// ── MockClient — Local Development ─────────────────────────

type MockClient struct{}

func NewMockClient() *MockClient {
	return &MockClient{}
}

func (m *MockClient) Generate(ctx context.Context, systemPrompt string, userPrompt string) (*LLMResponse, error) {
	mockJSON := buildMockJSON()
	return &LLMResponse{
		Content:      mockJSON,
		PromptTokens: 1500,
		OutputTokens: 3000,
	}, nil
}

func buildMockJSON() string {
	correctAnswers := []string{"A", "B", "C", "D", "E"}
	topics := []string{
		"environmental policy", "medical research", "urban planning",
		"educational reform", "economic theory", "technological innovation",
	}

	questions := "["
	for i := 0; i < 6; i++ {
		correctID := correctAnswers[i%5]
		topic := topics[i%len(topics)]

		if i > 0 {
			questions += ","
		}

		choices := "["
		for j, id := range correctAnswers {
			isCorrect := id == correctID
			label := "incorrect"
			wrongType := `"irrelevant"`
			if isCorrect {
				label = "correct"
				wrongType = "null"
			}
			if j > 0 {
				choices += ","
			}
			addressVerb := "fails to address"
			if isCorrect {
				addressVerb = "directly addresses"
			}
			choices += fmt.Sprintf(`{"id":"%s","text":"[Mock] This answer choice discusses %s and is %s for this question about %s. It provides additional context.","explanation":"[Mock] This choice is %s because it %s the argument's reasoning about %s.","wrong_answer_type":%s}`,
				id, topic, label, topic, label, addressVerb, topic, wrongType)
		}
		choices += "]"

		questions += fmt.Sprintf(`{"stimulus":"[Mock] A recent study on %s found that current approaches have significant limitations. Researchers examined data from multiple sources and concluded that alternative methods could yield better results. However, critics argue that the methodology used in the study was flawed. The lead researcher defended the findings, noting that the sample size was sufficiently large and the controls were appropriate.","question_stem":"Which of the following, if true, most strengthens the argument about %s?","choices":%s,"correct_answer_id":"%s","explanation":"[Mock] The correct answer is %s because it directly addresses the logical relationship in the argument about %s."}`,
			topic, topic, choices, correctID, correctID, topic)
	}
	questions += "]"

	return fmt.Sprintf(`{"questions":%s}`, questions)
}
