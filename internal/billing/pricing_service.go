package billing

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/lsat-prep/backend/internal/models"
)

// tierSpec is the fixed cadence + launch default for a plan tier. Only the amount
// is admin-editable; cadence is fixed so "quarterly" always means every 3 months.
type tierSpec struct {
	interval      string
	intervalCount int64
	defaultAmount int64 // minor units (cents)
	displayName   string
}

// Launch defaults: $19.99 / $49.99 / $149.99 (per BUSINESS_MODEL.md).
var tierSpecs = map[string]tierSpec{
	"monthly":   {"month", 1, 1999, "Monthly"},
	"quarterly": {"month", 3, 4999, "Quarterly"},
	"annual":    {"year", 1, 14999, "Annual"},
}

var tierOrder = []string{"monthly", "quarterly", "annual"}

func tierDisplayName(tier string) string {
	if s, ok := tierSpecs[tier]; ok {
		return s.displayName
	}
	return tier
}

// SeedDefaultPrices ensures each tier has an active Stripe price, creating the
// product + any missing prices at the launch defaults. Idempotent (tiers that
// already have a price are left untouched); a no-op when billing is disabled.
// Called once at startup so prices exist the moment Stripe keys are live.
func (s *Service) SeedDefaultPrices() error {
	if !s.Enabled() {
		return nil
	}
	existing, err := s.store.GetPlanPrices()
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, p := range existing {
		have[p.Tier] = true
	}
	missing := false
	for _, t := range tierOrder {
		if !have[t] {
			missing = true
		}
	}
	if !missing {
		return nil
	}
	productID, err := s.stripe.EnsureProduct()
	if err != nil {
		return fmt.Errorf("ensure product: %w", err)
	}
	for _, tier := range tierOrder {
		if have[tier] {
			continue
		}
		spec := tierSpecs[tier]
		pr, err := s.stripe.CreatePrice(productID, "usd", spec.interval, spec.intervalCount, spec.defaultAmount)
		if err != nil {
			return fmt.Errorf("create %s price: %w", tier, err)
		}
		if err := s.store.UpsertPlanPrice(models.PlanPrice{
			Tier: tier, StripePriceID: pr.ID, StripeProductID: productID,
			Amount: spec.defaultAmount, Currency: "usd",
			Interval: spec.interval, IntervalCount: spec.intervalCount,
		}); err != nil {
			return fmt.Errorf("store %s price: %w", tier, err)
		}
		log.Printf("[billing] seeded %s price %s ($%.2f)", tier, pr.ID, float64(spec.defaultAmount)/100)
	}
	return nil
}

// AdminPricing returns the current prices + change history for the admin UI.
func (s *Service) AdminPricing() (*models.AdminPricingResponse, error) {
	prices, err := s.store.GetPlanPrices()
	if err != nil {
		return nil, err
	}
	history, err := s.store.ListPriceChanges(100)
	if err != nil {
		return nil, err
	}
	return &models.AdminPricingResponse{Enabled: s.Enabled(), Prices: prices, History: history}, nil
}

// UpdatePrice changes a tier's price. Stripe Prices are immutable, so this creates
// a NEW price, repoints the tier, archives the old price, and records an audit
// row. New subscribers get the new price immediately; existing subscribers are
// grandfathered. On an INCREASE with notifyExisting set, current subscribers on
// that tier are queued an advance-notice email.
func (s *Service) UpdatePrice(adminID int64, tier string, req models.UpdatePriceRequest) (*models.PriceChange, error) {
	if !s.Enabled() {
		return nil, ErrBillingDisabled
	}
	spec, ok := tierSpecs[tier]
	if !ok {
		return nil, errors.New("unknown tier")
	}
	if req.Amount < 50 {
		return nil, errors.New("amount must be at least $0.50")
	}
	if strings.TrimSpace(req.Reason) == "" {
		return nil, errors.New("a reason is required for price changes")
	}

	current, err := s.store.GetPlanPrice(tier)
	if err != nil {
		return nil, err
	}
	if current != nil && current.Amount == req.Amount {
		return nil, errors.New("new price is the same as the current price")
	}

	productID := ""
	if current != nil {
		productID = current.StripeProductID
	}
	if productID == "" {
		if productID, err = s.stripe.EnsureProduct(); err != nil {
			return nil, fmt.Errorf("ensure product: %w", err)
		}
	}

	newPrice, err := s.stripe.CreatePrice(productID, "usd", spec.interval, spec.intervalCount, req.Amount)
	if err != nil {
		return nil, fmt.Errorf("create price: %w", err)
	}
	if err := s.store.UpsertPlanPrice(models.PlanPrice{
		Tier: tier, StripePriceID: newPrice.ID, StripeProductID: productID,
		Amount: req.Amount, Currency: "usd",
		Interval: spec.interval, IntervalCount: spec.intervalCount,
	}); err != nil {
		return nil, err
	}

	var oldAmount *int64
	oldPriceID := ""
	isIncrease := false
	if current != nil {
		oldPriceID = current.StripePriceID
		a := current.Amount
		oldAmount = &a
		isIncrease = req.Amount > current.Amount
		if aerr := s.stripe.ArchivePrice(current.StripePriceID); aerr != nil {
			log.Printf("[billing] could not archive old price %s: %v", current.StripePriceID, aerr)
		}
	}

	pc := models.PriceChange{
		Tier: tier, OldAmount: oldAmount, NewAmount: req.Amount,
		OldPriceID: oldPriceID, NewPriceID: newPrice.ID,
		ChangedBy: &adminID, Reason: strings.TrimSpace(req.Reason),
	}

	if req.NotifyExisting && isIncrease {
		subs, sErr := s.store.ActiveSubscribersForTier(tier)
		if sErr != nil {
			log.Printf("[billing] load subscribers for %s notice failed: %v", tier, sErr)
		} else {
			subject, bodyTmpl := priceIncreaseEmail(tier, *oldAmount, req.Amount)
			for _, c := range subs {
				body := fmt.Sprintf(bodyTmpl, firstName(c.Name))
				if qErr := s.store.QueueEmail(c.Email, c.UserID, subject, body, "price_change"); qErr != nil {
					log.Printf("[billing] queue price notice to %s failed: %v", c.Email, qErr)
				}
			}
			pc.NotifiedExisting = true
			pc.AffectedSubscribers = len(subs)
		}
	}

	id, err := s.store.InsertPriceChange(pc)
	if err != nil {
		return nil, err
	}
	pc.ID = id
	pc.CreatedAt = time.Now()
	if pc.ChangedBy != nil {
		if _, name, cerr := s.store.GetUserContact(adminID); cerr == nil {
			pc.ChangedByName = name
		}
	}
	return &pc, nil
}

func firstName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "there"
	}
	if i := strings.IndexByte(name, ' '); i > 0 {
		return name[:i]
	}
	return name
}

// priceIncreaseEmail returns the subject and a body template (one %s for the
// recipient's first name) for an advance price-increase notice.
func priceIncreaseEmail(tier string, oldAmt, newAmt int64) (subject, bodyTemplate string) {
	plan := tierDisplayName(tier)
	subject = "An update to your Score Right subscription price"
	bodyTemplate = fmt.Sprintf(`Hi %%s,

We're writing to let you know the price of the Score Right %s plan is changing from $%.2f to $%.2f.

Your price takes effect on your next renewal — there's nothing you need to do to keep studying. You can review or cancel your subscription anytime under Settings → Subscription.

Thanks for studying with Score Right.
— The Score Right team`, plan, float64(oldAmt)/100, float64(newAmt)/100)
	return subject, bodyTemplate
}
