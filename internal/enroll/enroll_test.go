package enroll_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/d31ma/heimdall/internal/enroll"
)

func newIssuer(t *testing.T) *enroll.Issuer {
	t.Helper()
	return &enroll.Issuer{
		Key:         []byte("a-32-byte-enrollment-signing-key!"),
		URL:         "https://heimdall.example:8443",
		Fingerprint: strings.Repeat("ab", 32),
	}
}

func TestRoundTrip(t *testing.T) {
	issuer := newIssuer(t)
	encoded, err := issuer.Issue("tgt-1", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	token, err := issuer.Verify(encoded)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if token.TargetID != "tgt-1" {
		t.Errorf("target = %q", token.TargetID)
	}
	if token.Fingerprint != strings.Repeat("ab", 32) {
		t.Errorf("fingerprint = %q", token.Fingerprint)
	}
	if token.URL != issuer.URL {
		t.Errorf("url = %q", token.URL)
	}
}

// TestIssueRefusesWithoutAFingerprint: a token with no pin cannot protect the
// agent's first connection, which is the only reason the token exists.
func TestIssueRefusesWithoutAFingerprint(t *testing.T) {
	issuer := newIssuer(t)
	issuer.Fingerprint = ""
	if _, err := issuer.Issue("tgt-1", time.Hour); err == nil {
		t.Fatal("issued a token with no certificate fingerprint")
	}
}

func TestIssueRefusesAWeakKey(t *testing.T) {
	issuer := newIssuer(t)
	issuer.Key = []byte("short")
	if _, err := issuer.Issue("tgt-1", time.Hour); err == nil {
		t.Fatal("issued a token with a weak key")
	}
}

func TestTamperingIsRejected(t *testing.T) {
	issuer := newIssuer(t)
	encoded, err := issuer.Issue("tgt-1", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// Flipping any byte of the encoded token must invalidate it.
	corrupted := []byte(encoded)
	corrupted[len(corrupted)/2] ^= 0x01
	if _, err := issuer.Verify(string(corrupted)); err == nil {
		t.Fatal("a tampered token verified")
	}

	for _, bogus := range []string{"", "not-base64!!", strings.Repeat("A", 40000)} {
		if _, err := issuer.Verify(bogus); err == nil {
			t.Errorf("verified %q", bogus[:min(len(bogus), 20)])
		}
	}
}

// TestATokenFromAnotherControlPlaneIsRejected: a valid token minted elsewhere
// must not enrol an agent here, even if the key were shared.
func TestATokenFromAnotherControlPlaneIsRejected(t *testing.T) {
	elsewhere := newIssuer(t)
	elsewhere.URL = "https://attacker.example:8443"
	encoded, err := elsewhere.Issue("tgt-1", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := newIssuer(t).Verify(encoded); err == nil {
		t.Fatal("a token for a different control plane verified")
	}
}

func TestADifferentKeyIsRejected(t *testing.T) {
	issuer := newIssuer(t)
	encoded, _ := issuer.Issue("tgt-1", time.Hour)

	other := newIssuer(t)
	other.Key = []byte("a-different-32-byte-signing-key!!")
	if _, err := other.Verify(encoded); err == nil {
		t.Fatal("a token signed with another key verified")
	}
}

func TestExpiryIsEnforced(t *testing.T) {
	issued := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	issuer := newIssuer(t)
	issuer.Now = func() time.Time { return issued }

	encoded, err := issuer.Issue("tgt-1", time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := issuer.Verify(encoded); err != nil {
		t.Fatalf("a fresh token was refused: %v", err)
	}

	issuer.Now = func() time.Time { return issued.Add(2 * time.Minute) }
	if _, err := issuer.Verify(encoded); err == nil {
		t.Fatal("an expired token verified")
	}
}

// TestEveryRefusalLooksTheSame: the caller is unauthenticated, so a distinct
// message per failure mode would tell an attacker which part of a guess was
// right.
func TestEveryRefusalLooksTheSame(t *testing.T) {
	issuer := newIssuer(t)
	expired := newIssuer(t)
	expired.Now = func() time.Time { return time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC) }
	stale, _ := expired.Issue("tgt-1", time.Second)

	other := newIssuer(t)
	other.Key = []byte("a-different-32-byte-signing-key!!")
	forged, _ := other.Issue("tgt-1", time.Hour)

	messages := map[string]bool{}
	for _, candidate := range []string{stale, forged, "garbage", ""} {
		if _, err := issuer.Verify(candidate); err != nil {
			messages[err.Error()] = true
		}
	}
	if len(messages) != 1 {
		t.Fatalf("refusals are distinguishable, which leaks which guess was close: %v", messages)
	}
}

// selfSigned builds a certificate the way a private control plane would.
func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "heimdall.local"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"127.0.0.1", "localhost"},
		IPAddresses:  nil,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// TestPinnedConnectionAcceptsTheRealServerAndRefusesAnImpostor is the whole
// point of the design: two self-signed servers, neither chaining to a public
// CA, distinguished only by the pin.
func TestPinnedConnectionAcceptsTheRealServerAndRefusesAnImpostor(t *testing.T) {
	real := selfSigned(t)
	impostor := selfSigned(t)

	fingerprint, err := enroll.FingerprintOf(real)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}

	pinned, err := enroll.PinnedTLSConfig(fingerprint)
	if err != nil {
		t.Fatalf("pin: %v", err)
	}

	serve := func(certificate tls.Certificate) *httptest.Server {
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ok"))
		}))
		server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}}
		server.StartTLS()
		t.Cleanup(server.Close)
		return server
	}

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: pinned}}

	response, err := client.Get(serve(real).URL)
	if err != nil {
		t.Fatalf("the pinned client refused the real control plane: %v", err)
	}
	_ = response.Body.Close()

	if _, err := client.Get(serve(impostor).URL); err == nil {
		t.Fatal("the pinned client accepted an impostor presenting a different certificate")
	}
}

func TestPinnedTLSConfigRejectsAMalformedFingerprint(t *testing.T) {
	for _, bogus := range []string{"", "abc", strings.Repeat("z", 64)} {
		if _, err := enroll.PinnedTLSConfig(bogus); err == nil {
			t.Errorf("accepted fingerprint %q", bogus)
		}
	}
}

func TestFingerprintFormsAreEquivalent(t *testing.T) {
	plain := strings.Repeat("ab", 32)
	if !enroll.Equal(plain, "SHA256="+strings.ToUpper(plain)) {
		t.Error("an operator-pasted sha256= form was not recognised")
	}
	if enroll.Equal(plain, strings.Repeat("cd", 32)) {
		t.Error("two different fingerprints compared equal")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
