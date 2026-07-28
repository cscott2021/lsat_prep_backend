package billing

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"

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
	// quota reports rolling-24h answered counts for the metered free tier. Wired
	// after construction via SetFreeQuotaCounter (the questions store). May be nil
	// (e.g. in tests), in which case free-tier usage is simply not reported.
	quota FreeQuotaCounter
	// foundingReady short-circuits the lazy EnsureFoundingCoupon call once the
	// Stripe coupon + promotion code are confirmed to exist, so we don't hit the
	// Stripe API on every founding subscribe.
	foundingReady atomic.Bool
	// lastMisconfigLogUnix throttles the fail-closed misconfiguration log
	// (BILLING_REQUIRED set but billing disabled) so a down paywall doesn't flood
	// the logs on every gated request while still alerting loudly. Unix seconds.
	lastMisconfigLogUnix atomic.Int64
}

// billingMisconfigured reports whether the paywall must FAIL CLOSED: billing is
// required (BILLING_REQUIRED=true, i.e. PROD) but not actually enabled (Stripe
// secret/webhook missing or rotated). It emits a loud, throttled log so the
// on-call sees the whole paid product is gated by a broken billing config.
func (s *Service) billingMisconfigured() bool {
	if !s.cfg.BillingRequired || s.Enabled() {
		return false
	}
	now := time.Now().Unix()
	last := s.lastMisconfigLogUnix.Load()
	if now-last >= 30 && s.lastMisconfigLogUnix.CompareAndSwap(last, now) {
		log.Printf("[billing] CRITICAL: BILLING_REQUIRED=true but Stripe billing is NOT configured — paywall FAILING CLOSED (503). Check STRIPE_SECRET_KEY / STRIPE_WEBHOOK_SECRET.")
	}
	return true
}

