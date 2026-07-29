package apple

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"
)

// testChain is a self-contained root -> intermediate -> leaf ECDSA hierarchy
// standing in for Apple's App Store signing chain.
type testChain struct {
	root         *x509.Certificate
	rootKey      *ecdsa.PrivateKey
	intermediate *x509.Certificate
	interKey     *ecdsa.PrivateKey
	leaf         *x509.Certificate
	leafKey      *ecdsa.PrivateKey
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return k
}

func serial(t *testing.T) *big.Int {
	t.Helper()
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	return n
}

func makeCert(t *testing.T, tmpl, parent *x509.Certificate, pub *ecdsa.PublicKey, parentKey *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, parentKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert
}

// buildTestChain creates a root CA, an intermediate signed by the root, and a
// leaf (digital-signature) signed by the intermediate — all valid for
// [now-1h, now+24h].
func buildTestChain(t *testing.T) *testChain {
	t.Helper()
	now := time.Now()

	rootKey := newKey(t)
	rootTmpl := &x509.Certificate{
		SerialNumber:          serial(t),
		Subject:               pkix.Name{CommonName: "Test Root CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	root := makeCert(t, rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey)

	interKey := newKey(t)
	interTmpl := &x509.Certificate{
		SerialNumber:          serial(t),
		Subject:               pkix.Name{CommonName: "Test Intermediate CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	inter := makeCert(t, interTmpl, root, &interKey.PublicKey, rootKey)

	leafKey := newKey(t)
	leafTmpl := &x509.Certificate{
		SerialNumber:          serial(t),
		Subject:               pkix.Name{CommonName: "Test Signing Leaf"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	leaf := makeCert(t, leafTmpl, inter, &leafKey.PublicKey, interKey)

	return &testChain{root: root, rootKey: rootKey, intermediate: inter, interKey: interKey, leaf: leaf, leafKey: leafKey}
}

func (c *testChain) rootPool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(c.root)
	return pool
}

// signJWS produces a compact ES256 JWS with the chain's x5c header, exactly
// the shape Apple emits (base64url header.payload, raw 64-byte R||S signature).
func (c *testChain) signJWS(t *testing.T, payload []byte) string {
	t.Helper()
	header := jwsHeader{
		Alg: "ES256",
		X5c: []string{
			base64.StdEncoding.EncodeToString(c.leaf.Raw),
			base64.StdEncoding.EncodeToString(c.intermediate.Raw),
		},
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	h := base64.RawURLEncoding.EncodeToString(headerJSON)
	p := base64.RawURLEncoding.EncodeToString(payload)

	digest := sha256.Sum256([]byte(h + "." + p))
	r, s, err := ecdsa.Sign(rand.Reader, c.leafKey, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return h + "." + p + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestVerifyValidChain(t *testing.T) {
	chain := buildTestChain(t)
	payload := []byte(`{"transactionId":"tx123","bundleId":"com.scoreright.app"}`)
	token := chain.signJWS(t, payload)

	got, err := Verify(token, chain.rootPool(), time.Now())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("payload mismatch: got %s", got)
	}
}

func TestVerifyRejectsWrongRoot(t *testing.T) {
	chain := buildTestChain(t)
	other := buildTestChain(t)
	token := chain.signJWS(t, []byte(`{"ok":true}`))

	if _, err := Verify(token, other.rootPool(), time.Now()); err == nil {
		t.Fatal("expected chain verification failure against a foreign root")
	}
}

func TestVerifyRejectsTamperedPayload(t *testing.T) {
	chain := buildTestChain(t)
	token := chain.signJWS(t, []byte(`{"amount":1}`))
	parts := strings.Split(token, ".")
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"amount":999}`))
	tampered := strings.Join(parts, ".")

	if _, err := Verify(tampered, chain.rootPool(), time.Now()); err == nil {
		t.Fatal("expected signature failure for tampered payload")
	}
}

func TestVerifyRejectsExpiredLeaf(t *testing.T) {
	chain := buildTestChain(t)
	token := chain.signJWS(t, []byte(`{"ok":true}`))

	// 48h ahead is past the 24h leaf validity window.
	if _, err := Verify(token, chain.rootPool(), time.Now().Add(48*time.Hour)); err == nil {
		t.Fatal("expected failure verifying after leaf expiry")
	}
}

func TestVerifyRejectsBadInputs(t *testing.T) {
	chain := buildTestChain(t)
	roots := chain.rootPool()

	cases := map[string]string{
		"empty":           "",
		"two segments":    "abc.def",
		"four segments":   "a.b.c.d",
		"bad header b64":  "!!!.e30.sig",
		"header not json": base64.RawURLEncoding.EncodeToString([]byte("nope")) + ".e30.c2ln",
	}
	for name, token := range cases {
		if _, err := Verify(token, roots, time.Now()); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func TestVerifyRejectsWrongAlg(t *testing.T) {
	chain := buildTestChain(t)
	headerJSON, _ := json.Marshal(jwsHeader{Alg: "HS256", X5c: []string{
		base64.StdEncoding.EncodeToString(chain.leaf.Raw),
	}})
	token := base64.RawURLEncoding.EncodeToString(headerJSON) + ".e30.c2ln"
	if _, err := Verify(token, chain.rootPool(), time.Now()); err == nil {
		t.Fatal("expected algorithm rejection")
	}
}

// TestDefaultRootsEmbedded proves the vendored Apple root CAs parse and are
// currently valid — the test going red is the early-warning that the vendored
// roots expired and certs/ must be refreshed from apple.com/certificateauthority.
func TestDefaultRootsEmbedded(t *testing.T) {
	pool, err := DefaultRoots()
	if err != nil {
		t.Fatalf("DefaultRoots: %v", err)
	}
	for _, der := range [][]byte{rootCAG3, rootCAG2, rootCAG1} {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatalf("parse embedded root: %v", err)
		}
		if time.Now().After(cert.NotAfter) {
			t.Errorf("embedded root %q EXPIRED at %v — refresh certs/", cert.Subject.CommonName, cert.NotAfter)
		}
		if !cert.IsCA {
			t.Errorf("embedded root %q is not a CA", cert.Subject.CommonName)
		}
	}
	if pool == nil {
		t.Fatal("nil pool")
	}
}

func TestVerifyAndDecode(t *testing.T) {
	chain := buildTestChain(t)
	tx := Transaction{
		TransactionID:         "tx1",
		OriginalTransactionID: "orig1",
		BundleID:              "com.scoreright.app",
		ProductID:             "app.scoreright.monthly.ios",
		Type:                  TransactionTypeAutoRenewable,
		Environment:           EnvironmentSandbox,
	}
	payload, _ := json.Marshal(tx)
	token := chain.signJWS(t, payload)

	var got Transaction
	if err := VerifyAndDecode(token, chain.rootPool(), time.Now(), &got); err != nil {
		t.Fatalf("VerifyAndDecode: %v", err)
	}
	if got.OriginalTransactionID != "orig1" || got.ProductID != tx.ProductID {
		t.Errorf("decoded transaction mismatch: %+v", got)
	}
}
