package billing

import (
	"errors"
	"time"

	stripe "github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/billingportal/session"
	"github.com/stripe/stripe-go/v81/coupon"
	"github.com/stripe/stripe-go/v81/customer"
	"github.com/stripe/stripe-go/v81/price"
	"github.com/stripe/stripe-go/v81/promotioncode"
	"github.com/stripe/stripe-go/v81/setupintent"
	"github.com/stripe/stripe-go/v81/subscription"

	"github.com/lsat-prep/backend/internal/models"
)

// ErrPromotionNotFound is returned when a promotion code cannot be resolved.
var ErrPromotionNotFound = errors.New("promotion code not found")

// stripeProvider wraps the Stripe SDK. All Stripe network calls go through here
// so the service/handlers stay free of SDK types. Stripe's Go SDK uses a single
// process-global API key (stripe.Key), set once in newStripeProvider.
type stripeProvider struct {
	cfg Config
}

func newStripeProvider(cfg Config) *stripeProvider {
	// Package-global key; safe because the whole process talks to one account.
	stripe.Key = cfg.SecretKey
	return &stripeProvider{cfg: cfg}
}

// planForPrice maps a Stripe price id back to a stable, human plan name using the
// configured monthly/annual price ids. Unknown prices fall back to the raw id.
func (p *stripeProvider) planForPrice(priceID string) string {
	switch priceID {
	case p.cfg.PriceMonthly:
		return "monthly"
	case p.cfg.PriceQuarterly:
		return "quarterly"
	case p.cfg.PriceAnnual:
		return "annual"
	default:
		return priceID
	}
}

// EnsureCustomer returns an existing Stripe customer id if one is passed,
// otherwise creates a new customer for the user and returns its id.
func (p *stripeProvider) EnsureCustomer(existingID, email, name string, userID int64) (string, error) {
	if existingID != "" {
		return existingID, nil
	}
	params := &stripe.CustomerParams{
		Email: stripe.String(email),
		Name:  stripe.String(name),
	}
	params.AddMetadata("user_id", int64ToStr(userID))
	c, err := customer.New(params)
	if err != nil {
		return "", err
	}
	return c.ID, nil
}

// CreateSubscription creates an incomplete subscription for the customer and
// returns the PaymentIntent client secret the app uses to collect payment via
// the Stripe Payment Sheet. payment_behavior=default_incomplete + expanding
// latest_invoice.payment_intent is the SDK-recommended in-app flow: the card is
// entered client-side and the raw PAN never reaches this server.
func (p *stripeProvider) CreateSubscription(customerID, priceID, promotionCode string, trialDays int) (clientSecret, subscriptionID string, err error) {
	params := &stripe.SubscriptionParams{
		Customer: stripe.String(customerID),
		Items: []*stripe.SubscriptionItemsParams{
			{Price: stripe.String(priceID)},
		},
		PaymentBehavior: stripe.String("default_incomplete"),
	}
	if trialDays > 0 {
		params.TrialPeriodDays = stripe.Int64(int64(trialDays))
	}
	if promotionCode != "" {
		// Resolve the customer-facing code to a promotion code id.
		promoID, perr := p.resolvePromotionID(promotionCode)
		if perr != nil {
			return "", "", perr
		}
		params.Discounts = []*stripe.SubscriptionDiscountParams{
			{PromotionCode: stripe.String(promoID)},
		}
	}
	params.AddExpand("latest_invoice.payment_intent")

	sub, err := subscription.New(params)
	if err != nil {
		return "", "", err
	}

	// With a trial the first invoice may not require immediate payment, so a
	// PaymentIntent (and thus client secret) may be absent — that is expected.
	if sub.LatestInvoice != nil && sub.LatestInvoice.PaymentIntent != nil {
		clientSecret = sub.LatestInvoice.PaymentIntent.ClientSecret
	}
	return clientSecret, sub.ID, nil
}

// CancelAtPeriodEnd flags the subscription to cancel at the end of the current
// period (non-destructive; the user keeps access until then).
func (p *stripeProvider) CancelAtPeriodEnd(subscriptionID string) (*models.Subscription, error) {
	sub, err := subscription.Update(subscriptionID, &stripe.SubscriptionParams{
		CancelAtPeriodEnd: stripe.Bool(true),
	})
	if err != nil {
		return nil, err
	}
	return p.mapSubscription(sub), nil
}

// CreateSetupIntent returns a SetupIntent client secret so the app can update the
// customer's saved card via the Payment Sheet (off-session future payments).
func (p *stripeProvider) CreateSetupIntent(customerID string) (string, error) {
	si, err := setupintent.New(&stripe.SetupIntentParams{
		Customer: stripe.String(customerID),
		Usage:    stripe.String("off_session"),
	})
	if err != nil {
		return "", err
	}
	return si.ClientSecret, nil
}

