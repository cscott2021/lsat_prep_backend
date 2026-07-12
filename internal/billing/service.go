package billing

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"

	stripe "github.com/stripe/stripe-go/v81"

	"github.com/lsat-prep/backend/internal/models"
)

// ErrBillingDisabled is returned by Stripe-backed operations when billing is not
// configured. Handlers translate it into a 503.
var ErrBillingDisabled = errors.New("billing not configured")

// Service holds billing business logic: it coordinates the local store with the
// Stripe provider. It is safe to construct even when billing is disabled — in
// that case stripe is nil and Stripe-backed methods return ErrBillingDisabled.
type Service struct {
	cfg    Config
	store  *Store
	stripe *stripeProvider
}

// NewService builds the billing service. If cfg.Enabled() is false the Stripe
// provider is not initialized and the server still starts cleanly.
func NewService(cfg Config, store *Store) *Service {
	svc := &Service{cfg: cfg, store: store}
	if cfg.Enabled() {
		svc.stripe = newStripeProvider(cfg)
		log.Printf("[billing] Stripe billing enabled (trial_days=%d)", cfg.TrialDays)
	} else {
		log.Printf("[billing] STRIPE_SECRET_KEY not set — billing disabled; endpoints return 503 and paywall is open")
	}
	return svc
}

func (s *Service) Enabled() bool           { return s.cfg.Enabled() }
func (s *Service) WebhookConfigured() bool { return s.cfg.WebhookConfigured() }
func (s *Service) Config() Config          { return s.cfg }
func (s *Service) Store() *Store           { return s.store }

// GetEntitlement resolves a user's access decision (derived from status/admin).
func (s *Service) GetEntitlement(userID int64) (Entitlement, error) {
	return s.store.GetEntitlement(userID)
}

// BuildConfigResponse assembles GET /billing/config, fetching live price details
// from Stripe for each configured plan.
func (s *Service) BuildConfigResponse() (*models.BillingConfigResponse, error) {
	if !s.Enabled() {
		return nil, ErrBillingDisabled
	}
	resp := &models.BillingConfigResponse{
		PublishableKey: s.cfg.PublishableKey,
		TrialDays:      s.cfg.TrialDays,
		Plans:          []models.BillingPlan{},
	}
	plans := []struct {
		id, name, priceID string
	}{
		{"monthly", "Monthly", s.cfg.PriceMonthly},
		{"annual", "Annual", s.cfg.PriceAnnual},
	}
	for _, pl := range plans {
		if pl.priceID == "" {
			continue
		}
		bp := models.BillingPlan{ID: pl.id, Name: pl.name, PriceID: pl.priceID}
		if pr, err := s.stripe.GetPrice(pl.priceID); err != nil {
			log.Printf("[billing] could not fetch price %s: %v", pl.priceID, err)
		} else {
			bp.Amount = pr.UnitAmount
			bp.Currency = string(pr.Currency)
			if pr.Recurring != nil {
				bp.Interval = string(pr.Recurring.Interval)
			}
		}
		resp.Plans = append(resp.Plans, bp)
	}
	return resp, nil
}

// GetSubscriptionResponse builds GET /billing/subscription, merging the stored
// row with the derived entitlement flag.
func (s *Service) GetSubscriptionResponse(userID int64) (*models.SubscriptionResponse, error) {
	ent, err := s.store.GetEntitlement(userID)
	if err != nil {
		return nil, err
	}
	sub, err := s.store.GetByUserID(userID)
	if err != nil {
		return nil, err
	}
	resp := &models.SubscriptionResponse{
		Status:   models.SubStatusNone,
		Provider: "",
		Entitled: ent.Entitled,
	}
	if ent.IsAdmin && (sub == nil || sub.Status == models.SubStatusNone) {
		// Admins are auto-comped even without a subscription row.
		resp.Status = models.SubStatusComp
		resp.Provider = models.ProviderComp
	}
	if sub != nil {
		resp.Status = sub.Status
		resp.Plan = sub.Plan
		resp.Provider = sub.Provider
		resp.CurrentPeriodEnd = sub.CurrentPeriodEnd
		resp.CancelAtPeriodEnd = sub.CancelAtPeriodEnd
		resp.TrialEnd = sub.TrialEnd
	}
	return resp, nil
}

