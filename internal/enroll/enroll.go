// Package enroll issues and verifies agent enrollment tokens.
//
// An agent's first connection is the one moment it has no way to tell the
// real control plane from an impostor: it has a URL and nothing else. A
// normal token does not fix that — an attacker who intercepts the first
// connection receives the token and can replay it.
//
// The token therefore carries the control plane's own certificate
// fingerprint. The agent pins that fingerprint before it will send the token,
// so a machine-in-the-middle presenting a different certificate is refused
// with nothing disclosed. This is Portainer's edge-key idea, which gets it
// right, and it is the reason enrollment is a Phase 1 item rather than a
// detail of the agent.
package enroll

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Token is what an operator copies onto a host. It is a single opaque string
// so there is nothing for them to get wrong, and it decodes to this struct.
type Token struct {
	// Version lets a future format be rejected clearly rather than
	// misparsed.
	Version int `json:"v"`
	// URL is the control plane the agent connects back to. The agent never
	// accepts a redirect away from it.
	URL string `json:"url"`
	// TargetID names the target this agent serves. One agent, one target.
	TargetID string `json:"target"`
	// Fingerprint is the SHA-256 of the control plane's TLS certificate, hex
	// encoded. The agent pins it before sending Secret.
	Fingerprint string `json:"fp"`
	// Secret proves the token was issued by this control plane. It is a
	// keyed digest, not a stored credential: nothing needs to be looked up to
	// verify it, so enrollment works while the store is still being read.
	Secret string `json:"s"`
	// ExpiresAt bounds the window. An enrollment token that never expires is
	// a permanent credential sitting in someone's terminal history.
	ExpiresAt time.Time `json:"exp"`
}

// maxTokenBytes bounds a decoded token. It comes from a host that has not
// authenticated yet.
const maxTokenBytes = 4 << 10

// tokenVersion is the current format.
const tokenVersion = 1

// DefaultLifetime is how long an enrollment token is good for. Long enough to
// copy onto a host, short enough that a leaked one is usually already dead.
const DefaultLifetime = time.Hour

// Issuer mints and verifies tokens. Key is the enrollment signing key; it
// lives with the deployment's other key material, outside every FYLO root.
type Issuer struct {
	Key         []byte
	URL         string
	Fingerprint string
	// Now is injectable so expiry can be tested without sleeping.
	Now func() time.Time
}

func (i *Issuer) now() time.Time {
	if i.Now != nil {
		return i.Now()
	}
	return time.Now().UTC()
}

