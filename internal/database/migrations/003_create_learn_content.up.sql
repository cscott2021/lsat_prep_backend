-- Learn content for each question subtype
CREATE TABLE learn_guides (
    id              BIGSERIAL PRIMARY KEY,
    section         TEXT        NOT NULL CHECK (section IN ('logical_reasoning', 'reading_comprehension')),
    subtype         TEXT        NOT NULL,
    overview        TEXT        NOT NULL,
    common_stems    TEXT[]      NOT NULL DEFAULT '{}',
    steps           JSONB       NOT NULL DEFAULT '[]',
    tips            TEXT[]      NOT NULL DEFAULT '{}',
    examples        JSONB       NOT NULL DEFAULT '[]',
    display_order   INT         NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (section, subtype)
);

-- Track which guides a user has opened/read
CREATE TABLE user_learn_progress (
    user_id         BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    guide_id        BIGINT      NOT NULL REFERENCES learn_guides(id) ON DELETE CASCADE,
    first_viewed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_viewed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    view_count      INT         NOT NULL DEFAULT 1,

    PRIMARY KEY (user_id, guide_id)
);

CREATE INDEX idx_learn_guides_section ON learn_guides (section);
CREATE INDEX idx_user_learn_progress_user ON user_learn_progress (user_id);