// NewService builds the billing service. If cfg.Enabled() is false the Stripe
// provider is not initialized and the server still starts cleanly.
func NewService(cfg Config, store *Store) *Service {
	svc := &Service{cfg: cfg, store: store}
	if cfg.Enabled() {
		svc.stripe = newStripeProvider(cfg, store)
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

// SetFreeQuotaCounter wires the metered-free-tier counter (the questions store).
// Wired after construction because the questions store and the billing service
// are built independently in main; safe to leave unset (usage just isn't
// reported and the paywall middleware falls back to a retryable 503).
func (s *Service) SetFreeQuotaCounter(c FreeQuotaCounter) { s.quota = c }

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
	// Plans are built from the admin-managed plan_prices table (the DB is the
	// source of truth for amounts/cadence — no per-request Stripe call needed).
	prices, err := s.store.GetPlanPrices()
	if err != nil {
		return nil, err
	}
	for _, p := range prices {
		resp.Plans = append(resp.Plans, models.BillingPlan{
			ID:            p.Tier,
			Name:          tierDisplayName(p.Tier),
			PriceID:       p.StripePriceID,
			Interval:      p.Interval,
			IntervalCount: p.IntervalCount,
			Amount:        p.Amount,
			Currency:      p.Currency,
		})
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

	// Free-tier usage + founding offer are only meaningful when billing is
	// enforced. When billing is disabled the paywall is open (everyone has
	// unlimited access and nothing is purchasable), so both stay null to avoid
	// misrepresenting the user's real state.
	if s.Enabled() {
		if !ent.Entitled {
			resp.FreeQuota = s.freeQuotaStatus(userID)
		}
		resp.FoundingOffer = s.foundingOfferFor(userID)
	}
	return resp, nil
}

// freeQuotaStatus builds the metered free-tier usage block for a non-entitled
// user. Returns a zero-usage status (reset_at null) if the counter is unwired or
// errors, so a transient counter failure never breaks the subscription view.
func (s *Service) freeQuotaStatus(userID int64) *models.FreeQuotaStatus {
	fq := &models.FreeQuotaStatus{Used: 0, Limit: freeTierLimit}
	if s.quota == nil {
		return fq
	}
	count, oldest, err := s.quota.CountAnsweredLast24h(userID)
	if err != nil {
		log.Printf("[billing] free-quota status count failed for user %d: %v", userID, err)
		return fq
	}
	fq.Used = count
	if count > 0 && oldest != nil {
		reset := oldest.Add(24 * time.Hour)
		fq.ResetAt = &reset
	}
	return fq
}

// isConfiguredPrice reports whether priceID is a currently-active plan price
// (admin-managed in plan_prices). Only these may back a new subscription.
func (s *Service) isConfiguredPrice(priceID string) bool {
	ok, err := s.store.IsActivePriceID(priceID)
	return err == nil && ok
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
	// Allowlist: only our configured plan prices may be purchased. Without this a
	// client could pass any active/archived/foreign Stripe price id and still get
	// full entitlement (which is derived from status alone) at the wrong price.
	if !s.isConfiguredPrice(req.PriceID) {
		return nil, errors.New("unknown price_id")
	}
	// Guard against duplicate subscriptions: if the user already has a live sub,
	// refuse to create a second one — Stripe would bill both and Cancel only
	// reaches the latest stripe_subscription_id.
	if existing, gerr := s.store.GetByUserID(userID); gerr == nil && existing != nil &&
		existing.StripeSubscriptionID != "" {
		switch existing.Status {
		case "trialing", "active", "past_due":
			return nil, errors.New("you already have an active subscription")
		}
	}

	customerID, err := s.ensureCustomer(userID)
	if err != nil {
		return nil, err
	}

	// A "free_days" promo code is a local trial offer (a bounded trial), NOT a
	// Stripe discount coupon. If the code matches one, apply it as trial days and
	// don't forward it to Stripe as a discount.
	trialDays := s.cfg.TrialDays
	stripePromo := req.PromotionCode
	trialCode := ""
	if req.PromotionCode != "" {
		if offer, oerr := s.store.GetRedeemableTrialOffer(req.PromotionCode); oerr == nil && offer != nil {
			trialDays = offer.FreeDays
			stripePromo = ""
			trialCode = offer.Code
		}
	} else if s.isFoundingEligible(userID) {
		// No client-supplied code + founding-eligible: auto-apply the founding
		// promotion code SERVER-SIDE (eligibility is computed from users.created_at
		// here — a client-supplied "founding" flag is never trusted). Best-effort:
		// if the coupon can't be ensured, subscribe at full price rather than
		// blocking the sale (never let a paying user get stuck).
		if err := s.ensureFoundingCoupon(); err != nil {
			log.Printf("[billing] founding coupon unavailable; subscribing user %d without discount: %v", userID, err)
		} else {
			stripePromo = foundingPromoCode
		}
	}

	clientSecret, mode, subID, err := s.stripe.CreateSubscription(customerID, req.PriceID, stripePromo, trialDays)
	if err != nil {
		return nil, err
	}
	if trialCode != "" {
		if ierr := s.store.IncrementTrialRedemption(trialCode); ierr != nil {
			log.Printf("[billing] could not record trial redemption for %s: %v", trialCode, ierr)
		}
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

	return &models.SubscribeResponse{ClientSecret: clientSecret, SubscriptionID: subID, Mode: mode}, nil
}

// ApplyCoupon previews a promotion code for the client.
func (s *Service) ApplyCoupon(code string) (*models.ApplyCouponResponse, error) {
	if !s.Enabled() {
		return nil, ErrBillingDisabled
	}
	if code == "" {
		return nil, errors.New("code is required")
	}
	// A free-days (trial) code is local, not a Stripe promotion code.
	if offer, oerr := s.store.GetRedeemableTrialOffer(code); oerr == nil && offer != nil {
		return &models.ApplyCouponResponse{
			Valid:       true,
			Description: fmt.Sprintf("%d days free", offer.FreeDays),
			FreeDays:    offer.FreeDays,
		}, nil
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
	// "free_days" is a bounded TRIAL, not a Stripe discount coupon (a coupon
	// would waive a whole billing period). Store it locally and apply it as
	// trial_period_days at subscribe time.
	if req.Type == "free_days" {
		return s.createTrialOffer(req)
	}
	return s.stripe.CreateOffer(req)
}

func (s *Service) createTrialOffer(req models.CreateCouponRequest) (*models.CreateCouponResponse, error) {
	if req.Value <= 0 || req.Value > 365 {
		return nil, errors.New("free_days must be between 1 and 365")
	}
	code := generatePromoCode()
	var maxR *int
	if req.MaxRedemptions != nil {
		v := int(*req.MaxRedemptions)
		maxR = &v
	}
	offer := models.TrialOffer{
		Code:           code,
		FreeDays:       int(req.Value),
		Name:           req.Name,
		MaxRedemptions: maxR,
		ExpiresAt:      req.ExpiresAt,
	}
	if err := s.store.CreateTrialOffer(offer); err != nil {
		return nil, err
	}
	return &models.CreateCouponResponse{Code: code, CouponID: "trial:" + code}, nil
}

func (s *Service) ListOffers() ([]models.CouponOffer, error) {
	if !s.Enabled() {
		return nil, ErrBillingDisabled
	}
	offers, err := s.stripe.ListOffers()
	if err != nil {
		return nil, err
	}
	// Merge in the local free-days (trial) offers so the admin sees all codes.
	if trials, terr := s.store.ListTrialOffers(); terr == nil {
		for _, t := range trials {
			co := models.CouponOffer{
				Code:          t.Code,
				CouponID:      "trial:" + t.Code,
				Name:          t.Name,
				Type:          "free_days",
				FreeDays:      t.FreeDays,
				TimesRedeemed: int64(t.RedeemedCount),
				Active:        t.Active,
				ExpiresAt:     t.ExpiresAt,
			}
			if t.MaxRedemptions != nil {
				co.MaxRedemptions = int64(*t.MaxRedemptions)
			}
			offers = append(offers, co)
		}
	}
	return offers, nil
}

// generatePromoCode returns a readable random code (no ambiguous characters).
func generatePromoCode() string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should not fail; fall back to a time-independent constant
		// prefix so the DB PK still applies uniqueness (caller can retry).
		return "FREEDAYS00"
	}
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}

// ── Founding-member launch promo ──────────────────────────

const (
	// foundingCouponID is the fixed, human-stable Stripe coupon id so
	// EnsureFoundingCoupon is idempotent (we look it up by this id rather than
	// creating a new random coupon each boot).
	foundingCouponID = "founding50_3mo"
	// foundingPromoCode is the redeemable promotion code applied to the
	// subscription (customers never type it — it is applied server-side).
	foundingPromoCode = "FOUNDING"
	// foundingPercentOff / foundingDurationMonths define the discount: 50% off for
	// the first 3 billing months (duration=repeating).
	foundingPercentOff     = 50
	foundingDurationMonths = 3
	// foundingWindowDays is how long after launch an account may be created and
	// still qualify as a founding member.
	foundingWindowDays = 14
)

// foundingWindow returns the [launch, ends] eligibility window and whether the
// feature is configured (FOUNDING_LAUNCH_DATE set).
func (s *Service) foundingWindow() (launch, ends time.Time, ok bool) {
	if s.cfg.FoundingLaunchDate == nil {
		return time.Time{}, time.Time{}, false
	}
	launch = *s.cfg.FoundingLaunchDate
	ends = launch.Add(foundingWindowDays * 24 * time.Hour)
	return launch, ends, true
}

// isFoundingEligible reports whether the user's account was created inside the
// founding window [LAUNCH_DATE, LAUNCH_DATE + 14d] (inclusive). Computed purely
// server-side from users.created_at.
func (s *Service) isFoundingEligible(userID int64) bool {
	launch, ends, ok := s.foundingWindow()
	if !ok {
		return false
	}
	createdAt, err := s.store.GetUserCreatedAt(userID)
	if err != nil {
		log.Printf("[billing] founding eligibility: could not load created_at for user %d: %v", userID, err)
		return false
	}
	return !createdAt.Before(launch) && !createdAt.After(ends)
}

// foundingOfferFor builds the founding_offer block for GET /billing/subscription,
// or nil when the feature is unconfigured or the user is outside the window.
func (s *Service) foundingOfferFor(userID int64) *models.FoundingOffer {
	_, ends, ok := s.foundingWindow()
	if !ok || !s.isFoundingEligible(userID) {
		return nil
	}
	return &models.FoundingOffer{
		Eligible:   true,
		PercentOff: foundingPercentOff,
		Months:     foundingDurationMonths,
		EndsAt:     ends,
	}
}

// ensureFoundingCoupon lazily creates the founding coupon + promotion code the
// first time it is needed, then short-circuits once confirmed present. It retries
// on failure (foundingReady flips only on success) so a transient Stripe error
// doesn't permanently disable the promo.
func (s *Service) ensureFoundingCoupon() error {
	if s.foundingReady.Load() {
		return nil
	}
	if err := s.stripe.EnsureFoundingCoupon(); err != nil {
		return err
	}
	s.foundingReady.Store(true)
	return nil
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

	// Idempotency: skip events we've already fully processed.
	processed, err := s.store.EventProcessed(event.ID)
	if err != nil {
		return fmt.Errorf("check billing event: %w", err)
	}
	if processed {
		log.Printf("[billing] duplicate webhook event %s (%s) ignored", event.ID, event.Type)
		return nil
	}

	// Dispatch FIRST. On a transient failure we return the error WITHOUT recording
	// the event id, so Stripe re-delivers and we retry; the apply funcs are
	// idempotent (upserts keyed by user_id), so a re-handle is safe.
	if derr := s.dispatchEvent(event); derr != nil {
		return derr
	}

	// Record only after successful handling. A failure to record here is benign:
	// a retry would re-handle the event idempotently.
	if _, merr := s.store.MarkEventProcessed(event.ID, string(event.Type)); merr != nil {
		log.Printf("[billing] event %s handled but not recorded (safe to re-handle on retry): %v", event.ID, merr)
	}
	return nil
}

// dispatchEvent routes a verified Stripe event to the appropriate handler.
func (s *Service) dispatchEvent(event stripe.Event) error {
	switch event.Type {
	case "customer.subscription.created",
		"customer.subscription.updated",
		"customer.subscription.deleted":
		return s.applySubscriptionEvent(event)
	case "invoice.paid", "invoice.payment_failed":
		return s.applyInvoiceEvent(event)
	case "checkout.session.completed":
		return s.applyCheckoutEvent(event)
	case "customer.subscription.trial_will_end":
		return s.applyTrialWillEndEvent(event)
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

// applyTrialWillEndEvent handles customer.subscription.trial_will_end (Stripe
// fires this ~3 days before a trial converts to a paid charge). It queues a
// heads-up email telling the user the plan/amount that will be charged, the
// charge date (= trial_end), and that they can cancel in Settings first.
//
// Best-effort: this must NEVER fail the webhook. Any error is logged and nil is
// returned, so the event is still marked processed (a missed reminder is far less
// harmful than jamming the webhook retry queue or double-charging logic). We only
// touch reads + the email outbox here; no subscription state is mutated.
func (s *Service) applyTrialWillEndEvent(event stripe.Event) error {
	var sub stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
		log.Printf("[billing] trial_will_end: unmarshal failed (skipping reminder): %v", err)
		return nil
	}
	mapped := s.stripe.mapSubscription(&sub)

	userID, err := s.resolveUserID(mapped.StripeCustomerID)
	if err != nil {
		log.Printf("[billing] trial_will_end: resolve user for customer %s failed: %v", mapped.StripeCustomerID, err)
		return nil
	}
	if userID == 0 {
		log.Printf("[billing] trial_will_end: no local user for customer %s; skipping reminder", mapped.StripeCustomerID)
		return nil
	}
	if mapped.TrialEnd == nil {
		log.Printf("[billing] trial_will_end: subscription %s has no trial_end; skipping reminder", mapped.StripeSubscriptionID)
		return nil
	}

	email, name, cerr := s.store.GetUserContact(userID)
	if cerr != nil || email == "" {
		log.Printf("[billing] trial_will_end: could not load contact for user %d (skipping reminder): %v", userID, cerr)
		return nil
	}

	// Amount/plan for the reminder come from plan_prices (the DB source of truth
	// for what a tier costs), matched by the subscription's resolved tier. If the
	// price can't be resolved we still send a reminder — just without a specific
	// dollar figure — rather than guessing or staying silent before a charge.
	var price *models.PlanPrice
	if mapped.Plan != "" {
		if p, perr := s.store.GetPlanPrice(mapped.Plan); perr != nil {
			log.Printf("[billing] trial_will_end: plan price lookup for tier %q failed: %v", mapped.Plan, perr)
		} else {
			price = p
		}
	}

	subject, body := trialEndingEmail(name, mapped.Plan, price, *mapped.TrialEnd)
	if qerr := s.store.QueueEmail(email, userID, subject, body, "trial_ending"); qerr != nil {
		log.Printf("[billing] trial_will_end: queue reminder to %s failed: %v", email, qerr)
		return nil
	}
	log.Printf("[billing] trial_will_end: queued reminder for user %d (trial ends %s)", userID, mapped.TrialEnd.Format(time.RFC3339))
	return nil
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
