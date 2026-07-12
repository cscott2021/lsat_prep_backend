//go:build billinge2e

// Hermetic end-to-end billing test: runs the REAL billing Service/Store against
// a real Postgres and the official stripe-mock — no production Stripe account.
// Proves the money-path: subscribe, webhook signature verification, entitlement,
// idempotency, the record-after-success retry fix, the duplicate-subscription
// guard, and the price allowlist.
//
// Run:
//   docker run -d -p 12111:12111 stripe/stripe-mock
//   createdb lsat_billing_test && apply migrations
//   go test -tags billinge2e -run TestBillingE2E ./internal/billing/ -v
package billing

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
	stripe "github.com/stripe/stripe-go/v81"

	"github.com/lsat-prep/backend/internal/models"
)

const (
	priceMonthly = "price_monthly_test"
	priceAnnual  = "price_annual_test"
	whSecret     = "whsec_e2e_test_secret"
)

func mustDB(t *testing.T) *sql.DB {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "host=/tmp port=5432 user=" + os.Getenv("USER") + " dbname=lsat_billing_test sslmode=disable"
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping db (%s): %v", dsn, err)
	}
	return db
}

func seedUser(t *testing.T, db *sql.DB, email string) int64 {
	var id int64
	if err := db.QueryRow(
		`INSERT INTO users (email, name, username, password) VALUES ($1,$2,$3,$4) RETURNING id`,
		email, "E2E User", email, "x",
	).Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

// signedEvent builds a Stripe webhook payload + a valid Stripe-Signature header.
func signedEvent(id, eventType string, object map[string]any) (payload []byte, sigHeader string) {
	ev := map[string]any{"id": id, "object": "event", "type": eventType,
		"data": map[string]any{"object": object}}
	payload, _ = json.Marshal(ev)
	ts := time.Now().Unix()
	mac := hmac.New(sha256.New, []byte(whSecret))
	mac.Write([]byte(fmt.Sprintf("%d.%s", ts, payload)))
	return payload, fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

func subObject(subID, customerID string, status any, priceID string) map[string]any {
	return map[string]any{
		"id": subID, "object": "subscription", "status": status,
		"customer":           customerID,
		"current_period_end": time.Now().Add(720 * time.Hour).Unix(),
		"items": map[string]any{"object": "list", "data": []any{
			map[string]any{"id": "si_1", "price": map[string]any{"id": priceID, "object": "price"}},
		}},
	}
}

func eventRows(t *testing.T, db *sql.DB, id string) int {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM billing_events WHERE stripe_event_id=$1`, id).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return n
}

func TestBillingE2E(t *testing.T) {
	base := os.Getenv("STRIPE_MOCK_URL")
	if base == "" {
		base = "http://localhost:12111"
	}
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(
		stripe.APIBackend, &stripe.BackendConfig{URL: stripe.String(base)}))

	db := mustDB(t)
	defer db.Close()
	if _, err := db.Exec("TRUNCATE billing_events, subscriptions RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	db.Exec("DELETE FROM users WHERE email LIKE 'e2e-%'")

	cfg := Config{
		SecretKey: "sk_test_mock", WebhookSecret: whSecret, PublishableKey: "pk_test",
		PriceMonthly: priceMonthly, PriceAnnual: priceAnnual, TrialDays: 0,
	}
	if !cfg.Enabled() {
		t.Fatal("cfg should be Enabled with secret + webhook set")
	}
	store := NewStore(db)
	svc := NewService(cfg, store)
	uid := seedUser(t, db, "e2e-buyer@test.com")

	// ── 1) Subscribe: real Stripe round-trip + local upsert ──────────────
	resp, err := svc.Subscribe(uid, models.SubscribeRequest{PriceID: priceMonthly})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if resp.SubscriptionID == "" {
		t.Fatal("Subscribe returned no subscription id")
	}
	row, err := store.GetByUserID(uid)
	if err != nil || row == nil {
		t.Fatalf("no local subscription row after Subscribe: %v", err)
	}
	custID := row.StripeCustomerID
	subID := row.StripeSubscriptionID
	if custID == "" || subID == "" {
		t.Fatalf("local row missing stripe ids: cust=%q sub=%q", custID, subID)
	}
	t.Logf("PASS subscribe: sub=%s customer=%s status=%s", subID, custID, row.Status)

	// Force the local status to 'incomplete' so the webhook has a real transition
	// to make (stripe-mock returns 'active' immediately; real Stripe would not).
	db.Exec(`UPDATE subscriptions SET status='incomplete' WHERE user_id=$1`, uid)
	if ent, _ := store.GetEntitlement(uid); ent.Entitled {
		t.Fatal("should NOT be entitled while incomplete")
	}

	// ── 2) Webhook flips entitlement + is recorded ───────────────────────
	pay, sig := signedEvent("evt_active_1", "customer.subscription.updated",
		subObject(subID, custID, "active", priceMonthly))
	if err := svc.HandleWebhook(pay, sig); err != nil {
		t.Fatalf("HandleWebhook(active): %v", err)
	}
	if ent, _ := store.GetEntitlement(uid); !ent.Entitled || ent.Status != "active" {
		t.Fatalf("expected entitled/active after webhook, got %+v", ent)
	}
	if eventRows(t, db, "evt_active_1") != 1 {
		t.Fatal("event should be recorded exactly once after successful handling")
	}
	t.Log("PASS webhook: entitlement active + event recorded")

	// ── 3) Idempotency: replay is skipped, not double-applied ────────────
	if err := svc.HandleWebhook(pay, sig); err != nil {
		t.Fatalf("HandleWebhook(replay): %v", err)
	}
	if n := eventRows(t, db, "evt_active_1"); n != 1 {
		t.Fatalf("replay must not add a second event row, got %d", n)
	}
	t.Log("PASS idempotency: duplicate event skipped")

	// ── 4) Bad signature is rejected ─────────────────────────────────────
	if err := svc.HandleWebhook(pay, "t=1,v1=deadbeef"); err == nil {
		t.Fatal("expected error on bad signature")
	}
	t.Log("PASS signature verification: forged signature rejected")

	// ── 5) Record-after-success retry: a failing handler is NOT recorded,
	//        then a retry of the SAME id succeeds ──────────────────────────
	badPay, badSig := signedEvent("evt_retry_1", "customer.subscription.updated",
		subObject(subID, custID, 12345 /* invalid status type → unmarshal error */, priceMonthly))
	if err := svc.HandleWebhook(badPay, badSig); err == nil {
		t.Fatal("expected error from malformed event object")
	}
	if eventRows(t, db, "evt_retry_1") != 0 {
		t.Fatal("a FAILED event must NOT be recorded (else it can never retry)")
	}
	goodPay, goodSig := signedEvent("evt_retry_1", "customer.subscription.updated",
		subObject(subID, custID, "active", priceMonthly))
	if err := svc.HandleWebhook(goodPay, goodSig); err != nil {
		t.Fatalf("retry of same id should now succeed: %v", err)
	}
	if eventRows(t, db, "evt_retry_1") != 1 {
		t.Fatal("successful retry should record the event")
	}
	t.Log("PASS retry: failed event not recorded, retry of same id processed")

	// ── 6) Duplicate-subscription guard ──────────────────────────────────
	if _, err := svc.Subscribe(uid, models.SubscribeRequest{PriceID: priceMonthly}); err == nil {
		t.Fatal("expected guard error subscribing while already active")
	}
	t.Log("PASS guard: second subscribe on an active user rejected")

	// ── 7) Price allowlist ───────────────────────────────────────────────
	uid2 := seedUser(t, db, "e2e-buyer2@test.com")
	if _, err := svc.Subscribe(uid2, models.SubscribeRequest{PriceID: "price_bogus_999"}); err == nil {
		t.Fatal("expected allowlist error for an unconfigured price")
	}
	t.Log("PASS allowlist: unconfigured price rejected")
}
