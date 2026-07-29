// Package apple verifies StoreKit 2 / App Store Server Notifications V2 signed
// payloads (JWS) entirely offline, using Apple's public root CA certificates
// vendored under certs/.
//
// WHY offline JWS verification instead of the App Store Server API:
// the App Store Server API requires an issuer id, key id, and a .p8 private
// key generated in App Store Connect — credentials that did not exist yet when
// this was written. Verifying the x5c certificate chain inside every Apple
// signed payload against Apple's public roots gives the same authenticity
// guarantee (the payload really came from Apple) with ZERO secrets to manage.
//
// TRADEOFFS vs the App Store Server API (documented for the operator):
//   - No revocation/CRL/OCSP checking of the x5c chain. For short-lived StoreKit
//     signing certs this is an accepted, industry-standard simplification.
//   - Apple root rotation: the vendored roots (certs/*.cer, valid to 2035/2039)
//     must eventually be refreshed. Apple publishes them at
//     https://www.apple.com/certificateauthority/ — drop new .cer files into
//     certs/ and redeploy; no code change needed.
//   - Refund lookups / consumption / extend-subscription / transaction history
//     queries are NOT possible offline. If those are ever needed, add the App
//     Store Server API with APPLE_ISSUER_ID / APPLE_KEY_ID /
//     APPLE_PRIVATE_KEY env vars (mirroring how STRIPE_* secrets are wired)
//     alongside — not instead of — this verifier.
package apple

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// Vendored Apple root CA certificates (public, downloaded from
// https://www.apple.com/certificateauthority/). StoreKit 2 JWS chains root at
// "Apple Root CA - G3"; the older roots are kept so chains issued during a
// transition still verify.
//
//go:embed certs/AppleRootCA-G3.cer
var rootCAG3 []byte

//go:embed certs/AppleRootCA-G2.cer
var rootCAG2 []byte

//go:embed certs/AppleIncRootCertificate.cer
var rootCAG1 []byte

// Errors returned by Verify. Wrapped with context, so errors.Is works.
var (
	ErrMalformedJWS   = errors.New("malformed JWS")
	ErrUnexpectedAlg  = errors.New("unexpected JWS algorithm")
	ErrBadCertificate = errors.New("certificate chain verification failed")
	ErrBadSignature   = errors.New("JWS signature verification failed")
)

// DefaultRoots returns a cert pool containing the vendored Apple root CAs.
func DefaultRoots() (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	for i, der := range [][]byte{rootCAG3, rootCAG2, rootCAG1} {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("parse embedded Apple root %d: %w", i, err)
		}
		pool.AddCert(cert)
	}
	return pool, nil
}

// jwsHeader is the protected header of an Apple signed payload. x5c carries
// the signing certificate chain (leaf first), base64-DER encoded.
type jwsHeader struct {
	Alg string   `json:"alg"`
	X5c []string `json:"x5c"`
}

// ParseHeader decodes the JWS protected header without verifying anything.
// Exported for tests and debugging; callers should use Verify.
func ParseHeader(token string) (*jwsHeader, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: expected 3 segments, got %d", ErrMalformedJWS, len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: header not base64url: %v", ErrMalformedJWS, err)
	}
	var h jwsHeader
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, fmt.Errorf("%w: header not JSON: %v", ErrMalformedJWS, err)
	}
	return &h, nil
}

// Verify authenticates an Apple signed payload (compact JWS, ES256, x5c chain)
// against the given root pool and returns the decoded payload bytes.
//
// Steps: parse -> alg check -> x5c chain verification against roots (valid at
// `at`) -> ES256 signature check with the LEAF certificate's public key.
// The payload claims themselves (bundleId, expiry, ...) are validated by the
// caller — this function only proves the payload is genuinely from Apple.
func Verify(token string, roots *x509.CertPool, at time.Time) ([]byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: expected 3 segments, got %d", ErrMalformedJWS, len(parts))
	}

	header, err := ParseHeader(token)
	if err != nil {
		return nil, err
	}
	if header.Alg != "ES256" {
		return nil, fmt.Errorf("%w: %q (want ES256)", ErrUnexpectedAlg, header.Alg)
	}
	if len(header.X5c) == 0 {
		return nil, fmt.Errorf("%w: empty x5c certificate chain", ErrBadCertificate)
	}

	certs := make([]*x509.Certificate, 0, len(header.X5c))
	for i, b64 := range header.X5c {
		der, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("%w: x5c[%d] not base64: %v", ErrBadCertificate, i, err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("%w: x5c[%d] unparseable: %v", ErrBadCertificate, i, err)
		}
		certs = append(certs, cert)
	}

	leaf := certs[0]
	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   at,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadCertificate, err)
	}

	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%w: leaf public key is not ECDSA", ErrBadSignature)
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("%w: signature not base64url: %v", ErrMalformedJWS, err)
	}
	// JWS ES256 signatures are the raw 64-byte R||S concatenation (P-256).
	if len(sig) != 64 {
		return nil, fmt.Errorf("%w: ES256 signature must be 64 bytes, got %d", ErrBadSignature, len(sig))
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])

	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(pub, digest[:], r, s) {
		return nil, ErrBadSignature
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: payload not base64url: %v", ErrMalformedJWS, err)
	}
	return payload, nil
}

// VerifyAndDecode is Verify plus JSON decoding into out.
func VerifyAndDecode(token string, roots *x509.CertPool, at time.Time, out interface{}) error {
	payload, err := Verify(token, roots, at)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("decode JWS payload JSON: %w", err)
	}
	return nil
}