// Issue mints a token for one target.
func (i *Issuer) Issue(targetID string, lifetime time.Duration) (string, error) {
	switch {
	case len(i.Key) < 32:
		return "", errors.New("HD0330: the enrollment key must be at least 32 bytes")
	case i.URL == "":
		return "", errors.New("HD0330: a control-plane URL is required")
	case i.Fingerprint == "":
		return "", errors.New(
			"HD0330: a certificate fingerprint is required; a token without one cannot protect " +
				"the agent's first connection, which is the only reason this token exists")
	case targetID == "":
		return "", errors.New("HD0330: a target id is required")
	}
	if lifetime <= 0 {
		lifetime = DefaultLifetime
	}

	token := Token{
		Version:     tokenVersion,
		URL:         i.URL,
		TargetID:    targetID,
		Fingerprint: normalizeFingerprint(i.Fingerprint),
		ExpiresAt:   i.now().Add(lifetime).UTC().Truncate(time.Second),
	}
	token.Secret = i.sign(token)

	encoded, err := json.Marshal(token)
	if err != nil {
		return "", fmt.Errorf("HD0331: encode enrollment token: %w", err)
	}
	if len(encoded) > maxTokenBytes {
		return "", errors.New("HD0331: enrollment token is too large")
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

// Verify decodes and authenticates a token. It returns the target the agent
// is enrolling for.
//
// Every failure returns the same error. A token is presented by an
// unauthenticated host, and distinguishing "expired" from "forged" from "not
// for this control plane" tells an attacker which part of a guess was right.
func (i *Issuer) Verify(encoded string) (Token, error) {
	refused := errors.New("HD0332: the enrollment token is not valid")

	if len(encoded) > maxTokenBytes*2 {
		return Token{}, refused
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(raw) > maxTokenBytes {
		return Token{}, refused
	}

	var token Token
	if err := json.Unmarshal(raw, &token); err != nil {
		return Token{}, refused
	}
	if token.Version != tokenVersion || token.TargetID == "" || token.Secret == "" {
		return Token{}, refused
	}
	// A token minted for a different control plane must not be accepted here,
	// even if the key somehow matched.
	if token.URL != i.URL || token.Fingerprint != normalizeFingerprint(i.Fingerprint) {
		return Token{}, refused
	}
	if i.now().After(token.ExpiresAt) {
		return Token{}, refused
	}

	// Constant time: a timing-variable comparison of a signature is a forgery
	// oracle, and this one is reachable by an unauthenticated caller.
	expected := i.sign(token)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(token.Secret)) != 1 {
		return Token{}, refused
	}
	return token, nil
}

// sign is a keyed digest over every field that must not be tampered with. The
// secret itself is excluded, which is what makes it verifiable without being
// stored.
func (i *Issuer) sign(token Token) string {
	mac := hmac.New(sha256.New, i.Key)
	// Length-prefixed fields, so no combination of values can be rearranged
	// into a different token with the same digest.
	for _, field := range []string{
		fmt.Sprint(token.Version),
		token.URL,
		token.TargetID,
		token.Fingerprint,
		token.ExpiresAt.UTC().Format(time.RFC3339),
	} {
		_, _ = fmt.Fprintf(mac, "%d:%s|", len(field), field)
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Fingerprint is the SHA-256 of a certificate's DER bytes, hex encoded. It is
// what an operator sees, what the token carries, and what the agent pins.
func Fingerprint(certificate *x509.Certificate) string {
	sum := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(sum[:])
}

// FingerprintOf reads the fingerprint from a loaded TLS certificate.
func FingerprintOf(certificate tls.Certificate) (string, error) {
	if len(certificate.Certificate) == 0 {
		return "", errors.New("HD0333: the TLS certificate carries no chain")
	}
	parsed, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return "", fmt.Errorf("HD0333: parse leaf certificate: %w", err)
	}
	return Fingerprint(parsed), nil
}

// normalizeFingerprint accepts the forms an operator may paste: bare hex,
// colon-separated, uppercase, or prefixed with sha256=. Lowercasing happens
// first so an uppercase prefix is still recognised.
func normalizeFingerprint(value string) string {
	lowered := strings.ToLower(strings.TrimSpace(value))
	return strings.NewReplacer(":", "", " ", "", "sha256=", "").Replace(lowered)
}

// PinnedTLSConfig is what an agent uses for its first connection.
//
// It sets InsecureSkipVerify to disable the *chain* check and supplies its own
// verification instead: the presented leaf must hash to the pinned
// fingerprint. That is stronger than PKI here, not weaker — the control plane
// is typically self-signed on a private network, where a public CA chain
// proves nothing and pinning proves exactly the right thing.
func PinnedTLSConfig(fingerprint string) (*tls.Config, error) {
	pinned := normalizeFingerprint(fingerprint)
	if len(pinned) != 64 {
		return nil, fmt.Errorf("HD0334: %q is not a SHA-256 certificate fingerprint", fingerprint)
	}
	if _, err := hex.DecodeString(pinned); err != nil {
		return nil, fmt.Errorf("HD0334: %q is not hex", fingerprint)
	}

	return &tls.Config{
		// Disabled deliberately, and replaced below. Leaving the default
		// verification on would additionally require a chain to a public CA,
		// which a private control plane will not have.
		InsecureSkipVerify: true, //nolint:gosec // the pin below is the verification
		MinVersion:         tls.VersionTLS12,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("HD0335: the control plane presented no certificate")
			}
			presented := sha256.Sum256(rawCerts[0])
			if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(presented[:])), []byte(pinned)) != 1 {
				return fmt.Errorf(
					"HD0335: the control plane's certificate does not match the pinned fingerprint; "+
						"expected %s, got %s — refusing to send the enrollment token",
					pinned, hex.EncodeToString(presented[:]))
			}
			return nil
		},
	}, nil
}

// Equal reports whether two fingerprints denote the same certificate,
// tolerating the colon-separated and sha256= forms an operator may paste.
func Equal(a, b string) bool {
	return hmac.Equal([]byte(normalizeFingerprint(a)), []byte(normalizeFingerprint(b)))
}
