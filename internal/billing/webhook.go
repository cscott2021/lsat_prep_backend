package billing

import (
	stripe "github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/webhook"
)

// constructEvent verifies the Stripe-Signature header against the signing secret
// and returns the parsed event. Wraps the SDK so the service does not import the
// webhook package directly.
//
// IgnoreAPIVersionMismatch is intentional: stripe-go pins an expected API
// version and otherwise REJECTS any event whose version differs, which would
// make every webhook fail (payer never reconciled) if the account's / endpoint's
// API version isn't an exact match. The fields we read (id, status, customer,
// items[].price, period end, cancel flag) are stable across recent API versions,
// so tolerating the version is strictly safer than dropping the event.
func constructEvent(payload []byte, signature, secret string) (stripe.Event, error) {
	return webhook.ConstructEventWithOptions(payload, signature, secret,
		webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
}

// invoiceSubscriptionID extracts the subscription id an invoice belongs to, or ""
// for one-off (non-subscription) invoices.
func invoiceSubscriptionID(inv *stripe.Invoice) string {
	if inv == nil || inv.Subscription == nil {
		return ""
	}
	return inv.Subscription.ID
}
