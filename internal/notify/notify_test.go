package notify

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ── decidePush ─────────────────────────────────────────────

// A fixed reference instant: 2026-07-29 00:30 UTC — 19:30 in Chicago (UTC-5
// in July), inside the evening window.
var refNow = time.Date(2026, 7, 29, 0, 30, 0, 0, time.UTC)

// A device registered by the app with both notification toggles on — the
// default state. Opt-out behaviour is covered separately below.
func chicagoCandidate() PushCandidate {
	return PushCandidate{
		UserID:          42,
		Token:           "deadbeef",
		Timezone:        "America/Chicago",
		CurrentStreak:   5,
		DailyGoalTarget: 6,
		StreakOptIn:     true,
		ReengageOptIn:   true,
	}
}

func utcDate(daysAgo int) *time.Time {
	t := refNow.UTC().Truncate(24*time.Hour).AddDate(0, 0, -daysAgo)
	return &t
}

func TestDecidePushStreakAtRisk(t *testing.T) {
	c := chicagoCandidate()
	c.LastActiveDate = utcDate(0) // active today, goal unmet
	d := decidePush(c, refNow)
	if !d.send || d.title != TitleStreakRisk {
		t.Fatalf("expected streak-at-risk push, got %+v", d)
	}
	if d.body == "" || d.threadID != ThreadStreak {
		t.Errorf("bad payload: %+v", d)
	}
}

func TestDecidePushFreezeGetsReassurance(t *testing.T) {
	c := chicagoCandidate()
	c.LastActiveDate = utcDate(1)
	c.StreakFreezesOwned = 1
	d := decidePush(c, refNow)
	if !d.send || d.title != TitleFreezeSaved {
		t.Fatalf("expected freeze-reassurance push, got %+v", d)
	}
}

func TestDecidePushGoalMetDigest(t *testing.T) {
	c := chicagoCandidate()
	c.LastActiveDate = utcDate(0)
	c.DailyGoalDate = utcDate(0)
	c.DailyGoalProgress = 6
	d := decidePush(c, refNow)
	if !d.send || d.title != TitleGoalMet {
		t.Fatalf("expected goal-met digest, got %+v", d)
	}
}

func TestDecidePushStaleGoalDateIsNotMet(t *testing.T) {
	c := chicagoCandidate()
	c.LastActiveDate = utcDate(0)
	c.DailyGoalDate = utcDate(1) // yesterday's completed goal
	c.DailyGoalProgress = 6
	d := decidePush(c, refNow)
	if d.send && d.title == TitleGoalMet {
		t.Fatalf("yesterday's goal must not count as met today: %+v", d)
	}
}

func TestDecidePushReengagementDays(t *testing.T) {
	for _, tc := range []struct {
		days      int
		wantSend  bool
		wantTitle string
	}{
		{3, true, TitleReengage3},
		{7, true, TitleReengage7},
		{4, false, ""},
		{5, false, ""},
		{6, false, ""},
		{8, false, ""},
		{30, false, ""},
	} {
		c := chicagoCandidate()
		c.LastActiveDate = utcDate(tc.days)
		d := decidePush(c, refNow)
		if d.send != tc.wantSend {
			t.Errorf("day %d: send = %v, want %v (%s)", tc.days, d.send, tc.wantSend, d.reason)
		}
		if d.send && d.title != tc.wantTitle {
			t.Errorf("day %d: title = %q, want %q", tc.days, d.title, tc.wantTitle)
		}
	}
}

func TestDecidePushQuietHours(t *testing.T) {
	c := chicagoCandidate()
	c.LastActiveDate = utcDate(0)
	// 03:30 UTC = 22:30 Chicago — quiet hours.
	lateNight := time.Date(2026, 7, 29, 3, 30, 0, 0, time.UTC)
	if d := decidePush(c, lateNight); d.send {
		t.Fatalf("quiet hours must suppress pushes (reason %q)", d.reason)
	}
}

func TestDecidePushOutsideEveningWindow(t *testing.T) {
	c := chicagoCandidate()
	c.LastActiveDate = utcDate(0)
	// 18:30 UTC = 13:30 Chicago — afternoon, not the evening window.
	afternoon := time.Date(2026, 7, 28, 18, 30, 0, 0, time.UTC)
	if d := decidePush(c, afternoon); d.send {
		t.Fatalf("outside evening window must suppress pushes (reason %q)", d.reason)
	}
}

