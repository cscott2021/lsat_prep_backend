package questions

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lsat-prep/backend/internal/gamification"
	"github.com/lsat-prep/backend/internal/generator"
	"github.com/lsat-prep/backend/internal/models"
)

var allLRSubtypes = []string{
	"strengthen", "weaken", "assumption", "flaw", "must_be_true",
	"most_strongly_supported", "method_of_reasoning", "parallel_reasoning",
	"parallel_flaw", "principle", "apply_principle", "evaluate",
	"main_conclusion", "role_of_statement",
}

var allRCSubtypes = []string{
	"rc_main_idea", "rc_detail", "rc_inference", "rc_attitude",
	"rc_function", "rc_organization", "rc_strengthen_weaken",
	"rc_analogy", "rc_relationship", "rc_agreement",
}

type Service struct {
	store              *Store
	generator          *generator.Generator
	validator          *generator.Validator
	validationEnabled  bool
	adversarialEnabled bool
	autoGenEnabledLR   bool
	autoGenEnabledRC   bool
	autoGenMinUnseen   int
	gamService         *gamification.Service
}

// SetGamificationService injects the gamification service for XP/streak/goal tracking.
func (s *Service) SetGamificationService(gs *gamification.Service) {
	s.gamService = gs
}

func NewService(store *Store, gen *generator.Generator, val *generator.Validator) *Service {
	validationEnabled := os.Getenv("VALIDATION_ENABLED") != "false"
	adversarialEnabled := os.Getenv("ADVERSARIAL_ENABLED") != "false"

	// Auto-generation section flags
	autoGenEnabledLR := os.Getenv("AUTO_GEN_ENABLED_LR") != "false"
	autoGenEnabledRC := os.Getenv("AUTO_GEN_ENABLED_RC") == "true"

	// Minimum unseen questions before triggering generation
	autoGenMinUnseen := 4
	if v := os.Getenv("AUTO_GEN_MIN_UNSEEN"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			autoGenMinUnseen = n
		}
	}

	// Disable validation in mock mode
	if os.Getenv("MOCK_GENERATOR") == "true" {
		validationEnabled = false
		adversarialEnabled = false
	}

	log.Printf("Service: validation=%v adversarial=%v autoGenLR=%v autoGenRC=%v minUnseen=%d",
		validationEnabled, adversarialEnabled, autoGenEnabledLR, autoGenEnabledRC, autoGenMinUnseen)

	return &Service{
		store:              store,
		generator:          gen,
		validator:          val,
		validationEnabled:  validationEnabled,
		adversarialEnabled: adversarialEnabled,
		autoGenEnabledLR:   autoGenEnabledLR,
		autoGenEnabledRC:   autoGenEnabledRC,
		autoGenMinUnseen:   autoGenMinUnseen,
	}
}

// ── Question Generation (3-Stage Pipeline) ──────────────

