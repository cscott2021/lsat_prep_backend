package questions

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

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

// signupDays is how many trailing calendar days the signups-over-time series
// covers (last 30 UTC days, inclusive of today).
const signupDays = 30

// zeroFillSignups turns a map of {YYYY-MM-DD (UTC): count} into a continuous,
// oldest-first series of exactly `days` points ending on `now`'s UTC calendar
// day. Days absent from the map are emitted with count 0 so the client always
// gets a gap-free timeline. Pure (no DB) so it is unit-tested directly.
func zeroFillSignups(counts map[string]int, now time.Time, days int) []models.SignupPoint {
	end := now.UTC().Truncate(24 * time.Hour) // midnight UTC of today
	points := make([]models.SignupPoint, 0, days)
	for i := days - 1; i >= 0; i-- {
		day := end.AddDate(0, 0, -i).Format("2006-01-02")
		points = append(points, models.SignupPoint{Date: day, Count: counts[day]})
	}
	return points
}

// GetAdminMetrics computes the admin growth dashboard: signups over the last 30
// UTC days (zero-filled), subscriber counts + an estimated MRR, DAU/WAU/MAU
// engagement, and early-cohort retention. It reads the users, subscriptions,
// plan_prices and user_question_history tables directly.
func (s *Store) GetAdminMetrics() (*models.AdminMetrics, error) {
	m := &models.AdminMetrics{}
	now := time.Now()

	// ── signups_over_time ──
	// Group signups by UTC calendar day over the last 30 days, then zero-fill the
	// gaps in Go so the returned series is continuous. Not index-backed (users is
	// small and there is no functional index on the UTC date of created_at).
	rows, err := s.db.Query(`
		SELECT to_char((created_at AT TIME ZONE 'UTC')::date, 'YYYY-MM-DD'), COUNT(*)
		  FROM users
		 WHERE created_at >= NOW() - INTERVAL '30 days'
		 GROUP BY 1`)
	if err != nil {
		return nil, fmt.Errorf("metrics signups: %w", err)
	}
	defer rows.Close()
	counts := make(map[string]int)
	for rows.Next() {
		var day string
		var c int
		if err := rows.Scan(&day, &c); err != nil {
			return nil, fmt.Errorf("scan signups: %w", err)
		}
		counts[day] = c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	m.SignupsOverTime = zeroFillSignups(counts, now, signupDays)

	// ── subscribers ──
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&m.Subscribers.TotalUsers); err != nil {
		return nil, fmt.Errorf("metrics total users: %w", err)
	}
	if err := s.db.QueryRow(`
		SELECT
		    COUNT(*) FILTER (WHERE status = 'active'),
		    COUNT(*) FILTER (WHERE status = 'trialing'),
		    COUNT(*) FILTER (WHERE status = 'past_due'),
		    COUNT(*) FILTER (WHERE status = 'canceled')
		  FROM subscriptions`).Scan(
		&m.Subscribers.Active, &m.Subscribers.Trialing,
		&m.Subscribers.PastDue, &m.Subscribers.Canceled); err != nil {
		return nil, fmt.Errorf("metrics subscriber counts: %w", err)
	}
	// free is everyone not actively paying or trialing (per the endpoint's spec).
	m.Subscribers.Free = m.Subscribers.TotalUsers - (m.Subscribers.Active + m.Subscribers.Trialing)
	if m.Subscribers.Free < 0 {
		m.Subscribers.Free = 0
	}

	// MRR estimate: sum the monthly-equivalent amount of every ACTIVE subscription.
	// subscriptions.plan holds the plan tier (see billing.planForPrice), so we join
	// plan_prices by tier (its PK) for a clean 1:1 match. Monthly-equivalent =
	// amount / (interval_count * months-per-interval), so annual → amount/12 and
	// quarterly → amount/3. This is an INTERNAL ESTIMATE, not Stripe-authoritative:
	// it values every active sub at the tier's CURRENT listed price (grandfathered
	// subs on an archived price are approximated at the current price) and uses
	// integer-cent division (sub-cent remainders are dropped).
	if err := s.db.QueryRow(`
		SELECT COALESCE(SUM(
		    CASE WHEN pp.interval = 'year'
		         THEN pp.amount / (12 * pp.interval_count)
		         ELSE pp.amount / pp.interval_count END
		), 0)::bigint
		  FROM subscriptions sub
		  JOIN plan_prices pp ON pp.tier = sub.plan
		 WHERE sub.status = 'active'`).Scan(&m.Subscribers.MRRCents); err != nil {
		return nil, fmt.Errorf("metrics mrr: %w", err)
	}
	// Currency comes from plan_prices; default to usd when no prices are configured.
	if err := s.db.QueryRow(
		`SELECT COALESCE((SELECT currency FROM plan_prices ORDER BY updated_at DESC LIMIT 1), 'usd')`,
	).Scan(&m.Subscribers.Currency); err != nil {
		return nil, fmt.Errorf("metrics currency: %w", err)
	}

	// ── engagement ── distinct users answering a question in the last 1/7/30 days.
	// Bounded to 30 days so the FILTERed COUNT(DISTINCT) scans a small slice.
	if err := s.db.QueryRow(`
		SELECT
		    COUNT(DISTINCT user_id) FILTER (WHERE answered_at > NOW() - INTERVAL '1 day'),
		    COUNT(DISTINCT user_id) FILTER (WHERE answered_at > NOW() - INTERVAL '7 days'),
		    COUNT(DISTINCT user_id) FILTER (WHERE answered_at > NOW() - INTERVAL '30 days')
		  FROM user_question_history
		 WHERE answered_at > NOW() - INTERVAL '30 days'`).Scan(
		&m.Engagement.DAU, &m.Engagement.WAU, &m.Engagement.MAU); err != nil {
		return nil, fmt.Errorf("metrics engagement: %w", err)
	}

	// ── retention ──
	// Cohort = users who signed up between 30 and 1 days ago (they have had at
	// least one full day to return). d1 = the fraction of that cohort who answered
	// >=1 question on a UTC calendar day AFTER their signup day; d7 = the fraction
	// who answered on/after signup_day + 7. The per-user EXISTS lookups hit
	// user_question_history by user_id (idx_history_user_date). Empty cohort → 0.
	var cohort, d1, d7 int
	if err := s.db.QueryRow(`
		WITH cohort AS (
		    SELECT id, (created_at AT TIME ZONE 'UTC')::date AS signup_day
		      FROM users
		     WHERE created_at <= NOW() - INTERVAL '1 day'
		       AND created_at >= NOW() - INTERVAL '30 days'
		)
		SELECT
		    COUNT(*),
		    COUNT(*) FILTER (WHERE EXISTS (
		        SELECT 1 FROM user_question_history h
		         WHERE h.user_id = c.id
		           AND (h.answered_at AT TIME ZONE 'UTC')::date > c.signup_day)),
		    COUNT(*) FILTER (WHERE EXISTS (
		        SELECT 1 FROM user_question_history h
		         WHERE h.user_id = c.id
		           AND (h.answered_at AT TIME ZONE 'UTC')::date >= c.signup_day + 7))
		  FROM cohort c`).Scan(&cohort, &d1, &d7); err != nil {
		return nil, fmt.Errorf("metrics retention: %w", err)
	}
	if cohort > 0 {
		m.Retention.D1Pct = float64(d1) / float64(cohort)
		m.Retention.D7Pct = float64(d7) / float64(cohort)
	}

	return m, nil
}