// Subscribe creates (or reuses) the Stripe customer and starts an incomplete
// subscription, returning the PaymentIntent client secret for the Payment Sheet.
func (s *Service) Subscribe(userID int64, req models.SubscribeRequest) (*models.SubscribeResponse, error) {
	if !s.Enabled() {
		return nil, ErrBillingDisabled
	}
	if req.PriceID == "" {
		return nil, errors.New("price_id is required")
	}

	customerID, err := s.ensureCustomer(userID)
	if err != nil {
		return nil, err
	}

	clientSecret, subID, err := s.stripe.CreateSubscription(customerID, req.PriceID, req.PromotionCode, s.cfg.TrialDays)
	if err != nil {
		return nil, err
	}

	// Persist an initial local mirror; the webhook will reconcile the canonical
	// state shortly after. Fetch the created subscription for accurate fields.
	if full, gerr := s.stripe.GetSubscription(subID); gerr == nil {
		full.UserID = userID
		full.StripeCustomerID = customerID
		if uerr := s.store.UpsertFromStripe(full); uerr != nil {
			log.Printf("[billing] subscribe: local upsert failed for user %d: %v", userID, uerr)
		}
	}

	return &models.SubscribeResponse{ClientSecret: clientSecret, SubscriptionID: subID}, nil
}

// ApplyCoupon previews a promotion code for the client.
func (s *Service) ApplyCoupon(code string) (*models.ApplyCouponResponse, error) {
	if !s.Enabled() {
		return nil, ErrBillingDisabled
	}
	if code == "" {
		return nil, errors.New("code is required")
	}
	resp, err := s.stripe.LookupPromotion(code)
	if errors.Is(err, ErrPromotionNotFound) {
		return &models.ApplyCouponResponse{Valid: false, Description: "Code not found"}, nil
	}
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// Cancel flags the subscription to cancel at period end and returns the updated
// local view.
func (s *Service) Cancel(userID int64) (*models.SubscriptionResponse, error) {
	if !s.Enabled() {
		return nil, ErrBillingDisabled
	}
	sub, err := s.store.GetByUserID(userID)
	if err != nil {
		return nil, err
	}
	if sub == nil || sub.StripeSubscriptionID == "" {
		return nil, errors.New("no active subscription")
	}
	updated, err := s.stripe.CancelAtPeriodEnd(sub.StripeSubscriptionID)
	if err != nil {
		return nil, err
	}
	updated.UserID = userID
	updated.StripeCustomerID = sub.StripeCustomerID
	if err := s.store.UpsertFromStripe(updated); err != nil {
		log.Printf("[billing] cancel: local upsert failed for user %d: %v", userID, err)
	}
	return s.GetSubscriptionResponse(userID)
}

// UpdatePayment returns a SetupIntent client secret for updating the saved card.
func (s *Service) UpdatePayment(userID int64) (*models.UpdatePaymentResponse, error) {
	if !s.Enabled() {
		return nil, ErrBillingDisabled
	}
	customerID, err := s.ensureCustomer(userID)
	if err != nil {
		return nil, err
	}
	cs, err := s.stripe.CreateSetupIntent(customerID)
	if err != nil {
		return nil, err
	}
	return &models.UpdatePaymentResponse{ClientSecret: cs}, nil
}

// Portal opens a Stripe Billing Portal session.
func (s *Service) Portal(userID int64) (*models.PortalResponse, error) {
	if !s.Enabled() {
		return nil, ErrBillingDisabled
	}
	customerID, err := s.ensureCustomer(userID)
	if err != nil {
		return nil, err
	}
	returnURL := s.cfg.AppBaseURL
	if returnURL == "" {
		returnURL = "https://scoreright.app"
	}
	url, err := s.stripe.CreatePortalSession(customerID, returnURL)
	if err != nil {
		return nil, err
	}
	return &models.PortalResponse{URL: url}, nil
}

// ── Admin operations ──────────────────────────────────────

func (s *Service) CreateOffer(req models.CreateCouponRequest) (*models.CreateCouponResponse, error) {
	if !s.Enabled() {
		return nil, ErrBillingDisabled
	}
	return s.stripe.CreateOffer(req)
}

func (s *Service) ListOffers() ([]models.CouponOffer, error) {
	if !s.Enabled() {
		return nil, ErrBillingDisabled
	}
	return s.stripe.ListOffers()
}

// Comp grants or revokes a manual comp for a user. This works even when Stripe is
// disabled — it only touches the local subscriptions table.
func (s *Service) Comp(userID int64, action string) error {
	switch action {
	case "grant":
		return s.store.GrantComp(userID)
	case "revoke":
		return s.store.RevokeComp(userID)
	default:
		return errors.New("action must be 'grant' or 'revoke'")
	}
}

// ensureCustomer returns the user's Stripe customer id, creating the customer and
// persisting the id on first use.
func (s *Service) ensureCustomer(userID int64) (string, error) {
	sub, err := s.store.GetByUserID(userID)
	if err != nil {
		return "", err
	}
	existing := ""
	if sub != nil {
		existing = sub.StripeCustomerID
	}
	email, name, err := s.store.GetUserContact(userID)
	if err != nil {
		return "", fmt.Errorf("load user contact: %w", err)
	}
	customerID, err := s.stripe.EnsureCustomer(existing, email, name, userID)
	if err != nil {
		return "", err
	}
	if existing == "" {
		if _, err := s.store.EnsureCustomer(userID, customerID); err != nil {
			return "", err
		}
	}
	return customerID, nil
}

// ── Webhooks ──────────────────────────────────────────────

// HandleWebhook verifies and processes a Stripe webhook event. It is idempotent:
// a duplicate event id is acknowledged without reprocessing. Returns an error
// only for signature/verification failures the caller should reject.
func (s *Service) HandleWebhook(payload []byte, signature string) error {
	if !s.WebhookConfigured() {
		return ErrBillingDisabled
	}
	event, err := constructEvent(payload, signature, s.cfg.WebhookSecret)
	if err != nil {
		return fmt.Errorf("webhook signature verification failed: %w", err)
	}

	// Idempotency: record the event id first; skip if already seen.
	fresh, err := s.store.MarkEventProcessed(event.ID, string(event.Type))
	if err != nil {
		return fmt.Errorf("record billing event: %w", err)
	}
	if !fresh {
		log.Printf("[billing] duplicate webhook event %s (%s) ignored", event.ID, event.Type)
		return nil
	}

	switch event.Type {
	case "customer.subscription.created",
		"customer.subscription.updated",
		"customer.subscription.deleted":
		return s.applySubscriptionEvent(event)
	case "invoice.paid", "invoice.payment_failed":
		return s.applyInvoiceEvent(event)
	case "checkout.session.completed":
		return s.applyCheckoutEvent(event)
	default:
		log.Printf("[billing] unhandled webhook event type: %s", event.Type)
		return nil
	}
}

// applySubscriptionEvent upserts local state from a subscription-shaped event.
func (s *Service) applySubscriptionEvent(event stripe.Event) error {
	var sub stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
		return fmt.Errorf("unmarshal subscription event: %w", err)
	}
	return s.reconcileMapped(s.stripe.mapSubscription(&sub))
}

