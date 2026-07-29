package billing

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/lsat-prep/backend/internal/billing/apple"
	"github.com/lsat-prep/backend/internal/models"
)

// ErrAppleInvalid marks a signed transaction that failed verification or
// business checks (wrong bundle, unknown product, expired, revoked). Handlers
// map it to 400 — the client (or a forger) sent something unusable.
var ErrAppleInvalid = errors.New("invalid Apple transaction")

// AppleProductIDs maps App Store Connect product ids to the SAME plan tiers the
// web (Stripe) store sells, so an iOS buyer lands on the identical plan name.
// These must be created in App Store Connect EXACTLY as keyed here. App Store
// pricing is deliberately ~1.4x the web tiers to offset Apple's 15-30% cut:
//
//	monthly    $19.99 web -> $27.99 App Store
//	quarterly  $49.99 web -> $69.99 App Store
//	annual    $149.99 web -> $199.99 App Store
var AppleProductIDs = map[string]string{
	"app.scoreright.monthly.ios":   "monthly",
	"app.scoreright.quarterly.ios": "quarterly",
	"app.scoreright.annual.ios":    "annual",
}

// AppleProductTier resolves a product id to its plan tier.
func AppleProductTier(productID string) (string, bool) {
	tier, ok := AppleProductIDs[productID]
	return tier, ok
}

// AppleConfig holds App Store verification configuration. Verification itself
// is OFFLINE (x5c chain against vendored Apple roots), so no secrets are
// required — only the bundle id to bind transactions to THIS app.
type AppleConfig struct {
	// BundleID must match the signed transaction's bundleId claim. Defaults to
	// the production bundle id; override with APPLE_BUNDLE_ID.
	BundleID string
	// Environment, when set to "Sandbox" or "Production" (APPLE_ENVIRONMENT),
	// restricts which signed transactions are accepted. Empty accepts both —
	// recommended so TestFlight/sandbox review builds and prod share one deploy.
	Environment string
}

// DefaultAppleBundleID is the App Store bundle id configured in Xcode.
const DefaultAppleBundleID = "com.scoreright.app"

// LoadAppleConfig reads APPLE_BUNDLE_ID / APPLE_ENVIRONMENT from the
// environment, mirroring how LoadConfig sources STRIPE_*.
func LoadAppleConfig() AppleConfig {
	return AppleConfig{
		BundleID:    getEnvDefault("APPLE_BUNDLE_ID", DefaultAppleBundleID),
		Environment: os.Getenv("APPLE_ENVIRONMENT"),
	}
}

func getEnvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// AppleService verifies StoreKit 2 signed transactions and applies App Store
// Server Notifications V2 to the SAME subscription/entitlement records Stripe
// uses. It is independent of Service (Stripe) so it can be constructed and
// tested without any Stripe configuration.
type AppleService struct {
	cfg   AppleConfig
	store *Store
	roots *x509.CertPool
	// now is injectable for tests.
	now func() time.Time
}

// NewAppleService builds the verifier, loading the vendored Apple root CAs.
func NewAppleService(cfg AppleConfig, store *Store) (*AppleService, error) {
	roots, err := apple.DefaultRoots()
	if err != nil {
		return nil, fmt.Errorf("load Apple root CAs: %w", err)
	}
	if cfg.BundleID == "" {
		cfg.BundleID = DefaultAppleBundleID
	}
	return &AppleService{cfg: cfg, store: store, roots: roots, now: time.Now}, nil
}

// statusForTransaction derives the local subscription status from a verified
// signed transaction: an unexpired intro-offer (free trial) period is
// 'trialing', any other unexpired period is 'active', and an already-expired
// transaction is recorded as 'canceled' (no entitlement).
func statusForTransaction(tx *apple.Transaction, now time.Time) (status string, trialEnd *time.Time, periodEnd *time.Time) {
	expires := time.UnixMilli(tx.ExpiresDate).UTC()
	periodEnd = &expires
	if tx.ExpiresDate > 0 && expires.After(now) {
		if tx.OfferType == apple.OfferTypeIntroductory {
			return models.SubStatusTrialing, &expires, &expires
		}
		return models.SubStatusActive, nil, &expires
	}
	return models.SubStatusCanceled, nil, &expires
}

// notificationEffect is the state change a notification applies, computed as a
// pure function for testability. apply=false means "acknowledge but change
// nothing" (e.g. TEST, or DID_FAIL_TO_RENEW while still in grace period).
type notificationEffect struct {
	apply             bool
	status            string
	cancelAtPeriodEnd *bool
	fullUpsert        bool // SUBSCRIBED / DID_RENEW rewrite the row from the transaction
}