func (s *Service) GenerateBatch(ctx context.Context, req models.GenerateBatchRequest) (*models.GenerateBatchResponse, error) {
	if req.Count <= 0 {
		req.Count = 6
	}

	// Create batch record (status: pending)
	batch, err := s.store.CreateBatch(req)
	if err != nil {
		return nil, fmt.Errorf("create batch: %w", err)
	}

	// Update to "generating"
	if err := s.store.UpdateBatchStatus(batch.ID, models.BatchGenerating); err != nil {
		return nil, fmt.Errorf("update status: %w", err)
	}

	// ── Stage 1: Generate questions ──────────────────────────
	startTime := time.Now()

	var genBatch *generator.GeneratedBatch
	var llmResp *generator.LLMResponse

	switch req.Section {
	case models.SectionLR:
		if req.LRSubtype == nil {
			s.store.FailBatch(batch.ID, "lr_subtype required for logical_reasoning")
			return nil, fmt.Errorf("lr_subtype required for logical_reasoning")
		}
		genBatch, llmResp, err = s.generator.GenerateLRBatch(ctx, *req.LRSubtype, req.Difficulty, req.Count)
	case models.SectionRC:
		genBatch, llmResp, err = s.generator.GenerateRCBatch(ctx, req.Difficulty, req.Count, req.SubjectArea, req.IsComparative)
	default:
		s.store.FailBatch(batch.ID, "invalid section")
		return nil, fmt.Errorf("invalid section: %s", req.Section)
	}

	if err != nil {
		errMsg := err.Error()
		s.store.FailBatch(batch.ID, errMsg)
		return nil, fmt.Errorf("generation failed: %w", err)
	}

	promptTokens := 0
	outputTokens := 0
	if llmResp != nil {
		promptTokens = llmResp.PromptTokens
		outputTokens = llmResp.OutputTokens
	}

	log.Printf("Stage 1 complete: generated %d questions for batch %d", len(genBatch.Questions), batch.ID)

	// ── Stage 2: Self-Verification ───────────────────────────
	var batchValidation *generator.BatchValidationResult
	validationTokens := 0

	if s.validationEnabled && s.validator != nil {
		if err := s.store.UpdateBatchStatus(batch.ID, models.BatchValidating); err != nil {
			log.Printf("WARN: failed to update batch status to validating: %v", err)
		}

		batchValidation, err = s.validator.ValidateBatch(ctx, genBatch)
		if err != nil {
			log.Printf("WARN: Stage 2 validation failed for batch %d: %v — skipping validation", batch.ID, err)
		} else {
			validationTokens += batchValidation.TotalPromptTokens + batchValidation.TotalOutputTokens
			log.Printf("Stage 2 complete: passed=%d flagged=%d rejected=%d",
				batchValidation.PassedCount, batchValidation.FlaggedCount, batchValidation.RejectedCount)
		}
	}

	// ── Stage 3: Adversarial Check ───────────────────────────
	var adversarialResults []generator.AdversarialResult

	if s.adversarialEnabled && s.validator != nil && req.Difficulty != models.DifficultyEasy {
		advResults, err := s.validator.AdversarialCheckBatch(ctx, genBatch)
		if err != nil {
			log.Printf("WARN: Stage 3 adversarial check failed for batch %d: %v — skipping", batch.ID, err)
		} else {
			adversarialResults = advResults
			for _, ar := range advResults {
				validationTokens += ar.PromptTokens + ar.OutputTokens
			}
			log.Printf("Stage 3 complete: checked %d questions", len(advResults))
		}
	}

	// ── Compute quality scores and build save options ─────────
	isRC := req.Section == models.SectionRC
	passedCount := 0
	flaggedCount := 0
	rejectedCount := 0

	opts := make([]QuestionSaveOptions, len(genBatch.Questions))

	for i, q := range genBatch.Questions {
		// Get validation result for this question
		var vr *generator.ValidationResult
		if batchValidation != nil && i < len(batchValidation.Results) {
			r := batchValidation.Results[i]
			vr = &r
		}

		// Get adversarial result for this question
		var ar *generator.AdversarialResult
		if i < len(adversarialResults) {
			r := adversarialResults[i]
			ar = &r
		}

		// Compute structural score
		structural := generator.ComputeStructuralScore(q, isRC)

		// Compute composite quality score
		qualityScore := generator.ComputeQualityScore(vr, ar, structural)
		classification := generator.ClassifyQuality(qualityScore)

		// Determine validation status
		valStatus := "unvalidated"
		var valReasoning *string
		var advScore *string
		flagged := false

		if vr != nil {
			if !vr.Matches {
				valStatus = string(models.ValidationRejected)
				reasoning := fmt.Sprintf("Validator selected %s (expected %s): %s",
					vr.SelectedAnswer, vr.GeneratedAnswer, vr.Reasoning)
				valReasoning = &reasoning
			} else if vr.Confidence == "high" {
				valStatus = string(models.ValidationPassed)
			} else {
				valStatus = string(models.ValidationFlagged)
				reasoning := fmt.Sprintf("Low confidence (%s): %s", vr.Confidence, vr.Reasoning)
				valReasoning = &reasoning
				flagged = true
			}
		}

		if ar != nil {
			score := generator.DetermineAdversarialScore(ar.Challenges)
			advScore = &score
			if score == "ambiguous" {
				valStatus = string(models.ValidationRejected)
				reasoning := "Adversarial check found strong defense for wrong answer"
				valReasoning = &reasoning
			} else if score == "minor_concern" && valStatus != string(models.ValidationRejected) {
				flagged = true
				if valStatus == string(models.ValidationPassed) {
					valStatus = string(models.ValidationFlagged)
				}
			}
		}

		// Override with quality classification if it would reject
		if classification == "reject" && valStatus != string(models.ValidationRejected) {
			valStatus = string(models.ValidationRejected)
		} else if classification == "flagged" && valStatus == string(models.ValidationPassed) {
			valStatus = string(models.ValidationFlagged)
			flagged = true
		}

		opts[i] = QuestionSaveOptions{
			ValidationStatus: valStatus,
			QualityScore:     &qualityScore,
			ValidationReason: valReasoning,
			AdversarialScore: advScore,
			Flagged:          flagged,
		}

		// Count for batch summary (only passed + flagged get saved for serving)
		switch valStatus {
		case string(models.ValidationRejected):
			rejectedCount++
		case string(models.ValidationFlagged):
			flaggedCount++
		default:
			passedCount++
		}

		// Log validation results
		if vr != nil {
			s.store.LogValidation(models.ValidationLog{
				QuestionID:      nil,
				BatchID:         &batch.ID,
				Stage:           "verification",
				ModelUsed:       s.validator.ModelName(),
				GeneratedAnswer: vr.GeneratedAnswer,
				ValidatorAnswer: vr.SelectedAnswer,
				Matches:         &vr.Matches,
				Confidence:      vr.Confidence,
				Reasoning:       vr.Reasoning,
				PromptTokens:    vr.PromptTokens,
				OutputTokens:    vr.OutputTokens,
			})
		}

		if ar != nil {
			s.store.LogValidation(models.ValidationLog{
				BatchID:      &batch.ID,
				Stage:        "adversarial",
				ModelUsed:    s.validator.ModelName(),
				Reasoning:    ar.OverallRecommendation,
				PromptTokens: ar.PromptTokens,
				OutputTokens: ar.OutputTokens,
			})
		}
	}

	// ── Filter out rejected questions before saving ──────────
	filteredBatch, filteredOpts := filterRejected(genBatch, opts)

	// Save surviving questions (use background context so saves aren't lost if HTTP client disconnects)
	if err := s.store.SaveGeneratedBatch(context.Background(), batch.ID, filteredBatch, req, filteredOpts); err != nil {
		errMsg := err.Error()
		s.store.FailBatch(batch.ID, errMsg)
		return nil, fmt.Errorf("save batch: %w", err)
	}

	// Mark completed
	elapsed := time.Since(startTime).Milliseconds()
	if err := s.store.CompleteBatch(batch.ID, passedCount, flaggedCount, rejectedCount,
		elapsed, promptTokens, outputTokens, validationTokens, s.generator.ModelName()); err != nil {
		return nil, fmt.Errorf("complete batch: %w", err)
	}

	return &models.GenerateBatchResponse{
		BatchID:           batch.ID,
		Status:            models.BatchCompleted,
		QuestionsPassed:   passedCount,
		QuestionsFlagged:  flaggedCount,
		QuestionsRejected: rejectedCount,
		Message:           fmt.Sprintf("Generated %d questions (%d passed, %d flagged, %d rejected)", len(genBatch.Questions), passedCount, flaggedCount, rejectedCount),
	}, nil
}

