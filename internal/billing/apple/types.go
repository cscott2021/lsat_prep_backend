package apple

// StoreKit 2 / App Store Server Notifications V2 payload types. Only the
// fields ScoreRight actually consumes are modeled; unknown JSON fields are
// ignored on decode. Field names match Apple's JSON exactly (camelCase).
//
// Reference: Apple docs "JWSTransactionDecodedPayload",
// "JWSRenewalInfoDecodedPayload", "App Store Server Notifications V2".

// TransactionType is the `type` field of a signed transaction.
const TransactionTypeAutoRenewable = "Auto-Renewable Subscription"

// OfferType values from JWSTransactionDecodedPayload.offerType.
const (
	OfferTypeIntroductory = 1 // free trial / intro offer -> maps to 'trialing'
	OfferTypePromotional  = 2
	OfferTypeOfferCode    = 3
)

// Environment values from the `environment` claim.
const (
	EnvironmentSandbox    = "Sandbox"
	EnvironmentProduction = "Production"
)

// Transaction is the decoded JWSTransactionDecodedPayload.
type Transaction struct {
	TransactionID         string `json:"transactionId"`
	OriginalTransactionID string `json:"originalTransactionId"`
	BundleID              string `json:"bundleId"`
	ProductID             string `json:"productId"`
	Type                  string `json:"type"`
	Environment           string `json:"environment"`
	// PurchaseDate / ExpiresDate are milliseconds since the Unix epoch.
	PurchaseDate int64 `json:"purchaseDate"`
	ExpiresDate  int64 `json:"expiresDate"`
	// OfferType is present when the purchase used an offer (1 = intro/trial).
	OfferType        int    `json:"offerType"`
	Storefront       string `json:"storefront"`
	SignedDate       int64  `json:"signedDate"`
	RevocationDate   int64  `json:"revocationDate"`
	RevocationReason int    `json:"revocationReason"`
}

// RenewalInfo is the decoded JWSRenewalInfoDecodedPayload (auto-renew state).
type RenewalInfo struct {
	OriginalTransactionID string `json:"originalTransactionId"`
	AutoRenewProductID    string `json:"autoRenewProductId"`
	ProductID             string `json:"productId"`
	// AutoRenewStatus: 1 = will renew, 0 = user turned off renewal.
	AutoRenewStatus int `json:"autoRenewStatus"`
	// ExpirationIntent is set when the subscription expired: 1 = user canceled,
	// 2 = billing error, 3 = price increase declined, 4 = product unavailable.
	ExpirationIntent int   `json:"expirationIntent"`
	SignedDate       int64 `json:"signedDate"`
}

// NotificationBody is the outer JSON Apple POSTs to the notifications
// endpoint: a single signedPayload JWS.
type NotificationBody struct {
	SignedPayload string `json:"signedPayload"`
}

// Notification is the decoded responseBodyV2DecodedPayload.
type Notification struct {
	NotificationType string `json:"notificationType"`
	Subtype          string `json:"subtype"`
	NotificationUUID string `json:"notificationUUID"`
	Version          string `json:"version"`
	SignedDate       int64  `json:"signedDate"`
	// Data is present for transaction-related notification types.
	Data *NotificationData `json:"data"`
}

// NotificationData carries the signed transaction + renewal JWSs.
type NotificationData struct {
	BundleID              string `json:"bundleId"`
	Environment           string `json:"environment"`
	SignedTransactionInfo string `json:"signedTransactionInfo"`
	SignedRenewalInfo     string `json:"signedRenewalInfo"`
}

// Notification type constants (subset relevant to subscriptions).
const (
	NotifSubscribed             = "SUBSCRIBED"
	NotifDidRenew               = "DID_RENEW"
	NotifDidChangeRenewalStatus = "DID_CHANGE_RENEWAL_STATUS"
	NotifDidFailToRenew         = "DID_FAIL_TO_RENEW"
	NotifExpired                = "EXPIRED"
	NotifRefund                 = "REFUND"
	NotifRevoke                 = "REVOKE"
	NotifGracePeriodExpired     = "GRACE_PERIOD_EXPIRED"
	NotifTest                   = "TEST"
)

// Subtype constants.
const (
	SubtypeGracePeriod       = "GRACE_PERIOD"
	SubtypeAutoRenewEnabled  = "AUTO_RENEW_ENABLED"
	SubtypeAutoRenewDisabled = "AUTO_RENEW_DISABLED"
)
