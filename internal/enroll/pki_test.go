package enroll_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/d31ma/heimdall/internal/enroll"
)

func ensure(t *testing.T) (*enroll.Material, string) {
	t.Helper()
	deployment := t.TempDir()
	material, err := enroll.Ensure(deployment, []string{"heimdall.local", "127.0.0.1"})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	return material, deployment
}

func TestEnsureIsIdempotent(t *testing.T) {
	material, deployment := ensure(t)
	first := material.ServerFingerprint()

	// `heimdall init` runs again; regenerating would invalidate every
	// outstanding enrollment token and every issued agent certificate.
	again, err := enroll.Ensure(deployment, []string{"heimdall.local"})
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if again.ServerFingerprint() != first {
		t.Fatal("re-running init rotated the server certificate")
	}
	if string(again.EnrollmentKey) != string(material.EnrollmentKey) {
		t.Fatal("re-running init rotated the enrollment key")
	}
}

func TestKeysAreNotWorldReadable(t *testing.T) {
	_, deployment := ensure(t)
	for _, name := range []string{"agent-ca.key", "server.key", "enrollment.key"} {
		info, err := os.Stat(filepath.Join(enroll.KeysDir(deployment), name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s has mode %o, want 0600", name, mode)
		}
	}
}

func TestKeysLiveOutsideEveryFYLORoot(t *testing.T) {
	_, deployment := ensure(t)
	// A FYLO snapshot alone must not be able to restore a control plane that
	// can authenticate agents. That is why the DR unit is the deployment
	// directory, and this asserts the layout it depends on.
	keys := enroll.KeysDir(deployment)
	if strings.Contains(keys, "fylo-root") {
		t.Fatalf("key material is inside a FYLO root: %s", keys)
	}
}

func TestLoadFailsBeforeInit(t *testing.T) {
	if _, err := enroll.Load(t.TempDir()); err == nil {
		t.Fatal("loaded key material from an uninitialised deployment")
	}
}

// TestIssuedCertificateAuthenticatesAnAgent is the whole mTLS path: a CSR from
// an agent becomes a certificate that a server built from ServerTLSConfig
// verifies, and that IdentityOf reads the target from.
func TestIssuedCertificateAuthenticatesAnAgent(t *testing.T) {
	material, _ := ensure(t)

	csr, key, err := enroll.NewCertificateRequest("agent-alpha")
	if err != nil {
		t.Fatalf("csr: %v", err)
	}
	certificate, err := material.IssueAgentCertificate(csr, "agt-1", "tgt-1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	var seen enroll.AgentIdentity
	var authenticated bool
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, authenticated = enroll.IdentityOf(r.TLS)
		w.WriteHeader(http.StatusOK)
	}))
	server.TLS = material.ServerTLSConfig()
	server.StartTLS()
	defer server.Close()

	agentTLS, err := enroll.AgentTLSConfig(certificate, key, material.CACertificatePEM())
	if err != nil {
		t.Fatalf("agent tls: %v", err)
	}
	agentTLS.ServerName = "127.0.0.1"

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: agentTLS}}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("agent request: %v", err)
	}
	_ = response.Body.Close()

	if !authenticated {
		t.Fatal("the server did not authenticate the agent")
	}
	// The subject comes from the enrollment token, never from the CSR.
	if seen.AgentID != "agt-1" || seen.TargetID != "tgt-1" {
		t.Fatalf("identity = %+v", seen)
	}
}

// TestACSRCannotChooseItsOwnTarget is the reason the subject is overwritten: a
// CSR is attacker-controlled, and a host that could name its own target could
// enroll as another.
func TestACSRCannotChooseItsOwnTarget(t *testing.T) {
	material, _ := ensure(t)

	csr, _, err := enroll.NewCertificateRequest("i-am-the-production-target")
	if err != nil {
		t.Fatalf("csr: %v", err)
	}
	issued, err := material.IssueAgentCertificate(csr, "agt-2", "tgt-staging")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	certificate := parsePEM(t, issued)
	if certificate.Subject.CommonName != "agt-2" {
		t.Errorf("common name = %q, want the id the control plane chose", certificate.Subject.CommonName)
	}
	if certificate.Subject.OrganizationalUnit[0] != "tgt-staging" {
		t.Errorf("target = %v, want the target the token named", certificate.Subject.OrganizationalUnit)
	}
}

// TestAForeignCertificateIsNotAnAgent: a certificate from another CA, even a
// well-formed one, must not authenticate.
func TestAForeignCertificateIsNotAnAgent(t *testing.T) {
	real, _ := ensure(t)
	other, _ := ensure(t)

	csr, key, err := enroll.NewCertificateRequest("agent-alpha")
	if err != nil {
		t.Fatalf("csr: %v", err)
	}
	foreign, err := other.IssueAgentCertificate(csr, "agt-1", "tgt-1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := enroll.IdentityOf(r.TLS); !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	server.TLS = real.ServerTLSConfig()
	server.StartTLS()
	defer server.Close()

	agentTLS, err := enroll.AgentTLSConfig(foreign, key, other.CACertificatePEM())
	if err != nil {
		t.Fatalf("agent tls: %v", err)
	}
	// Trust the real server so the failure is the *client* certificate being
	// rejected, not the server's.
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(real.CACertificatePEM())
	agentTLS.RootCAs = pool
	agentTLS.ServerName = "127.0.0.1"

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: agentTLS}}
	response, err := client.Get(server.URL)
	if err != nil {
		// The handshake itself refused it, which is the strongest outcome.
		return
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a certificate from another CA authenticated: status %d", response.StatusCode)
	}
}