// filterRejected removes rejected questions from the batch and options slices.
func filterRejected(batch *generator.GeneratedBatch, opts []QuestionSaveOptions) (*generator.GeneratedBatch, []QuestionSaveOptions) {
	filtered := &generator.GeneratedBatch{
		Passage: batch.Passage,
	}
	var filteredOpts []QuestionSaveOptions

	for i, q := range batch.Questions {
		if i < len(opts) && opts[i].ValidationStatus == string(models.ValidationRejected) {
			continue
		}
		filtered.Questions = append(filtered.Questions, q)
		if i < len(opts) {
			filteredOpts = append(filteredOpts, opts[i])
		}
	}

	return filtered, filteredOpts
}

// ── Batch/Question Access ────────────────────────────────

func (s *Service) GetBatch(batchID int64) (*models.QuestionBatch, error) {
	return s.store.GetBatch(batchID)
}

func (s *Service) ListBatches(status *models.BatchStatus, limit, offset int) ([]models.QuestionBatch, error) {
	return s.store.ListBatches(status, limit, offset)
}

func (s *Service) GetQuestion(questionID int64) (*models.Question, error) {
	return s.store.GetQuestionWithChoices(questionID)
}

func (s *Service) GetDrillQuestions(section models.Section, subtype *models.LRSubtype, difficulty models.Difficulty, count int) ([]models.Question, error) {
	return s.store.GetDrillQuestions(section, subtype, difficulty, count)
}

func (s *Service) GetPassage(passageID int64) (*models.RCPassage, error) {
	return s.store.GetPassage(passageID)
}

// ── Answer Submission + Ability Updates ──────────────────

func (s *Service) SubmitAnswer(userID int64, questionID int64, selectedChoiceID string, timeSpentSeconds *float64) (*models.SubmitAnswerResponse, error) {
	question, err := s.store.GetQuestionWithChoices(questionID)
	if err != nil {
		return nil, err
	}

	isCorrect := question.CorrectAnswerID == selectedChoiceID

	// Increment counters
	s.store.IncrementServed(questionID)
	if isCorrect {
		s.store.IncrementCorrect(questionID)
	}

	// Record user history (with selected answer and time)
	if err := s.store.RecordAnswer(userID, questionID, isCorrect, &selectedChoiceID, timeSpentSeconds); err != nil {
		log.Printf("WARN: failed to record answer history: %v", err)
	}

	// Update ability scores
	var abilitySnapshot *models.AbilitySnapshot
	snapshot, err := s.UpdateAbilityScores(userID, question, isCorrect)
	if err != nil {
		log.Printf("WARN: failed to update ability scores: %v", err)
	} else {
		abilitySnapshot = snapshot
	}

	// Gamification: award XP, update daily goal, streak, counters
	var xpAwarded int
	if s.gamService != nil {
		if isCorrect && abilitySnapshot != nil {
			xpAwarded = s.gamService.AwardQuestionXP(userID, question.DifficultyScore, abilitySnapshot.SubtypeAbility)
		}
		s.gamService.UpdateDailyGoal(userID, 1)
		s.gamService.UpdateStreak(userID)
		s.gamService.IncrementCounters(userID, isCorrect)
	}

	// Async check generation queue for this question's difficulty range
	subtype := ""
	if question.LRSubtype != nil {
		subtype = string(*question.LRSubtype)
	} else if question.RCSubtype != nil {
		subtype = string(*question.RCSubtype)
	}
	if subtype != "" {
		subtypePtr := subtype
		go s.CheckAndQueueGeneration(string(question.Section), &subtypePtr,
			max(0, question.DifficultyScore-15), min(100, question.DifficultyScore+15))
		go s.CheckUserInventoryAndQueue(userID, string(question.Section), subtype)
	}

	return &models.SubmitAnswerResponse{
		Correct:         isCorrect,
		CorrectAnswerID: question.CorrectAnswerID,
		Explanation:     question.Explanation,
		Choices:         question.Choices,
		AbilityUpdated:  abilitySnapshot,
		XPAwarded:       xpAwarded,
	}, nil
}

func (s *Service) UpdateAbilityScores(userID int64, question *models.Question, correct bool) (*models.AbilitySnapshot, error) {
	section := string(question.Section)
	subtype := ""
	if question.LRSubtype != nil {
		subtype = string(*question.LRSubtype)
	} else if question.RCSubtype != nil {
		subtype = string(*question.RCSubtype)
	}

	// 1. Update overall
	overall, err := s.store.GetOrCreateAbility(userID, models.ScopeOverall, nil)
	if err != nil {
		return nil, fmt.Errorf("get overall ability: %w", err)
	}
	newOverall := ComputeNewAbility(overall.AbilityScore, question.DifficultyScore, correct, overall.QuestionsAnswered)
	if err := s.store.UpdateAbility(userID, models.ScopeOverall, nil, newOverall, correct); err != nil {
		return nil, fmt.Errorf("update overall ability: %w", err)
	}

	// 2. Update section
	sectionAbility, err := s.store.GetOrCreateAbility(userID, models.ScopeSection, &section)
	if err != nil {
		return nil, fmt.Errorf("get section ability: %w", err)
	}
	newSection := ComputeNewAbility(sectionAbility.AbilityScore, question.DifficultyScore, correct, sectionAbility.QuestionsAnswered)
	if err := s.store.UpdateAbility(userID, models.ScopeSection, &section, newSection, correct); err != nil {
		return nil, fmt.Errorf("update section ability: %w", err)
	}

	// 3. Update subtype (only if we have one)
	newSubtype := newSection // default fallback
	if subtype != "" {
		subtypeAbility, err := s.store.GetOrCreateAbility(userID, models.ScopeSubtype, &subtype)
		if err != nil {
			return nil, fmt.Errorf("get subtype ability: %w", err)
		}
		newSubtype = ComputeNewAbility(subtypeAbility.AbilityScore, question.DifficultyScore, correct, subtypeAbility.QuestionsAnswered)
		if err := s.store.UpdateAbility(userID, models.ScopeSubtype, &subtype, newSubtype, correct); err != nil {
			return nil, fmt.Errorf("update subtype ability: %w", err)
		}
	}

	return &models.AbilitySnapshot{
		OverallAbility: newOverall,
		SectionAbility: newSection,
		SubtypeAbility: newSubtype,
	}, nil
}

