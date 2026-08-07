package notify

import (
	"database/sql"
	"fmt"
	"time"
)

// Store persists APNs device tokens and serves the daily worker's fan-out
// query. All user-facing writes re-key on the UNIQUE token: a token belongs to
// exactly one app install, so the newest registration always wins.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// DeviceRegistration is one POST /devices payload, already validated.
type DeviceRegistration struct {
	UserID        int64
	Platform      string
	Token         string
	Timezone      string
	StreakOptIn   bool // "Practice reminders" — ThreadStreak + ThreadGoalMet
	ReengageOptIn bool // "Re-engagement"      — ThreadReengage
}

// UpsertToken registers (or refreshes) a device token for a user, recording
// the device-reported IANA timezone and the user's push preferences. Called on
// login/app start, on APNs token refresh, and whenever a notification toggle
// changes.
//
// last_notified_at is cleared ONLY when the token changes hands to a different
// user, so a fresh install isn't throttled by the previous owner's row. It used
// to be cleared unconditionally, which quietly defeated the 1-push-per-device-
// per-day cap: the app re-registers on every dashboard visit and drill
// completion, so the stamp was wiped several times a day.
func (s *Store) UpsertToken(reg DeviceRegistration) error {
	_, err := s.db.Exec(
		`INSERT INTO device_tokens
		     (user_id, platform, token, timezone,
		      push_streak_enabled, push_reengage_enabled, last_seen_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
		 ON CONFLICT (token) DO UPDATE SET
		    user_id               = EXCLUDED.user_id,
		    platform              = EXCLUDED.platform,
		    timezone              = EXCLUDED.timezone,
		    push_streak_enabled   = EXCLUDED.push_streak_enabled,
		    push_reengage_enabled = EXCLUDED.push_reengage_enabled,
		    last_seen_at          = NOW(),
		    last_notified_at      = CASE
		        WHEN device_tokens.user_id IS DISTINCT FROM EXCLUDED.user_id
		        THEN NULL
		        ELSE device_tokens.last_notified_at
		    END,
		    updated_at            = NOW()`,
		reg.UserID, reg.Platform, reg.Token, reg.Timezone,
		reg.StreakOptIn, reg.ReengageOptIn,
	)
	if err != nil {
		return fmt.Errorf("upsert device token: %w", err)
	}
	return nil
}

// DeleteToken unregisters one device (logout). Deleting by token (not by
// user) so signing out of one device doesn't strip push from the user's
// OTHER devices.
func (s *Store) DeleteToken(userID int64, token string) error {
	_, err := s.db.Exec(
		`DELETE FROM device_tokens WHERE token = $1 AND user_id = $2`,
		token, userID,
	)
	return err
}

// DeleteByToken prunes a token APNs declared dead (400/410). No user scoping:
// Apple is authoritative that this token can never deliver again.
func (s *Store) DeleteByToken(token string) error {
	_, err := s.db.Exec(`DELETE FROM device_tokens WHERE token = $1`, token)
	return err
}

// TouchLastSeen refreshes last_seen_at on app start (used for re-engagement
// recency alongside the gamification last_active_date).
func (s *Store) TouchLastSeen(token string) error {
	_, err := s.db.Exec(
		`UPDATE device_tokens SET last_seen_at = NOW() WHERE token = $1`, token,
	)
	return err
}

// MarkNotified stamps the daily-cap timestamp after a successful send.
func (s *Store) MarkNotified(token string, at time.Time) error {
	_, err := s.db.Exec(
		`UPDATE device_tokens SET last_notified_at = $2 WHERE token = $1`,
		token, at,
	)
	return err
}

// PushCandidate is one device joined with its owner's engagement state — the
// input the daily worker's decision logic runs on.
type PushCandidate struct {
	UserID             int64
	Token              string
	Timezone           string
	LastNotifiedAt     *time.Time
	StreakOptIn        bool
	ReengageOptIn      bool
	CurrentStreak      int
	LastActiveDate     *time.Time
	StreakFreezeActive bool
	StreakFreezesOwned int
	DailyGoalTarget    int
	DailyGoalProgress  int
	DailyGoalDate      *time.Time
}

// ListPushCandidates joins every registered device with its owner's
// gamification row. Users with no gamification row yet scan as zero-state.
func (s *Store) ListPushCandidates() ([]PushCandidate, error) {
	rows, err := s.db.Query(
		// A device with BOTH categories off can never produce a push, so skip
		// it in SQL. The per-category check still happens in decidePush, which
		// is where it is unit-tested and where the skip gets logged.
		`SELECT d.user_id, d.token, d.timezone, d.last_notified_at,
		        d.push_streak_enabled, d.push_reengage_enabled,
		        COALESCE(g.current_streak, 0),
		        g.last_active_date,
		        COALESCE(g.streak_freeze_active, FALSE),
		        COALESCE(g.streak_freezes_owned, 0),
		        COALESCE(g.daily_goal_target, 6),
		        COALESCE(g.daily_goal_progress, 0),
		        g.daily_goal_date
		   FROM device_tokens d
		   LEFT JOIN user_gamification g ON g.user_id = d.user_id
		  WHERE d.push_streak_enabled OR d.push_reengage_enabled`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PushCandidate
	for rows.Next() {
		var c PushCandidate
		if err := rows.Scan(
			&c.UserID, &c.Token, &c.Timezone, &c.LastNotifiedAt,
			&c.StreakOptIn, &c.ReengageOptIn,
			&c.CurrentStreak, &c.LastActiveDate, &c.StreakFreezeActive,
			&c.StreakFreezesOwned, &c.DailyGoalTarget, &c.DailyGoalProgress,
			&c.DailyGoalDate,
		); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
