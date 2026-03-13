package learn

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/lib/pq"
	"github.com/lsat-prep/backend/internal/models"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) ListGuides(userID int64, section models.Section) ([]models.LearnGuideListItem, error) {
	rows, err := s.db.Query(`
		SELECT
			lg.id,
			lg.section,
			lg.subtype,
			lg.overview,
			lg.display_order,
			COALESCE(ulp.view_count, 0) AS view_count
		FROM learn_guides lg
		LEFT JOIN user_learn_progress ulp
			ON ulp.guide_id = lg.id AND ulp.user_id = $1
		WHERE lg.section = $2
		ORDER BY lg.display_order ASC
	`, userID, string(section))
	if err != nil {
		return nil, fmt.Errorf("list guides: %w", err)
	}
	defer rows.Close()

	var items []models.LearnGuideListItem
	for rows.Next() {
		var item models.LearnGuideListItem
		if err := rows.Scan(
			&item.ID,
			&item.Section,
			&item.Subtype,
			&item.Overview,
			&item.DisplayOrder,
			&item.ViewCount,
		); err != nil {
			return nil, fmt.Errorf("scan guide list item: %w", err)
		}
		item.Viewed = item.ViewCount > 0
		items = append(items, item)
	}
	if items == nil {
		items = []models.LearnGuideListItem{}
	}
	return items, rows.Err()
}

func (s *Store) GetGuide(guideID int64) (*models.LearnGuide, error) {
	var guide models.LearnGuide
	var stepsJSON []byte
	var examplesJSON []byte

	err := s.db.QueryRow(`
		SELECT id, section, subtype, overview, common_stems, steps, tips, examples,
		       display_order, created_at, updated_at
		FROM learn_guides
		WHERE id = $1
	`, guideID).Scan(
		&guide.ID,
		&guide.Section,
		&guide.Subtype,
		&guide.Overview,
		pq.Array(&guide.CommonStems),
		&stepsJSON,
		pq.Array(&guide.Tips),
		&examplesJSON,
		&guide.DisplayOrder,
		&guide.CreatedAt,
		&guide.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("guide not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get guide: %w", err)
	}

	if err := json.Unmarshal(stepsJSON, &guide.Steps); err != nil {
		return nil, fmt.Errorf("unmarshal steps: %w", err)
	}
	if err := json.Unmarshal(examplesJSON, &guide.Examples); err != nil {
		return nil, fmt.Errorf("unmarshal examples: %w", err)
	}

	return &guide, nil
}

func (s *Store) GetGuideBySubtype(section models.Section, subtype string) (*models.LearnGuide, error) {
	var guide models.LearnGuide
	var stepsJSON []byte
	var examplesJSON []byte

	err := s.db.QueryRow(`
		SELECT id, section, subtype, overview, common_stems, steps, tips, examples,
		       display_order, created_at, updated_at
		FROM learn_guides
		WHERE section = $1 AND subtype = $2
	`, string(section), subtype).Scan(
		&guide.ID,
		&guide.Section,
		&guide.Subtype,
		&guide.Overview,
		pq.Array(&guide.CommonStems),
		&stepsJSON,
		pq.Array(&guide.Tips),
		&examplesJSON,
		&guide.DisplayOrder,
		&guide.CreatedAt,
		&guide.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("guide not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get guide by subtype: %w", err)
	}

	if err := json.Unmarshal(stepsJSON, &guide.Steps); err != nil {
		return nil, fmt.Errorf("unmarshal steps: %w", err)
	}
	if err := json.Unmarshal(examplesJSON, &guide.Examples); err != nil {
		return nil, fmt.Errorf("unmarshal examples: %w", err)
	}

	return &guide, nil
}

func (s *Store) RecordView(userID, guideID int64) error {
	_, err := s.db.Exec(`
		INSERT INTO user_learn_progress (user_id, guide_id, first_viewed_at, last_viewed_at, view_count)
		VALUES ($1, $2, NOW(), NOW(), 1)
		ON CONFLICT (user_id, guide_id)
		DO UPDATE SET
			last_viewed_at = NOW(),
			view_count = user_learn_progress.view_count + 1
	`, userID, guideID)
	if err != nil {
		return fmt.Errorf("record view: %w", err)
	}
	return nil
}

func (s *Store) GetUserProgress(userID, guideID int64) (*models.UserLearnProgress, error) {
	var p models.UserLearnProgress
	err := s.db.QueryRow(`
		SELECT user_id, guide_id, first_viewed_at, last_viewed_at, view_count
		FROM user_learn_progress
		WHERE user_id = $1 AND guide_id = $2
	`, userID, guideID).Scan(
		&p.UserID, &p.GuideID, &p.FirstViewedAt, &p.LastViewedAt, &p.ViewCount,
	)
	if err == sql.ErrNoRows {
		return nil, nil // Not viewed yet, not an error
	}
	if err != nil {
		return nil, fmt.Errorf("get user progress: %w", err)
	}
	return &p, nil
}