// applyInvoiceEvent reconciles from the invoice's subscription id (invoice.paid
// / invoice.payment_failed carry the definitive paid/past_due transition). We
// re-fetch the subscription so the stored state is the current, canonical one.
func (s *Service) applyInvoiceEvent(event stripe.Event) error {
	var inv stripe.Invoice
	if err := json.Unmarshal(event.Data.Raw, &inv); err != nil {
		return fmt.Errorf("unmarshal invoice event: %w", err)
	}
	subID := invoiceSubscriptionID(&inv)
	if subID == "" {
		return nil // not a subscription invoice
	}
	mapped, err := s.stripe.GetSubscription(subID)
	if err != nil {
		return fmt.Errorf("fetch subscription %s: %w", subID, err)
	}
	return s.reconcileMapped(mapped)
}

// applyCheckoutEvent handles checkout.session.completed by reconciling the
// subscription the session created (Payment Sheet is primary; this covers the
// hosted-checkout fallback path).
func (s *Service) applyCheckoutEvent(event stripe.Event) error {
	var sess struct {
		Customer     string `json:"customer"`
		Subscription string `json:"subscription"`
	}
	if err := json.Unmarshal(event.Data.Raw, &sess); err != nil {
		return fmt.Errorf("unmarshal checkout session: %w", err)
	}
	if sess.Subscription == "" {
		return nil
	}
	mapped, err := s.stripe.GetSubscription(sess.Subscription)
	if err != nil {
		return fmt.Errorf("fetch subscription %s: %w", sess.Subscription, err)
	}
	return s.reconcileMapped(mapped)
}

// reconcileMapped resolves the owning user from the customer id and upserts the
// normalized subscription. A comp row is never overwritten by a Stripe event.
func (s *Service) reconcileMapped(mapped *models.Subscription) error {
	userID, err := s.resolveUserID(mapped.StripeCustomerID)
	if err != nil {
		return err
	}
	if userID == 0 {
		log.Printf("[billing] webhook: no local user for customer %s; skipping", mapped.StripeCustomerID)
		return nil
	}
	mapped.UserID = userID
	if err := s.store.UpsertFromStripe(mapped); err != nil {
		return err
	}
	log.Printf("[billing] reconciled subscription for user %d -> status=%s", userID, mapped.Status)
	return nil
}

// resolveUserID finds the local user id for a Stripe customer id.
func (s *Service) resolveUserID(customerID string) (int64, error) {
	if customerID == "" {
		return 0, nil
	}
	local, err := s.store.GetByStripeCustomerID(customerID)
	if err != nil {
		return 0, err
	}
	if local == nil {
		return 0, nil
	}
	return local.UserID, nil
}
