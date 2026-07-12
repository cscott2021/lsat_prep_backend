package questions

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/lsat-prep/backend/internal/models"
)

// AdminQuestionFilter holds the optional filters for the admin question browser.
type AdminQuestionFilter struct {
	Section    string
	Subtype    string
	Difficulty string
	Status     string
	Flagged    *bool
	Search     string
}

// GetAdminQuestions returns full questions (answers included — this is an
// admin-only path) matching the filter, newest first, with a total count for
// pagination.
func (s *Store) GetAdminQuestions(f AdminQuestionFilter, limit, offset int) ([]models.Question, int, error) {
	var where []string
	var args []interface{}
	if f.Section != "" {
		args = append(args, f.Section)
		where = append(where, fmt.Sprintf("section = $%d", len(args)))
	}
	if f.Subtype != "" {
		args = append(args, f.Subtype)
		where = append(where, fmt.Sprintf("(lr_subtype = $%d OR rc_subtype = $%d)", len(args), len(args)))
	}
	if f.Difficulty != "" {
		args = append(args, f.Difficulty)
		where = append(where, fmt.Sprintf("difficulty = $%d", len(args)))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		where = append(where, fmt.Sprintf("validation_status = $%d", len(args)))
	}
	if f.Flagged != nil {
		args = append(args, *f.Flagged)
		where = append(where, fmt.Sprintf("flagged = $%d", len(args)))
	}
	if f.Search != "" {
		args = append(args, "%"+f.Search+"%")
		where = append(where, fmt.Sprintf("(stimulus ILIKE $%d OR question_stem ILIKE $%d)", len(args), len(args)))
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = "WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM questions "+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count admin questions: %w", err)
	}

	args = append(args, limit, offset)
	query := fmt.Sprintf(`SELECT id, batch_id, section, lr_subtype, rc_subtype, difficulty, difficulty_score,
		    stimulus, question_stem, correct_answer_id, explanation, passage_id, quality_score,
		    validation_status, validation_reasoning, adversarial_score, flagged, times_served, times_correct, created_at
		 FROM questions %s
		 ORDER BY created_at DESC
		 LIMIT $%d OFFSET $%d`, whereSQL, len(args)-1, len(args))

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query admin questions: %w", err)
	}
	defer rows.Close()

	var questions []models.Question
	var ids []int64
	for rows.Next() {
		var q models.Question
		if err := rows.Scan(&q.ID, &q.BatchID, &q.Section, &q.LRSubtype, &q.RCSubtype,
			&q.Difficulty, &q.DifficultyScore, &q.Stimulus, &q.QuestionStem, &q.CorrectAnswerID,
			&q.Explanation, &q.PassageID, &q.QualityScore, &q.ValidationStatus, &q.ValidationReasoning,
			&q.AdversarialScore, &q.Flagged, &q.TimesServed, &q.TimesCorrect, &q.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan admin question: %w", err)
		}
		questions = append(questions, q)
		ids = append(ids, q.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	choiceMap, err := s.loadChoicesForQuestions(ids)
	if err != nil {
		return nil, 0, err
	}
	for i := range questions {
		questions[i].Choices = choiceMap[questions[i].ID]
		if questions[i].Choices == nil {
			questions[i].Choices = []models.AnswerChoice{}
		}
	}
	return questions, total, nil
}

func (s *Store) spreadQuery(query string) ([]models.SpreadItem, error) {
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []models.SpreadItem{}
	for rows.Next() {
		var it models.SpreadItem
		var key sql.NullString
		if err := rows.Scan(&key, &it.Count); err != nil {
			return nil, err
		}
		it.Key = key.String
		if it.Key == "" {
			it.Key = "unknown"
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// GetQuestionSpread returns the composition of the question bank.
func (s *Store) GetQuestionSpread() (*models.QuestionSpread, error) {
	sp := &models.QuestionSpread{}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM questions`).Scan(&sp.Total); err != nil {
		return nil, fmt.Errorf("spread total: %w", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM questions WHERE flagged = true OR validation_status = 'flagged'`).Scan(&sp.Flagged); err != nil {
		return nil, fmt.Errorf("spread flagged: %w", err)
	}
	var err error
	if sp.BySection, err = s.spreadQuery(`SELECT section, COUNT(*) FROM questions GROUP BY section ORDER BY COUNT(*) DESC`); err != nil {
		return nil, fmt.Errorf("spread by section: %w", err)
	}
	if sp.BySubtype, err = s.spreadQuery(`SELECT COALESCE(lr_subtype, rc_subtype, 'none'), COUNT(*) FROM questions GROUP BY COALESCE(lr_subtype, rc_subtype, 'none') ORDER BY COUNT(*) DESC`); err != nil {
		return nil, fmt.Errorf("spread by subtype: %w", err)
	}
	if sp.ByDifficulty, err = s.spreadQuery(`SELECT difficulty, COUNT(*) FROM questions GROUP BY difficulty ORDER BY MIN(difficulty_score)`); err != nil {
		return nil, fmt.Errorf("spread by difficulty: %w", err)
	}
	if sp.ByStatus, err = s.spreadQuery(`SELECT validation_status, COUNT(*) FROM questions GROUP BY validation_status ORDER BY COUNT(*) DESC`); err != nil {
		return nil, fmt.Errorf("spread by status: %w", err)
	}
	return sp, nil
}

// GetUserProgress returns an aggregate summary plus a per-user progress table.
func (s *Store) GetUserProgress(limit, offset int) (*models.UserProgressResponse, error) {
	resp := &models.UserProgressResponse{Users: []models.UserProgressRow{}, PageSize: limit}
	agg := &resp.Aggregate

	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&agg.TotalUsers); err != nil {
		return nil, fmt.Errorf("progress total users: %w", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(DISTINCT user_id) FROM user_question_history WHERE answered_at >= NOW() - INTERVAL '7 days'`).Scan(&agg.ActiveUsers); err != nil {
		return nil, fmt.Errorf("progress active users: %w", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*), COUNT(*) FILTER (WHERE correct) FROM user_question_history`).Scan(&agg.TotalAnswered, &agg.TotalCorrect); err != nil {
		return nil, fmt.Errorf("progress answered: %w", err)
	}
	if agg.TotalAnswered > 0 {
		agg.AvgAccuracy = float64(agg.TotalCorrect) / float64(agg.TotalAnswered)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM questions`).Scan(&agg.QuestionsInBank); err != nil {
		return nil, fmt.Errorf("progress bank: %w", err)
	}

	resp.Total = agg.TotalUsers
	resp.Page = offset/max(limit, 1) + 1

	rows, err := s.db.Query(`
		SELECT u.id, u.name, u.email,
		       COUNT(h.id) AS answered,
		       COUNT(h.id) FILTER (WHERE h.correct) AS correct,
		       COALESCE(g.total_xp, 0), COALESCE(g.current_streak, 0),
		       MAX(h.answered_at) AS last_active
		FROM users u
		LEFT JOIN user_question_history h ON h.user_id = u.id
		LEFT JOIN user_gamification g ON g.user_id = u.id
		GROUP BY u.id, u.name, u.email, g.total_xp, g.current_streak
		ORDER BY answered DESC, u.id
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("progress rows: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r models.UserProgressRow
		var last sql.NullTime
		if err := rows.Scan(&r.UserID, &r.Name, &r.Email, &r.Answered, &r.Correct, &r.TotalXP, &r.CurrentStreak, &last); err != nil {
			return nil, fmt.Errorf("scan progress: %w", err)
		}
		if r.Answered > 0 {
			r.Accuracy = float64(r.Correct) / float64(r.Answered)
		}
		if last.Valid {
			r.LastActive = &last.Time
		}
		resp.Users = append(resp.Users, r)
	}
	return resp, rows.Err()
}