func TestDecidePushDailyCap(t *testing.T) {
	c := chicagoCandidate()
	c.LastActiveDate = utcDate(0)
	recent := refNow.Add(-2 * time.Hour)
	c.LastNotifiedAt = &recent
	if d := decidePush(c, refNow); d.send {
		t.Fatalf("daily cap violated: %+v", d)
	}
	// Cap expires after 20h — the next evening's window opens again.
	old := refNow.Add(-21 * time.Hour)
	c.LastNotifiedAt = &old
	if d := decidePush(c, refNow); !d.send {
		t.Fatalf("daily cap should have expired after 21h: %q", d.reason)
	}
}

func TestDecidePushBadTimezoneFallsBackToUTC(t *testing.T) {
	c := chicagoCandidate()
	c.Timezone = "Not/AZone"
	c.LastActiveDate = utcDate(0)
	// refNow is 00:30 UTC — outside the UTC evening window, so no send.
	if d := decidePush(c, refNow); d.send {
		t.Fatalf("UTC fallback should apply window in UTC: %+v", d)
	}
}

func TestDecidePushNeverActive(t *testing.T) {
	c := chicagoCandidate()
	c.LastActiveDate = nil
	if d := decidePush(c, refNow); d.send {
		t.Fatalf("never-active users are not pushed: %+v", d)
	}
}

func TestDecidePushShortStreakNoPressure(t *testing.T) {
	c := chicagoCandidate()
	c.LastActiveDate = utcDate(0)
	c.CurrentStreak = 1
	if d := decidePush(c, refNow); d.send {
		t.Fatalf("a 1-day streak gets no at-risk pressure push: %+v", d)
	}
}

func TestDaysInactive(t *testing.T) {
	if got := daysInactive(utcDate(0), refNow); got != 0 {
		t.Errorf("today: got %d, want 0", got)
	}
	if got := daysInactive(utcDate(3), refNow); got != 3 {
		t.Errorf("3 days ago: got %d, want 3", got)
	}
	if got := daysInactive(nil, refNow); got != -1 {
		t.Errorf("never active: got %d, want -1", got)
	}
}

// ── Per-device opt-out ─────────────────────────────────────
//
// The regression these guard: before migration 013 the app's Notification
// Settings toggles only cancelled LOCAL notifications, so a user who switched
// reminders off kept receiving server pushes.

// Each case is a candidate that WOULD be pushed, paired with the toggle that
// must suppress it.
func TestDecidePushRespectsOptOut(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(c *PushCandidate)
		wantSend bool
	}{
		{
			name: "streak at risk, practice reminders off",
			setup: func(c *PushCandidate) {
				c.LastActiveDate = utcDate(0)
				c.StreakOptIn = false
			},
		},
		{
			name: "goal-met digest, practice reminders off",
			setup: func(c *PushCandidate) {
				c.LastActiveDate = utcDate(0)
				c.DailyGoalProgress = 6
				c.DailyGoalDate = utcDate(0)
				c.StreakOptIn = false
			},
		},
		{
			name: "re-engagement, re-engagement off",
			setup: func(c *PushCandidate) {
				c.LastActiveDate = utcDate(3)
				c.ReengageOptIn = false
			},
		},
		{
			// Categories are independent: muting one must not mute the other.
			name: "re-engagement still sends when only practice reminders are off",
			setup: func(c *PushCandidate) {
				c.LastActiveDate = utcDate(3)
				c.StreakOptIn = false
			},
			wantSend: true,
		},
		{
			name: "streak still sends when only re-engagement is off",
			setup: func(c *PushCandidate) {
				c.LastActiveDate = utcDate(0)
				c.ReengageOptIn = false
			},
			wantSend: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := chicagoCandidate()
			tc.setup(&c)
			d := decidePush(c, refNow)
			if d.send != tc.wantSend {
				t.Fatalf("send = %v, want %v (reason: %s)", d.send, tc.wantSend, d.reason)
			}
			// The underlying engagement logic must be unchanged — a suppressed
			// push is suppressed by the toggle, not by a broken decision.
			if !tc.wantSend && !decidePushForState(c, refNow).send {
				t.Error("expected the engagement logic itself to still want to send")
			}
		})
	}
}

