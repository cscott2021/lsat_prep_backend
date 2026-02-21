package generator

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

type GeneratedBatch struct {
	Questions []GeneratedQuestion `json:"questions"`
	Passage   *GeneratedPassage   `json:"passage,omitempty"`
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
	WrongAnswerType *string `json:"wrong_answer_type"`
}

type GeneratedPassage struct {
	Title         string `json:"title"`
	SubjectArea   string `json:"subject_area"`
	Content       string `json:"content"`
	IsComparative bool   `json:"is_comparative"`
	PassageB      string `json:"passage_b,omitempty"`
}

type ValidationError struct {
	Errors []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation failed: %s", strings.Join(e.Errors, "; "))
}

func ParseResponse(responseBody string) (*GeneratedBatch, error) {
	cleaned := stripCodeFences(responseBody)

	var batch GeneratedBatch
	if err := json.Unmarshal([]byte(cleaned), &batch); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	if err := validateBatch(&batch); err != nil {
		return nil, err
	}

	return &batch, nil
}

func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimSpace(s)
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSpace(s)
	}
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	return s
}

var validChoiceIDs = map[string]bool{"A": true, "B": true, "C": true, "D": true, "E": true}

func validateBatch(batch *GeneratedBatch) error {
	var errs []string

	if len(batch.Questions) == 0 {
		return &ValidationError{Errors: []string{"no questions in batch"}}
	}

	// RC passage length validation
	if batch.Passage != nil {
		passageLen := len(batch.Passage.Content)
		if passageLen < 1500 || passageLen > 3000 {
			log.Printf("WARNING: RC passage length %d outside recommended range [1500, 3000]", passageLen)
		}
	}

	correctAnswerCounts := make(map[string]int)

	for i, q := range batch.Questions {
		qNum := i + 1

		if len(q.Choices) != 5 {
			errs = append(errs, fmt.Sprintf("question %d: expected 5 choices, got %d", qNum, len(q.Choices)))
			continue
		}

		expectedIDs := []string{"A", "B", "C", "D", "E"}
		for j, c := range q.Choices {
			if c.ID != expectedIDs[j] {
				errs = append(errs, fmt.Sprintf("question %d: choice %d has id %q, expected %q", qNum, j+1, c.ID, expectedIDs[j]))
			}
		}

		if !validChoiceIDs[q.CorrectAnswerID] {
			errs = append(errs, fmt.Sprintf("question %d: invalid correct_answer_id %q", qNum, q.CorrectAnswerID))
		}

		// Stimulus length check — skip for RC questions (empty stimulus, passage is the stimulus)
		stimLen := len(q.Stimulus)
		if batch.Passage == nil && (stimLen < 100 || stimLen > 700) {
			errs = append(errs, fmt.Sprintf("question %d: stimulus length %d outside range [100, 700]", qNum, stimLen))
		}

		for j, c := range q.Choices {
			textLen := len(c.Text)
			if textLen < 20 || textLen > 400 {
				errs = append(errs, fmt.Sprintf("question %d: choice %s text length %d outside range [20, 400]", qNum, c.ID, textLen))
			}
			if c.Explanation == "" {
				errs = append(errs, fmt.Sprintf("question %d: choice %d has empty explanation", qNum, j+1))
			}

			// Warn if wrong answer is missing wrong_answer_type
			if c.ID != q.CorrectAnswerID && c.WrongAnswerType == nil {
				log.Printf("WARNING: question %d choice %s missing wrong_answer_type", qNum, c.ID)
			}
		}

		if q.Explanation == "" {
			errs = append(errs, fmt.Sprintf("question %d: empty explanation", qNum))
		}

		if q.QuestionStem == "" {
			errs = append(errs, fmt.Sprintf("question %d: empty question_stem", qNum))
		}

		correctAnswerCounts[q.CorrectAnswerID]++
	}

	// Warn (but don't reject) if correct answers are clustered
	for letter, count := range correctAnswerCounts {
		if count > 2 && len(batch.Questions) >= 6 {
			log.Printf("WARNING: correct answer %q appears %d times in batch of %d questions", letter, count, len(batch.Questions))
		}
	}

	// Topic diversity check (Jaccard keyword overlap warning)
	checkTopicDiversity(batch.Questions)

	if len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}

	return nil
}

// checkTopicDiversity warns if any two stimuli share >60% keyword overlap.
func checkTopicDiversity(questions []GeneratedQuestion) {
	if len(questions) < 2 {
		return
	}

	tokenSets := make([]map[string]bool, len(questions))
	for i, q := range questions {
		tokenSets[i] = tokenize(q.Stimulus)
	}

	for i := 0; i < len(questions); i++ {
		for j := i + 1; j < len(questions); j++ {
			overlap := jaccardSimilarity(tokenSets[i], tokenSets[j])
			if overlap > 0.60 {
				log.Printf("WARNING: questions %d and %d have %.0f%% keyword overlap — consider more topic diversity", i+1, j+1, overlap*100)
			}
		}
	}
}

func tokenize(s string) map[string]bool {
	tokens := make(map[string]bool)
	for _, word := range strings.Fields(strings.ToLower(s)) {
		// Skip very short words (articles, prepositions)
		if len(word) > 3 {
			tokens[word] = true
		}
	}
	return tokens
}

func jaccardSimilarity(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}

	intersection := 0
	for k := range a {
		if b[k] {
			intersection++
		}
	}

	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}

	return float64(intersection) / float64(union)
}