// CreatePortalSession opens a Stripe Billing Portal session (the hosted fallback
// for managing the subscription / payment method).
func (p *stripeProvider) CreatePortalSession(customerID, returnURL string) (string, error) {
	s, err := session.New(&stripe.BillingPortalSessionParams{
		Customer:  stripe.String(customerID),
		ReturnURL: stripe.String(returnURL),
	})
	if err != nil {
		return "", err
	}
	return s.URL, nil
}

// GetPrice fetches a Stripe price so /billing/config can report amount/currency/
// interval without the client hardcoding them.
func (p *stripeProvider) GetPrice(priceID string) (*stripe.Price, error) {
	return price.Get(priceID, nil)
}

// LookupPromotion resolves a customer-facing code and returns a normalized
// preview (validity, description, percent off, free days). Returns
// ErrPromotionNotFound if the code does not exist / is inactive.
func (p *stripeProvider) LookupPromotion(code string) (*models.ApplyCouponResponse, error) {
	pc, err := p.findPromotion(code)
	if err != nil {
		return nil, err
	}
	resp := &models.ApplyCouponResponse{
		Valid: pc.Active && pc.Coupon != nil && pc.Coupon.Valid,
	}
	if pc.Coupon != nil {
		resp.PercentOff = pc.Coupon.PercentOff
		resp.Description = pc.Coupon.Name
		resp.FreeDays = freeDaysFromCoupon(pc.Coupon)
	}
	return resp, nil
}

// CreateOffer creates a Stripe Coupon plus a redeemable Promotion Code.
//
// Two offer types are supported:
//   - "percent": a percent_off coupon (duration=forever) — a straightforward
//     recurring discount.
//   - "free_days": modeled as a 100%-off, duration=once coupon whose metadata
//     records the intended free-day count. This is the cleanest Stripe-native
//     way to express "first N days free" for a promo-code redemption without a
//     bespoke trial: the redemption waives the first invoice. (The
//     subscribe-time trial_period_days handles the default trial for everyone;
//     this coupon layers an additional free-first-period incentive.)
func (p *stripeProvider) CreateOffer(req models.CreateCouponRequest) (*models.CreateCouponResponse, error) {
	couponParams := &stripe.CouponParams{
		Name: stripe.String(req.Name),
	}
	switch req.Type {
	case "percent":
		if req.Value <= 0 || req.Value > 100 {
			return nil, errors.New("percent value must be between 1 and 100")
		}
		couponParams.PercentOff = stripe.Float64(float64(req.Value))
		couponParams.Duration = stripe.String(string(stripe.CouponDurationForever))
	case "free_days":
		// Deferred for launch. Modeling "N free days" as a 100%-off duration=once
		// coupon waives the ENTIRE first billing period (a "14 free days" code on
		// the annual plan = a free year), because Stripe coupons cannot express a
		// day-bounded discount. The correct primitive is a bounded trial
		// (trial_period_days) set at subscribe time. Until that is wired, reject
		// creation so an admin cannot accidentally over-grant. For free access,
		// use an admin comp; for a discount, use a percentage code.
		return nil, errors.New("free_days offers are temporarily unavailable — use a percentage discount or an admin comp")
	default:
		return nil, errors.New("type must be 'percent' or 'free_days'")
	}
	if req.MaxRedemptions != nil {
		couponParams.MaxRedemptions = req.MaxRedemptions
	}
	if req.ExpiresAt != nil {
		couponParams.RedeemBy = stripe.Int64(req.ExpiresAt.Unix())
	}

	cp, err := coupon.New(couponParams)
	if err != nil {
		return nil, err
	}

	promoParams := &stripe.PromotionCodeParams{
		Coupon: stripe.String(cp.ID),
	}
	if req.MaxRedemptions != nil {
		promoParams.MaxRedemptions = req.MaxRedemptions
	}
	if req.ExpiresAt != nil {
		promoParams.ExpiresAt = stripe.Int64(req.ExpiresAt.Unix())
	}
	pc, err := promotioncode.New(promoParams)
	if err != nil {
		return nil, err
	}

	return &models.CreateCouponResponse{Code: pc.Code, CouponID: cp.ID}, nil
}

