package generator

import (
	"encoding/json"
	"strings"
	"testing"
)

func validBatchJSON(count int) string {
	correctAnswers := []string{"A", "B", "C", "D", "E"}
	batch := GeneratedBatch{Questions: make([]GeneratedQuestion, count)}

	for i := 0; i < count; i++ {
		correctID := correctAnswers[i%5]
		choices := make([]GeneratedChoice, 5)
		for j, id := range correctAnswers {
			isCorrect := id == correctID
			label := "incorrect"
			if isCorrect {
				label = "correct"
			}
			choices[j] = GeneratedChoice{
				ID:          id,
				Text:        strings.Repeat("x", 30) + " " + label + " choice for question",
				Explanation: "This is the explanation for why this choice is " + label,
			}
		}
		batch.Questions[i] = GeneratedQuestion{
			Stimulus:        strings.Repeat("A recent study found that ", 5) + "the conclusion follows from the premises presented.",
			QuestionStem:    "Which of the following, if true, most strengthens the argument?",
			Choices:         choices,
			CorrectAnswerID: correctID,
			Explanation:     "The correct answer directly addresses the logical gap in the argument.",
		}
	}

	data, _ := json.Marshal(batch)
	return string(data)
}

func TestParseResponse_ValidJSON(t *testing.T) {
	input := validBatchJSON(6)

	batch, err := ParseResponse(input)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(batch.Questions) != 6 {
		t.Errorf("expected 6 questions, got %d", len(batch.Questions))
	}

	for i, q := range batch.Questions {
		if len(q.Choices) != 5 {
			t.Errorf("question %d: expected 5 choices, got %d", i+1, len(q.Choices))
		}
		if q.CorrectAnswerID == "" {
			t.Errorf("question %d: empty correct_answer_id", i+1)
		}
	}
}

func TestParseResponse_MarkdownFences(t *testing.T) {
	input := "```json\n" + validBatchJSON(3) + "\n```"

	batch, err := ParseResponse(input)
	if err != nil {
		t.Fatalf("expected no error with markdown fences, got: %v", err)
	}

	if len(batch.Questions) != 3 {
		t.Errorf("expected 3 questions, got %d", len(batch.Questions))
	}
}

