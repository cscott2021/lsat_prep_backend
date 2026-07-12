package billing

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/lsat-prep/backend/internal/models"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Entitlement is the derived access decision for a user, plus the raw status so
// the paywall can distinguish "no subscription" (402 subscription_required) from
// "payment failed" (402 payment_past_due).
type Entitlement struct {
	Entitled bool
	IsAdmin  bool
	Status   string // subscription status, or 'none' if no row
}

// GetEntitlement resolves whether a user may access gated features. Entitlement
// is DERIVED, never stored: admins are auto-comped, otherwise status must be one
// of EntitledStatuses. A missing subscription row is treated as status 'none'.
func (s *Store) GetEntitlement(userID int64) (Entitlement, error) {
	var isAdmin bool
	var status string
	err := s.db.QueryRow(
		`SELECT u.is_admin, COALESCE(sub.status, 'none')
		   FROM users u
		   LEFT JOIN subscriptions sub ON sub.user_id = u.id
		  WHERE u.id = $1`,
		userID,
	).Scan(&isAdmin, &status)
	if err != nil {
		return Entitlement{}, err
	}

	ent := Entitlement{IsAdmin: isAdmin, Status: status}
	ent.Entitled = isAdmin || models.EntitledStatuses[status]
	return ent, nil
}

// GetUserContact returns the email and name used to create a Stripe customer.
func (s *Store) GetUserContact(userID int64) (email, name string, err error) {
	err = s.db.QueryRow(
		`SELECT email, name FROM users WHERE id = $1`, userID,
	).Scan(&email, &name)
	return email, name, err
}

// GetByUserID returns a user's subscription, or (nil, nil) if none exists.
func (s *Store) GetByUserID(userID int64) (*models.Subscription, error) {
	return s.scanOne(
		`SELECT user_id, provider, COALESCE(stripe_customer_id, ''), COALESCE(stripe_subscription_id, ''),
		        status, plan, price_id, current_period_end, cancel_at_period_end, trial_end, created_at, updated_at
		   FROM subscriptions WHERE user_id = $1`,
		userID,
	)
}

// GetByStripeCustomerID resolves the local subscription row a Stripe customer id
// maps to. Used by webhook handlers that only carry the customer id.
func (s *Store) GetByStripeCustomerID(customerID string) (*models.Subscription, error) {
	return s.scanOne(
		`SELECT user_id, provider, COALESCE(stripe_customer_id, ''), COALESCE(stripe_subscription_id, ''),
		        status, plan, price_id, current_period_end, cancel_at_period_end, trial_end, created_at, updated_at
		   FROM subscriptions WHERE stripe_customer_id = $1`,
		customerID,
	)
}

func (s *Store) scanOne(query string, arg interface{}) (*models.Subscription, error) {
	var sub models.Subscription
	err := s.db.QueryRow(query, arg).Scan(
		&sub.UserID, &sub.Provider, &sub.StripeCustomerID, &sub.StripeSubscriptionID,
		&sub.Status, &sub.Plan, &sub.PriceID, &sub.CurrentPeriodEnd,
		&sub.CancelAtPeriodEnd, &sub.TrialEnd, &sub.CreatedAt, &sub.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

// EnsureCustomer records the Stripe customer id for a user, creating the row if
// needed. It never downgrades an existing status. Returns the current row.
func (s *Store) EnsureCustomer(userID int64, customerID string) (*models.Subscription, error) {
	_, err := s.db.Exec(
		`INSERT INTO subscriptions (user_id, provider, stripe_customer_id, status, updated_at)
		 VALUES ($1, 'stripe', $2, 'none', NOW())
		 ON CONFLICT (user_id) DO UPDATE
		   SET stripe_customer_id = COALESCE(subscriptions.stripe_customer_id, EXCLUDED.stripe_customer_id),
		       updated_at = NOW()`,
		userID, customerID,
	)
	if err != nil {
		return nil, fmt.Errorf("ensure customer: %w", err)
	}
	return s.GetByUserID(userID)
}

// UpsertFromStripe writes the authoritative Stripe subscription state onto the
// local row. Keyed by user_id (each user has at most one subscription).
func (s *Store) UpsertFromStripe(sub *models.Subscription) error {
	_, err := s.db.Exec(
		`INSERT INTO subscriptions
		   (user_id, provider, stripe_customer_id, stripe_subscription_id, status, plan,
		    price_id, current_period_end, cancel_at_period_end, trial_end, created_at, updated_at)
		 VALUES ($1, 'stripe', $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
		 ON CONFLICT (user_id) DO UPDATE SET
		    provider               = 'stripe',
		    stripe_customer_id     = COALESCE(EXCLUDED.stripe_customer_id, subscriptions.stripe_customer_id),
		    stripe_subscription_id = COALESCE(EXCLUDED.stripe_subscription_id, subscriptions.stripe_subscription_id),
		    status                 = EXCLUDED.status,
		    plan                   = EXCLUDED.plan,
		    price_id               = EXCLUDED.price_id,
		    current_period_end     = EXCLUDED.current_period_end,
		    cancel_at_period_end   = EXCLUDED.cancel_at_period_end,
		    trial_end              = EXCLUDED.trial_end,
		    updated_at             = NOW()`,
		sub.UserID, nullStr(sub.StripeCustomerID), nullStr(sub.StripeSubscriptionID),
		sub.Status, sub.Plan, sub.PriceID, sub.CurrentPeriodEnd, sub.CancelAtPeriodEnd, sub.TrialEnd,
	)
	if err != nil {
		return fmt.Errorf("upsert subscription: %w", err)
	}
	return nil
}

// SetCancelAtPeriodEnd flips the local cancel flag (mirrors the Stripe update the
// webhook will also confirm asynchronously).
func (s *Store) SetCancelAtPeriodEnd(userID int64, cancel bool) error {
	_, err := s.db.Exec(
		`UPDATE subscriptions SET cancel_at_period_end = $2, updated_at = NOW() WHERE user_id = $1`,
		userID, cancel,
	)
	return err
}

// GrantComp upserts a manual/admin comp subscription for a user. Comped users
// are entitled indefinitely until revoked.
func (s *Store) GrantComp(userID int64) error {
	_, err := s.db.Exec(
		`INSERT INTO subscriptions (user_id, provider, status, plan, updated_at)
		 VALUES ($1, 'comp', 'comp', 'comp', NOW())
		 ON CONFLICT (user_id) DO UPDATE
		   SET provider = 'comp', status = 'comp', plan = 'comp', updated_at = NOW()`,
		userID,
	)
	return err
}

// RevokeComp removes a comp. It only acts on rows that are actually comps so it
// never clobbers a real Stripe subscription; the row is deleted, returning the
// user to status 'none'.
func (s *Store) RevokeComp(userID int64) error {
	_, err := s.db.Exec(
		`DELETE FROM subscriptions WHERE user_id = $1 AND provider = 'comp'`,
		userID,
	)
	return err
}

// ── Webhook idempotency ───────────────────────────────────

// MarkEventProcessed records a Stripe event id and reports whether it is new.
// Returns (false, nil) if the event was already processed (duplicate delivery).
func (s *Store) MarkEventProcessed(eventID, eventType string) (bool, error) {
	res, err := s.db.Exec(
		`INSERT INTO billing_events (stripe_event_id, type, received_at)
		 VALUES ($1, $2, $3) ON CONFLICT (stripe_event_id) DO NOTHING`,
		eventID, eventType, time.Now(),
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// nullStr converts an empty string to a SQL NULL so the partial unique indexes on
// stripe_customer_id / stripe_subscription_id do not collide on "".
func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
