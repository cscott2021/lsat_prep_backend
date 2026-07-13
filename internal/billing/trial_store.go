package billing

import (
	"database/sql"

	"github.com/lsat-prep/backend/internal/models"
)

// CreateTrialOffer inserts an admin-issued free-days promo code.
func (s *Store) CreateTrialOffer(o models.TrialOffer) error {
	var maxR sql.NullInt64
	if o.MaxRedemptions != nil {
		maxR = sql.NullInt64{Int64: int64(*o.MaxRedemptions), Valid: true}
	}
	var exp sql.NullTime
	if o.ExpiresAt != nil {
		exp = sql.NullTime{Time: *o.ExpiresAt, Valid: true}
	}
	_, err := s.db.Exec(
		`INSERT INTO trial_offers (code, free_days, name, max_redemptions, expires_at, active)
		 VALUES ($1,$2,$3,$4,$5,TRUE)`,
		o.Code, o.FreeDays, o.Name, maxR, exp,
	)
	return err
}

// GetRedeemableTrialOffer returns a free-days offer for code only if it is active,
// not expired, and has redemptions remaining. Returns (nil, nil) otherwise.
func (s *Store) GetRedeemableTrialOffer(code string) (*models.TrialOffer, error) {
	if code == "" {
		return nil, nil
	}
	o, err := s.scanTrialOffer(
		`SELECT code, free_days, name, max_redemptions, redeemed_count, expires_at, active, created_at
		   FROM trial_offers
		  WHERE code = $1 AND active = TRUE
		    AND (expires_at IS NULL OR expires_at > NOW())
		    AND (max_redemptions IS NULL OR redeemed_count < max_redemptions)`, code)
	return o, err
}

// IncrementTrialRedemption bumps the redemption count for a used code.
func (s *Store) IncrementTrialRedemption(code string) error {
	_, err := s.db.Exec(
		`UPDATE trial_offers SET redeemed_count = redeemed_count + 1 WHERE code = $1`, code)
	return err
}

// ListTrialOffers returns all free-days offers for the admin UI.
func (s *Store) ListTrialOffers() ([]models.TrialOffer, error) {
	rows, err := s.db.Query(
		`SELECT code, free_days, name, max_redemptions, redeemed_count, expires_at, active, created_at
		   FROM trial_offers ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.TrialOffer
	for rows.Next() {
		o, err := scanTrialOfferRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

func (s *Store) scanTrialOffer(query, arg string) (*models.TrialOffer, error) {
	row := s.db.QueryRow(query, arg)
	o, err := scanTrialOfferRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return o, err
}

// scanner is implemented by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...interface{}) error
}

func scanTrialOfferRow(r scanner) (*models.TrialOffer, error) {
	var o models.TrialOffer
	var maxR sql.NullInt64
	var exp sql.NullTime
	if err := r.Scan(&o.Code, &o.FreeDays, &o.Name, &maxR, &o.RedeemedCount, &exp, &o.Active, &o.CreatedAt); err != nil {
		return nil, err
	}
	if maxR.Valid {
		v := int(maxR.Int64)
		o.MaxRedemptions = &v
	}
	if exp.Valid {
		t := exp.Time.UTC()
		o.ExpiresAt = &t
	}
	return &o, nil
}
