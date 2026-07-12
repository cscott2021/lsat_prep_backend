package models

import (
	"encoding/json"
	"strings"
	"testing"
)

// ToDrillQuestion is the answer-stripped serving view. Its JSON must never carry
// the correct answer, the explanation, or any per-choice answer data — that is
// the whole point of the type (serving a question before the user answers).
func TestToDrillQuestion_StripsAllAnswerData(t *testing.T) {
	q := &Question{
		ID:              42,
		Section:         "logical_reasoning",
		DifficultyScore: 60,
		Stimulus:        "All cats are mammals.",
		QuestionStem:    "Which must be true?",
		CorrectAnswerID: "C",
		Explanation:     "Because cats are mammals.",
		Choices: []AnswerChoice{
			{ChoiceID: "A", ChoiceText: "Some cats are pets", IsCorrect: false, Explanation: "wrong because...", WrongAnswerType: "irrelevant"},
			{ChoiceID: "C", ChoiceText: "Some mammals are cats", IsCorrect: true, Explanation: "right because..."},
		},
	}

	dq := q.ToDrillQuestion()

	if len(dq.Choices) != 2 {
		t.Fatalf("expected 2 choices, got %d", len(dq.Choices))
	}
	for _, c := range dq.Choices {
		if c.ChoiceText == "" || c.ChoiceID == "" {
			t.Errorf("choice text/id should be preserved, got %+v", c)
		}
	}

	// The strongest assertion: the serialized bytes must not contain any
	// answer-bearing key or value.
	b, err := json.Marshal(dq)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(b)
	for _, forbidden := range []string{
		"correct_answer_id", "explanation", "is_correct", "wrong_answer_type",
		"Because cats are mammals", "\"C\":true", "irrelevant",
	} {
		if strings.Contains(js, forbidden) {
			t.Errorf("served drill question JSON leaks %q: %s", forbidden, js)
		}
	}
}
