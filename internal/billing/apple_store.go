package billing

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/lsat-prep/backend/internal/models"
)

// Apple-specific Store methods. Kept in their own file so the Stripe surface
// (store.go) is untouched; everything lands in the same subscriptions table
// and the same derived-entitlement model.

// UpsertFromApple writes verified App Store state onto the user's subscription
// row. Mirrors UpsertFromStripe, including the comp guard (a comped user is
// never clobbered). Keyed by user_id; also records the STABLE Apple original
// transaction id so server notifications can find this row later.
func (s *Store) UpsertFromApple(sub *models.Subscription, originalTransactionID string) error {
	_, err := s.db.Exec(
		`INSERT INTO subscriptions
		   (user_id, provider, apple_original_transaction_id, status, plan,
		    price_id, current_period_end, cancel_at_period_end, trial_end, created_at, updated_at)
		 VALUES ($1, 'apple', $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
		 ON CONFLICT (user_id) DO UPDATE SET
		    provider                     = 'apple',
		    apple_original_transaction_id = COALESCE(EXCLUDED.apple_original_transaction_id, subscriptions.apple_original_transaction_id),
		    status                       = EXCLUDED.status,
		    plan                         = EXCLUDED.plan,
		    price_id                     = EXCLUDED.price_id,
		    current_period_end           = EXCLUDED.current_period_end,
		    cancel_at_period_end         = EXCLUDED.cancel_at_period_end,
		    trial_end                    = EXCLUDED.trial_end,
		    updated_at                   = NOW()
		 WHERE subscriptions.provider <> 'comp'`,
		sub.UserID, nullStr(originalTransactionID),
		sub.Status, sub.Plan, sub.PriceID, sub.CurrentPeriodEnd, sub.CancelAtPeriodEnd, sub.TrialEnd,
	)
	if err != nil {
		return fmt.Errorf("upsert apple subscription: %w", err)
	}
	return nil
}

// GetByAppleOriginalTransactionID resolves the local row for an Apple original
// transaction id. Notifications carry only Apple identifiers, so this is how
// renewal/cancel events find their user. Returns (nil, nil) when unknown.
func (s *Store) GetByAppleOriginalTransactionID(originalTransactionID string) (*models.Subscription, error) {
	var sub models.Subscription
	var appleTx sql.NullString
	err := s.db.QueryRow(
		`SELECT user_id, provider, status, plan, price_id, current_period_end,
		        cancel_at_period_end, trial_end, apple_original_transaction_id, created_at, updated_at
		   FROM subscriptions WHERE apple_original_transaction_id = $1`,
		originalTransactionID,
	).Scan(
		&sub.UserID, &sub.Provider, &sub.Status, &sub.Plan, &sub.PriceID,
		&sub.CurrentPeriodEnd, &sub.CancelAtPeriodEnd, &sub.TrialEnd, &appleTx,
		&sub.CreatedAt, &sub.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if appleTx.Valid {
		sub.AppleOriginalTransactionID = appleTx.String
	}
	return &sub, nil
}

// UpdateFromAppleTx applies a partial state change (status and/or cancel flag)
// to the row matching an Apple original transaction id. Only fields with a
// non-empty status / non-nil flag are written; current_period_end is updated
// whenever provided. Returns false when no row matched (unknown transaction).
// The comp guard applies here too: comp rows are never mutated by Apple events.
func (s *Store) UpdateFromAppleTx(originalTransactionID, status string, cancelAtPeriodEnd *bool, periodEnd *time.Time) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE subscriptions SET
		    status               = COALESCE(NULLIF($2, ''), status),
		    cancel_at_period_end = COALESCE($3, cancel_at_period_end),
		    current_period_end   = COALESCE($4, current_period_end),
		    updated_at           = NOW()
		 WHERE apple_original_transaction_id = $1
		   AND provider <> 'comp'`,
		originalTransactionID, status, cancelAtPeriodEnd, periodEnd,
	)
	if err != nil {
		return false, fmt.Errorf("update apple subscription: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// MarkAppleEventProcessed records an App Store notification UUID and reports
// whether it is new (false = duplicate delivery; skip re-processing). Mirrors
// MarkEventProcessed for Stripe.
func (s *Store) MarkAppleEventProcessed(uuid, notifType string) (bool, error) {
	res, err := s.db.Exec(
		`INSERT INTO apple_events (notification_uuid, type, received_at)
		 VALUES ($1, $2, $3) ON CONFLICT (notification_uuid) DO NOTHING`,
		uuid, notifType, time.Now(),
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