// TestNoClientCertificateIsNotAnAgent: the listener also serves browsers and
// the CLI, which authenticate with a session. They must never read as agents.
func TestNoClientCertificateIsNotAnAgent(t *testing.T) {
	material, _ := ensure(t)

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := enroll.IdentityOf(r.TLS); ok {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	server.TLS = material.ServerTLSConfig()
	server.StartTLS()
	defer server.Close()

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(material.CACertificatePEM())
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12},
	}}

	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("a session-authenticated client could not connect at all: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("a client with no certificate was treated as an agent")
	}
}

func TestIssueRejectsMalformedRequests(t *testing.T) {
	material, _ := ensure(t)
	for _, bogus := range [][]byte{
		nil,
		[]byte("not pem"),
		[]byte("-----BEGIN CERTIFICATE-----\nnope\n-----END CERTIFICATE-----\n"),
	} {
		if _, err := material.IssueAgentCertificate(bogus, "agt-1", "tgt-1"); err == nil {
			t.Errorf("signed a malformed request: %q", bogus)
		}
	}
}

func TestHostsForNeverProducesACertificateValidForNothing(t *testing.T) {
	hosts := enroll.HostsFor("0.0.0.0:8443", "heimdall.example")
	joined := strings.Join(hosts, " ")
	if !strings.Contains(joined, "localhost") || !strings.Contains(joined, "heimdall.example") {
		t.Fatalf("hosts = %v", hosts)
	}
	for _, host := range hosts {
		if host == "0.0.0.0" {
			t.Fatal("a wildcard bind address became a certificate name")
		}
	}
}

// parsePEM decodes a single certificate for assertions.
func parsePEM(t *testing.T, encoded []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(encoded)
	if block == nil {
		t.Fatal("not PEM")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return certificate
}

// TestServerCertificateIsAcceptableToStrictVerifiers pins the 398-day rule.
// Apple's verifier and Chrome reject a longer-lived TLS server certificate
// with "certificate is not standards compliant", which costs an afternoon to
// diagnose because nothing says the lifetime is the problem.
func TestServerCertificateIsAcceptableToStrictVerifiers(t *testing.T) {
	material, _ := ensure(t)

	leaf := material.Server.Leaf
	if leaf == nil {
		t.Fatal("no server certificate")
	}
	lifetime := leaf.NotAfter.Sub(leaf.NotBefore)
	if lifetime > 398*24*time.Hour {
		t.Fatalf("server certificate is valid for %.0f days; strict verifiers reject anything over 398",
			lifetime.Hours()/24)
	}
	// The CA is not subject to that rule and should outlive the leaf, or
	// every renewal would need a new trust anchor distributed.
	caLifetime := material.CACertificate.NotAfter.Sub(material.CACertificate.NotBefore)
	if caLifetime <= lifetime {
		t.Fatalf("the CA (%.0f days) does not outlive the server certificate (%.0f days)",
			caLifetime.Hours()/24, lifetime.Hours()/24)
	}
}

// TestARenewedServerCertificateStillWorksForEnrolledAgents: rotation must not
// require re-enrolling every host.
func TestARenewedServerCertificateStillWorksForEnrolledAgents(t *testing.T) {
	material, deployment := ensure(t)

	csr, key, err := enroll.NewCertificateRequest("agent-alpha")
	if err != nil {
		t.Fatalf("csr: %v", err)
	}
	certificate, err := material.IssueAgentCertificate(csr, "agt-1", "tgt-1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	ca := material.CACertificatePEM()

	// Force a renewal by deleting the leaf and re-running Ensure.
	if err := os.Remove(filepath.Join(enroll.KeysDir(deployment), "server.crt")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := os.Remove(filepath.Join(enroll.KeysDir(deployment), "server.key")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	renewed, err := enroll.Ensure(deployment, []string{"heimdall.local", "127.0.0.1"})
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if renewed.ServerFingerprint() == material.ServerFingerprint() {
		t.Fatal("the renewal did not change the certificate")
	}

	// The agent's stored CA is unchanged, so its config still builds and the
	// new leaf chains to the same anchor.
	agentTLS, err := enroll.AgentTLSConfig(certificate, key, ca)
	if err != nil {
		t.Fatalf("agent tls after renewal: %v", err)
	}
	agentTLS.ServerName = "127.0.0.1"

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := enroll.IdentityOf(r.TLS); !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	server.TLS = renewed.ServerTLSConfig()
	server.StartTLS()
	defer server.Close()

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: agentTLS}}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("an enrolled agent could not reach the renewed control plane: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d after renewal", response.StatusCode)
	}
}
