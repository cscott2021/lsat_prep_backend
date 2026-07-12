package billing

// Provider identifies a billing backend. Only Stripe is wired today.
//
// EXTENSION POINT — Apple App Store IAP:
// Entitlement is DERIVED from the local `subscriptions` table (see
// Store.GetEntitlement) independent of provider, and the paywall
// (RequireEntitlement) reads only that table. Adding Apple therefore does NOT
// touch entitlement or paywall logic. A future appleProvider only needs to:
//
//  1. Handle inbound Apple App Store Server Notifications v2 at a new public
//     route (e.g. POST /billing/apple/notifications), verifying the signed JWS
//     payload — analogous to Service.HandleWebhook for Stripe.
//  2. Verify StoreKit transactions / the app receipt on POST from the app
//     (e.g. POST /billing/apple/verify) to bootstrap state at purchase time.
//  3. Upsert the normalized result into the SAME store with provider='apple'
//     (add an UpsertFromApple mirroring UpsertFromStripe; status values already
//     cover the Apple lifecycle: trialing/active/past_due/canceled).
//
// The web subscribe flow (client_secret / Payment Sheet) is intentionally
// Stripe-specific and lives on stripeProvider; Apple's purchase happens
// on-device via StoreKit, so it has no server-side "subscribe" call — only
// verification + notifications. Keeping the shared surface at the store layer
// (not a forced-generic "subscribe" method) is what keeps both providers honest.
type Provider string

const (
	ProviderStripe Provider = "stripe"
	ProviderApple  Provider = "apple"
	ProviderComp   Provider = "comp"
)

func (p Provider) String() string { return string(p) }
