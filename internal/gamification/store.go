package gamification

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/lsat-prep/backend/internal/models"
)

// ErrAlreadyNudgedToday is returned by SendNudge when the sender has already
// nudged the same receiver during the current UTC day. Callers surface this as
// a 429 rate-limit response.
var ErrAlreadyNudgedToday = errors.New("already nudged this person today")

// GetDrillResults returns, among the given question IDs, how many this user has
// actually answered (total) and how many correctly (correct), read from the
// authoritative answer history. Drill completion uses this instead of the
// client-supplied correct_ids so a client cannot forge a perfect drill.
func (s *Store) GetDrillResults(userID int64, questionIDs []int64) (int, int, error) {
	if len(questionIDs) == 0 {
		return 0, 0, nil
	}
	var total, correct int
	err := s.db.QueryRow(
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE correct)
		 FROM user_question_history
		 WHERE user_id = $1 AND question_id = ANY($2)`,
		userID, pq.Array(questionIDs),
	).Scan(&total, &correct)
	return total, correct, err
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// ── Core Gamification CRUD ──────────────────────────────

func (s *Store) GetOrCreateGamification(userID int64) (*models.UserGamification, error) {
	_, err := s.db.Exec(
		`INSERT INTO user_gamification (user_id) VALUES ($1)
		 ON CONFLICT (user_id) DO NOTHING`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert gamification: %w", err)
	}

	var g models.UserGamification
	err = s.db.QueryRow(
		`SELECT user_id, total_xp, weekly_xp, weekly_xp_reset_at,
		        current_streak, longest_streak, last_active_date,
		        streak_freeze_active, streak_freezes_owned, gems,
		        daily_goal_target, daily_goal_progress, daily_goal_date,
		        league_tier, questions_answered_total, questions_correct_total,
		        drills_completed_total, perfect_drills_total,
		        created_at, updated_at
		 FROM user_gamification WHERE user_id = $1`,
		userID,
	).Scan(&g.UserID, &g.TotalXP, &g.WeeklyXP, &g.WeeklyXPResetAt,
		&g.CurrentStreak, &g.LongestStreak, &g.LastActiveDate,
		&g.StreakFreezeActive, &g.StreakFreezesOwned, &g.Gems,
		&g.DailyGoalTarget, &g.DailyGoalProgress, &g.DailyGoalDate,
		&g.LeagueTier, &g.QuestionsAnsweredTotal, &g.QuestionsCorrectTotal,
		&g.DrillsCompletedTotal, &g.PerfectDrillsTotal,
		&g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get gamification: %w", err)
	}
	return &g, nil
}

// UpdateGamification persists the streak and daily-goal fields, which are
// read-modify-write state owned by UpdateStreak / UpdateDailyGoal.
//
// It deliberately does NOT write the columns that have a competing atomic
// writer — total_xp/weekly_xp (AddXP/ResetWeeklyXP), gems (AwardGems/
// BuyStreakFreeze), questions_*_total (IncrementCounters), drills_*_total
// (IncrementDrillCounters) and league_tier (ProcessLeagueChanges). Writing
// those from an in-memory snapshot read earlier would clobber a concurrent
// atomic increment (the caller's struct is stale by the time this runs); that
// bug was silently erasing all drill-completion XP on every drill.
//
// NOTE: streak_freezes_owned/streak_freeze_active are still written here from a
// snapshot and also mutated atomically by BuyStreakFreeze. That residual
// read-modify-write is safe on a single instance but should move to a
// SELECT ... FOR UPDATE transaction if this service is ever scaled out.
func (s *Store) UpdateGamification(userID int64, g *models.UserGamification) error {
	_, err := s.db.Exec(
		`UPDATE user_gamification SET
		    current_streak = $2, longest_streak = $3, last_active_date = $4,
		    streak_freeze_active = $5, streak_freezes_owned = $6,
		    daily_goal_target = $7, daily_goal_progress = $8, daily_goal_date = $9,
		    updated_at = NOW()
		 WHERE user_id = $1`,
		userID,
		g.CurrentStreak, g.LongestStreak, g.LastActiveDate,
		g.StreakFreezeActive, g.StreakFreezesOwned,
		g.DailyGoalTarget, g.DailyGoalProgress, g.DailyGoalDate,
	)
	return err
}

// IncrementDrillCounters atomically bumps the drill counters and returns the
// new drills_completed_total (used to detect the first-ever drill).
func (s *Store) IncrementDrillCounters(userID int64, perfect bool) (int, error) {
	perfectInc := 0
	if perfect {
		perfectInc = 1
	}
	var drillsCompleted int
	err := s.db.QueryRow(
		`UPDATE user_gamification SET
		    drills_completed_total = drills_completed_total + 1,
		    perfect_drills_total = perfect_drills_total + $2,
		    updated_at = NOW()
		 WHERE user_id = $1
		 RETURNING drills_completed_total`,
		userID, perfectInc,
	).Scan(&drillsCompleted)
	return drillsCompleted, err
}

func (s *Store) IncrementCounters(userID int64, correct bool) error {
	correctInc := 0
	if correct {
		correctInc = 1
	}
	_, err := s.db.Exec(
		`UPDATE user_gamification SET
		    questions_answered_total = questions_answered_total + 1,
		    questions_correct_total = questions_correct_total + $2,
		    updated_at = NOW()
		 WHERE user_id = $1`,
		userID, correctInc,
	)
	return err
}

// ── XP Operations ───────────────────────────────────────

func (s *Store) AddXP(userID int64, amount int) error {
	_, err := s.db.Exec(
		`UPDATE user_gamification SET
		    total_xp = total_xp + $2,
		    weekly_xp = weekly_xp + $2,
		    updated_at = NOW()
		 WHERE user_id = $1`,
		userID, amount,
	)
	return err
}

func (s *Store) LogXPEvent(userID int64, eventType string, xpAmount int, metadata map[string]interface{}) error {
	var metaJSON *string
	if metadata != nil {
		b, err := json.Marshal(metadata)
		if err == nil {
			s := string(b)
			metaJSON = &s
		}
	}
	_, err := s.db.Exec(
		`INSERT INTO xp_events (user_id, event_type, xp_amount, metadata)
		 VALUES ($1, $2, $3, $4)`,
		userID, eventType, xpAmount, metaJSON,
	)
	return err
}

// ── Leaderboard ─────────────────────────────────────────

func (s *Store) GetGlobalLeaderboard(limit int) ([]models.LeaderboardEntry, error) {
	rows, err := s.db.Query(
		`SELECT u.id, u.name, COALESCE(u.username, ''), g.weekly_xp, g.league_tier, g.current_streak,
		        ROW_NUMBER() OVER (ORDER BY g.weekly_xp DESC) as rank
		 FROM user_gamification g
		 JOIN users u ON u.id = g.user_id
		 WHERE g.weekly_xp > 0
		 ORDER BY g.weekly_xp DESC
		 LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get global leaderboard: %w", err)
	}
	defer rows.Close()

	return scanLeaderboard(rows)
}