// ── Adaptive Drill Serving ──────────────────────────────

func (s *Service) GetQuickDrill(ctx context.Context, userID int64, req models.QuickDrillRequest) ([]models.DrillQuestion, error) {
	if req.Count <= 0 {
		req.Count = 6
	}

	// RC section: delegate to passage-based RC drill flow
	if req.Section == "reading_comprehension" {
		rcReq := models.RCDrillRequest{
			DifficultySlider: req.DifficultySlider,
			Count:            req.Count,
		}
		resp, err := s.GetRCDrill(ctx, userID, rcReq)
		if err != nil {
			return nil, err
		}
		if resp == nil {
			return []models.DrillQuestion{}, nil
		}
		// Convert RCDrillResponse questions (which include passage) to DrillQuestion slice
		var questions []models.DrillQuestion
		for _, q := range resp.Questions {
			questions = append(questions, q)
		}
		return questions, nil
	}

	// Get user's section ability
	section := req.Section
	if section == "both" {
		section = "logical_reasoning"
	}
	sectionAbility, err := s.store.GetOrCreateAbility(userID, models.ScopeSection, &section)
	if err != nil {
		sectionAbility = &models.UserAbilityScore{AbilityScore: 50}
	}

	// Use request slider, or fetch saved slider if not provided
	slider := req.DifficultySlider
	if slider == 0 {
		saved, err := s.store.GetDifficultySlider(userID)
		if err == nil && saved > 0 {
			slider = saved
		} else {
			slider = 50
		}
	}

	target := TargetDifficulty(sectionAbility.AbilityScore, slider)
	minDiff := max(0, target-15)
	maxDiff := min(100, target+15)

	// Collect subtypes
	var subtypes []string
	if req.Section == "logical_reasoning" || req.Section == "both" {
		subtypes = append(subtypes, allLRSubtypes...)
	}
	if req.Section == "both" {
		subtypes = append(subtypes, allRCSubtypes...)
	}

	// Shuffle and pick first N
	rand.Shuffle(len(subtypes), func(i, j int) { subtypes[i], subtypes[j] = subtypes[j], subtypes[i] })
	if len(subtypes) > req.Count {
		subtypes = subtypes[:req.Count]
	}

	// For each selected subtype, fetch 1 question in the difficulty window.
	// Track which subtypes are missing so we can generate for them.
	// For RC subtypes, reuse the same passage to avoid passage fragmentation.
	var questions []models.DrillQuestion
	var missingSubtypes []string
	var rcPassageID int64

	for _, st := range subtypes {
		querySection := "logical_reasoning"
		if strings.HasPrefix(st, "rc_") {
			querySection = "reading_comprehension"
		}

		var q *models.DrillQuestion
		var err error

		// For RC subtypes with a known passage, try same-passage first
		if strings.HasPrefix(st, "rc_") && rcPassageID > 0 {
			q, err = s.store.GetOneAdaptiveQuestionFromPassage(userID, st, rcPassageID, minDiff, maxDiff)
			if err != nil || q == nil {
				q, err = s.store.GetOneAdaptiveQuestionFromPassage(userID, st, rcPassageID, max(0, target-35), min(100, target+35))
			}
		}

		// Fall back to any passage
		if q == nil {
			q, err = s.store.GetOneAdaptiveQuestion(userID, querySection, st, minDiff, maxDiff)
			if err != nil || q == nil {
				q, err = s.store.GetOneAdaptiveQuestion(userID, querySection, st, max(0, target-35), min(100, target+35))
				if err != nil || q == nil {
					missingSubtypes = append(missingSubtypes, st)
					continue
				}
			}
		}

		// Track the first RC passage found for deduplication
		if strings.HasPrefix(st, "rc_") && q.Passage != nil && rcPassageID == 0 {
			rcPassageID = q.Passage.ID
		}

		questions = append(questions, *q)
	}

	// Synchronous generation for missing subtypes
	if len(missingSubtypes) > 0 {
		difficulty := mapScoreToDifficulty(target)

		for _, st := range missingSubtypes {
			if len(questions) >= req.Count {
				break
			}

			genSection := models.SectionLR
			if strings.HasPrefix(st, "rc_") {
				genSection = models.SectionRC
			}
			genReq := models.GenerateBatchRequest{
				Section:    genSection,
				Difficulty: difficulty,
				Count:      6,
			}
			if genSection == models.SectionLR {
				ls := models.LRSubtype(st)
				genReq.LRSubtype = &ls
			}

			log.Printf("[quick-drill] Generating for %s/%s", genSection, st)
			_, genErr := s.GenerateBatch(ctx, genReq)
			if genErr != nil {
				log.Printf("WARN: generation failed for %s/%s: %v", genSection, st, genErr)
				continue
			}

			querySection := string(genSection)
			q, err := s.store.GetOneAdaptiveQuestion(userID, querySection, st, 0, 100)
			if err != nil || q == nil {
				continue
			}
			questions = append(questions, *q)
		}
	}

	// Shuffle final order
	rand.Shuffle(len(questions), func(i, j int) { questions[i], questions[j] = questions[j], questions[i] })

	// Async: check generation queue
	go s.CheckAndQueueGeneration(req.Section, nil, minDiff, maxDiff)

	return questions, nil
}

