package billing

import (
	"database/sql"
	"time"

	"github.com/lsat-prep/backend/internal/models"
)

// ── plan_prices (admin-managed current prices) ─────────────

// GetPlanPrices returns the current active price for every configured tier.
func (s *Store) GetPlanPrices() ([]models.PlanPrice, error) {
	rows, err := s.db.Query(
		`SELECT tier, stripe_price_id, stripe_product_id, amount, currency,
		        interval, interval_count, updated_at
		   FROM plan_prices
		  ORDER BY CASE tier WHEN 'monthly' THEN 0 WHEN 'quarterly' THEN 1 ELSE 2 END`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.PlanPrice
	for rows.Next() {
		var p models.PlanPrice
		if err := rows.Scan(&p.Tier, &p.StripePriceID, &p.StripeProductID, &p.Amount,
			&p.Currency, &p.Interval, &p.IntervalCount, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPlanPrice returns one tier's active price, or (nil, nil) if unset.
func (s *Store) GetPlanPrice(tier string) (*models.PlanPrice, error) {
	var p models.PlanPrice
	err := s.db.QueryRow(
		`SELECT tier, stripe_price_id, stripe_product_id, amount, currency,
		        interval, interval_count, updated_at
		   FROM plan_prices WHERE tier = $1`, tier,
	).Scan(&p.Tier, &p.StripePriceID, &p.StripeProductID, &p.Amount,
		&p.Currency, &p.Interval, &p.IntervalCount, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// UpsertPlanPrice sets the active price for a tier (insert or replace).
func (s *Store) UpsertPlanPrice(p models.PlanPrice) error {
	_, err := s.db.Exec(
		`INSERT INTO plan_prices
		   (tier, stripe_price_id, stripe_product_id, amount, currency, interval, interval_count, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7, NOW())
		 ON CONFLICT (tier) DO UPDATE SET
		   stripe_price_id   = EXCLUDED.stripe_price_id,
		   stripe_product_id = EXCLUDED.stripe_product_id,
		   amount            = EXCLUDED.amount,
		   currency          = EXCLUDED.currency,
		   interval          = EXCLUDED.interval,
		   interval_count    = EXCLUDED.interval_count,
		   updated_at        = NOW()`,
		p.Tier, p.StripePriceID, p.StripeProductID, p.Amount, p.Currency, p.Interval, p.IntervalCount,
	)
	return err
}

// IsActivePriceID reports whether priceID is a currently-active plan price (only
// these may back a NEW subscription — the subscribe allowlist).
func (s *Store) IsActivePriceID(priceID string) (bool, error) {
	if priceID == "" {
		return false, nil
	}
	var exists bool
	err := s.db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM plan_prices WHERE stripe_price_id = $1)`, priceID,
	).Scan(&exists)
	return exists, err
}

// TierForPriceID resolves a Stripe price id back to its plan tier, checking the
// current prices AND the change history so a grandfathered subscriber on an old
// (archived) price still maps to the right tier. Returns "" if unknown.
func (s *Store) TierForPriceID(priceID string) (string, error) {
	if priceID == "" {
		return "", nil
	}
	var tier string
	err := s.db.QueryRow(
		`SELECT tier FROM plan_prices WHERE stripe_price_id = $1
		 UNION
		 SELECT tier FROM price_changes WHERE new_price_id = $1 OR old_price_id = $1
		 LIMIT 1`, priceID,
	).Scan(&tier)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return tier, err
}

// ── price_changes (audit trail) ────────────────────────────

// InsertPriceChange records an audited price change and returns its id.
func (s *Store) InsertPriceChange(pc models.PriceChange) (int64, error) {
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO price_changes
		   (tier, old_amount, new_amount, old_price_id, new_price_id, changed_by,
		    reason, notified_existing, affected_subscribers)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		pc.Tier, pc.OldAmount, pc.NewAmount, nullStr(pc.OldPriceID), pc.NewPriceID,
		pc.ChangedBy, pc.Reason, pc.NotifiedExisting, pc.AffectedSubscribers,
	).Scan(&id)
	return id, err
}

// ListPriceChanges returns the most recent price changes (with the admin's name).
func (s *Store) ListPriceChanges(limit int) ([]models.PriceChange, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT pc.id, pc.tier, pc.old_amount, pc.new_amount,
		        COALESCE(pc.old_price_id,''), pc.new_price_id, pc.changed_by,
		        COALESCE(u.name,''), pc.reason, pc.notified_existing,
		        pc.affected_subscribers, pc.created_at
		   FROM price_changes pc
		   LEFT JOIN users u ON u.id = pc.changed_by
		  ORDER BY pc.created_at DESC LIMIT $1`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.PriceChange
	for rows.Next() {
		var c models.PriceChange
		if err := rows.Scan(&c.ID, &c.Tier, &c.OldAmount, &c.NewAmount, &c.OldPriceID,
			&c.NewPriceID, &c.ChangedBy, &c.ChangedByName, &c.Reason,
			&c.NotifiedExisting, &c.AffectedSubscribers, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ── subscriber lookup (for notifications) ──────────────────

// Contact is a minimal user contact record for notifications.
type Contact struct {
	UserID int64
	Email  string
	Name   string
}

// ActiveSubscribersForTier returns paying subscribers currently on the given tier
// (status trialing/active/past_due, real Stripe provider) so they can be notified
// of a change to that tier.
func (s *Store) ActiveSubscribersForTier(tier string) ([]Contact, error) {
	rows, err := s.db.Query(
		`SELECT u.id, u.email, u.name
		   FROM subscriptions sub JOIN users u ON u.id = sub.user_id
		  WHERE sub.provider = 'stripe'
		    AND sub.plan = $1
		    AND sub.status IN ('trialing','active','past_due')`, tier,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Contact
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.UserID, &c.Email, &c.Name); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ── email_outbox ───────────────────────────────────────────

// QueueEmail enqueues a transactional email for the sender worker.
func (s *Store) QueueEmail(toEmail string, toUserID int64, subject, body, category string) error {
	_, err := s.db.Exec(
		`INSERT INTO email_outbox (to_email, to_user_id, subject, body, category)
		 VALUES ($1,$2,$3,$4,$5)`,
		toEmail, toUserID, subject, body, category,
	)
	return err
}

// QueuedEmail is one outbox row awaiting send.
type QueuedEmail struct {
	ID      int64
	To      string
	Subject string
	Body    string
}

// NextQueuedEmails returns up to limit queued emails for the sender worker.
func (s *Store) NextQueuedEmails(limit int) ([]QueuedEmail, error) {
	rows, err := s.db.Query(
		`SELECT id, to_email, subject, body FROM email_outbox
		  WHERE status = 'queued' ORDER BY created_at LIMIT $1`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QueuedEmail
	for rows.Next() {
		var e QueuedEmail
		if err := rows.Scan(&e.ID, &e.To, &e.Subject, &e.Body); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) MarkEmailSent(id int64) error {
	_, err := s.db.Exec(
		`UPDATE email_outbox SET status='sent', sent_at=$1 WHERE id=$2`, time.Now(), id)
	return err
}

func (s *Store) MarkEmailStatus(id int64, status, errMsg string) error {
	_, err := s.db.Exec(
		`UPDATE email_outbox SET status=$1, error=$2 WHERE id=$3`, status, errMsg, id)
	return err
}
