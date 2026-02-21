package generator

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// CLIClient shells out to the claude CLI for local dev generation.
// Uses your existing Claude plan — no API key needed, no per-token charges.
type CLIClient struct {
	cliPath string
}

func NewCLIClient(cliPath string) *CLIClient {
	return &CLIClient{cliPath: cliPath}
}

func (c *CLIClient) Generate(ctx context.Context, systemPrompt string, userPrompt string) (*LLMResponse, error) {
	cmd := exec.CommandContext(ctx,
		c.cliPath,
		"--print",
		"--output-format", "text",
		"--system-prompt", systemPrompt,
		"--max-turns", "1",
	)

	cmd.Stdin = strings.NewReader(userPrompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

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

	return &LLMResponse{
		Content:      responseText,
		PromptTokens: 0,
		OutputTokens: 0,
	}, nil
}