func (s *Service) GetSubtypeDrill(ctx context.Context, userID int64, req models.SubtypeDrillRequest) ([]models.DrillQuestion, error) {
	if req.Count <= 0 {
		req.Count = 6
	}

	var subtype string
	if req.LRSubtype != nil {
		subtype = *req.LRSubtype
	} else if req.RCSubtype != nil {
		subtype = *req.RCSubtype
	}

	// Get subtype ability
	subtypeAbility, err := s.store.GetOrCreateAbility(userID, models.ScopeSubtype, &subtype)
	if err != nil {
		subtypeAbility = &models.UserAbilityScore{AbilityScore: 50}
	}

	slider := req.DifficultySlider
	if slider == 0 {
		saved, _ := s.store.GetDifficultySlider(userID)
		if saved > 0 {
			slider = saved
		} else {
			slider = 50
		}
	}

	target := TargetDifficulty(subtypeAbility.AbilityScore, slider)
	minDiff := max(0, target-15)
	maxDiff := min(100, target+15)

	questions, err := s.store.GetAdaptiveQuestions(
		userID, req.Section, &subtype, minDiff, maxDiff, req.Count, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("get subtype drill: %w", err)
	}

	// Widen window if insufficient
	if len(questions) < req.Count {
		minDiff = max(0, target-35)
		maxDiff = min(100, target+35)
		questions, err = s.store.GetAdaptiveQuestions(
			userID, req.Section, &subtype, minDiff, maxDiff, req.Count, nil,
		)
		if err != nil {
			return nil, fmt.Errorf("get subtype drill (wide): %w", err)
		}
	}

	// Synchronous generation fallback if 0 questions found
	if len(questions) == 0 {
		difficulty := mapScoreToDifficulty(target)
		genReq := models.GenerateBatchRequest{
			Section:    models.Section(req.Section),
			Difficulty: difficulty,
			Count:      req.Count,
		}
		if req.LRSubtype != nil {
			ls := models.LRSubtype(*req.LRSubtype)
			genReq.LRSubtype = &ls
		}

		_, genErr := s.GenerateBatch(ctx, genReq)
		if genErr != nil {
			log.Printf("WARN: synchronous generation failed for subtype drill: %v", genErr)
		} else {
			// Retry fetch after generation
			questions, _ = s.store.GetAdaptiveQuestions(
				userID, req.Section, &subtype, minDiff, maxDiff, req.Count, nil,
			)
		}
	}

	// Async queue
	go s.CheckAndQueueGeneration(req.Section, &subtype, minDiff, maxDiff)

	return questions, nil
}

// ── RC Drill Serving ────────────────────────────────────

func (s *Service) GetRCDrill(ctx context.Context, userID int64, req models.RCDrillRequest) (*models.RCDrillResponse, error) {
	if req.Count <= 0 {
		req.Count = 8
	}

	// Get user's RC section ability
	section := "reading_comprehension"
	sectionAbility, err := s.store.GetOrCreateAbility(userID, models.ScopeSection, &section)
	if err != nil {
		sectionAbility = &models.UserAbilityScore{AbilityScore: 50}
	}

	slider := req.DifficultySlider
	if slider == 0 {
		saved, err := s.store.GetDifficultySlider(userID)
		if err == nil && saved > 0 {
			slider = saved
		} else {
			slider = 50
		}
	}

	target := TargetDifficulty(sectionAbility.AbilityScore, slider)
	minDiff := max(0, target-15)
	maxDiff := min(100, target+15)

	// Try narrow window
	passage, questions, err := s.store.GetRCPassageWithQuestions(
		userID, minDiff, maxDiff, req.RCSubtype, req.Comparative, req.Count,
	)
	if err != nil {
		return nil, fmt.Errorf("rc drill: %w", err)
	}

	// Widen window if no passage found
	if passage == nil {
		minDiff = max(0, target-35)
		maxDiff = min(100, target+35)
		passage, questions, err = s.store.GetRCPassageWithQuestions(
			userID, minDiff, maxDiff, req.RCSubtype, req.Comparative, req.Count,
		)
		if err != nil {
			return nil, fmt.Errorf("rc drill (wide): %w", err)
		}
	}

	// Synchronous generation fallback
	if passage == nil {
		difficulty := mapScoreToDifficulty(target)
		genReq := models.GenerateBatchRequest{
			Section:    models.SectionRC,
			Difficulty: difficulty,
			Count:      6,
		}

		log.Printf("[rc-drill] No passage found, generating synchronously")
		_, genErr := s.GenerateBatch(ctx, genReq)
		if genErr != nil {
			log.Printf("WARN: RC synchronous generation failed: %v", genErr)
		} else {
			// Retry after generation
			passage, questions, err = s.store.GetRCPassageWithQuestions(
				userID, 0, 100, req.RCSubtype, req.Comparative, req.Count,
			)
			if err != nil {
				return nil, fmt.Errorf("rc drill (post-gen): %w", err)
			}
		}
	}

	if passage == nil {
		return nil, fmt.Errorf("no RC passages available")
	}

	// Convert to drill questions (strip answer data)
	drillPassage := passage.ToDrillPassage()
	drillQuestions := make([]models.DrillQuestion, 0, len(questions))
	for _, q := range questions {
		dq := models.DrillQuestion{
			ID:              q.ID,
			Section:         q.Section,
			LRSubtype:       q.LRSubtype,
			RCSubtype:       q.RCSubtype,
			Difficulty:      q.Difficulty,
			DifficultyScore: q.DifficultyScore,
			Stimulus:        q.Stimulus,
			QuestionStem:    q.QuestionStem,
			Passage:         &drillPassage,
		}
		for _, c := range q.Choices {
			dq.Choices = append(dq.Choices, models.DrillChoice{
				ChoiceID:   c.ChoiceID,
				ChoiceText: c.ChoiceText,
			})
		}
		drillQuestions = append(drillQuestions, dq)
	}

	// Async check RC inventory
	go s.CheckRCInventory(minDiff, maxDiff, req.RCSubtype)

	return &models.RCDrillResponse{
		Passage:   drillPassage,
		Questions: drillQuestions,
		Total:     len(drillQuestions),
		Page:      1,
		PageSize:  req.Count,
	}, nil
}