func (s *Store) GetFriendsLeaderboard(userID int64) ([]models.LeaderboardEntry, error) {
	rows, err := s.db.Query(
		`SELECT u.id, u.name, COALESCE(u.username, ''), g.weekly_xp, g.league_tier, g.current_streak,
		        ROW_NUMBER() OVER (ORDER BY g.weekly_xp DESC) as rank
		 FROM user_gamification g
		 JOIN users u ON u.id = g.user_id
		 WHERE g.user_id IN (
		     SELECT friend_id FROM friendships WHERE user_id = $1 AND status = 'accepted'
		     UNION
		     SELECT user_id FROM friendships WHERE friend_id = $1 AND status = 'accepted'
		     UNION
		     SELECT $1
		 )
		 ORDER BY g.weekly_xp DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("get friends leaderboard: %w", err)
	}
	defer rows.Close()

	return scanLeaderboard(rows)
}

func scanLeaderboard(rows *sql.Rows) ([]models.LeaderboardEntry, error) {
	var entries []models.LeaderboardEntry
	for rows.Next() {
		var e models.LeaderboardEntry
		var fullName string
		if err := rows.Scan(&e.UserID, &fullName, &e.Username, &e.WeeklyXP, &e.LeagueTier, &e.CurrentStreak, &e.Rank); err != nil {
			return nil, fmt.Errorf("scan leaderboard entry: %w", err)
		}
		e.DisplayName = formatDisplayName(fullName)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *Store) GetUserRank(userID int64) (int, error) {
	var rank int
	err := s.db.QueryRow(
		`SELECT COALESCE(
		    (SELECT rank FROM (
		        SELECT user_id, ROW_NUMBER() OVER (ORDER BY weekly_xp DESC) as rank
		        FROM user_gamification WHERE weekly_xp > 0
		    ) r WHERE r.user_id = $1),
		    0
		)`,
		userID,
	).Scan(&rank)
	return rank, err
}

func (s *Store) ResetWeeklyXP() error {
	_, err := s.db.Exec(
		`UPDATE user_gamification SET weekly_xp = 0, weekly_xp_reset_at = NOW()`,
	)
	return err
}

// LeagueChange represents a user's league tier change.
type LeagueChange struct {
	UserID  int64
	OldTier string
	NewTier string
}

func (s *Store) ProcessLeagueChanges() ([]LeagueChange, error) {
	rows, err := s.db.Query(
		`SELECT user_id, weekly_xp, league_tier FROM user_gamification`,
	)
	if err != nil {
		return nil, fmt.Errorf("get league data: %w", err)
	}
	defer rows.Close()

	var changes []LeagueChange
	for rows.Next() {
		var userID int64
		var weeklyXP int64
		var tier string
		if err := rows.Scan(&userID, &weeklyXP, &tier); err != nil {
			return nil, err
		}

		newTier := evaluateLeague(tier, weeklyXP)
		if newTier != tier {
			changes = append(changes, LeagueChange{UserID: userID, OldTier: tier, NewTier: newTier})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Apply updates only after the result set is fully read and closed — running
	// writes on the same connection while a query is still streaming rows is
	// fragile, and the write error was previously swallowed.
	for _, c := range changes {
		if _, err := s.db.Exec(
			`UPDATE user_gamification SET league_tier = $1 WHERE user_id = $2`,
			c.NewTier, c.UserID,
		); err != nil {
			return changes, fmt.Errorf("apply league change for user %d: %w", c.UserID, err)
		}
	}
	return changes, nil
}

func evaluateLeague(currentTier string, weeklyXP int64) string {
	switch currentTier {
	case models.LeagueBronze:
		if weeklyXP >= 500 {
			return models.LeagueSilver
		}
	case models.LeagueSilver:
		if weeklyXP >= 1000 {
			return models.LeagueGold
		}
		if weeklyXP < 200 {
			return models.LeagueBronze
		}
	case models.LeagueGold:
		if weeklyXP >= 2000 {
			return models.LeagueDiamond
		}
		if weeklyXP < 500 {
			return models.LeagueSilver
		}
	case models.LeagueDiamond:
		if weeklyXP >= 4000 {
			return models.LeagueObsidian
		}
		if weeklyXP < 1000 {
			return models.LeagueGold
		}
	case models.LeagueObsidian:
		if weeklyXP < 2000 {
			return models.LeagueDiamond
		}
	}
	return currentTier
}

// ── Friends ─────────────────────────────────────────────

func (s *Store) LookupUserByID(userID int64) (int64, string, string, error) {
	var id int64
	var name, username string
	err := s.db.QueryRow(`SELECT id, name, COALESCE(username, '') FROM users WHERE id = $1`, userID).Scan(&id, &name, &username)
	if err != nil {
		return 0, "", "", err
	}
	return id, name, username, nil
}

func (s *Store) SendFriendRequest(userID, friendID int64) (int64, error) {
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO friendships (user_id, friend_id, status) VALUES ($1, $2, 'pending')
		 RETURNING id`,
		userID, friendID,
	).Scan(&id)
	return id, err
}

