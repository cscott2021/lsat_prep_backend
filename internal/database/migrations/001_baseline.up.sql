-- ============================================================
-- Migration 001: Baseline schema
-- Captures the complete schema as of initial golang-migrate adoption.
-- All statements are idempotent (IF NOT EXISTS / exception blocks)
-- so this is safe to run on existing production databases.
-- ============================================================

-- ============ TABLES ============

CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    email VARCHAR(255) UNIQUE NOT NULL,
    name VARCHAR(255) NOT NULL,
    username VARCHAR(50) UNIQUE,
    password VARCHAR(255) NOT NULL,
    difficulty_slider INT NOT NULL DEFAULT 50,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

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

CREATE TABLE IF NOT EXISTS rc_passages (
    id              BIGSERIAL PRIMARY KEY,
    batch_id        BIGINT NOT NULL REFERENCES question_batches(id),
    title           VARCHAR(255) NOT NULL,
    subject_area    VARCHAR(50) NOT NULL DEFAULT 'law',
    content         TEXT NOT NULL,
    is_comparative  BOOLEAN DEFAULT FALSE,
    passage_b       TEXT,
    word_count      INT,
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS questions (
    id                    BIGSERIAL PRIMARY KEY,
    batch_id              BIGINT NOT NULL REFERENCES question_batches(id),
    section               VARCHAR(50) NOT NULL,
    lr_subtype            VARCHAR(50),
    rc_subtype            VARCHAR(50),
    difficulty            VARCHAR(20) NOT NULL,
    difficulty_score      INT NOT NULL DEFAULT 50 CHECK (difficulty_score >= 0 AND difficulty_score <= 100),
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
    id                  BIGSERIAL PRIMARY KEY,
    user_id             BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    question_id         BIGINT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    answered_at         TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    correct             BOOLEAN NOT NULL,
    selected_choice_id  VARCHAR(5),
    time_spent_seconds  REAL,
    attempt_count       INT NOT NULL DEFAULT 1,
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
    subject_area         VARCHAR(50),
    is_comparative       BOOLEAN DEFAULT FALSE,
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

CREATE TABLE IF NOT EXISTS user_bookmarks (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    question_id BIGINT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    note        TEXT,
    created_at  TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(user_id, question_id)
);

-- ============ ALTER TABLE for columns that may be missing on older DBs ============

ALTER TABLE question_batches ADD COLUMN IF NOT EXISTS questions_passed INT NOT NULL DEFAULT 0;
ALTER TABLE question_batches ADD COLUMN IF NOT EXISTS questions_flagged INT NOT NULL DEFAULT 0;
ALTER TABLE question_batches ADD COLUMN IF NOT EXISTS questions_rejected INT NOT NULL DEFAULT 0;
ALTER TABLE question_batches ADD COLUMN IF NOT EXISTS validation_tokens INT;
ALTER TABLE question_batches ADD COLUMN IF NOT EXISTS total_cost_cents INT;
ALTER TABLE rc_passages ADD COLUMN IF NOT EXISTS subject_area VARCHAR(50) NOT NULL DEFAULT 'law';
ALTER TABLE rc_passages ADD COLUMN IF NOT EXISTS word_count INT;
ALTER TABLE questions ADD COLUMN IF NOT EXISTS validation_status VARCHAR(20) DEFAULT 'unvalidated';
ALTER TABLE questions ADD COLUMN IF NOT EXISTS validation_reasoning TEXT;
ALTER TABLE questions ADD COLUMN IF NOT EXISTS adversarial_score VARCHAR(20);
ALTER TABLE questions ADD COLUMN IF NOT EXISTS difficulty_score INT CHECK (difficulty_score >= 0 AND difficulty_score <= 100);
ALTER TABLE questions ADD COLUMN IF NOT EXISTS rc_subtype VARCHAR(50);
ALTER TABLE answer_choices ADD COLUMN IF NOT EXISTS wrong_answer_type VARCHAR(50);
ALTER TABLE users ADD COLUMN IF NOT EXISTS difficulty_slider INT NOT NULL DEFAULT 50;
ALTER TABLE users ADD COLUMN IF NOT EXISTS username VARCHAR(50) UNIQUE;
ALTER TABLE generation_queue ADD COLUMN IF NOT EXISTS subject_area VARCHAR(50);
ALTER TABLE generation_queue ADD COLUMN IF NOT EXISTS is_comparative BOOLEAN DEFAULT FALSE;
ALTER TABLE user_question_history ADD COLUMN IF NOT EXISTS selected_choice_id VARCHAR(5);
ALTER TABLE user_question_history ADD COLUMN IF NOT EXISTS time_spent_seconds REAL;
ALTER TABLE user_question_history ADD COLUMN IF NOT EXISTS attempt_count INT NOT NULL DEFAULT 1;

-- ============ BACKFILLS ============

-- Backfill difficulty_score from difficulty enum
UPDATE questions SET difficulty_score = CASE
    WHEN difficulty = 'easy' THEN 25
    WHEN difficulty = 'medium' THEN 50
    WHEN difficulty = 'hard' THEN 75
    ELSE 50
END WHERE difficulty_score IS NULL;

-- Set NOT NULL + default on difficulty_score after backfill
DO $$ BEGIN
    ALTER TABLE questions ALTER COLUMN difficulty_score SET NOT NULL;
EXCEPTION WHEN others THEN NULL;
END $$;
ALTER TABLE questions ALTER COLUMN difficulty_score SET DEFAULT 50;

-- Backfill usernames for existing users (pure SQL)
UPDATE users
SET username = left(
    regexp_replace(lower(name), '[^a-z0-9]', '', 'g'),
    12
) || lpad(floor(random() * 10000)::int::text, 4, '0')
WHERE username IS NULL;

-- Handle edge case: users with empty name after stripping non-alphanumeric
UPDATE users
SET username = 'user' || lpad(floor(random() * 10000)::int::text, 4, '0')
WHERE username IS NULL OR username = '' OR username ~ '^\d{4}$';

-- Set NOT NULL on username after backfill
DO $$ BEGIN
    ALTER TABLE users ALTER COLUMN username SET NOT NULL;
EXCEPTION WHEN others THEN NULL;
END $$;

-- Backfill word_count on existing RC passages
UPDATE rc_passages
SET word_count = array_length(regexp_split_to_array(trim(content), '\s+'), 1)
WHERE word_count IS NULL;

-- ============ INDEXES ============

CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_batches_status ON question_batches(status);
CREATE INDEX IF NOT EXISTS idx_batches_section ON question_batches(section, lr_subtype);
CREATE INDEX IF NOT EXISTS idx_questions_batch ON questions(batch_id);
CREATE INDEX IF NOT EXISTS idx_questions_section ON questions(section, lr_subtype, difficulty);
CREATE INDEX IF NOT EXISTS idx_questions_serving ON questions(section, lr_subtype, difficulty, times_served);
CREATE INDEX IF NOT EXISTS idx_questions_validation ON questions(validation_status);
CREATE INDEX IF NOT EXISTS idx_questions_quality ON questions(quality_score) WHERE quality_score IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_questions_adaptive ON questions(section, lr_subtype, difficulty_score);
CREATE INDEX IF NOT EXISTS idx_questions_rc_subtype ON questions(section, rc_subtype, difficulty_score);
CREATE INDEX IF NOT EXISTS idx_questions_passage ON questions(passage_id) WHERE passage_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_choices_question ON answer_choices(question_id);
CREATE INDEX IF NOT EXISTS idx_validation_question ON validation_logs(question_id);
CREATE INDEX IF NOT EXISTS idx_validation_batch ON validation_logs(batch_id, stage);
CREATE INDEX IF NOT EXISTS idx_ability_user ON user_ability_scores(user_id);
CREATE INDEX IF NOT EXISTS idx_ability_lookup ON user_ability_scores(user_id, scope, scope_value);
CREATE INDEX IF NOT EXISTS idx_history_user ON user_question_history(user_id);
CREATE INDEX IF NOT EXISTS idx_history_user_question ON user_question_history(user_id, question_id);
CREATE INDEX IF NOT EXISTS idx_history_user_correct ON user_question_history(user_id, correct);
CREATE INDEX IF NOT EXISTS idx_history_user_date ON user_question_history(user_id, answered_at DESC);
CREATE INDEX IF NOT EXISTS idx_genqueue_status ON generation_queue(status);
CREATE INDEX IF NOT EXISTS idx_genqueue_lookup ON generation_queue(section, lr_subtype, status);
CREATE INDEX IF NOT EXISTS idx_passages_subject ON rc_passages(subject_area);
CREATE INDEX IF NOT EXISTS idx_passages_comparative ON rc_passages(is_comparative);
CREATE INDEX IF NOT EXISTS idx_xp_events_user ON xp_events(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_friends_user ON friendships(user_id, status);
CREATE INDEX IF NOT EXISTS idx_friends_friend ON friendships(friend_id, status);
CREATE INDEX IF NOT EXISTS idx_nudges_receiver ON nudges(receiver_id, read, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_achievements_user ON achievements(user_id);
CREATE INDEX IF NOT EXISTS idx_bookmarks_user ON user_bookmarks(user_id, created_at DESC);