var rcSubjectAreas = []string{"law", "natural_science", "social_science", "humanities"}

func (s *Service) NextRCSubjectArea() string {
	last := s.store.GetLastRCSubjectArea()
	if last == "" {
		return rcSubjectAreas[0]
	}
	for i, sa := range rcSubjectAreas {
		if sa == last {
			return rcSubjectAreas[(i+1)%len(rcSubjectAreas)]
		}
	}
	return rcSubjectAreas[0]
}

func (s *Service) ShouldGenerateComparative() bool {
	comparative, total := s.store.GetComparativeRatio()
	if total < 4 {
		return false
	}
	return float64(comparative)/float64(total) < 0.25
}

func (s *Service) CheckRCInventory(minDiff, maxDiff int, rcSubtype *string) {
	if !s.autoGenEnabledRC {
		return
	}

	type bucket struct {
		min, max   int
		difficulty string
	}
	buckets := []bucket{
		{0, 20, "easy"}, {21, 40, "easy"}, {41, 60, "medium"},
		{61, 80, "hard"}, {81, 100, "hard"},
	}

	for _, b := range buckets {
		if b.max < minDiff || b.min > maxDiff {
			continue
		}
		count := s.store.CountRCPassagesInBucket(b.min, b.max)
		if count < 3 {
			subjectArea := s.NextRCSubjectArea()
			isComparative := s.ShouldGenerateComparative()
			s.store.UpsertRCGenerationQueue(b.min, b.max, b.difficulty, subjectArea, isComparative)
			log.Printf("[rc-inventory] Queued RC generation: bucket=%d-%d subject=%s comparative=%v",
				b.min, b.max, subjectArea, isComparative)
		}
	}
}

func mapScoreToDifficulty(score int) models.Difficulty {
	if score <= 35 {
		return models.DifficultyEasy
	}
	if score <= 65 {
		return models.DifficultyMedium
	}
	return models.DifficultyHard
}

// ── Generation Queue ────────────────────────────────────

func (s *Service) CheckAndQueueGeneration(section string, subtype *string, minDiff, maxDiff int) {
	type bucket struct {
		min, max   int
		difficulty string
	}
	buckets := []bucket{
		{0, 20, "easy"}, {21, 40, "easy"}, {41, 60, "medium"},
		{61, 80, "hard"}, {81, 100, "hard"},
	}

	for _, b := range buckets {
		if b.max < minDiff || b.min > maxDiff {
			continue
		}
		count, err := s.store.CountQuestionsInBucket(section, subtype, b.min, b.max)
		if err != nil {
			log.Printf("[gen-queue] count error: %v", err)
			continue
		}
		if count < 6 {
			needed := 6 - count
			if needed < 1 {
				needed = 1
			}
			s.store.UpsertGenerationQueue(section, subtype, b.min, b.max, b.difficulty, needed)
		}
	}
}

// CheckUserInventoryAndQueue checks if a specific user is running low on
// unseen questions for a given subtype and queues generation if so.
// This complements CheckAndQueueGeneration (which checks global counts)
// by ensuring individual users don't exhaust their question pool.
func (s *Service) CheckUserInventoryAndQueue(userID int64, section string, subtype string) {
	// Check if auto-gen is enabled for this section
	switch section {
	case "logical_reasoning":
		if !s.autoGenEnabledLR {
			return
		}
	case "reading_comprehension":
		if !s.autoGenEnabledRC {
			return
		}
	default:
		return
	}

	// Count unseen questions for this user+subtype
	unseen, err := s.store.CountUnseenForUser(userID, section, subtype)
	if err != nil {
		log.Printf("[user-gen] count unseen error for user=%d section=%s subtype=%s: %v",
			userID, section, subtype, err)
		return
	}

	if unseen >= s.autoGenMinUnseen {
		return
	}

	log.Printf("[user-gen] user=%d low on %s/%s: unseen=%d threshold=%d, queueing generation",
		userID, section, subtype, unseen, s.autoGenMinUnseen)

	// Get user's ability score for this subtype to determine target difficulty
	subtypeAbility, err := s.store.GetOrCreateAbility(userID, models.ScopeSubtype, &subtype)
	if err != nil {
		log.Printf("[user-gen] get ability error: %v", err)
		subtypeAbility = &models.UserAbilityScore{AbilityScore: 50}
	}

	// Compute target difficulty from ability (centered, slider=50)
	target := TargetDifficulty(subtypeAbility.AbilityScore, 50)
	minDiff := max(0, target-15)
	maxDiff := min(100, target+15)

	// Queue generation for overlapping difficulty buckets
	type bucket struct {
		min, max   int
		difficulty string
	}
	buckets := []bucket{
		{0, 20, "easy"}, {21, 40, "easy"}, {41, 60, "medium"},
		{61, 80, "hard"}, {81, 100, "hard"},
	}

	for _, b := range buckets {
		if b.max < minDiff || b.min > maxDiff {
			continue
		}
		s.store.UpsertGenerationQueue(section, &subtype, b.min, b.max, b.difficulty, 6)
	}
}