// ListOffers returns active promotion codes with their live redemption counts.
func (p *stripeProvider) ListOffers() ([]models.CouponOffer, error) {
	params := &stripe.PromotionCodeListParams{}
	params.Limit = stripe.Int64(100)
	iter := promotioncode.List(params)

	offers := []models.CouponOffer{}
	for iter.Next() {
		pc := iter.PromotionCode()
		offer := models.CouponOffer{
			Code:           pc.Code,
			TimesRedeemed:  pc.TimesRedeemed,
			MaxRedemptions: pc.MaxRedemptions,
			Active:         pc.Active,
		}
		if pc.ExpiresAt > 0 {
			t := time.Unix(pc.ExpiresAt, 0).UTC()
			offer.ExpiresAt = &t
		}
		if pc.Coupon != nil {
			offer.CouponID = pc.Coupon.ID
			offer.Name = pc.Coupon.Name
			if fd := freeDaysFromCoupon(pc.Coupon); fd > 0 {
				offer.Type = "free_days"
				offer.FreeDays = fd
			} else {
				offer.Type = "percent"
				offer.PercentOff = pc.Coupon.PercentOff
			}
		}
		offers = append(offers, offer)
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	return offers, nil
}

// GetSubscription fetches a subscription (expanding items) and normalizes it.
func (p *stripeProvider) GetSubscription(subscriptionID string) (*models.Subscription, error) {
	sub, err := subscription.Get(subscriptionID, nil)
	if err != nil {
		return nil, err
	}
	return p.mapSubscription(sub), nil
}

// resolvePromotionID resolves a customer-facing code to its promotion code id.
func (p *stripeProvider) resolvePromotionID(code string) (string, error) {
	pc, err := p.findPromotion(code)
	if err != nil {
		return "", err
	}
	return pc.ID, nil
}

// findPromotion looks up an active promotion code by its (case-insensitive) code.
func (p *stripeProvider) findPromotion(code string) (*stripe.PromotionCode, error) {
	active := true
	params := &stripe.PromotionCodeListParams{
		Code:   stripe.String(code),
		Active: &active,
	}
	params.Limit = stripe.Int64(1)
	iter := promotioncode.List(params)
	if iter.Next() {
		return iter.PromotionCode(), nil
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	return nil, ErrPromotionNotFound
}

// mapSubscription normalizes a Stripe subscription into our local model, mapping
// Stripe statuses onto the app's status vocabulary and pulling the price id from
// the first subscription item.
func (p *stripeProvider) mapSubscription(sub *stripe.Subscription) *models.Subscription {
	out := &models.Subscription{
		Provider:             models.ProviderStripe,
		StripeSubscriptionID: sub.ID,
		Status:               mapStripeStatus(sub.Status),
		CancelAtPeriodEnd:    sub.CancelAtPeriodEnd,
	}
	if sub.Customer != nil {
		out.StripeCustomerID = sub.Customer.ID
	}
	if sub.CurrentPeriodEnd > 0 {
		t := time.Unix(sub.CurrentPeriodEnd, 0).UTC()
		out.CurrentPeriodEnd = &t
	}
	if sub.TrialEnd > 0 {
		t := time.Unix(sub.TrialEnd, 0).UTC()
		out.TrialEnd = &t
	}
	if sub.Items != nil && len(sub.Items.Data) > 0 && sub.Items.Data[0].Price != nil {
		out.PriceID = sub.Items.Data[0].Price.ID
		out.Plan = p.planForPrice(out.PriceID)
	}
	return out
}

// mapStripeStatus maps Stripe's subscription statuses onto the app's status set
// (migration 007). Terminal/failed states collapse so the paywall can treat them
// uniformly (past_due prompts a payment update; the rest deny access).
func mapStripeStatus(s stripe.SubscriptionStatus) string {
	switch s {
	case stripe.SubscriptionStatusTrialing:
		return models.SubStatusTrialing
	case stripe.SubscriptionStatusActive:
		return models.SubStatusActive
	case stripe.SubscriptionStatusPastDue, stripe.SubscriptionStatusUnpaid:
		return models.SubStatusPastDue
	case stripe.SubscriptionStatusCanceled, stripe.SubscriptionStatusPaused:
		return models.SubStatusCanceled
	case stripe.SubscriptionStatusIncomplete, stripe.SubscriptionStatusIncompleteExpired:
		return models.SubStatusIncomplete
	default:
		return models.SubStatusNone
	}
}

// freeDaysFromCoupon reads the free-day count off a coupon's metadata (set by
// CreateOffer for free_days offers). Returns 0 for percent offers.
func freeDaysFromCoupon(c *stripe.Coupon) int {
	if c == nil || c.Metadata == nil {
		return 0
	}
	if v, ok := c.Metadata["free_days"]; ok {
		return strToInt(v)
	}
	return 0
}
