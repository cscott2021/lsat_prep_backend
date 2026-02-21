package questions

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lsat-prep/backend/internal/generator"
	"github.com/lsat-prep/backend/internal/models"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// ── Batch Management ────────────────────────────────────

func (s *Store) CreateBatch(req models.GenerateBatchRequest) (*models.QuestionBatch, error) {
	var batch models.QuestionBatch
	err := s.db.QueryRow(
		`INSERT INTO question_batches (section, lr_subtype, difficulty, status)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, section, lr_subtype, difficulty, status, question_count, created_at`,
		req.Section, req.LRSubtype, req.Difficulty, models.BatchPending,
	).Scan(&batch.ID, &batch.Section, &batch.LRSubtype, &batch.Difficulty,
		&batch.Status, &batch.QuestionCount, &batch.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create batch: %w", err)
	}
	return &batch, nil
}

func (s *Store) UpdateBatchStatus(batchID int64, status models.BatchStatus) error {
	_, err := s.db.Exec(
		`UPDATE question_batches SET status = $1 WHERE id = $2`,
		status, batchID,
	)
	return err
}

func (s *Store) FailBatch(batchID int64, errMsg string) error {
	_, err := s.db.Exec(
		`UPDATE question_batches SET status = $1, error_message = $2, completed_at = NOW() WHERE id = $3`,
		models.BatchFailed, errMsg, batchID,
	)
	return err
}

func (s *Store) CompleteBatch(batchID int64, passed, flagged, rejected int, timeMs int64, promptTokens, outputTokens, validationTokens int, modelUsed string) error {
	totalCount := passed + flagged
	_, err := s.db.Exec(
		`UPDATE question_batches
		 SET status = $1, question_count = $2, questions_passed = $3, questions_flagged = $4,
		     questions_rejected = $5, generation_time_ms = $6, prompt_tokens = $7,
		     output_tokens = $8, validation_tokens = $9, model_used = $10, completed_at = NOW()
		 WHERE id = $11`,
		models.BatchCompleted, totalCount, passed, flagged, rejected,
		timeMs, promptTokens, outputTokens, validationTokens, modelUsed, batchID,
	)
	return err
}

func (s *Store) GetBatch(batchID int64) (*models.QuestionBatch, error) {
	var batch models.QuestionBatch
	err := s.db.QueryRow(
		`SELECT id, section, lr_subtype, difficulty, status, question_count,
		        questions_passed, questions_flagged, questions_rejected,
		        model_used, prompt_tokens, output_tokens, validation_tokens,
		        generation_time_ms, total_cost_cents, error_message, created_at, completed_at
		 FROM question_batches WHERE id = $1`,
		batchID,
	).Scan(&batch.ID, &batch.Section, &batch.LRSubtype, &batch.Difficulty,
		&batch.Status, &batch.QuestionCount,
		&batch.QuestionsPassed, &batch.QuestionsFlagged, &batch.QuestionsRejected,
		&batch.ModelUsed, &batch.PromptTokens, &batch.OutputTokens, &batch.ValidationTokens,
		&batch.GenerationTimeMs, &batch.TotalCostCents, &batch.ErrorMessage, &batch.CreatedAt, &batch.CompletedAt)
	if err != nil {
		return nil, fmt.Errorf("get batch: %w", err)
	}
	return &batch, nil
}