func (s *Store) GetFriendship(friendshipID int64) (*models.Friendship, error) {
	var f models.Friendship
	err := s.db.QueryRow(
		`SELECT id, user_id, friend_id, status, created_at, accepted_at
		 FROM friendships WHERE id = $1`,
		friendshipID,
	).Scan(&f.ID, &f.UserID, &f.FriendID, &f.Status, &f.CreatedAt, &f.AcceptedAt)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *Store) CheckExistingFriendship(userID, friendID int64) (*models.Friendship, error) {
	var f models.Friendship
	err := s.db.QueryRow(
		`SELECT id, user_id, friend_id, status, created_at, accepted_at
		 FROM friendships
		 WHERE (user_id = $1 AND friend_id = $2) OR (user_id = $2 AND friend_id = $1)`,
		userID, friendID,
	).Scan(&f.ID, &f.UserID, &f.FriendID, &f.Status, &f.CreatedAt, &f.AcceptedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *Store) RespondFriendRequest(friendshipID int64, accept bool) error {
	if accept {
		_, err := s.db.Exec(
			`UPDATE friendships SET status = 'accepted', accepted_at = NOW() WHERE id = $1`,
			friendshipID,
		)
		return err
	}
	_, err := s.db.Exec(`DELETE FROM friendships WHERE id = $1`, friendshipID)
	return err
}

func (s *Store) GetFriends(userID int64) (*models.FriendsResponse, error) {
	resp := &models.FriendsResponse{}

	// Accepted friends
	rows, err := s.db.Query(
		`SELECT fs.id, u.id, u.name, COALESCE(u.username, ''), COALESCE(g.weekly_xp, 0), COALESCE(g.current_streak, 0),
		        COALESCE(g.league_tier, 'bronze'), g.last_active_date
		 FROM friendships fs
		 JOIN users u ON u.id = CASE WHEN fs.user_id = $1 THEN fs.friend_id ELSE fs.user_id END
		 LEFT JOIN user_gamification g ON g.user_id = u.id
		 WHERE (fs.user_id = $1 OR fs.friend_id = $1) AND fs.status = 'accepted'
		 ORDER BY COALESCE(g.weekly_xp, 0) DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("get friends: %w", err)
	}
	defer rows.Close()

	today := time.Now().Truncate(24 * time.Hour)
	for rows.Next() {
		var f models.FriendEntry
		var fullName string
		var lastActive *time.Time
		if err := rows.Scan(&f.FriendshipID, &f.UserID, &fullName, &f.Username, &f.WeeklyXP, &f.CurrentStreak, &f.LeagueTier, &lastActive); err != nil {
			return nil, err
		}
		f.DisplayName = formatDisplayName(fullName)
		if lastActive != nil {
			f.LastActiveDate = lastActive.Format("2006-01-02")
			f.IsOnlineToday = lastActive.Truncate(24 * time.Hour).Equal(today)
		}
		resp.Friends = append(resp.Friends, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Pending received
	recvRows, err := s.db.Query(
		`SELECT f.id, f.user_id, u.name, COALESCE(u.username, ''), f.created_at
		 FROM friendships f
		 JOIN users u ON u.id = f.user_id
		 WHERE f.friend_id = $1 AND f.status = 'pending'
		 ORDER BY f.created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("get pending received: %w", err)
	}
	defer recvRows.Close()

	for recvRows.Next() {
		var p models.PendingFriendEntry
		var fullName string
		if err := recvRows.Scan(&p.FriendshipID, &p.UserID, &fullName, &p.Username, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.DisplayName = formatDisplayName(fullName)
		resp.PendingReceived = append(resp.PendingReceived, p)
	}

	// Pending sent
	sentRows, err := s.db.Query(
		`SELECT f.id, f.friend_id, u.name, COALESCE(u.username, ''), f.created_at
		 FROM friendships f
		 JOIN users u ON u.id = f.friend_id
		 WHERE f.user_id = $1 AND f.status = 'pending'
		 ORDER BY f.created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("get pending sent: %w", err)
	}
	defer sentRows.Close()

	for sentRows.Next() {
		var p models.PendingFriendEntry
		var fullName string
		if err := sentRows.Scan(&p.FriendshipID, &p.UserID, &fullName, &p.Username, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.DisplayName = formatDisplayName(fullName)
		resp.PendingSent = append(resp.PendingSent, p)
	}

	// Ensure non-nil slices for JSON
	if resp.Friends == nil {
		resp.Friends = []models.FriendEntry{}
	}
	if resp.PendingReceived == nil {
		resp.PendingReceived = []models.PendingFriendEntry{}
	}
	if resp.PendingSent == nil {
		resp.PendingSent = []models.PendingFriendEntry{}
	}

	return resp, nil
}

func (s *Store) RemoveFriend(friendshipID int64, userID int64) error {
	result, err := s.db.Exec(
		`DELETE FROM friendships WHERE id = $1 AND (user_id = $2 OR friend_id = $2)`,
		friendshipID, userID,
	)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("friendship not found or not authorized")
	}
	return nil
}

func (s *Store) SearchUsers(query string, currentUserID int64) ([]models.UserSearchResult, error) {
	searchPattern := "%" + query + "%"
	rows, err := s.db.Query(
		`SELECT u.id, u.name, COALESCE(u.username, ''), COALESCE(g.league_tier, 'bronze'),
		    CASE
		        WHEN EXISTS(
		            SELECT 1 FROM friendships
		            WHERE ((user_id = $2 AND friend_id = u.id) OR (user_id = u.id AND friend_id = $2))
		            AND status = 'accepted'
		        ) THEN 'friends'
		        WHEN EXISTS(
		            SELECT 1 FROM friendships
		            WHERE user_id = $2 AND friend_id = u.id AND status = 'pending'
		        ) THEN 'pending_sent'
		        WHEN EXISTS(
		            SELECT 1 FROM friendships
		            WHERE user_id = u.id AND friend_id = $2 AND status = 'pending'
		        ) THEN 'pending_received'
		        ELSE 'none'
		    END as relationship_status
		 FROM users u
		 LEFT JOIN user_gamification g ON g.user_id = u.id
		 WHERE u.id != $2 AND (u.name ILIKE $1 OR u.email ILIKE $1 OR COALESCE(u.username, '') ILIKE $1)
		 LIMIT 20`,
		searchPattern, currentUserID,
	)
	if err != nil {
		return nil, fmt.Errorf("search users: %w", err)
	}
	defer rows.Close()

	var results []models.UserSearchResult
	for rows.Next() {
		var r models.UserSearchResult
		var fullName string
		if err := rows.Scan(&r.UserID, &fullName, &r.Username, &r.LeagueTier, &r.RelationshipStatus); err != nil {
			return nil, err
		}
		r.DisplayName = formatDisplayName(fullName)
		results = append(results, r)
	}
	if results == nil {
		results = []models.UserSearchResult{}
	}
	return results, rows.Err()
}

// formatDisplayName converts "John Smith" → "John S."
func formatDisplayName(fullName string) string {
	parts := strings.Fields(fullName)
	if len(parts) <= 1 {
		return fullName
	}
	lastName := parts[len(parts)-1]
	firstRune := []rune(lastName)
	if len(firstRune) > 0 {
		return parts[0] + " " + string(firstRune[0]) + "."
	}
	return parts[0]
}

func (s *Store) CountFriends(userID int64) (int, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM friendships
		 WHERE ((user_id = $1 OR friend_id = $1)) AND status = 'accepted'`,
		userID,
	).Scan(&count)
	return count, err
}

func (s *Store) AreFriends(userID, otherID int64) (bool, error) {
	var exists bool
	err := s.db.QueryRow(
		`SELECT EXISTS(
		    SELECT 1 FROM friendships
		    WHERE ((user_id = $1 AND friend_id = $2) OR (user_id = $2 AND friend_id = $1))
		    AND status = 'accepted'
		)`,
		userID, otherID,
	).Scan(&exists)
	return exists, err
}

// ── Nudges ──────────────────────────────────────────────

func (s *Store) SendNudge(senderID, receiverID int64, nudgeType, message string) (int64, error) {
	// Enforce one nudge per sender→receiver per UTC day. There is no DB unique
	// constraint, so this app-level check is what actually rate-limits nudges
	// (previously the "already nudged today" branch was dead: a plain INSERT
	// never conflicted, so a client could spam nudges). Sufficient on a single
	// instance; a partial unique index would be needed if scaled out.
	var existing int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM nudges
		 WHERE sender_id = $1 AND receiver_id = $2
		   AND (created_at AT TIME ZONE 'UTC')::date = (NOW() AT TIME ZONE 'UTC')::date`,
		senderID, receiverID,
	).Scan(&existing); err != nil {
		return 0, fmt.Errorf("check nudge rate limit: %w", err)
	}
	if existing > 0 {
		return 0, ErrAlreadyNudgedToday
	}

	var msgPtr *string
	if message != "" {
		msgPtr = &message
	}
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO nudges (sender_id, receiver_id, nudge_type, message)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		senderID, receiverID, nudgeType, msgPtr,
	).Scan(&id)
	return id, err
}

func (s *Store) GetUnreadNudges(userID int64) ([]models.NudgeEntry, error) {
	rows, err := s.db.Query(
		`SELECT n.id, u.name, n.sender_id, n.nudge_type, COALESCE(n.message, ''), n.created_at
		 FROM nudges n
		 JOIN users u ON u.id = n.sender_id
		 WHERE n.receiver_id = $1 AND n.read = false
		 ORDER BY n.created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("get unread nudges: %w", err)
	}
	defer rows.Close()

	var nudges []models.NudgeEntry
	for rows.Next() {
		var n models.NudgeEntry
		if err := rows.Scan(&n.ID, &n.SenderName, &n.SenderID, &n.NudgeType, &n.Message, &n.CreatedAt); err != nil {
			return nil, err
		}
		nudges = append(nudges, n)
	}
	if nudges == nil {
		nudges = []models.NudgeEntry{}
	}
	return nudges, rows.Err()
}

func (s *Store) CountUnreadNudges(userID int64) (int, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM nudges WHERE receiver_id = $1 AND read = false`,
		userID,
	).Scan(&count)
	return count, err
}

func (s *Store) MarkNudgeRead(nudgeID, userID int64) error {
	result, err := s.db.Exec(
		`UPDATE nudges SET read = true WHERE id = $1 AND receiver_id = $2`,
		nudgeID, userID,
	)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("nudge not found or not authorized")
	}
	return nil
}

// ── Achievements ────────────────────────────────────────

func (s *Store) GetUserAchievements(userID int64) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT achievement FROM achievements WHERE user_id = $1 ORDER BY earned_at`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("get achievements: %w", err)
	}
	defer rows.Close()

	var achievements []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		achievements = append(achievements, a)
	}
	if achievements == nil {
		achievements = []string{}
	}
	return achievements, rows.Err()
}

