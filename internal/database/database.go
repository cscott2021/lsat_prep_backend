package database

import (
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

func Connect() (*sql.DB, error) {
	host := getEnv("DB_HOST", "localhost")
	port := getEnv("DB_PORT", "5432")
	user := getEnv("DB_USER", "lsat_user")
	password := getEnv("DB_PASSWORD", "lsat_password")
	dbname := getEnv("DB_NAME", "lsat_prep")
	sslmode := getEnv("DB_SSLMODE", "disable")

	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		host, port, user, password, dbname, sslmode,
	)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)

	return db, nil
}

func Migrate(db *sql.DB) error {
	query := `
	CREATE TABLE IF NOT EXISTS users (
		id BIGSERIAL PRIMARY KEY,
		email VARCHAR(255) UNIQUE NOT NULL,
		name VARCHAR(255) NOT NULL,
		username VARCHAR(50) UNIQUE,
		password VARCHAR(255) NOT NULL,
		created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
		updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);

	CREATE TABLE IF NOT EXISTS question_batches (
		id                BIGSERIAL PRIMARY KEY,
		section           VARCHAR(50) NOT NULL,
		lr_subtype        VARCHAR(50),
		difficulty        VARCHAR(20) NOT NULL,
		status            VARCHAR(20) NOT NULL DEFAULT 'pending',
		question_count    INT NOT NULL DEFAULT 0,
		questions_passed  INT NOT NULL DEFAULT 0,
		questions_flagged INT NOT NULL DEFAULT 0,
		questions_rejected INT NOT NULL DEFAULT 0,
		model_used        VARCHAR(100),
		prompt_tokens     INT,
		output_tokens     INT,
		validation_tokens INT,
		generation_time_ms INT,
		total_cost_cents  INT,
		error_message     TEXT,
		created_at        TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
		completed_at      TIMESTAMP WITH TIME ZONE
	);

	CREATE INDEX IF NOT EXISTS idx_batches_status ON question_batches(status);
	CREATE INDEX IF NOT EXISTS idx_batches_section ON question_batches(section, lr_subtype);

	CREATE TABLE IF NOT EXISTS rc_passages (
		id              BIGSERIAL PRIMARY KEY,
		batch_id        BIGINT NOT NULL REFERENCES question_batches(id),
		title           VARCHAR(255) NOT NULL,
		subject_area    VARCHAR(50) NOT NULL DEFAULT 'law',
		content         TEXT NOT NULL,
		is_comparative  BOOLEAN DEFAULT FALSE,
		passage_b       TEXT,
		created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS questions (
		id                    BIGSERIAL PRIMARY KEY,
		batch_id              BIGINT NOT NULL REFERENCES question_batches(id),
		section               VARCHAR(50) NOT NULL,
		lr_subtype            VARCHAR(50),
		difficulty            VARCHAR(20) NOT NULL,
		stimulus              TEXT NOT NULL,
		question_stem         TEXT NOT NULL,
		correct_answer_id     VARCHAR(1) NOT NULL,
		explanation           TEXT NOT NULL,
		passage_id            BIGINT REFERENCES rc_passages(id),
		quality_score         DECIMAL(3,2),
		validation_status     VARCHAR(20) DEFAULT 'unvalidated',
		validation_reasoning  TEXT,
		adversarial_score     VARCHAR(20),
		flagged               BOOLEAN DEFAULT FALSE,
		times_served          INT DEFAULT 0,
		times_correct         INT DEFAULT 0,
		created_at            TIMESTAMP WITH TIME ZONE DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_questions_batch ON questions(batch_id);
	CREATE INDEX IF NOT EXISTS idx_questions_section ON questions(section, lr_subtype, difficulty);
	CREATE INDEX IF NOT EXISTS idx_questions_serving ON questions(section, lr_subtype, difficulty, times_served);

	CREATE TABLE IF NOT EXISTS answer_choices (
		id              BIGSERIAL PRIMARY KEY,
		question_id     BIGINT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
		choice_id       VARCHAR(1) NOT NULL,
		choice_text     TEXT NOT NULL,
		explanation     TEXT NOT NULL,
		is_correct      BOOLEAN NOT NULL DEFAULT FALSE,
		wrong_answer_type VARCHAR(50),
		created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
		UNIQUE(question_id, choice_id)
	);

	CREATE INDEX IF NOT EXISTS idx_choices_question ON answer_choices(question_id);

	CREATE TABLE IF NOT EXISTS validation_logs (
		id                    BIGSERIAL PRIMARY KEY,
		question_id           BIGINT REFERENCES questions(id),
		batch_id              BIGINT REFERENCES question_batches(id),
		stage                 VARCHAR(20) NOT NULL,
		model_used            VARCHAR(100),
		generated_answer      VARCHAR(1),
		validator_answer      VARCHAR(1),
		matches               BOOLEAN,
		confidence            VARCHAR(20),
		reasoning             TEXT,
		adversarial_details   JSONB,
		prompt_tokens         INT,
		output_tokens         INT,
		created_at            TIMESTAMP WITH TIME ZONE DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_validation_question ON validation_logs(question_id);
	CREATE INDEX IF NOT EXISTS idx_validation_batch ON validation_logs(batch_id, stage);

	CREATE TABLE IF NOT EXISTS user_ability_scores (
		id              BIGSERIAL PRIMARY KEY,
		user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		scope           VARCHAR(20) NOT NULL,
		scope_value     VARCHAR(100),
		ability_score   INT NOT NULL DEFAULT 50 CHECK (ability_score >= 0 AND ability_score <= 100),
		questions_answered INT NOT NULL DEFAULT 0,
		questions_correct  INT NOT NULL DEFAULT 0,
		last_updated    TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
		UNIQUE(user_id, scope, scope_value)
	);

	CREATE TABLE IF NOT EXISTS user_question_history (
		id          BIGSERIAL PRIMARY KEY,
		user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		question_id BIGINT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
		answered_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
		correct     BOOLEAN NOT NULL,
		UNIQUE(user_id, question_id)
	);

	CREATE TABLE IF NOT EXISTS generation_queue (
		id                   BIGSERIAL PRIMARY KEY,
		section              VARCHAR(50) NOT NULL,
		lr_subtype           VARCHAR(50),
		rc_subtype           VARCHAR(50),
		difficulty_bucket_min INT NOT NULL,
		difficulty_bucket_max INT NOT NULL,
		target_difficulty     VARCHAR(20) NOT NULL,
		status               VARCHAR(20) NOT NULL DEFAULT 'pending',
		questions_needed     INT NOT NULL DEFAULT 6,
		error_message        TEXT,
		created_at           TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
		completed_at         TIMESTAMP WITH TIME ZONE
	);

	CREATE TABLE IF NOT EXISTS user_gamification (
		user_id              BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		total_xp             BIGINT NOT NULL DEFAULT 0,
		weekly_xp            BIGINT NOT NULL DEFAULT 0,
		weekly_xp_reset_at   TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
		current_streak       INT NOT NULL DEFAULT 0,
		longest_streak       INT NOT NULL DEFAULT 0,
		last_active_date     DATE,
		streak_freeze_active BOOLEAN NOT NULL DEFAULT FALSE,
		streak_freezes_owned INT NOT NULL DEFAULT 0,
		gems                 INT NOT NULL DEFAULT 0,
		daily_goal_target    INT NOT NULL DEFAULT 6,
		daily_goal_progress  INT NOT NULL DEFAULT 0,
		daily_goal_date      DATE DEFAULT CURRENT_DATE,
		league_tier          VARCHAR(20) NOT NULL DEFAULT 'bronze',
		questions_answered_total INT NOT NULL DEFAULT 0,
		questions_correct_total  INT NOT NULL DEFAULT 0,
		drills_completed_total   INT NOT NULL DEFAULT 0,
		perfect_drills_total     INT NOT NULL DEFAULT 0,
		created_at           TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
		updated_at           TIMESTAMP WITH TIME ZONE DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS xp_events (
		id          BIGSERIAL PRIMARY KEY,
		user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		event_type  VARCHAR(50) NOT NULL,
		xp_amount   INT NOT NULL,
		metadata    JSONB,
		created_at  TIMESTAMP WITH TIME ZONE DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS friendships (
		id          BIGSERIAL PRIMARY KEY,
		user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		friend_id   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		status      VARCHAR(20) NOT NULL DEFAULT 'pending',
		created_at  TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
		accepted_at TIMESTAMP WITH TIME ZONE,
		UNIQUE(user_id, friend_id),
		CHECK(user_id != friend_id)
	);

	CREATE TABLE IF NOT EXISTS nudges (
		id          BIGSERIAL PRIMARY KEY,
		sender_id   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		receiver_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		message     VARCHAR(200),
		nudge_type  VARCHAR(30) NOT NULL DEFAULT 'comeback',
		read        BOOLEAN NOT NULL DEFAULT FALSE,
		created_at  TIMESTAMP WITH TIME ZONE DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS achievements (
		id          BIGSERIAL PRIMARY KEY,
		user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		achievement VARCHAR(100) NOT NULL,
		earned_at   TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
		UNIQUE(user_id, achievement)
	);
	`

	_, err := db.Exec(query)
	if err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}

	// Run ALTER TABLE statements to add columns to existing tables
	// These are idempotent for databases created before this migration
	alterStatements := []string{
		`ALTER TABLE question_batches ADD COLUMN IF NOT EXISTS questions_passed INT NOT NULL DEFAULT 0`,
		`ALTER TABLE question_batches ADD COLUMN IF NOT EXISTS questions_flagged INT NOT NULL DEFAULT 0`,
		`ALTER TABLE question_batches ADD COLUMN IF NOT EXISTS questions_rejected INT NOT NULL DEFAULT 0`,
		`ALTER TABLE question_batches ADD COLUMN IF NOT EXISTS validation_tokens INT`,
		`ALTER TABLE question_batches ADD COLUMN IF NOT EXISTS total_cost_cents INT`,
		`ALTER TABLE rc_passages ADD COLUMN IF NOT EXISTS subject_area VARCHAR(50) NOT NULL DEFAULT 'law'`,
		`ALTER TABLE questions ADD COLUMN IF NOT EXISTS validation_status VARCHAR(20) DEFAULT 'unvalidated'`,
		`ALTER TABLE questions ADD COLUMN IF NOT EXISTS validation_reasoning TEXT`,
		`ALTER TABLE questions ADD COLUMN IF NOT EXISTS adversarial_score VARCHAR(20)`,
		`ALTER TABLE answer_choices ADD COLUMN IF NOT EXISTS wrong_answer_type VARCHAR(50)`,
		// Adaptive system columns
		`ALTER TABLE questions ADD COLUMN IF NOT EXISTS difficulty_score INT CHECK (difficulty_score >= 0 AND difficulty_score <= 100)`,
		`ALTER TABLE questions ADD COLUMN IF NOT EXISTS rc_subtype VARCHAR(50)`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS difficulty_slider INT NOT NULL DEFAULT 50`,
		// Username column
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS username VARCHAR(50) UNIQUE`,
		// RC passage enhancements
		`ALTER TABLE rc_passages ADD COLUMN IF NOT EXISTS word_count INT`,
		`ALTER TABLE generation_queue ADD COLUMN IF NOT EXISTS subject_area VARCHAR(50)`,
		`ALTER TABLE generation_queue ADD COLUMN IF NOT EXISTS is_comparative BOOLEAN DEFAULT FALSE`,
	}

	for _, stmt := range alterStatements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("alter table failed: %w", err)
		}
	}

	// Backfill difficulty_score from existing difficulty enum
	var nullCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM questions WHERE difficulty_score IS NULL`).Scan(&nullCount); err == nil && nullCount > 0 {
		if _, err := db.Exec(`UPDATE questions SET difficulty_score = CASE
			WHEN difficulty = 'easy' THEN 25
			WHEN difficulty = 'medium' THEN 50
			WHEN difficulty = 'hard' THEN 75
			ELSE 50
		END WHERE difficulty_score IS NULL`); err != nil {
			return fmt.Errorf("backfill difficulty_score: %w", err)
		}
	}

	// Set NOT NULL + default on difficulty_score (safe after backfill)
	db.Exec(`DO $$ BEGIN ALTER TABLE questions ALTER COLUMN difficulty_score SET NOT NULL; EXCEPTION WHEN others THEN NULL; END $$`)
	db.Exec(`ALTER TABLE questions ALTER COLUMN difficulty_score SET DEFAULT 50`)

	// Backfill usernames for existing users that don't have one
	var usersWithoutUsername int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE username IS NULL`).Scan(&usersWithoutUsername); err == nil && usersWithoutUsername > 0 {
		// Generate random usernames from lowercase name + random digits
		rows, err := db.Query(`SELECT id, name FROM users WHERE username IS NULL`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id int64
				var name string
				if rows.Scan(&id, &name) == nil {
					base := generateUsernameBase(name)
					// Try up to 10 times with different random suffixes
					for attempt := 0; attempt < 10; attempt++ {
						candidate := fmt.Sprintf("%s%04d", base, randomInt(10000))
						_, err := db.Exec(
							`UPDATE users SET username = $1 WHERE id = $2 AND username IS NULL`,
							candidate, id,
						)
						if err == nil {
							break
						}
					}
				}
			}
		}
	}

	// Set NOT NULL on username (safe after backfill)
	db.Exec(`DO $$ BEGIN ALTER TABLE users ALTER COLUMN username SET NOT NULL; EXCEPTION WHEN others THEN NULL; END $$`)

	// Backfill word_count on existing RC passages
	db.Exec(`UPDATE rc_passages SET word_count = array_length(regexp_split_to_array(trim(content), '\s+'), 1) WHERE word_count IS NULL`)

	// Extend user_question_history with richer tracking columns
	historyAlters := []string{
		`ALTER TABLE user_question_history ADD COLUMN IF NOT EXISTS selected_choice_id VARCHAR(5)`,
		`ALTER TABLE user_question_history ADD COLUMN IF NOT EXISTS time_spent_seconds REAL`,
		`ALTER TABLE user_question_history ADD COLUMN IF NOT EXISTS attempt_count INT NOT NULL DEFAULT 1`,
	}
	for _, stmt := range historyAlters {
		db.Exec(stmt)
	}

	// Bookmarks table
	db.Exec(`CREATE TABLE IF NOT EXISTS user_bookmarks (
		id          BIGSERIAL PRIMARY KEY,
		user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		question_id BIGINT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
		note        TEXT,
		created_at  TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
		UNIQUE(user_id, question_id)
	)`)

	// Create indexes on new columns (must run after ALTER TABLE)
	newIndexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_questions_validation ON questions(validation_status)`,
		`CREATE INDEX IF NOT EXISTS idx_questions_quality ON questions(quality_score) WHERE quality_score IS NOT NULL`,
		// Adaptive system indexes
		`CREATE INDEX IF NOT EXISTS idx_questions_adaptive ON questions(section, lr_subtype, difficulty_score)`,
		`CREATE INDEX IF NOT EXISTS idx_questions_rc_subtype ON questions(section, rc_subtype, difficulty_score)`,
		`CREATE INDEX IF NOT EXISTS idx_ability_user ON user_ability_scores(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ability_lookup ON user_ability_scores(user_id, scope, scope_value)`,
		`CREATE INDEX IF NOT EXISTS idx_history_user ON user_question_history(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_history_user_question ON user_question_history(user_id, question_id)`,
		`CREATE INDEX IF NOT EXISTS idx_genqueue_status ON generation_queue(status)`,
		`CREATE INDEX IF NOT EXISTS idx_genqueue_lookup ON generation_queue(section, lr_subtype, status)`,
		// RC passage indexes
		`CREATE INDEX IF NOT EXISTS idx_questions_passage ON questions(passage_id) WHERE passage_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_passages_subject ON rc_passages(subject_area)`,
		`CREATE INDEX IF NOT EXISTS idx_passages_comparative ON rc_passages(is_comparative)`,
		// Username index (must be after ALTER TABLE adds the column)
		`CREATE INDEX IF NOT EXISTS idx_users_username ON users(username)`,
		// Gamification indexes
		`CREATE INDEX IF NOT EXISTS idx_xp_events_user ON xp_events(user_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_friends_user ON friendships(user_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_friends_friend ON friendships(friend_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_nudges_receiver ON nudges(receiver_id, read, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_achievements_user ON achievements(user_id)`,
		// History & bookmark indexes
		`CREATE INDEX IF NOT EXISTS idx_history_user_correct ON user_question_history(user_id, correct)`,
		`CREATE INDEX IF NOT EXISTS idx_history_user_date ON user_question_history(user_id, answered_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_bookmarks_user ON user_bookmarks(user_id, created_at DESC)`,
	}
	for _, stmt := range newIndexes {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("create index failed: %w", err)
		}
	}

	return nil
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// generateUsernameBase creates a lowercase alphanumeric base from a user's name.
func generateUsernameBase(name string) string {
	var result []byte
	for _, c := range strings.ToLower(name) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			result = append(result, byte(c))
		}
	}
	if len(result) == 0 {
		return "user"
	}
	if len(result) > 12 {
		result = result[:12]
	}
	return string(result)
}

// rng is a seeded random source for username generation.
var rng = rand.New(rand.NewSource(time.Now().UnixNano()))

// randomInt returns a random integer in [0, max).
func randomInt(max int) int {
	return rng.Intn(max)
}

// GenerateUsername creates a unique username from a name by appending random digits.
// It tries up to 10 times to find a unique one. Caller should handle the unique constraint.
func GenerateUsername(name string) string {
	base := generateUsernameBase(name)
	return fmt.Sprintf("%s%04d", base, randomInt(10000))
}