func (s *Store) ListBatches(status *models.BatchStatus, limit, offset int) ([]models.QuestionBatch, error) {
	var rows *sql.Rows
	var err error

	selectCols := `id, section, lr_subtype, difficulty, status, question_count,
		        questions_passed, questions_flagged, questions_rejected,
		        model_used, prompt_tokens, output_tokens, validation_tokens,
		        generation_time_ms, total_cost_cents, error_message, created_at, completed_at`

	if status != nil {
		rows, err = s.db.Query(
			fmt.Sprintf(`SELECT %s FROM question_batches WHERE status = $1
			 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, selectCols),
			*status, limit, offset,
		)
	} else {
		rows, err = s.db.Query(
			fmt.Sprintf(`SELECT %s FROM question_batches
			 ORDER BY created_at DESC LIMIT $1 OFFSET $2`, selectCols),
			limit, offset,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list batches: %w", err)
	}
	defer rows.Close()

	var batches []models.QuestionBatch
	for rows.Next() {
		var b models.QuestionBatch
		if err := rows.Scan(&b.ID, &b.Section, &b.LRSubtype, &b.Difficulty,
			&b.Status, &b.QuestionCount,
			&b.QuestionsPassed, &b.QuestionsFlagged, &b.QuestionsRejected,
			&b.ModelUsed, &b.PromptTokens, &b.OutputTokens, &b.ValidationTokens,
			&b.GenerationTimeMs, &b.TotalCostCents, &b.ErrorMessage, &b.CreatedAt, &b.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan batch: %w", err)
		}
		batches = append(batches, b)
	}
	return batches, rows.Err()
}

// ── Question Storage ────────────────────────────────────

type QuestionSaveOptions struct {
	ValidationStatus string
	QualityScore     *float64
	ValidationReason *string
	AdversarialScore *string
	Flagged          bool
}

func (s *Store) SaveGeneratedBatch(ctx context.Context, batchID int64, batch *generator.GeneratedBatch, req models.GenerateBatchRequest, opts []QuestionSaveOptions) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// For RC: insert passage first
	var passageID *int64
	if batch.Passage != nil {
		var pid int64
		subjectArea := batch.Passage.SubjectArea
		if subjectArea == "" {
			subjectArea = "law"
		}
		err := tx.QueryRow(
			`INSERT INTO rc_passages (batch_id, title, subject_area, content, is_comparative, passage_b)
			 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			batchID, batch.Passage.Title, subjectArea, batch.Passage.Content,
			batch.Passage.IsComparative, nullString(batch.Passage.PassageB),
		).Scan(&pid)
		if err != nil {
			return fmt.Errorf("insert passage: %w", err)
		}
		passageID = &pid
	}

	// Insert each question + its choices
	for i, gq := range batch.Questions {
		var questionID int64
		valStatus := "unvalidated"
		var qualityScore *float64
		var valReasoning *string
		var advScore *string
		flagged := false

		if i < len(opts) {
			valStatus = opts[i].ValidationStatus
			qualityScore = opts[i].QualityScore
			valReasoning = opts[i].ValidationReason
			advScore = opts[i].AdversarialScore
			flagged = opts[i].Flagged
		}

		diffScore := generator.AssignDifficultyScore(req.Difficulty)

		err := tx.QueryRow(
			`INSERT INTO questions
			 (batch_id, section, lr_subtype, rc_subtype, difficulty, difficulty_score,
			  stimulus, question_stem, correct_answer_id, explanation, passage_id,
			  quality_score, validation_status, validation_reasoning, adversarial_score, flagged)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
			 RETURNING id`,
			batchID, req.Section, req.LRSubtype, req.RCSubtype, req.Difficulty, diffScore,
			gq.Stimulus, gq.QuestionStem, gq.CorrectAnswerID, gq.Explanation,
			passageID, qualityScore, valStatus, valReasoning, advScore, flagged,
		).Scan(&questionID)
		if err != nil {
			return fmt.Errorf("insert question: %w", err)
		}

		for _, gc := range gq.Choices {
			isCorrect := gc.ID == gq.CorrectAnswerID
			var wrongType *string
			if gc.WrongAnswerType != nil {
				wrongType = gc.WrongAnswerType
			}
			_, err := tx.Exec(
				`INSERT INTO answer_choices
				 (question_id, choice_id, choice_text, explanation, is_correct, wrong_answer_type)
				 VALUES ($1, $2, $3, $4, $5, $6)`,
				questionID, gc.ID, gc.Text, gc.Explanation, isCorrect, wrongType,
			)
			if err != nil {
				return fmt.Errorf("insert choice: %w", err)
			}
		}
	}

	return tx.Commit()
}

// ── Validation Logging ──────────────────────────────────

func (s *Store) LogValidation(vlog models.ValidationLog) error {
	_, err := s.db.Exec(
		`INSERT INTO validation_logs
		 (question_id, batch_id, stage, model_used, generated_answer, validator_answer,
		  matches, confidence, reasoning, adversarial_details, prompt_tokens, output_tokens)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		vlog.QuestionID, vlog.BatchID, vlog.Stage, vlog.ModelUsed,
		nullString(vlog.GeneratedAnswer), nullString(vlog.ValidatorAnswer),
		vlog.Matches, nullString(vlog.Confidence), nullString(vlog.Reasoning),
		nullString(vlog.AdversarialDetails), vlog.PromptTokens, vlog.OutputTokens,
	)
	return err
}

func (s *Store) UpdateQuestionValidation(questionID int64, status string, reasoning *string, adversarialScore *string, qualityScore *float64, flagged bool) error {
	_, err := s.db.Exec(
		`UPDATE questions SET validation_status = $1, validation_reasoning = $2,
		        adversarial_score = $3, quality_score = $4, flagged = $5
		 WHERE id = $6`,
		status, reasoning, adversarialScore, qualityScore, flagged, questionID,
	)
	return err
}

// ── Serving Questions to Users ──────────────────────────

func (s *Store) GetQuestionWithChoices(questionID int64) (*models.Question, error) {
	var q models.Question
	err := s.db.QueryRow(
		`SELECT id, batch_id, section, lr_subtype, rc_subtype, difficulty, difficulty_score,
		        stimulus, question_stem, correct_answer_id, explanation, passage_id, quality_score,
		        validation_status, validation_reasoning, adversarial_score,
		        flagged, times_served, times_correct, created_at
		 FROM questions WHERE id = $1`,
		questionID,
	).Scan(&q.ID, &q.BatchID, &q.Section, &q.LRSubtype, &q.RCSubtype, &q.Difficulty, &q.DifficultyScore,
		&q.Stimulus, &q.QuestionStem, &q.CorrectAnswerID, &q.Explanation,
		&q.PassageID, &q.QualityScore,
		&q.ValidationStatus, &q.ValidationReasoning, &q.AdversarialScore,
		&q.Flagged, &q.TimesServed, &q.TimesCorrect, &q.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get question: %w", err)
	}

	choices, err := s.getChoicesForQuestion(questionID)
	if err != nil {
		return nil, err
	}
	q.Choices = choices

	return &q, nil
}

func (s *Store) GetDrillQuestions(section models.Section, subtype *models.LRSubtype, difficulty models.Difficulty, count int) ([]models.Question, error) {
	var rows *sql.Rows
	var err error

	qCols := `q.id, q.batch_id, q.section, q.lr_subtype, q.rc_subtype, q.difficulty, q.difficulty_score,
		q.stimulus, q.question_stem, q.correct_answer_id, q.explanation,
		q.passage_id, q.quality_score, q.validation_status, q.validation_reasoning,
		q.adversarial_score, q.flagged, q.times_served, q.times_correct, q.created_at`
	acCols := `ac.id, ac.choice_id, ac.choice_text, ac.explanation, ac.is_correct, COALESCE(ac.wrong_answer_type, '')`

	// Filter: only serve passed/unvalidated questions with acceptable quality
	// Flagged questions require admin review before serving
	validationFilter := `AND q.validation_status IN ('passed', 'unvalidated')
		AND (q.quality_score >= 0.50 OR q.quality_score IS NULL)`

	if subtype != nil {
		rows, err = s.db.Query(
			fmt.Sprintf(`SELECT %s, %s
			 FROM questions q
			 JOIN answer_choices ac ON ac.question_id = q.id
			 WHERE q.section = $1 AND q.lr_subtype = $2 AND q.difficulty = $3
			 %s
			 ORDER BY q.times_served ASC, q.id, ac.choice_id
			 LIMIT $4`, qCols, acCols, validationFilter),
			section, *subtype, difficulty, count*5,
		)
	} else {
		rows, err = s.db.Query(
			fmt.Sprintf(`SELECT %s, %s
			 FROM questions q
			 JOIN answer_choices ac ON ac.question_id = q.id
			 WHERE q.section = $1 AND q.difficulty = $2
			 %s
			 ORDER BY q.times_served ASC, q.id, ac.choice_id
			 LIMIT $3`, qCols, acCols, validationFilter),
			section, difficulty, count*5,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("get drill questions: %w", err)
	}
	defer rows.Close()

	return s.scanQuestionsWithChoices(rows, count)
}

func (s *Store) scanQuestionsWithChoices(rows *sql.Rows, maxQuestions int) ([]models.Question, error) {
	questionMap := make(map[int64]*models.Question)
	var questionOrder []int64

	for rows.Next() {
		var q models.Question
		var choice models.AnswerChoice

		if err := rows.Scan(
			&q.ID, &q.BatchID, &q.Section, &q.LRSubtype, &q.RCSubtype, &q.Difficulty, &q.DifficultyScore,
			&q.Stimulus, &q.QuestionStem, &q.CorrectAnswerID, &q.Explanation,
			&q.PassageID, &q.QualityScore, &q.ValidationStatus, &q.ValidationReasoning,
			&q.AdversarialScore, &q.Flagged, &q.TimesServed, &q.TimesCorrect, &q.CreatedAt,
			&choice.ID, &choice.ChoiceID, &choice.ChoiceText, &choice.Explanation, &choice.IsCorrect,
			&choice.WrongAnswerType,
		); err != nil {
			return nil, fmt.Errorf("scan question row: %w", err)
		}

		choice.QuestionID = q.ID

		if existing, ok := questionMap[q.ID]; ok {
			existing.Choices = append(existing.Choices, choice)
		} else {
			if len(questionMap) >= maxQuestions {
				continue
			}
			q.Choices = []models.AnswerChoice{choice}
			questionMap[q.ID] = &q
			questionOrder = append(questionOrder, q.ID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	questions := make([]models.Question, 0, len(questionOrder))
	for _, id := range questionOrder {
		questions = append(questions, *questionMap[id])
	}
	return questions, nil
}

func (s *Store) getChoicesForQuestion(questionID int64) ([]models.AnswerChoice, error) {
	rows, err := s.db.Query(
		`SELECT id, question_id, choice_id, choice_text, explanation, is_correct, COALESCE(wrong_answer_type, '')
		 FROM answer_choices WHERE question_id = $1 ORDER BY choice_id`,
		questionID,
	)
	if err != nil {
		return nil, fmt.Errorf("get choices: %w", err)
	}
	defer rows.Close()

	var choices []models.AnswerChoice
	for rows.Next() {
		var c models.AnswerChoice
		if err := rows.Scan(&c.ID, &c.QuestionID, &c.ChoiceID, &c.ChoiceText,
			&c.Explanation, &c.IsCorrect, &c.WrongAnswerType); err != nil {
			return nil, fmt.Errorf("scan choice: %w", err)
		}
		choices = append(choices, c)
	}
	return choices, rows.Err()
}

func (s *Store) IncrementServed(questionID int64) error {
	_, err := s.db.Exec(`UPDATE questions SET times_served = times_served + 1 WHERE id = $1`, questionID)
	return err
}

func (s *Store) IncrementCorrect(questionID int64) error {
	_, err := s.db.Exec(`UPDATE questions SET times_correct = times_correct + 1 WHERE id = $1`, questionID)
	return err
}

// ── Ability Scores ──────────────────────────────────────

func (s *Store) GetOrCreateAbility(userID int64, scope models.AbilityScope, scopeValue *string) (*models.UserAbilityScore, error) {
	_, err := s.db.Exec(
		`INSERT INTO user_ability_scores (user_id, scope, scope_value)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, scope, scope_value) DO NOTHING`,
		userID, scope, scopeValue,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert ability: %w", err)
	}

	var a models.UserAbilityScore
	err = s.db.QueryRow(
		`SELECT id, user_id, scope, scope_value, ability_score,
		        questions_answered, questions_correct, last_updated
		 FROM user_ability_scores
		 WHERE user_id = $1 AND scope = $2 AND scope_value IS NOT DISTINCT FROM $3`,
		userID, scope, scopeValue,
	).Scan(&a.ID, &a.UserID, &a.Scope, &a.ScopeValue, &a.AbilityScore,
		&a.QuestionsAnswered, &a.QuestionsCorrect, &a.LastUpdated)
	if err != nil {
		return nil, fmt.Errorf("get ability: %w", err)
	}
	return &a, nil
}

func (s *Store) UpdateAbility(userID int64, scope models.AbilityScope, scopeValue *string, newScore int, correct bool) error {
	correctIncrement := 0
	if correct {
		correctIncrement = 1
	}
	_, err := s.db.Exec(
		`UPDATE user_ability_scores
		 SET ability_score = $1,
		     questions_answered = questions_answered + 1,
		     questions_correct = questions_correct + $2,
		     last_updated = NOW()
		 WHERE user_id = $3 AND scope = $4 AND scope_value IS NOT DISTINCT FROM $5`,
		newScore, correctIncrement, userID, scope, scopeValue,
	)
	return err
}

func (s *Store) GetAllAbilities(userID int64) (*models.AbilityResponse, error) {
	rows, err := s.db.Query(
		`SELECT scope, scope_value, ability_score
		 FROM user_ability_scores WHERE user_id = $1`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("get all abilities: %w", err)
	}
	defer rows.Close()

	resp := &models.AbilityResponse{
		OverallAbility:   50,
		SectionAbilities: make(map[string]int),
		SubtypeAbilities: make(map[string]int),
	}

	for rows.Next() {
		var scope string
		var scopeValue *string
		var score int
		if err := rows.Scan(&scope, &scopeValue, &score); err != nil {
			return nil, err
		}
		switch models.AbilityScope(scope) {
		case models.ScopeOverall:
			resp.OverallAbility = score
		case models.ScopeSection:
			if scopeValue != nil {
				resp.SectionAbilities[*scopeValue] = score
			}
		case models.ScopeSubtype:
			if scopeValue != nil {
				resp.SubtypeAbilities[*scopeValue] = score
			}
		}
	}
	return resp, rows.Err()
}

// ── Question History ────────────────────────────────────

func (s *Store) RecordAnswer(userID, questionID int64, correct bool) error {
	_, err := s.db.Exec(
		`INSERT INTO user_question_history (user_id, question_id, correct)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, question_id)
		 DO UPDATE SET correct = $3, answered_at = NOW()`,
		userID, questionID, correct,
	)
	return err
}

// CountUnseenForUser counts servable questions in a section+subtype that the
// user has not yet answered. Used by user-aware auto-generation to detect when
// an active user is running low on fresh questions.
func (s *Store) CountUnseenForUser(userID int64, section string, subtype string) (int, error) {
	var count int
	var filterClause string
	if strings.HasPrefix(subtype, "rc_") {
		filterClause = "AND q.rc_subtype = $3"
	} else {
		filterClause = "AND q.lr_subtype = $3"
	}

	err := s.db.QueryRow(
		fmt.Sprintf(`SELECT COUNT(*)
		 FROM questions q
		 LEFT JOIN user_question_history h ON h.question_id = q.id AND h.user_id = $1
		 WHERE q.section = $2
		   %s
		   AND h.id IS NULL
		   AND q.validation_status IN ('passed', 'unvalidated')
		   AND (q.quality_score >= 0.50 OR q.quality_score IS NULL)`, filterClause),
		userID, section, subtype,
	).Scan(&count)
	return count, err
}

// ── Adaptive Serving ────────────────────────────────────

func (s *Store) GetOneAdaptiveQuestion(userID int64, section string, subtype string, minDiff, maxDiff int) (*models.DrillQuestion, error) {
	var filterClause string
	if strings.HasPrefix(subtype, "rc_") {
		filterClause = "AND q.rc_subtype = $5"
	} else {
		filterClause = "AND q.lr_subtype = $5"
	}

	// First, pick one question
	pickQuery := fmt.Sprintf(`
		SELECT q.id, q.section, q.lr_subtype, q.rc_subtype, q.difficulty, q.difficulty_score,
		       q.stimulus, q.question_stem
		FROM questions q
		LEFT JOIN user_question_history h ON h.question_id = q.id AND h.user_id = $1
		WHERE q.section = $2
		  AND q.difficulty_score >= $3
		  AND q.difficulty_score <= $4
		  %s
		  AND q.validation_status IN ('passed', 'unvalidated')
		  AND (q.quality_score >= 0.50 OR q.quality_score IS NULL)
		ORDER BY
		    CASE WHEN h.id IS NULL THEN 0 ELSE 1 END,
		    RANDOM()
		LIMIT 1`, filterClause)

	var id int64
	var sect, difficulty string
	var lrSubtype, rcSubtype *string
	var diffScore int
	var stimulus, stem string

	err := s.db.QueryRow(pickQuery, userID, section, minDiff, maxDiff, subtype).Scan(
		&id, &sect, &lrSubtype, &rcSubtype, &difficulty, &diffScore, &stimulus, &stem)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get one adaptive question: %w", err)
	}

	dq := &models.DrillQuestion{
		ID:              id,
		Section:         models.Section(sect),
		Difficulty:      models.Difficulty(difficulty),
		DifficultyScore: diffScore,
		Stimulus:        stimulus,
		QuestionStem:    stem,
	}
	if lrSubtype != nil {
		ls := models.LRSubtype(*lrSubtype)
		dq.LRSubtype = &ls
	}
	if rcSubtype != nil {
		rs := models.RCSubtype(*rcSubtype)
		dq.RCSubtype = &rs
	}

	// Then fetch all choices for that question
	choiceRows, err := s.db.Query(
		`SELECT choice_id, choice_text FROM answer_choices WHERE question_id = $1 ORDER BY choice_id`, id)
	if err != nil {
		return nil, fmt.Errorf("get choices: %w", err)
	}
	defer choiceRows.Close()

	for choiceRows.Next() {
		var choiceID, choiceText string
		if err := choiceRows.Scan(&choiceID, &choiceText); err != nil {
			return nil, err
		}
		dq.Choices = append(dq.Choices, models.DrillChoice{
			ChoiceID:   choiceID,
			ChoiceText: choiceText,
		})
	}

	return dq, choiceRows.Err()
}

func (s *Store) GetAdaptiveQuestions(userID int64, section string, subtype *string, minDiff, maxDiff, count int, excludeIDs []int64) ([]models.DrillQuestion, error) {
	args := []interface{}{userID, section, minDiff, maxDiff}
	paramIdx := 5

	var filterClauses []string

	if subtype != nil {
		if strings.HasPrefix(*subtype, "rc_") {
			filterClauses = append(filterClauses, fmt.Sprintf("AND q.rc_subtype = $%d", paramIdx))
		} else {
			filterClauses = append(filterClauses, fmt.Sprintf("AND q.lr_subtype = $%d", paramIdx))
		}
		args = append(args, *subtype)
		paramIdx++
	}

	if len(excludeIDs) > 0 {
		placeholders := make([]string, len(excludeIDs))
		for i, id := range excludeIDs {
			placeholders[i] = fmt.Sprintf("$%d", paramIdx)
			args = append(args, id)
			paramIdx++
		}
		filterClauses = append(filterClauses, fmt.Sprintf("AND q.id NOT IN (%s)", strings.Join(placeholders, ",")))
	}

	extra := strings.Join(filterClauses, " ")

	// First, pick the question IDs
	pickQuery := fmt.Sprintf(`
		SELECT q.id
		FROM questions q
		LEFT JOIN user_question_history h ON h.question_id = q.id AND h.user_id = $1
		WHERE q.section = $2
		  AND q.difficulty_score >= $3
		  AND q.difficulty_score <= $4
		  %s
		  AND q.validation_status IN ('passed', 'unvalidated')
		  AND (q.quality_score >= 0.50 OR q.quality_score IS NULL)
		ORDER BY
		    CASE WHEN h.id IS NULL THEN 0 ELSE 1 END,
		    RANDOM()
		LIMIT %d`, extra, count)

	idRows, err := s.db.Query(pickQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("get adaptive questions: %w", err)
	}
	defer idRows.Close()

	var questionIDs []int64
	for idRows.Next() {
		var id int64
		if err := idRows.Scan(&id); err != nil {
			return nil, err
		}
		questionIDs = append(questionIDs, id)
	}
	if err := idRows.Err(); err != nil {
		return nil, err
	}
	if len(questionIDs) == 0 {
		return nil, nil
	}

	// Then fetch full questions + choices for those IDs
	placeholders := make([]string, len(questionIDs))
	fullArgs := make([]interface{}, len(questionIDs))
	for i, id := range questionIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		fullArgs[i] = id
	}

	fullQuery := fmt.Sprintf(`
		SELECT q.id, q.section, q.lr_subtype, q.rc_subtype, q.difficulty, q.difficulty_score,
		       q.stimulus, q.question_stem,
		       ac.choice_id, ac.choice_text
		FROM questions q
		JOIN answer_choices ac ON ac.question_id = q.id
		WHERE q.id IN (%s)
		ORDER BY q.id, ac.choice_id`, strings.Join(placeholders, ","))

	rows, err := s.db.Query(fullQuery, fullArgs...)
	if err != nil {
		return nil, fmt.Errorf("get adaptive question details: %w", err)
	}
	defer rows.Close()

	return s.scanDrillQuestions(rows, count)
}

func (s *Store) scanDrillQuestions(rows *sql.Rows, maxQuestions int) ([]models.DrillQuestion, error) {
	questionMap := make(map[int64]*models.DrillQuestion)
	var questionOrder []int64

	for rows.Next() {
		var id int64
		var sect, difficulty string
		var lrSubtype, rcSubtype *string
		var diffScore int
		var stimulus, stem string
		var choiceID, choiceText string

		if err := rows.Scan(&id, &sect, &lrSubtype, &rcSubtype, &difficulty, &diffScore,
			&stimulus, &stem, &choiceID, &choiceText); err != nil {
			return nil, fmt.Errorf("scan drill question: %w", err)
		}

		if existing, ok := questionMap[id]; ok {
			existing.Choices = append(existing.Choices, models.DrillChoice{
				ChoiceID:   choiceID,
				ChoiceText: choiceText,
			})
		} else {
			if len(questionMap) >= maxQuestions {
				continue
			}
			dq := &models.DrillQuestion{
				ID:              id,
				Section:         models.Section(sect),
				Difficulty:      models.Difficulty(difficulty),
				DifficultyScore: diffScore,
				Stimulus:        stimulus,
				QuestionStem:    stem,
				Choices: []models.DrillChoice{{
					ChoiceID:   choiceID,
					ChoiceText: choiceText,
				}},
			}
			if lrSubtype != nil {
				ls := models.LRSubtype(*lrSubtype)
				dq.LRSubtype = &ls
			}
			if rcSubtype != nil {
				rs := models.RCSubtype(*rcSubtype)
				dq.RCSubtype = &rs
			}
			questionMap[id] = dq
			questionOrder = append(questionOrder, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	questions := make([]models.DrillQuestion, 0, len(questionOrder))
	for _, id := range questionOrder {
		questions = append(questions, *questionMap[id])
	}
	return questions, nil
}

func (s *Store) CountQuestionsInBucket(section string, subtype *string, minDiff, maxDiff int) (int, error) {
	var count int
	var err error

	baseQuery := `SELECT COUNT(*) FROM questions
		WHERE section = $1
		AND difficulty_score >= %s AND difficulty_score <= %s
		AND validation_status IN ('passed', 'unvalidated')
		AND (quality_score >= 0.50 OR quality_score IS NULL)`

	if subtype != nil {
		if strings.HasPrefix(*subtype, "rc_") {
			err = s.db.QueryRow(
				fmt.Sprintf(`SELECT COUNT(*) FROM questions
				 WHERE section = $1 AND rc_subtype = $2
				 AND difficulty_score >= $3 AND difficulty_score <= $4
				 AND validation_status IN ('passed', 'unvalidated')
				 AND (quality_score >= 0.50 OR quality_score IS NULL)`),
				section, *subtype, minDiff, maxDiff,
			).Scan(&count)
		} else {
			err = s.db.QueryRow(
				fmt.Sprintf(`SELECT COUNT(*) FROM questions
				 WHERE section = $1 AND lr_subtype = $2
				 AND difficulty_score >= $3 AND difficulty_score <= $4
				 AND validation_status IN ('passed', 'unvalidated')
				 AND (quality_score >= 0.50 OR quality_score IS NULL)`),
				section, *subtype, minDiff, maxDiff,
			).Scan(&count)
		}
	} else {
		err = s.db.QueryRow(
			fmt.Sprintf(baseQuery, "$2", "$3"),
			section, minDiff, maxDiff,
		).Scan(&count)
	}
	return count, err
}

// ── Generation Queue ────────────────────────────────────

func (s *Store) UpsertGenerationQueue(section string, subtype *string, minDiff, maxDiff int, targetDiff string, needed int) error {
	var lrSubtype, rcSubtype *string
	if subtype != nil {
		if strings.HasPrefix(*subtype, "rc_") {
			rcSubtype = subtype
		} else {
			lrSubtype = subtype
		}
	}

	_, err := s.db.Exec(
		`INSERT INTO generation_queue (section, lr_subtype, rc_subtype, difficulty_bucket_min, difficulty_bucket_max, target_difficulty, questions_needed)
		 SELECT $1, $2, $3, $4, $5, $6, $7
		 WHERE NOT EXISTS (
		     SELECT 1 FROM generation_queue
		     WHERE section = $1
		     AND lr_subtype IS NOT DISTINCT FROM $2
		     AND rc_subtype IS NOT DISTINCT FROM $3
		     AND difficulty_bucket_min = $4
		     AND difficulty_bucket_max = $5
		     AND status IN ('pending', 'generating')
		 )`,
		section, lrSubtype, rcSubtype, minDiff, maxDiff, targetDiff, needed,
	)
	return err
}

func (s *Store) GetPendingGenerations(limit int) ([]models.GenerationQueueItem, error) {
	rows, err := s.db.Query(
		`SELECT id, section, lr_subtype, rc_subtype,
		        difficulty_bucket_min, difficulty_bucket_max,
		        target_difficulty, status, questions_needed,
		        error_message, created_at, completed_at
		 FROM generation_queue
		 WHERE status = 'pending'
		 ORDER BY created_at ASC
		 LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get pending generations: %w", err)
	}
	defer rows.Close()

	var items []models.GenerationQueueItem
	for rows.Next() {
		var item models.GenerationQueueItem
		if err := rows.Scan(&item.ID, &item.Section, &item.LRSubtype, &item.RCSubtype,
			&item.DifficultyBucketMin, &item.DifficultyBucketMax,
			&item.TargetDifficulty, &item.Status, &item.QuestionsNeeded,
			&item.ErrorMessage, &item.CreatedAt, &item.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan generation queue item: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpdateGenerationStatus(id int64, status string, errMsg *string) error {
	if status == "completed" || status == "failed" {
		_, err := s.db.Exec(
			`UPDATE generation_queue SET status = $1, error_message = $2, completed_at = NOW() WHERE id = $3`,
			status, errMsg, id,
		)
		return err
	}
	_, err := s.db.Exec(
		`UPDATE generation_queue SET status = $1 WHERE id = $2`,
		status, id,
	)
	return err
}

// ── User Settings ───────────────────────────────────────

func (s *Store) GetDifficultySlider(userID int64) (int, error) {
	var slider int
	err := s.db.QueryRow(
		`SELECT difficulty_slider FROM users WHERE id = $1`,
		userID,
	).Scan(&slider)
	return slider, err
}

func (s *Store) SetDifficultySlider(userID int64, value int) error {
	_, err := s.db.Exec(
		`UPDATE users SET difficulty_slider = $1 WHERE id = $2`,
		value, userID,
	)
	return err
}

// ── Admin Queries ───────────────────────────────────────

func (s *Store) GetQualityStats() (*models.QualityStats, error) {
	stats := &models.QualityStats{
		QualityDistribution: make(map[string]int),
	}

	err := s.db.QueryRow(
		`SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE validation_status = 'passed'),
			COUNT(*) FILTER (WHERE validation_status = 'flagged'),
			COUNT(*) FILTER (WHERE validation_status = 'rejected')
		 FROM questions`,
	).Scan(&stats.TotalGenerated, &stats.TotalPassed, &stats.TotalFlagged, &stats.TotalRejected)
	if err != nil {
		return nil, fmt.Errorf("quality stats: %w", err)
	}

	if stats.TotalGenerated > 0 {
		stats.PassRate = float64(stats.TotalPassed) / float64(stats.TotalGenerated)
	}

	// Quality score distribution
	rows, err := s.db.Query(
		`SELECT
			CASE
				WHEN quality_score >= 0.9 THEN '0.9-1.0'
				WHEN quality_score >= 0.8 THEN '0.8-0.9'
				WHEN quality_score >= 0.7 THEN '0.7-0.8'
				ELSE 'below_0.7'
			END as bucket,
			COUNT(*)
		 FROM questions WHERE quality_score IS NOT NULL
		 GROUP BY bucket`,
	)
	if err != nil {
		return nil, fmt.Errorf("quality distribution: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var bucket string
		var count int
		if err := rows.Scan(&bucket, &count); err != nil {
			return nil, err
		}
		stats.QualityDistribution[bucket] = count
	}

	return stats, rows.Err()
}

func (s *Store) GetGenerationStats() (*models.GenerationStats, error) {
	stats := &models.GenerationStats{}

	// Batch counts by time period
	err := s.db.QueryRow(
		`SELECT
			COUNT(*) FILTER (WHERE created_at >= CURRENT_DATE),
			COUNT(*) FILTER (WHERE created_at >= date_trunc('week', CURRENT_DATE)),
			COUNT(*) FILTER (WHERE created_at >= date_trunc('month', CURRENT_DATE))
		 FROM question_batches WHERE status = 'completed'`,
	).Scan(&stats.Batches.Today, &stats.Batches.ThisWeek, &stats.Batches.ThisMonth)
	if err != nil {
		return nil, fmt.Errorf("generation stats batches: %w", err)
	}

	// Token totals
	err = s.db.QueryRow(
		`SELECT
			COALESCE(SUM(prompt_tokens + output_tokens), 0),
			COALESCE(SUM(validation_tokens), 0)
		 FROM question_batches WHERE status = 'completed'`,
	).Scan(&stats.Tokens.GenerationTotal, &stats.Tokens.ValidationTotal)
	if err != nil {
		return nil, fmt.Errorf("generation stats tokens: %w", err)
	}

	// Cost totals
	err = s.db.QueryRow(
		`SELECT
			COALESCE(SUM(total_cost_cents) FILTER (WHERE created_at >= CURRENT_DATE), 0),
			COALESCE(SUM(total_cost_cents) FILTER (WHERE created_at >= date_trunc('week', CURRENT_DATE)), 0),
			COALESCE(SUM(total_cost_cents) FILTER (WHERE created_at >= date_trunc('month', CURRENT_DATE)), 0)
		 FROM question_batches WHERE status = 'completed'`,
	).Scan(&stats.Cost.TodayCents, &stats.Cost.ThisWeekCents, &stats.Cost.ThisMonthCents)
	if err != nil {
		return nil, fmt.Errorf("generation stats cost: %w", err)
	}

	stats.Cost.DailyLimitCents = 1000 // default $10

	return stats, nil
}

func (s *Store) GetFlaggedQuestions(limit, offset int) ([]models.Question, int, error) {
	var total int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM questions WHERE flagged = true OR validation_status = 'flagged'`,
	).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count flagged: %w", err)
	}

	rows, err := s.db.Query(
		`SELECT id, batch_id, section, lr_subtype, rc_subtype, difficulty, difficulty_score,
		        stimulus, question_stem, correct_answer_id, explanation,
		        passage_id, quality_score,
		        validation_status, validation_reasoning, adversarial_score,
		        flagged, times_served, times_correct, created_at
		 FROM questions
		 WHERE flagged = true OR validation_status = 'flagged'
		 ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("get flagged: %w", err)
	}
	defer rows.Close()

	var questions []models.Question
	for rows.Next() {
		var q models.Question
		if err := rows.Scan(&q.ID, &q.BatchID, &q.Section, &q.LRSubtype, &q.RCSubtype,
			&q.Difficulty, &q.DifficultyScore,
			&q.Stimulus, &q.QuestionStem, &q.CorrectAnswerID, &q.Explanation,
			&q.PassageID, &q.QualityScore,
			&q.ValidationStatus, &q.ValidationReasoning, &q.AdversarialScore,
			&q.Flagged, &q.TimesServed, &q.TimesCorrect, &q.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan flagged: %w", err)
		}
		choices, err := s.getChoicesForQuestion(q.ID)
		if err != nil {
			return nil, 0, err
		}
		q.Choices = choices
		questions = append(questions, q)
	}

	return questions, total, rows.Err()
}

func (s *Store) GetRecalibrationCandidates(minResponses int) ([]models.RecalibrationCandidate, error) {
	rows, err := s.db.Query(
		`SELECT id, difficulty, times_served, times_correct
		 FROM questions
		 WHERE times_served >= $1
		 ORDER BY times_served DESC`,
		minResponses,
	)
	if err != nil {
		return nil, fmt.Errorf("recalibration candidates: %w", err)
	}
	defer rows.Close()

	var candidates []models.RecalibrationCandidate
	for rows.Next() {
		var c models.RecalibrationCandidate
		var difficulty string
		if err := rows.Scan(&c.QuestionID, &difficulty, &c.TimesServed, &c.TimesCorrect); err != nil {
			return nil, err
		}
		c.LabeledDifficulty = difficulty
		c.ActualAccuracy = float64(c.TimesCorrect) / float64(c.TimesServed)

		// Determine suggested difficulty based on accuracy
		actualDifficulty := 1.0 - c.ActualAccuracy
		if actualDifficulty < 0.30 {
			c.SuggestedDifficulty = "easy"
		} else if actualDifficulty <= 0.65 {
			c.SuggestedDifficulty = "medium"
		} else {
			c.SuggestedDifficulty = "hard"
		}

		if c.LabeledDifficulty != c.SuggestedDifficulty {
			candidates = append(candidates, c)
		}
	}

	return candidates, rows.Err()
}

func (s *Store) UpdateQuestionDifficulty(questionID int64, difficulty string) error {
	_, err := s.db.Exec(`UPDATE questions SET difficulty = $1 WHERE id = $2`, difficulty, questionID)
	return err
}

func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ── Export/Import ────────────────────────────────────────

type StimulusStemPair struct {
	Stimulus     string
	QuestionStem string
}

type ImportBatchGroup struct {
	Section    models.Section
	LRSubtype  *models.LRSubtype
	Difficulty models.Difficulty
	Passage    *models.ExportPassage
	Questions  []models.ExportQuestion
}

func (s *Store) ExportPassedQuestions() ([]models.ExportQuestion, error) {
	// Step 1: Get all passed question IDs
	idRows, err := s.db.Query(`SELECT id FROM questions WHERE validation_status = 'passed' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("export query ids: %w", err)
	}
	defer idRows.Close()

	var questionIDs []int64
	for idRows.Next() {
		var id int64
		if err := idRows.Scan(&id); err != nil {
			return nil, err
		}
		questionIDs = append(questionIDs, id)
	}
	if err := idRows.Err(); err != nil {
		return nil, err
	}
	if len(questionIDs) == 0 {
		return nil, nil
	}

	// Step 2: Fetch full question data + choices
	placeholders := make([]string, len(questionIDs))
	args := make([]interface{}, len(questionIDs))
	for i, id := range questionIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT q.id, q.section, q.lr_subtype, q.rc_subtype, q.difficulty, q.difficulty_score,
		       q.stimulus, q.question_stem, q.correct_answer_id, q.explanation,
		       q.quality_score, q.validation_status, q.passage_id,
		       ac.choice_id, ac.choice_text, ac.explanation, ac.is_correct,
		       COALESCE(ac.wrong_answer_type, '')
		FROM questions q
		JOIN answer_choices ac ON ac.question_id = q.id
		WHERE q.id IN (%s)
		ORDER BY q.id, ac.choice_id`, strings.Join(placeholders, ","))

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("export query: %w", err)
	}
	defer rows.Close()

	type questionWithPassage struct {
		question  models.ExportQuestion
		passageID *int64
	}

	questionMap := make(map[int64]*questionWithPassage)
	var questionOrder []int64

	for rows.Next() {
		var qID int64
		var sect, difficulty string
		var lrSubtype, rcSubtype *string
		var diffScore int
		var stimulus, stem, correctID, explanation string
		var qualityScore *float64
		var valStatus string
		var passageID *int64
		var choiceID, choiceText, choiceExplanation string
		var isCorrect bool
		var wrongType string

		if err := rows.Scan(&qID, &sect, &lrSubtype, &rcSubtype, &difficulty, &diffScore,
			&stimulus, &stem, &correctID, &explanation,
			&qualityScore, &valStatus, &passageID,
			&choiceID, &choiceText, &choiceExplanation, &isCorrect, &wrongType); err != nil {
			return nil, fmt.Errorf("scan export row: %w", err)
		}

		existing, ok := questionMap[qID]
		if ok {
			existing.question.Choices = append(existing.question.Choices, models.ExportChoice{
				ChoiceID:        choiceID,
				ChoiceText:      choiceText,
				Explanation:     choiceExplanation,
				IsCorrect:       isCorrect,
				WrongAnswerType: wrongType,
			})
		} else {
			eq := models.ExportQuestion{
				Section:          models.Section(sect),
				Difficulty:       models.Difficulty(difficulty),
				DifficultyScore:  diffScore,
				Stimulus:         stimulus,
				QuestionStem:     stem,
				CorrectAnswerID:  correctID,
				Explanation:      explanation,
				QualityScore:     qualityScore,
				ValidationStatus: models.ValidationStatus(valStatus),
				Choices: []models.ExportChoice{{
					ChoiceID:        choiceID,
					ChoiceText:      choiceText,
					Explanation:     choiceExplanation,
					IsCorrect:       isCorrect,
					WrongAnswerType: wrongType,
				}},
			}
			if lrSubtype != nil {
				ls := models.LRSubtype(*lrSubtype)
				eq.LRSubtype = &ls
			}
			if rcSubtype != nil {
				rs := models.RCSubtype(*rcSubtype)
				eq.RCSubtype = &rs
			}
			questionMap[qID] = &questionWithPassage{question: eq, passageID: passageID}
			questionOrder = append(questionOrder, qID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Step 3: Fetch passages for RC questions
	passageIDs := make(map[int64]bool)
	for _, qp := range questionMap {
		if qp.passageID != nil {
			passageIDs[*qp.passageID] = true
		}
	}

	if len(passageIDs) > 0 {
		pPlaceholders := make([]string, 0, len(passageIDs))
		pArgs := make([]interface{}, 0, len(passageIDs))
		i := 1
		for pid := range passageIDs {
			pPlaceholders = append(pPlaceholders, fmt.Sprintf("$%d", i))
			pArgs = append(pArgs, pid)
			i++
		}

		pQuery := fmt.Sprintf(`SELECT id, title, subject_area, content, is_comparative, COALESCE(passage_b, '')
			FROM rc_passages WHERE id IN (%s)`, strings.Join(pPlaceholders, ","))

		pRows, err := s.db.Query(pQuery, pArgs...)
		if err != nil {
			return nil, fmt.Errorf("export passages: %w", err)
		}
		defer pRows.Close()

		passageMap := make(map[int64]*models.ExportPassage)
		for pRows.Next() {
			var pid int64
			var p models.ExportPassage
			if err := pRows.Scan(&pid, &p.Title, &p.SubjectArea, &p.Content, &p.IsComparative, &p.PassageB); err != nil {
				return nil, fmt.Errorf("scan passage: %w", err)
			}
			passageMap[pid] = &p
		}

		for _, qp := range questionMap {
			if qp.passageID != nil {
				if p, ok := passageMap[*qp.passageID]; ok {
					qp.question.Passage = p
				}
			}
		}
	}

	// Build result in order
	result := make([]models.ExportQuestion, 0, len(questionOrder))
	for _, id := range questionOrder {
		result = append(result, questionMap[id].question)
	}
	return result, nil
}

func (s *Store) CheckExistingQuestions(pairs []StimulusStemPair) (map[string]bool, error) {
	existing := make(map[string]bool)
	for _, p := range pairs {
		var exists bool
		err := s.db.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM questions WHERE stimulus = $1 AND question_stem = $2)`,
			p.Stimulus, p.QuestionStem,
		).Scan(&exists)
		if err != nil {
			return nil, fmt.Errorf("check existing: %w", err)
		}
		if exists {
			existing[p.Stimulus+"||"+p.QuestionStem] = true
		}
	}
	return existing, nil
}

func (s *Store) ImportQuestions(ctx context.Context, groups []ImportBatchGroup) (*models.ImportResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	result := &models.ImportResult{}

	for _, group := range groups {
		// Create batch record
		var batchID int64
		err := tx.QueryRow(
			`INSERT INTO question_batches (section, lr_subtype, difficulty, status, question_count, questions_passed, model_used, completed_at)
			 VALUES ($1, $2, $3, 'completed', $4, $4, 'import', NOW())
			 RETURNING id`,
			group.Section, group.LRSubtype, group.Difficulty, len(group.Questions),
		).Scan(&batchID)
		if err != nil {
			return nil, fmt.Errorf("create import batch: %w", err)
		}

		// Insert RC passage if present
		var passageID *int64
		if group.Passage != nil {
			var pid int64
			subjectArea := group.Passage.SubjectArea
			if subjectArea == "" {
				subjectArea = "law"
			}
			err := tx.QueryRow(
				`INSERT INTO rc_passages (batch_id, title, subject_area, content, is_comparative, passage_b)
				 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
				batchID, group.Passage.Title, subjectArea, group.Passage.Content,
				group.Passage.IsComparative, nullString(group.Passage.PassageB),
			).Scan(&pid)
			if err != nil {
				return nil, fmt.Errorf("insert import passage: %w", err)
			}
			passageID = &pid
		}

		// Insert questions and choices
		for _, q := range group.Questions {
			var questionID int64
			err := tx.QueryRow(
				`INSERT INTO questions
				 (batch_id, section, lr_subtype, rc_subtype, difficulty, difficulty_score,
				  stimulus, question_stem, correct_answer_id, explanation, passage_id,
				  quality_score, validation_status, flagged)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
				 RETURNING id`,
				batchID, q.Section, q.LRSubtype, q.RCSubtype, q.Difficulty, q.DifficultyScore,
				q.Stimulus, q.QuestionStem, q.CorrectAnswerID, q.Explanation,
				passageID, q.QualityScore, q.ValidationStatus, false,
			).Scan(&questionID)
			if err != nil {
				return nil, fmt.Errorf("insert import question: %w", err)
			}

			for _, c := range q.Choices {
				_, err := tx.Exec(
					`INSERT INTO answer_choices (question_id, choice_id, choice_text, explanation, is_correct, wrong_answer_type)
					 VALUES ($1, $2, $3, $4, $5, $6)`,
					questionID, c.ChoiceID, c.ChoiceText, c.Explanation, c.IsCorrect, nullString(c.WrongAnswerType),
				)
				if err != nil {
					return nil, fmt.Errorf("insert import choice: %w", err)
				}
			}

			result.Imported++
		}

		result.BatchesCreated++
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit import: %w", err)
	}

	return result, nil
}
