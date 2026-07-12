package billing

import (
	stripe "github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/webhook"
)

// constructEvent verifies the Stripe-Signature header against the signing secret
// and returns the parsed event. Wraps the SDK so the service does not import the
// webhook package directly.
func constructEvent(payload []byte, signature, secret string) (stripe.Event, error) {
	return webhook.ConstructEvent(payload, signature, secret)
}

// invoiceSubscriptionID extracts the subscription id an invoice belongs to, or ""
// for one-off (non-subscription) invoices.
func invoiceSubscriptionID(inv *stripe.Invoice) string {
	if inv == nil || inv.Subscription == nil {
		return ""
	}
	return inv.Subscription.ID
}