func TestParseResponse_MissingChoice(t *testing.T) {
	batch := GeneratedBatch{
		Questions: []GeneratedQuestion{
			{
				Stimulus:     strings.Repeat("A recent study found that ", 5) + "the conclusion follows.",
				QuestionStem: "Which of the following strengthens the argument?",
				Choices: []GeneratedChoice{
					{ID: "A", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
					{ID: "B", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
					{ID: "C", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
					{ID: "D", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
					// Missing E
				},
				CorrectAnswerID: "A",
				Explanation:     "The answer is A.",
			},
		},
	}
	data, _ := json.Marshal(batch)

	_, err := ParseResponse(string(data))
	if err == nil {
		t.Fatal("expected validation error for missing choice")
	}

	var ve *ValidationError
	if !isValidationError(err, &ve) {
		t.Fatalf("expected ValidationError, got: %T", err)
	}

	found := false
	for _, e := range ve.Errors {
		if strings.Contains(e, "expected 5 choices") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about 5 choices, got: %v", ve.Errors)
	}
}

func TestParseResponse_InvalidCorrectAnswerID(t *testing.T) {
	batch := GeneratedBatch{
		Questions: []GeneratedQuestion{
			{
				Stimulus:     strings.Repeat("A recent study found that ", 5) + "the conclusion follows.",
				QuestionStem: "Which of the following strengthens the argument?",
				Choices: []GeneratedChoice{
					{ID: "A", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
					{ID: "B", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
					{ID: "C", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
					{ID: "D", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
					{ID: "E", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
				},
				CorrectAnswerID: "F",
				Explanation:     "The answer is F.",
			},
		},
	}
	data, _ := json.Marshal(batch)

	_, err := ParseResponse(string(data))
	if err == nil {
		t.Fatal("expected validation error for invalid correct_answer_id")
	}

	var ve *ValidationError
	if !isValidationError(err, &ve) {
		t.Fatalf("expected ValidationError, got: %T", err)
	}

	found := false
	for _, e := range ve.Errors {
		if strings.Contains(e, "invalid correct_answer_id") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about invalid correct_answer_id, got: %v", ve.Errors)
	}
}

func TestParseResponse_StimulusTooShort(t *testing.T) {
	batch := GeneratedBatch{
		Questions: []GeneratedQuestion{
			{
				Stimulus:     "Too short.",
				QuestionStem: "Which of the following strengthens the argument?",
				Choices: []GeneratedChoice{
					{ID: "A", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
					{ID: "B", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
					{ID: "C", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
					{ID: "D", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
					{ID: "E", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
				},
				CorrectAnswerID: "A",
				Explanation:     "The answer is A.",
			},
		},
	}
	data, _ := json.Marshal(batch)

	_, err := ParseResponse(string(data))
	if err == nil {
		t.Fatal("expected validation error for short stimulus")
	}

	var ve *ValidationError
	if !isValidationError(err, &ve) {
		t.Fatalf("expected ValidationError, got: %T", err)
	}

	found := false
	for _, e := range ve.Errors {
		if strings.Contains(e, "stimulus length") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about stimulus length, got: %v", ve.Errors)
	}
}

func TestParseResponse_MalformedJSON(t *testing.T) {
	_, err := ParseResponse("this is not json at all")
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}

	// Should NOT be a ValidationError — should be a parse error
	var ve *ValidationError
	if isValidationError(err, &ve) {
		t.Fatal("expected parse error, not ValidationError")
	}
}

func TestParseResponse_EmptyExplanation(t *testing.T) {
	batch := GeneratedBatch{
		Questions: []GeneratedQuestion{
			{
				Stimulus:     strings.Repeat("A recent study found that ", 5) + "the conclusion follows.",
				QuestionStem: "Which of the following strengthens the argument?",
				Choices: []GeneratedChoice{
					{ID: "A", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
					{ID: "B", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
					{ID: "C", Text: strings.Repeat("x", 30) + " choice text", Explanation: ""},
					{ID: "D", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
					{ID: "E", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
				},
				CorrectAnswerID: "A",
				Explanation:     "The answer is A.",
			},
		},
	}
	data, _ := json.Marshal(batch)

	_, err := ParseResponse(string(data))
	if err == nil {
		t.Fatal("expected validation error for empty choice explanation")
	}
}

func TestParseResponse_WrongAnswerType(t *testing.T) {
	wrongType := "irrelevant"
	batch := GeneratedBatch{
		Questions: []GeneratedQuestion{
			{
				Stimulus:     strings.Repeat("A recent study found that ", 5) + "the conclusion follows.",
				QuestionStem: "Which of the following strengthens the argument?",
				Choices: []GeneratedChoice{
					{ID: "A", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation", WrongAnswerType: nil},
					{ID: "B", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation", WrongAnswerType: &wrongType},
					{ID: "C", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation", WrongAnswerType: &wrongType},
					{ID: "D", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation", WrongAnswerType: &wrongType},
					{ID: "E", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation", WrongAnswerType: &wrongType},
				},
				CorrectAnswerID: "A",
				Explanation:     "The answer is A.",
			},
		},
	}
	data, _ := json.Marshal(batch)

	parsed, err := ParseResponse(string(data))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	q := parsed.Questions[0]

	// Correct answer should have nil wrong_answer_type
	if q.Choices[0].WrongAnswerType != nil {
		t.Error("correct answer should have nil wrong_answer_type")
	}

	// Wrong answers should have wrong_answer_type set
	for _, c := range q.Choices[1:] {
		if c.WrongAnswerType == nil {
			t.Errorf("choice %s: expected wrong_answer_type to be set", c.ID)
		} else if *c.WrongAnswerType != "irrelevant" {
			t.Errorf("choice %s: expected wrong_answer_type 'irrelevant', got %q", c.ID, *c.WrongAnswerType)
		}
	}
}

func TestParseResponse_RCPassageBatch(t *testing.T) {
	batch := GeneratedBatch{
		Passage: &GeneratedPassage{
			Title:       "Test Passage",
			SubjectArea: "law",
			Content:     strings.Repeat("The legal framework ", 100), // ~2000 chars
		},
		Questions: []GeneratedQuestion{
			{
				Stimulus:     "", // RC questions have empty stimulus
				QuestionStem: "What is the main point of the passage?",
				Choices: []GeneratedChoice{
					{ID: "A", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
					{ID: "B", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
					{ID: "C", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
					{ID: "D", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
					{ID: "E", Text: strings.Repeat("x", 30) + " choice text", Explanation: "explanation"},
				},
				CorrectAnswerID: "A",
				Explanation:     "The answer is A.",
			},
		},
	}
	data, _ := json.Marshal(batch)

	parsed, err := ParseResponse(string(data))
	if err != nil {
		t.Fatalf("expected no error for RC batch, got: %v", err)
	}

	if parsed.Passage == nil {
		t.Fatal("expected passage to be present")
	}
	if parsed.Passage.SubjectArea != "law" {
		t.Errorf("expected subject_area 'law', got %q", parsed.Passage.SubjectArea)
	}
}

// isValidationError checks if err is a *ValidationError via type assertion
func isValidationError(err error, target **ValidationError) bool {
	ve, ok := err.(*ValidationError)
	if ok && target != nil {
		*target = ve
	}
	return ok
}