func (s *Service) StartGenerationWorker(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	log.Println("[gen-worker] Background generation worker started")

	for {
		select {
		case <-ctx.Done():
			log.Println("[gen-worker] Shutting down")
			return
		case <-ticker.C:
			s.processGenerationQueue(ctx)
		}
	}
}

func (s *Service) processGenerationQueue(ctx context.Context) {
	items, err := s.store.GetPendingGenerations(5)
	if err != nil {
		log.Printf("[gen-queue] error fetching queue: %v", err)
		return
	}

	for _, item := range items {
		s.store.UpdateGenerationStatus(item.ID, "generating", nil)

		genReq := models.GenerateBatchRequest{
			Section:    models.Section(item.Section),
			Difficulty: models.Difficulty(item.TargetDifficulty),
			Count:      item.QuestionsNeeded,
		}
		if item.LRSubtype != nil {
			sub := models.LRSubtype(*item.LRSubtype)
			genReq.LRSubtype = &sub
		}
		if item.RCSubtype != nil {
			sub := models.RCSubtype(*item.RCSubtype)
			genReq.RCSubtype = &sub
		}
		if item.SubjectArea != nil {
			genReq.SubjectArea = *item.SubjectArea
		}
		genReq.IsComparative = item.IsComparative

		_, err := s.GenerateBatch(ctx, genReq)
		if err != nil {
			errMsg := err.Error()
			s.store.UpdateGenerationStatus(item.ID, "failed", &errMsg)
			log.Printf("[gen-queue] failed: section=%s subtype=%v bucket=%d-%d err=%v",
				item.Section, item.LRSubtype, item.DifficultyBucketMin, item.DifficultyBucketMax, err)
		} else {
			s.store.UpdateGenerationStatus(item.ID, "completed", nil)
			log.Printf("[gen-queue] completed: section=%s subtype=%v bucket=%d-%d",
				item.Section, item.LRSubtype, item.DifficultyBucketMin, item.DifficultyBucketMax)
		}
	}
}

// ── User Settings ───────────────────────────────────────

func (s *Service) GetAbilities(userID int64) (*models.AbilityResponse, error) {
	resp, err := s.store.GetAllAbilities(userID)
	if err != nil {
		return nil, err
	}
	slider, err := s.store.GetDifficultySlider(userID)
	if err == nil {
		resp.DifficultySlider = slider
	}
	return resp, nil
}

func (s *Service) SetDifficultySlider(userID int64, value int) error {
	return s.store.SetDifficultySlider(userID, value)
}

// ── Admin Methods ───────────────────────────────────────

func (s *Service) GetQualityStats() (*models.QualityStats, error) {
	return s.store.GetQualityStats()
}

func (s *Service) GetGenerationStats() (*models.GenerationStats, error) {
	return s.store.GetGenerationStats()
}

func (s *Service) GetFlaggedQuestions(limit, offset int) ([]models.Question, int, error) {
	return s.store.GetFlaggedQuestions(limit, offset)
}

func (s *Service) RecalibrateDifficulty() (*models.RecalibrationReport, error) {
	candidates, err := s.store.GetRecalibrationCandidates(50)
	if err != nil {
		return nil, fmt.Errorf("get recalibration candidates: %w", err)
	}

	recalibrated := 0
	for _, c := range candidates {
		err := s.store.UpdateQuestionDifficulty(c.QuestionID, c.SuggestedDifficulty)
		if err != nil {
			log.Printf("WARN: failed to recalibrate question %d: %v", c.QuestionID, err)
			continue
		}
		recalibrated++
	}

	return &models.RecalibrationReport{
		TotalEvaluated: len(candidates),
		Recalibrated:   recalibrated,
		Details:        candidates,
	}, nil
}

// ── Export/Import ────────────────────────────────────────

func (s *Service) ExportQuestions() (*models.ExportEnvelope, error) {
	questions, err := s.store.ExportPassedQuestions()
	if err != nil {
		return nil, fmt.Errorf("export questions: %w", err)
	}
	if questions == nil {
		questions = []models.ExportQuestion{}
	}
	return &models.ExportEnvelope{
		Version:    1,
		ExportedAt: time.Now().UTC(),
		Questions:  questions,
	}, nil
}

func (s *Service) ImportQuestions(ctx context.Context, envelope models.ExportEnvelope) (*models.ImportResult, error) {
	if envelope.Version != 1 {
		return nil, fmt.Errorf("unsupported export version: %d", envelope.Version)
	}

	// Validate all questions structurally
	for i, q := range envelope.Questions {
		if err := validateExportQuestion(q); err != nil {
			return nil, fmt.Errorf("question %d: %w", i+1, err)
		}
	}

	// Check for duplicates
	pairs := make([]StimulusStemPair, len(envelope.Questions))
	for i, q := range envelope.Questions {
		pairs[i] = StimulusStemPair{Stimulus: q.Stimulus, QuestionStem: q.QuestionStem}
	}
	existing, err := s.store.CheckExistingQuestions(pairs)
	if err != nil {
		return nil, fmt.Errorf("check duplicates: %w", err)
	}

	// Filter duplicates and group by (section, subtype, difficulty)
	type batchKey struct {
		Section    string
		Subtype    string
		Difficulty string
		PassageKey string
	}
	groupMap := make(map[batchKey]*ImportBatchGroup)
	totalSkipped := 0

	for _, q := range envelope.Questions {
		key := q.Stimulus + "||" + q.QuestionStem
		if existing[key] {
			totalSkipped++
			continue
		}

		subtype := ""
		if q.LRSubtype != nil {
			subtype = string(*q.LRSubtype)
		}
		if q.RCSubtype != nil {
			subtype = string(*q.RCSubtype)
		}
		passageKey := ""
		if q.Passage != nil {
			passageKey = q.Passage.Title + "||" + q.Passage.Content
		}

		bk := batchKey{
			Section:    string(q.Section),
			Subtype:    subtype,
			Difficulty: string(q.Difficulty),
			PassageKey: passageKey,
		}

		group, ok := groupMap[bk]
		if !ok {
			group = &ImportBatchGroup{
				Section:    q.Section,
				Difficulty: q.Difficulty,
				Passage:    q.Passage,
			}
			if q.LRSubtype != nil {
				group.LRSubtype = q.LRSubtype
			}
			groupMap[bk] = group
		}
		group.Questions = append(group.Questions, q)
	}

	// Convert to slice
	groups := make([]ImportBatchGroup, 0, len(groupMap))
	for _, g := range groupMap {
		groups = append(groups, *g)
	}

	if len(groups) == 0 {
		return &models.ImportResult{
			TotalInPayload: len(envelope.Questions),
			Skipped:        totalSkipped,
		}, nil
	}

	result, err := s.store.ImportQuestions(ctx, groups)
	if err != nil {
		return nil, fmt.Errorf("import questions: %w", err)
	}

	result.TotalInPayload = len(envelope.Questions)
	result.Skipped = totalSkipped
	return result, nil
}