func effectForNotification(notifType, subtype string, renewal *apple.RenewalInfo) notificationEffect {
	boolPtr := func(b bool) *bool { return &b }
	switch notifType {
	case apple.NotifSubscribed, apple.NotifDidRenew:
		return notificationEffect{apply: true, fullUpsert: true}
	case apple.NotifDidChangeRenewalStatus:
		// Auto-renew off = cancel at period end (access continues until expiry);
		// auto-renew back on clears the flag. Prefer the subtype; fall back to
		// the renewal info's autoRenewStatus when the subtype is absent.
		cancel := subtype == apple.SubtypeAutoRenewDisabled
		if subtype == "" && renewal != nil {
			cancel = renewal.AutoRenewStatus == 0
		}
		return notificationEffect{apply: true, cancelAtPeriodEnd: boolPtr(cancel)}
	case apple.NotifDidFailToRenew:
		if subtype == apple.SubtypeGracePeriod {
			// Still inside Billing Grace Period — Apple keeps the subscription
			// alive, so we keep access and change nothing.
			return notificationEffect{apply: false}
		}
		return notificationEffect{apply: true, status: models.SubStatusPastDue}
	case apple.NotifGracePeriodExpired:
		return notificationEffect{apply: true, status: models.SubStatusPastDue}
	case apple.NotifExpired, apple.NotifRefund, apple.NotifRevoke:
		return notificationEffect{apply: true, status: models.SubStatusCanceled, cancelAtPeriodEnd: boolPtr(false)}
	default:
		return notificationEffect{apply: false}
	}
}

// verifyTransaction authenticates a signed transaction JWS and applies the
// business checks (bundle binding, environment, product allowlist, type,
// revocation). Returns the decoded transaction on success.
func (s *AppleService) verifyTransaction(signedTx string) (*apple.Transaction, error) {
	var tx apple.Transaction
	if err := apple.VerifyAndDecode(signedTx, s.roots, s.now(), &tx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAppleInvalid, err)
	}
	if tx.BundleID != s.cfg.BundleID {
		return nil, fmt.Errorf("%w: bundleId %q does not match this app", ErrAppleInvalid, tx.BundleID)
	}
	if s.cfg.Environment != "" && tx.Environment != s.cfg.Environment {
		return nil, fmt.Errorf("%w: environment %q not accepted here", ErrAppleInvalid, tx.Environment)
	}
	if tx.Type != "" && tx.Type != apple.TransactionTypeAutoRenewable {
		return nil, fmt.Errorf("%w: unsupported transaction type %q", ErrAppleInvalid, tx.Type)
	}
	if _, ok := AppleProductTier(tx.ProductID); !ok {
		return nil, fmt.Errorf("%w: unknown product %q", ErrAppleInvalid, tx.ProductID)
	}
	if tx.RevocationDate > 0 {
		return nil, fmt.Errorf("%w: transaction was revoked/refunded", ErrAppleInvalid)
	}
	return &tx, nil
}

// VerifyPurchase is the POST /billing/apple/verify flow: authenticate the
// StoreKit 2 signed transaction, upsert provider='apple' state for the caller,
// and return the fresh entitlement so the app can unlock immediately.
func (s *AppleService) VerifyPurchase(userID int64, req models.AppleVerifyRequest) (*models.AppleVerifyResponse, error) {
	if strings.TrimSpace(req.SignedTransaction) == "" {
		return nil, fmt.Errorf("%w: signed_transaction is required", ErrAppleInvalid)
	}
	tx, err := s.verifyTransaction(req.SignedTransaction)
	if err != nil {
		return nil, err
	}
	// Cross-check the client-supplied product id against the signed payload.
	if req.ProductID != "" && req.ProductID != tx.ProductID {
		return nil, fmt.Errorf("%w: product_id does not match signed transaction", ErrAppleInvalid)
	}

	tier, _ := AppleProductTier(tx.ProductID)
	status, trialEnd, periodEnd := statusForTransaction(tx, s.now())
	sub := &models.Subscription{
		UserID:            userID,
		Provider:          models.ProviderApple,
		Status:            status,
		Plan:              tier,
		PriceID:           tx.ProductID,
		CurrentPeriodEnd:  periodEnd,
		TrialEnd:          trialEnd,
		CancelAtPeriodEnd: false,
	}
	if err := s.store.UpsertFromApple(sub, tx.OriginalTransactionID); err != nil {
		return nil, err
	}

	ent, err := s.store.GetEntitlement(userID)
	if err != nil {
		return nil, err
	}
	return &models.AppleVerifyResponse{
		Status:           status,
		Plan:             tier,
		Provider:         models.ProviderApple,
		CurrentPeriodEnd: periodEnd,
		Entitled:         ent.Entitled,
		Environment:      tx.Environment,
	}, nil
}

