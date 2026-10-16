package enroll

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// HEIMDALL issues its own agent certificates rather than asking an operator
// for a PKI. Two reasons: an agent's trust anchor is the fingerprint it pinned
// at enrollment, not a public CA, so a public chain would prove nothing; and
// requiring an operator to run a CA to deploy a container is how a security
// feature ends up disabled.
//
// Every key here is written 0600 under <deployment>/keys, outside every FYLO
// root. A FYLO snapshot alone therefore restores a control plane that cannot
// authenticate any agent — which is why the DR unit is the deployment
// directory and not a root.

const (
	// caLifetime outlives any agent certificate by a wide margin; rotating it
	// invalidates every enrollment, so it is deliberately long.
	caLifetime = 10 * 365 * 24 * time.Hour
	// serverLifetime is capped at 397 days because Apple's verifier — and
	// Chrome's — reject a TLS server certificate valid for longer, with the
	// unhelpful message "certificate is not standards compliant". A five-year
	// certificate works for agents, which pin a fingerprint and skip chain
	// validation, and fails for every browser and for the CLI.
	//
	// Rotation is cheap: an agent trusts the CA, not the leaf, so a renewed
	// server certificate keeps working. Only new enrollment tokens carry the
	// new fingerprint.
	serverLifetime = 397 * 24 * time.Hour
	// renewBefore triggers regeneration while the old certificate is still
	// valid, so a control plane that is restarted occasionally never serves
	// an expired one.
	renewBefore = 30 * 24 * time.Hour
	// agentLifetime bounds a compromised agent credential. Renewal is the
	// agent re-enrolling, which is cheap.
	agentLifetime = 365 * 24 * time.Hour
)

// Files inside <deployment>/keys.
const (
	caCertFile     = "agent-ca.crt"
	caKeyFile      = "agent-ca.key"
	serverCertFile = "server.crt"
	serverKeyFile  = "server.key"
	enrollKeyFile  = "enrollment.key"
)

// Material is the control plane's key material for agent enrollment: the CA
// that signs agent certificates, the server certificate agents pin, and the
// key that signs enrollment tokens.
type Material struct {
	Dir string

	CACertificate *x509.Certificate
	CAKey         *ecdsa.PrivateKey
	Server        tls.Certificate
	// EnrollmentKey signs enrollment tokens. It is separate from the CA key
	// so that a token-signing compromise does not also mint certificates.
	EnrollmentKey []byte
}

// KeysDir is where a deployment keeps its key material.
func KeysDir(deployment string) string { return filepath.Join(deployment, "keys") }

// Ensure creates the key material if it is absent and loads it if it is
// present. It is idempotent, so `heimdall init` can run again safely.
//
// hosts are the names and addresses the control plane is reached at. They go
// into the server certificate; an agent connects by pinned fingerprint, but a
// browser still validates the name.
func Ensure(deployment string, hosts []string) (*Material, error) {
	dir := KeysDir(deployment)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("HD0340: create key directory: %w", err)
	}

	material := &Material{Dir: dir}
	if err := material.ensureCA(); err != nil {
		return nil, err
	}
	if err := material.ensureServer(hosts); err != nil {
		return nil, err
	}
	if err := material.ensureEnrollmentKey(); err != nil {
		return nil, err
	}
	return material, nil
}

// Load reads existing material and fails if any part is missing. `heimdall
// serve` uses it: a control plane that cannot authenticate agents should say
// so at startup, not on the first enrollment.
func Load(deployment string) (*Material, error) {
	dir := KeysDir(deployment)
	if _, err := os.Stat(filepath.Join(dir, caCertFile)); err != nil {
		return nil, fmt.Errorf("HD0341: no agent CA in %s; run `heimdall init`", dir)
	}
	return Ensure(deployment, nil)
}