func validateExportQuestion(q models.ExportQuestion) error {
	if q.Section != models.SectionLR && q.Section != models.SectionRC {
		return fmt.Errorf("invalid section %q", q.Section)
	}
	if q.Difficulty != models.DifficultyEasy && q.Difficulty != models.DifficultyMedium && q.Difficulty != models.DifficultyHard {
		return fmt.Errorf("invalid difficulty %q", q.Difficulty)
	}
	if len(q.Choices) != 5 {
		return fmt.Errorf("expected 5 choices, got %d", len(q.Choices))
	}
	expectedIDs := []string{"A", "B", "C", "D", "E"}
	for i, c := range q.Choices {
		if c.ChoiceID != expectedIDs[i] {
			return fmt.Errorf("choice %d has id %q, expected %q", i, c.ChoiceID, expectedIDs[i])
		}
	}
	if q.QuestionStem == "" {
		return fmt.Errorf("empty question_stem")
	}
	if q.CorrectAnswerID == "" {
		return fmt.Errorf("empty correct_answer_id")
	}
	if q.Section == models.SectionLR && q.Stimulus == "" {
		return fmt.Errorf("LR question has empty stimulus")
	}
	if q.Section == models.SectionRC && q.Passage == nil {
		return fmt.Errorf("RC question has no passage")
	}
	if q.Passage != nil && (q.Passage.Title == "" || q.Passage.Content == "") {
		return fmt.Errorf("RC passage missing title or content")
	}
	if q.Section == models.SectionLR && q.LRSubtype != nil {
		if !models.ValidLRSubtypes[*q.LRSubtype] {
			return fmt.Errorf("invalid lr_subtype %q", *q.LRSubtype)
		}
	}
	if q.Section == models.SectionRC && q.RCSubtype != nil {
		if !models.ValidRCSubtypes[*q.RCSubtype] {
			return fmt.Errorf("invalid rc_subtype %q", *q.RCSubtype)
		}
	}
	return nil
}

// ── History & Bookmarks ───────────────────────────────────

func (s *Service) GetUserHistory(userID int64, req models.HistoryListRequest) (*models.HistoryListResponse, error) {
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 {
		req.PageSize = 20
	}
	if req.PageSize > 50 {
		req.PageSize = 50
	}
	if req.SortBy == "" {
		req.SortBy = "answered_at"
	}
	if req.SortOrder == "" {
		req.SortOrder = "desc"
	}

	questions, total, err := s.store.GetUserHistory(userID, req)
	if err != nil {
		return nil, err
	}
	if questions == nil {
		questions = []models.HistoryQuestion{}
	}
	return &models.HistoryListResponse{
		Questions: questions,
		Total:     total,
		Page:      req.Page,
		PageSize:  req.PageSize,
	}, nil
}

func (s *Service) GetUserMistakes(userID int64, page, pageSize int) (*models.HistoryListResponse, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 50 {
		pageSize = 50
	}

	questions, total, err := s.store.GetUserMistakes(userID, page, pageSize)
	if err != nil {
		return nil, err
	}
	if questions == nil {
		questions = []models.HistoryQuestion{}
	}
	return &models.HistoryListResponse{
		Questions: questions,
		Total:     total,
		Page:      page,
		PageSize:  pageSize,
	}, nil
}

func (s *Service) GetUserHistoryStats(userID int64) (*models.HistoryStatsResponse, error) {
	return s.store.GetUserHistoryStats(userID)
}

func (s *Service) GetDrillReview(userID int64, questionIDs []int64) ([]models.HistoryQuestion, error) {
	return s.store.GetDrillReview(userID, questionIDs)
}

func (s *Service) CreateBookmark(userID, questionID int64, note *string) error {
	return s.store.CreateBookmark(userID, questionID, note)
}

func (s *Service) DeleteBookmark(userID, questionID int64) error {
	return s.store.DeleteBookmark(userID, questionID)
}

func (s *Service) GetBookmarks(userID int64, page, pageSize int) (*models.BookmarkListResponse, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 50 {
		pageSize = 50
	}

	bookmarks, total, err := s.store.GetBookmarks(userID, page, pageSize)
	if err != nil {
		return nil, err
	}
	return &models.BookmarkListResponse{
		Bookmarks: bookmarks,
		Total:     total,
		Page:      page,
		PageSize:  pageSize,
	}, nil
}
