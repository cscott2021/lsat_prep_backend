package questions

import (
	"testing"
	"time"

	"github.com/lsat-prep/backend/internal/models"
)

func answerBearingHistoryQuestion() *models.HistoryQuestion {
	return &models.HistoryQuestion{
		QuestionID:      7,
		CorrectAnswerID: "B",
		Explanation:     "why B is right",
		Choices: []models.AnswerChoice{
			{ChoiceID: "A", ChoiceText: "a", IsCorrect: false, Explanation: "why A is wrong", WrongAnswerType: "irrelevant"},
			{ChoiceID: "B", ChoiceText: "b", IsCorrect: true, Explanation: "why B is right"},
		},
	}
}

// An unanswered review question must have all answer data blanked.
func TestStripUnansweredAnswer_Unanswered(t *testing.T) {
	hq := answerBearingHistoryQuestion() // AnsweredAt is the zero value => unanswered

	stripUnansweredAnswer(hq)

	if hq.CorrectAnswerID != "" || hq.Explanation != "" {
		t.Errorf("expected answer/explanation blanked, got %q / %q", hq.CorrectAnswerID, hq.Explanation)
	}
	for _, c := range hq.Choices {
		if c.IsCorrect || c.Explanation != "" || c.WrongAnswerType != "" {
			t.Errorf("expected per-choice answer data blanked, got %+v", c)
		}
		if c.ChoiceText == "" || c.ChoiceID == "" {
			t.Errorf("choice id/text must be preserved, got %+v", c)
		}
	}
}

// An answered review question keeps its answer data (post-answer reveal is fine).
func TestStripUnansweredAnswer_Answered(t *testing.T) {
	hq := answerBearingHistoryQuestion()
	hq.AnsweredAt = time.Now()

	stripUnansweredAnswer(hq)

	if hq.CorrectAnswerID != "B" || hq.Explanation == "" {
		t.Errorf("answered question must keep its answer, got %q / %q", hq.CorrectAnswerID, hq.Explanation)
	}
	if !hq.Choices[1].IsCorrect {
		t.Error("answered question must keep per-choice correctness")
	}
}