// AwardAchievement records an achievement idempotently and reports whether a
// new row was actually inserted (false = the user already had it). Callers use
// the inserted flag to gate one-time gem rewards.
func (s *Store) AwardAchievement(userID int64, achievement string) (bool, error) {
	res, err := s.db.Exec(
		`INSERT INTO achievements (user_id, achievement) VALUES ($1, $2)
		 ON CONFLICT (user_id, achievement) DO NOTHING`,
		userID, achievement,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) AwardGems(userID int64, amount int) error {
	_, err := s.db.Exec(
		`UPDATE user_gamification SET gems = gems + $2, updated_at = NOW() WHERE user_id = $1`,
		userID, amount,
	)
	return err
}

// ── Streak Freeze ───────────────────────────────────────

func (s *Store) BuyStreakFreeze(userID int64) error {
	result, err := s.db.Exec(
		`UPDATE user_gamification
		 SET gems = gems - 50, streak_freezes_owned = streak_freezes_owned + 1, updated_at = NOW()
		 WHERE user_id = $1 AND gems >= 50 AND streak_freezes_owned < 3`,
		userID,
	)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("insufficient gems or max freezes reached")
	}
	return nil
}

// ── Daily Goal ──────────────────────────────────────────

func (s *Store) SetDailyGoalTarget(userID int64, target int) error {
	_, err := s.db.Exec(
		`UPDATE user_gamification SET daily_goal_target = $2, updated_at = NOW() WHERE user_id = $1`,
		userID, target,
	)
	return err
}

// ── Weekly Reset Helpers ────────────────────────────────

func (s *Store) GetAllGamificationForStreakCheck() ([]models.UserGamification, error) {
	rows, err := s.db.Query(
		`SELECT user_id, current_streak, longest_streak, last_active_date,
		        streak_freeze_active, streak_freezes_owned
		 FROM user_gamification
		 WHERE current_streak > 0`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []models.UserGamification
	for rows.Next() {
		var g models.UserGamification
		if err := rows.Scan(&g.UserID, &g.CurrentStreak, &g.LongestStreak,
			&g.LastActiveDate, &g.StreakFreezeActive, &g.StreakFreezesOwned); err != nil {
			return nil, err
		}
		users = append(users, g)
	}
	return users, rows.Err()
}

func (s *Store) UpdateStreakData(userID int64, currentStreak, longestStreak int, freezeActive bool, freezesOwned int) error {
	_, err := s.db.Exec(
		`UPDATE user_gamification SET
		    current_streak = $2, longest_streak = $3,
		    streak_freeze_active = $4, streak_freezes_owned = $5,
		    updated_at = NOW()
		 WHERE user_id = $1`,
		userID, currentStreak, longestStreak, freezeActive, freezesOwned,
	)
	return err
}