func (m *Material) ensureCA() error {
	certPath := filepath.Join(m.Dir, caCertFile)
	keyPath := filepath.Join(m.Dir, caKeyFile)

	if certificate, key, err := readPair(certPath, keyPath); err == nil {
		m.CACertificate, m.CAKey = certificate, key
		return nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("HD0342: generate CA key: %w", err)
	}
	serial, err := serialNumber()
	if err != nil {
		return err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "HEIMDALL agent CA", Organization: []string{"HEIMDALL"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(caLifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		// The CA signs leaf certificates only; there is no intermediate tier
		// to manage and none to be abused.
		MaxPathLenZero: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("HD0342: create CA certificate: %w", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return fmt.Errorf("HD0342: parse CA certificate: %w", err)
	}

	if err := writePair(certPath, keyPath, der, key); err != nil {
		return err
	}
	m.CACertificate, m.CAKey = certificate, key
	return nil
}

func (m *Material) ensureServer(hosts []string) error {
	certPath := filepath.Join(m.Dir, serverCertFile)
	keyPath := filepath.Join(m.Dir, serverKeyFile)

	if certificate, key, err := readPair(certPath, keyPath); err == nil {
		if time.Now().Add(renewBefore).Before(certificate.NotAfter) {
			m.Server = tls.Certificate{
				Certificate: [][]byte{certificate.Raw}, PrivateKey: key, Leaf: certificate,
			}
			return nil
		}
		// Expiring or expired. Regenerating changes the fingerprint, which
		// invalidates outstanding enrollment tokens but not enrolled agents —
		// they trust the CA, not this leaf.
	}
	if len(hosts) == 0 {
		hosts = []string{"localhost", "127.0.0.1", "::1"}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("HD0343: generate server key: %w", err)
	}
	serial, err := serialNumber()
	if err != nil {
		return err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: hosts[0], Organization: []string{"HEIMDALL"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(serverLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
			continue
		}
		template.DNSNames = append(template.DNSNames, host)
	}

	// Signed by the agent CA rather than self-signed, so an operator who does
	// want to distribute one trust anchor has exactly one to distribute.
	der, err := x509.CreateCertificate(rand.Reader, template, m.CACertificate, &key.PublicKey, m.CAKey)
	if err != nil {
		return fmt.Errorf("HD0343: create server certificate: %w", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return fmt.Errorf("HD0343: parse server certificate: %w", err)
	}
	if err := writePair(certPath, keyPath, der, key); err != nil {
		return err
	}
	m.Server = tls.Certificate{
		Certificate: [][]byte{der}, PrivateKey: key, Leaf: certificate,
	}
	return nil
}

func (m *Material) ensureEnrollmentKey() error {
	path := filepath.Join(m.Dir, enrollKeyFile)
	if existing, err := os.ReadFile(path); err == nil && len(existing) >= 32 {
		m.EnrollmentKey = existing
		return nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("HD0344: generate enrollment key: %w", err)
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return fmt.Errorf("HD0344: write enrollment key: %w", err)
	}
	m.EnrollmentKey = key
	return nil
}

// ServerFingerprint is what an enrollment token carries and an agent pins.
func (m *Material) ServerFingerprint() string {
	if m.Server.Leaf == nil {
		return ""
	}
	return Fingerprint(m.Server.Leaf)
}

// ServerTLSConfig is the listener's configuration.
//
// Client certificates are *requested* and verified when presented, but not
// required: the same listener serves browsers and the CLI, which authenticate
// with a SESAME session instead. The agent routes check for a verified client
// certificate themselves, so an agent route is never reachable without one.
func (m *Material) ServerTLSConfig() *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(m.CACertificate)
	return &tls.Config{
		Certificates: []tls.Certificate{m.Server},
		ClientCAs:    pool,
		ClientAuth:   tls.VerifyClientCertIfGiven,
		MinVersion:   tls.VersionTLS12,
	}
}

// AgentIdentity is what a verified client certificate proves.
type AgentIdentity struct {
	AgentID  string
	TargetID string
}

// agentOrganization marks a certificate this CA issued to an agent, so a
// certificate issued for some other purpose cannot be replayed as one.
const agentOrganization = "HEIMDALL agent"

// IssueAgentCertificate signs a certificate signing request from an enrolling
// agent. The subject is set from the enrollment token, never from the CSR: a
// CSR is attacker-controlled, and letting it name its own target would let one
// host enroll as another.
func (m *Material) IssueAgentCertificate(csrPEM []byte, agentID, targetID string) ([]byte, error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, errors.New("HD0345: the enrollment request is not a PEM certificate request")
	}
	request, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("HD0345: parse certificate request: %w", err)
	}
	if err := request.CheckSignature(); err != nil {
		return nil, fmt.Errorf("HD0345: the certificate request is not self-signed correctly: %w", err)
	}

	serial, err := serialNumber()
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   agentID,
			Organization: []string{agentOrganization},
			// The target is carried in the subject rather than looked up, so
			// an agent's authority is decided once, at enrollment, and is
			// legible in the certificate itself.
			OrganizationalUnit: []string{targetID},
		},
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(agentLifetime),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, m.CACertificate, request.PublicKey, m.CAKey)
	if err != nil {
		return nil, fmt.Errorf("HD0345: sign agent certificate: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

// CACertificatePEM is the trust anchor an agent stores after enrollment.
func (m *Material) CACertificatePEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: m.CACertificate.Raw})
}

