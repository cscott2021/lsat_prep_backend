package notify

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// APNs endpoints (HTTP/2; Go's default transport negotiates h2 via ALPN).
const (
	apnsProductionHost = "https://api.push.apple.com"
	apnsSandboxHost    = "https://api.sandbox.push.apple.com"
)

// providerTokenLifetime is how long a cached provider JWT is reused. APNs
// requires the token be refreshed at least hourly; 50 minutes leaves margin.
const providerTokenLifetime = 50 * time.Minute

// ErrTokenGone marks APNs responses that mean the device token will never
// work again (400 BadDeviceToken, 410 Unregistered) — the caller prunes it.
var ErrTokenGone = errors.New("device token unregistered")

// Sender delivers pushes over APNs HTTP/2 with provider-token (JWT) auth.
// Construct only when Config.Enabled(); the zero value is never used.
type Sender struct {
	cfg    Config
	key    *ecdsa.PrivateKey
	client *http.Client

	mu          sync.Mutex
	cachedToken string
	tokenIssued time.Time
}

// NewSender parses the .p8 key and prepares the HTTP client. The HTTP/2
// requirement is satisfied by the standard transport (ALPN negotiates h2 with
// Apple's endpoints).
func NewSender(cfg Config) (*Sender, error) {
	block, _ := pem.Decode([]byte(cfg.PrivateKey))
	if block == nil {
		return nil, fmt.Errorf("APNS_PRIVATE_KEY is not valid PEM")
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse APNs .p8 key: %w", err)
	}
	key, ok := keyAny.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("APNs .p8 key is not an ECDSA key (got %T)", keyAny)
	}
	return &Sender{
		cfg:    cfg,
		key:    key,
		client: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// providerToken returns a cached ES256 JWT (iss=TeamID, kid=KeyID), minting a
// new one when the cache is older than providerTokenLifetime.
func (s *Sender) providerToken(now time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cachedToken != "" && now.Sub(s.tokenIssued) < providerTokenLifetime {
		return s.cachedToken, nil
	}
	claims := jwt.MapClaims{
		"iss": s.cfg.TeamID,
		"iat": now.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["kid"] = s.cfg.KeyID
	signed, err := token.SignedString(s.key)
	if err != nil {
		return "", fmt.Errorf("sign APNs provider token: %w", err)
	}
	s.cachedToken = signed
	s.tokenIssued = now
	return signed, nil
}

// Payload is one alert push.
type Payload struct {
	Title string
	Body  string
	// ThreadID collapses repeat sends of the same kind on the device lock
	// screen (Duolingo-style: the reminder replaces, not stacks).
	ThreadID string
}

// apnsBody is the wire format APNs expects.
type apnsBody struct {
	Aps apnsAps `json:"aps"`
}
type apnsAps struct {
	Alert    apnsAlert `json:"alert"`
	Sound    string    `json:"sound"`
	ThreadID string    `json:"thread-id,omitempty"`
}
type apnsAlert struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Send delivers one push to a device token. ErrTokenGone is returned for
// permanently-dead tokens (prune them); other failures are transient and the
// caller may retry on a later tick.
func (s *Sender) Send(ctx context.Context, deviceToken string, p Payload) error {
	jwtToken, err := s.providerToken(time.Now())
	if err != nil {
		return err
	}

	host := apnsProductionHost
	if s.cfg.UseSandbox {
		host = apnsSandboxHost
	}
	bodyBytes, err := json.Marshal(apnsBody{Aps: apnsAps{
		Alert:    apnsAlert{Title: p.Title, Body: p.Body},
		Sound:    "default",
		ThreadID: p.ThreadID,
	}})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		host+"/3/device/"+deviceToken, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "bearer "+jwtToken)
	req.Header.Set("apns-topic", s.cfg.BundleID)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("content-type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("apns request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil
	}

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	reason := apnsReason(respBody)
	switch resp.StatusCode {
	case http.StatusBadRequest, http.StatusGone:
		// 400 BadDeviceToken / 410 Unregistered — prune.
		return fmt.Errorf("%w: status %d (%s)", ErrTokenGone, resp.StatusCode, reason)
	default:
		return fmt.Errorf("apns status %d (%s)", resp.StatusCode, reason)
	}
}

// apnsReason extracts Apple's {"reason":"..."} error detail for logs.
func apnsReason(body []byte) string {
	var parsed struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Reason != "" {
		return parsed.Reason
	}
	return string(body)
}
