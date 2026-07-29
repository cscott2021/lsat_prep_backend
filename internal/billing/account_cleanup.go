package billing

import (
	"log"

	stripe "github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/customer"
	"github.com/stripe/stripe-go/v81/subscription"

	"github.com/lsat-prep/backend/internal/models"
)

// CleanupUserBilling performs BEST-EFFORT billing teardown before an account is
// deleted (Apple requires in-app account deletion; leaving a live Stripe
// subscription behind would keep charging a deleted user). It never returns an
// error: teardown failures are logged and the account deletion proceeds — a
// stranded Stripe customer is an ops problem, not a reason to block deletion.
//
// Apple subscriptions cannot be canceled server-side (only the user can, in App
// Store settings), so for provider='apple' this only logs a reminder; the local
// row is removed by the users-table cascade regardless.
func (s *Service) CleanupUserBilling(userID int64) {
	sub, err := s.store.GetByUserID(userID)
	if err != nil {
		log.Printf("[billing] cleanup: load subscription for user %d: %v", userID, err)
		return
	}
	if sub == nil {
		return
	}

	switch sub.Provider {
	case models.ProviderStripe:
		if !s.Enabled() || s.stripe == nil {
			log.Printf("[billing] cleanup: Stripe disabled; customer %s (user %d) left for manual teardown",
				sub.StripeCustomerID, userID)
			return
		}
		if sub.StripeCustomerID == "" {
			return
		}
		if err := s.stripe.DeleteCustomerNow(sub.StripeCustomerID); err != nil {
			log.Printf("[billing] cleanup: delete Stripe customer %s (user %d): %v",
				sub.StripeCustomerID, userID, err)
		}
	case models.ProviderApple:
		log.Printf("[billing] cleanup: user %d had an Apple subscription — it must be canceled by the user in App Store settings (server cannot cancel StoreKit subscriptions)", userID)
	}
}

// DeleteCustomerNow cancels ALL of the customer's subscriptions immediately
// (not at period end — the account is being deleted) and then deletes the
// Stripe customer object, which also detaches any saved payment methods.
func (p *stripeProvider) DeleteCustomerNow(customerID string) error {
	iter := subscription.List(&stripe.SubscriptionListParams{
		Customer: stripe.String(customerID),
		Status:   stripe.String(string(stripe.SubscriptionStatusActive)),
	})
	for iter.Next() {
		if _, err := subscription.Cancel(iter.Subscription().ID, nil); err != nil {
			return err
		}
	}
	if err := iter.Err(); err != nil {
		return err
	}
	// Trialing subs are not returned by status=active.
	trialIter := subscription.List(&stripe.SubscriptionListParams{
		Customer: stripe.String(customerID),
		Status:   stripe.String(string(stripe.SubscriptionStatusTrialing)),
	})
	for trialIter.Next() {
		if _, err := subscription.Cancel(trialIter.Subscription().ID, nil); err != nil {
			return err
		}
	}
	if err := trialIter.Err(); err != nil {
		return err
	}
	_, err := customer.Del(customerID, nil)
	return err
}