// IdentityOf reads the agent identity from a verified TLS connection. It
// returns false when the peer presented no certificate, presented one this CA
// did not issue for an agent, or presented one with no target.
//
// It reads only VerifiedChains. PeerCertificates holds what the client sent,
// verified or not; trusting that would accept any self-signed certificate.
func IdentityOf(state *tls.ConnectionState) (AgentIdentity, bool) {
	if state == nil || len(state.VerifiedChains) == 0 {
		return AgentIdentity{}, false
	}
	leaf := state.VerifiedChains[0][0]
	if len(leaf.Subject.Organization) == 0 || leaf.Subject.Organization[0] != agentOrganization {
		return AgentIdentity{}, false
	}
	if len(leaf.Subject.OrganizationalUnit) == 0 || leaf.Subject.OrganizationalUnit[0] == "" {
		return AgentIdentity{}, false
	}
	if leaf.Subject.CommonName == "" {
		return AgentIdentity{}, false
	}
	return AgentIdentity{
		AgentID:  leaf.Subject.CommonName,
		TargetID: leaf.Subject.OrganizationalUnit[0],
	}, true
}

// NewCertificateRequest generates an agent's key and CSR. The subject is
// ignored by the issuer — it is set from the enrollment token — so nothing
// here is trusted; the CSR exists to carry a public key.
func NewCertificateRequest(agentName string) (csrPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("HD0346: generate agent key: %w", err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: agentName},
	}, key)
	if err != nil {
		return nil, nil, fmt.Errorf("HD0346: create certificate request: %w", err)
	}
	encodedKey, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("HD0346: encode agent key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: encodedKey}),
		nil
}

// AgentTLSConfig is what an enrolled agent uses: its own certificate for mTLS,
// and the control plane verified against the CA it was given at enrollment.
func AgentTLSConfig(certPEM, keyPEM, caPEM []byte) (*tls.Config, error) {
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("HD0347: load agent certificate: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("HD0347: the stored CA certificate is not valid PEM")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{certificate},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func serialNumber() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("HD0348: generate serial number: %w", err)
	}
	return serial, nil
}

func readPair(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, err
	}
	certBlock, _ := pem.Decode(certPEM)
	keyBlock, _ := pem.Decode(keyPEM)
	if certBlock == nil || keyBlock == nil {
		return nil, nil, errors.New("HD0349: stored key material is not valid PEM")
	}
	certificate, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return certificate, key, nil
}

func writePair(certPath, keyPath string, der []byte, key *ecdsa.PrivateKey) error {
	if err := os.WriteFile(certPath,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return fmt.Errorf("HD0350: write certificate: %w", err)
	}
	encoded, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("HD0350: encode key: %w", err)
	}
	// 0600, and never inside a FYLO root.
	if err := os.WriteFile(keyPath,
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: encoded}), 0o600); err != nil {
		return fmt.Errorf("HD0350: write key: %w", err)
	}
	return nil
}

// HostsFor turns a listen address into the names a server certificate should
// carry, so `--addr 0.0.0.0:8443` does not produce a certificate valid for
// nothing.
func HostsFor(addr string, extra ...string) []string {
	hosts := []string{"localhost", "127.0.0.1", "::1"}
	host, _, err := net.SplitHostPort(addr)
	if err == nil && host != "" && host != "0.0.0.0" && host != "::" {
		hosts = append([]string{host}, hosts...)
	}
	for _, name := range extra {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			hosts = append(hosts, trimmed)
		}
	}
	return hosts
}
