package generator

import (
	"strings"
	"testing"

	"github.com/lsat-prep/backend/internal/models"
)

func TestAllSubtypesHaveStems(t *testing.T) {
	for subtype := range models.ValidLRSubtypes {
		stems := GetSubtypeStems(subtype)
		if len(stems) == 0 {
			t.Errorf("subtype %q has no stems defined", subtype)
		}
	}
}

func TestLRSystemPrompt(t *testing.T) {
	prompt := LRSystemPrompt()

	required := []string{"LSAT", "5 choices", "A through E", "JSON", "STIMULUS", "ANSWER CHOICES", "DIFFICULTY"}
	for _, keyword := range required {
		if !strings.Contains(prompt, keyword) {
			t.Errorf("LR system prompt missing keyword %q", keyword)
		}
	}
}

func TestRCSystemPrompt(t *testing.T) {
	prompt := RCSystemPrompt()

	required := []string{"LSAT", "PASSAGE", "Main Point", "Inference"}
	for _, keyword := range required {
		if !strings.Contains(prompt, keyword) {
			t.Errorf("RC system prompt missing keyword %q", keyword)
		}
	}

	// RC prompt should be standalone and mention RC-specific content
	if !strings.Contains(prompt, "Reading Comprehension") {
		t.Error("RC system prompt should mention Reading Comprehension")
	}
}

func TestBuildLRUserPrompt(t *testing.T) {
	prompt := BuildLRUserPrompt(models.SubtypeStrengthen, models.DifficultyMedium, 6)

	required := []string{"6", "strengthen", "medium", "correct_answer_id", "choices", "wrong_answer_type"}
	for _, keyword := range required {
		if !strings.Contains(prompt, keyword) {
			t.Errorf("LR user prompt missing keyword %q", keyword)
		}
	}

	// Should contain at least one stem for strengthen
	if !strings.Contains(prompt, "strengthens the argument") {
		t.Error("LR user prompt should contain a strengthen stem")
	}

	// Should contain correct answer rules
	if !strings.Contains(prompt, "CORRECT ANSWER RULES") {
		t.Error("LR user prompt should contain correct answer rules section")
	}

	// Should contain wrong answer construction rules
	if !strings.Contains(prompt, "WRONG ANSWER CONSTRUCTION") {
		t.Error("LR user prompt should contain wrong answer construction section")
	}
}

func TestBuildRCUserPrompt(t *testing.T) {
	prompt := BuildRCUserPrompt(models.DifficultyHard, 5)

	required := []string{"5", "hard", "passage", "correct_answer_id", "wrong_answer_type", "subject_area"}
	for _, keyword := range required {
		if !strings.Contains(prompt, keyword) {
			t.Errorf("RC user prompt missing keyword %q", keyword)
		}
	}
}

func TestAllSubtypesHaveCorrectAnswerRules(t *testing.T) {
	for subtype := range models.ValidLRSubtypes {
		rules := GetCorrectAnswerRules(subtype)
		if rules == "" {
			t.Errorf("subtype %q has no correct answer rules", subtype)
		}
	}
}

func TestAllSubtypesHaveWrongAnswerRules(t *testing.T) {
	for subtype := range models.ValidLRSubtypes {
		rules := GetWrongAnswerRules(subtype)
		if rules == "" {
			t.Errorf("subtype %q has no wrong answer rules", subtype)
		}
	}
}

func TestCorrectAnswerRulesInjectedIntoPrompt(t *testing.T) {
	for subtype := range models.ValidLRSubtypes {
		prompt := BuildLRUserPrompt(subtype, models.DifficultyMedium, 3)
		rules := GetCorrectAnswerRules(subtype)

		// The rules should appear in the prompt (at least the first line)
		firstLine := strings.Split(rules, "\n")[0]
		if !strings.Contains(prompt, firstLine) {
			t.Errorf("subtype %q: correct answer rules not found in user prompt", subtype)
		}
	}
}

func TestWrongAnswerRulesInjectedIntoPrompt(t *testing.T) {
	for subtype := range models.ValidLRSubtypes {
		prompt := BuildLRUserPrompt(subtype, models.DifficultyMedium, 3)
		rules := GetWrongAnswerRules(subtype)

		firstLine := strings.Split(rules, "\n")[0]
		if !strings.Contains(prompt, firstLine) {
			t.Errorf("subtype %q: wrong answer rules not found in user prompt", subtype)
		}
	}
}
