// Package notify implements server-side engagement push: APNs delivery and a
// daily worker that reaches users whose app is CLOSED (the on-device local
// notifications in the Flutter app only fire from an installed schedule).
//
// Everything here no-ops cleanly when APNs credentials are absent (staging /
// dev): LoadConfig reports Enabled() == false, the worker logs one line and
// idles, and token registration still works so go-live is config-only.
package notify

import (
	"os"
	"strings"
)

// Config holds APNs provider-token configuration, sourced from the
// environment (SSM-backed in ECS) — mirroring how billing.LoadConfig sources
// STRIPE_*. Absent keys DISABLE push gracefully, never crash.
type Config struct {
	// KeyID is the 10-char id of the .p8 APNs Auth Key (Apple Developer portal
	// → Certificates, Identifiers & Profiles → Keys). APNS_KEY_ID.
	KeyID string
	// TeamID is the 10-char Apple Developer team id. APNS_TEAM_ID.
	TeamID string
	// PrivateKey is the .p8 contents (PEM, ES256). APNS_PRIVATE_KEY may carry
	// the PEM inline (with literal \n escapes, as SSM parameters often do) or
	// a path to a mounted file — see LoadConfig.
	PrivateKey string
	// BundleID is the APNs topic. APNS_BUNDLE_ID, default com.scoreright.app.
	BundleID string
	// UseSandbox targets api.sandbox.push.apple.com (TestFlight/dev builds)
	// instead of production. APNS_USE_SANDBOX ("true"/"1"/"yes").
	UseSandbox bool
}

// DefaultAPNSBundleID matches the iOS bundle id configured in Xcode.
const DefaultAPNSBundleID = "com.scoreright.app"

// LoadConfig reads APNS_* from the environment. It never fails: missing keys
// simply disable the sender (Enabled() == false).
func LoadConfig() Config {
	key := os.Getenv("APNS_PRIVATE_KEY")
	if key == "" {
		if path := os.Getenv("APNS_PRIVATE_KEY_PATH"); path != "" {
			if b, err := os.ReadFile(path); err == nil {
				key = string(b)
			}
		}
	}
	// SSM/env carriage often flattens PEM newlines into literal \n sequences.
	key = strings.ReplaceAll(key, `\n`, "\n")

	return Config{
		KeyID:      os.Getenv("APNS_KEY_ID"),
		TeamID:     os.Getenv("APNS_TEAM_ID"),
		PrivateKey: key,
		BundleID:   envDefault("APNS_BUNDLE_ID", DefaultAPNSBundleID),
		UseSandbox: parseTruthy(os.Getenv("APNS_USE_SANDBOX")),
	}
}

// Enabled reports whether the APNs sender can operate: all three credential
// parts are required (there is no partial mode — a missing .p8 means no push).
func (c Config) Enabled() bool {
	return c.KeyID != "" && c.TeamID != "" && c.PrivateKey != ""
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}