// maxAppleNotificationBody caps the notification payload we buffer.
const maxAppleNotificationBody = 1 << 20 // 1 MiB

// HandleNotification processes one App Store Server Notifications V2 POST body
// ({"signedPayload": "<JWS>"}). It verifies the payload's signature against the
// vendored Apple roots, dedupes by notificationUUID, and applies the lifecycle
// effect to the local subscription row. Unknown users and non-transaction
// notification types are acknowledged (nil error) so Apple stops retrying.
func (s *AppleService) HandleNotification(body []byte) error {
	var outer apple.NotificationBody
	if err := json.Unmarshal(body, &outer); err != nil {
		return fmt.Errorf("%w: notification body not JSON", ErrAppleInvalid)
	}
	var notif apple.Notification
	if err := apple.VerifyAndDecode(outer.SignedPayload, s.roots, s.now(), &notif); err != nil {
		return fmt.Errorf("%w: %v", ErrAppleInvalid, err)
	}

	if notif.NotificationUUID != "" {
		isNew, err := s.store.MarkAppleEventProcessed(notif.NotificationUUID, notif.NotificationType)
		if err != nil {
			return err // transient DB failure -> 500 so Apple retries
		}
		if !isNew {
			return nil // duplicate delivery
		}
	}

	effect := effectForNotification(notif.NotificationType, notif.Subtype, nil)
	if notif.NotificationType == apple.NotifTest {
		log.Printf("[billing/apple] TEST notification received and verified (environment %v)", environmentOf(notif.Data))
		return nil
	}
	if notif.Data == nil {
		log.Printf("[billing/apple] %s: no transaction data, acknowledged", notif.NotificationType)
		return nil
	}
	if notif.Data.BundleID != "" && notif.Data.BundleID != s.cfg.BundleID {
		return fmt.Errorf("%w: notification bundleId %q does not match this app", ErrAppleInvalid, notif.Data.BundleID)
	}

	tx, err := s.verifyTransaction(notif.Data.SignedTransactionInfo)
	if err != nil {
		return err
	}

	// Renewal info drives the auto-renew (cancel-at-period-end) flag.
	var renewal *apple.RenewalInfo
	if notif.Data.SignedRenewalInfo != "" {
		var ri apple.RenewalInfo
		if err := apple.VerifyAndDecode(notif.Data.SignedRenewalInfo, s.roots, s.now(), &ri); err == nil {
			renewal = &ri
		}
	}
	effect = effectForNotification(notif.NotificationType, notif.Subtype, renewal)
	if !effect.apply {
		return nil
	}

	row, err := s.store.GetByAppleOriginalTransactionID(tx.OriginalTransactionID)
	if err != nil {
		return err
	}
	if row == nil {
		// We never saw the purchase (e.g. bought before this deploy). The app's
		// verify call is the bootstrap path; acknowledge so Apple stops retrying.
		log.Printf("[billing/apple] %s for unknown original transaction %s — acknowledged, no local row",
			notif.NotificationType, tx.OriginalTransactionID)
		return nil
	}

	if effect.fullUpsert {
		tier, _ := AppleProductTier(tx.ProductID)
		status, trialEnd, periodEnd := statusForTransaction(tx, s.now())
		sub := &models.Subscription{
			UserID:           row.UserID,
			Provider:         models.ProviderApple,
			Status:           status,
			Plan:             tier,
			PriceID:          tx.ProductID,
			CurrentPeriodEnd: periodEnd,
			TrialEnd:         trialEnd,
		}
		if effect.cancelAtPeriodEnd != nil {
			sub.CancelAtPeriodEnd = *effect.cancelAtPeriodEnd
		}
		if err := s.store.UpsertFromApple(sub, tx.OriginalTransactionID); err != nil {
			return err
		}
		log.Printf("[billing/apple] %s applied: user %d -> %s (period ends %v)",
			notif.NotificationType, row.UserID, status, periodEnd)
		return nil
	}

	var periodEnd *time.Time
	if tx.ExpiresDate > 0 {
		t := time.UnixMilli(tx.ExpiresDate).UTC()
		periodEnd = &t
	}
	updated, err := s.store.UpdateFromAppleTx(tx.OriginalTransactionID, effect.status, effect.cancelAtPeriodEnd, periodEnd)
	if err != nil {
		return err
	}
	if updated {
		log.Printf("[billing/apple] %s applied: user %d -> status=%q cancelAtPeriodEnd=%v",
			notif.NotificationType, row.UserID, effect.status, effect.cancelAtPeriodEnd)
	}
	return nil
}

func environmentOf(data *apple.NotificationData) string {
	if data == nil {
		return ""
	}
	return data.Environment
}