// A device with both toggles off is filtered out in SQL, but the decision
// layer must not depend on that.
func TestDecidePushBothOptedOutNeverSends(t *testing.T) {
	for daysAgo := 0; daysAgo <= 8; daysAgo++ {
		c := chicagoCandidate()
		c.StreakOptIn = false
		c.ReengageOptIn = false
		c.LastActiveDate = utcDate(daysAgo)
		if d := decidePush(c, refNow); d.send {
			t.Errorf("daysAgo=%d: sent despite both toggles off: %+v", daysAgo, d)
		}
	}
}

// ── Config ─────────────────────────────────────────────────

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("APNS_KEY_ID", "")
	t.Setenv("APNS_TEAM_ID", "")
	t.Setenv("APNS_PRIVATE_KEY", "")
	t.Setenv("APNS_BUNDLE_ID", "")
	t.Setenv("APNS_USE_SANDBOX", "")
	cfg := LoadConfig()
	if cfg.Enabled() {
		t.Error("push must be disabled with no credentials")
	}
	if cfg.BundleID != DefaultAPNSBundleID {
		t.Errorf("BundleID = %q, want %q", cfg.BundleID, DefaultAPNSBundleID)
	}
	if cfg.UseSandbox {
		t.Error("UseSandbox must default to false")
	}
}

func TestLoadConfigUnescapesPEM(t *testing.T) {
	t.Setenv("APNS_PRIVATE_KEY", `-----BEGIN x-----\nabc\ndef\n-----END x-----`)
	cfg := LoadConfig()
	if cfg.PrivateKey != "-----BEGIN x-----\nabc\ndef\n-----END x-----" {
		t.Errorf("literal \\n escapes were not unfolded: %q", cfg.PrivateKey)
	}
}

// ── Sender (provider-token JWT) ────────────────────────────

func testSenderConfig(t *testing.T) (Config, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	return Config{
		KeyID:      "KEYID12345",
		TeamID:     "TEAMID6789",
		PrivateKey: pemStr,
		BundleID:   DefaultAPNSBundleID,
	}, key
}

func TestNewSenderAndProviderToken(t *testing.T) {
	cfg, key := testSenderConfig(t)
	sender, err := NewSender(cfg)
	if err != nil {
		t.Fatalf("NewSender: %v", err)
	}

	now := time.Now()
	tokenStr, err := sender.providerToken(now)
	if err != nil {
		t.Fatalf("providerToken: %v", err)
	}

	// The JWT must parse against the corresponding PUBLIC key and carry the
	// APNs-required claims (iss = team id, kid header = key id).
	parsed, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodES256 {
			t.Fatalf("unexpected alg: %v", token.Header["alg"])
		}
		return &key.PublicKey, nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("provider token did not verify: %v", err)
	}
	if kid, _ := parsed.Header["kid"].(string); kid != cfg.KeyID {
		t.Errorf("kid = %q, want %q", kid, cfg.KeyID)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if iss, _ := claims["iss"].(string); iss != cfg.TeamID {
		t.Errorf("iss = %q, want %q", iss, cfg.TeamID)
	}

	// Within the reuse window the SAME token is returned (APNs rejects
	// too-frequent re-mints).
	again, err := sender.providerToken(now.Add(10 * time.Minute))
	if err != nil {
		t.Fatalf("providerToken (cached): %v", err)
	}
	if again != tokenStr {
		t.Error("expected cached token reuse within 50 minutes")
	}
}

func TestNewSenderRejectsBadKeys(t *testing.T) {
	if _, err := NewSender(Config{PrivateKey: "not pem"}); err == nil {
		t.Error("expected PEM decode failure")
	}
	// Valid PEM but not a key.
	bogus := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("nope")}))
	if _, err := NewSender(Config{PrivateKey: bogus}); err == nil {
		t.Error("expected key parse failure")
	}
}

func TestWorkerDisabledWithoutCredentials(t *testing.T) {
	w, err := NewWorker(LoadConfig(), nil)
	if err != nil {
		t.Fatalf("NewWorker without credentials: %v", err)
	}
	if w.sender != nil {
		t.Error("sender must be nil when push is disabled")
	}
}
