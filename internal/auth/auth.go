// Package auth owns HEIMDALL's entire relationship with SESAME: supervising
// the engine child process and asking it exactly one question per request.
//
// There is no authentication or authorization logic in this package or any
// other. No password is hashed here, no session is stored here, no role name
// is compared anywhere. Everything below is transport, mapping, and failing
// closed when the answer does not arrive.
package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/d31ma/sesame/clients/go/sesame"
)

// decideTimeout bounds a single authorization question. A slow engine must
// become a 503, not an unbounded request.
const decideTimeout = 5 * time.Second

// loginTimeout bounds a whole login transaction. Argon2id is deliberately
// slow, so this is wider than a decision but still bounded.
const loginTimeout = 30 * time.Second

// Outcome is the trichotomy every caller needs and the only one it needs.
// Allow and Deny are answers; Unavailable means no answer arrived and the
// request must fail closed with a 503 rather than a 403, because a 403 would
// tell an operator their grant is wrong when the truth is the engine is down.
type Outcome int

const (
	Unavailable Outcome = iota
	Deny
	Allow
)

func (o Outcome) String() string {
	switch o {
	case Allow:
		return "allow"
	case Deny:
		return "deny"
	default:
		return "unavailable"
	}
}

// Options configures the supervised engine.
type Options struct {
	// Deployment is the directory created by `sesame init`. Its keys/
	// subdirectory holds the signing, snapshot, and sealed-secrets keys and
	// is deliberately outside the FYLO root: a FYLO snapshot alone restores
	// nothing that can verify a session.
	Deployment string
	// Binary names the engine. Empty falls back to SESAME_BINARY then PATH.
	Binary string
	// TenantID scopes every decision. HEIMDALL is self-hosted, so this is one
	// installation-wide value resolved at startup.
	TenantID string
	// Stderr receives engine diagnostics. Nil discards all but the startup
	// window, which the client captures for its own error messages.
	Stderr io.Writer
}

// Engine is a supervised SESAME child process. It is safe for concurrent use;
// the underlying client serializes frames on its own.
type Engine struct {
	mu     sync.RWMutex
	client *sesame.Client
	// apiClientID and apiClientSecret authenticate this control plane to
	// SESAME's introspection endpoint. Set by UseAPIClient at startup.
	apiClientID     string
	apiClientSecret string
	// tenantID is resolved from Options.TenantID at Start and never re-read
	// from a request, so a caller cannot ask about someone else's tenant.
	tenantID string
}

// Start launches the engine and proves it can answer before returning. A
// failure here is fatal to `heimdall serve` by design: the process must not
// come up able to serve routes it cannot authorize.
func Start(ctx context.Context, options Options) (*Engine, error) {
	if options.Deployment == "" {
		return nil, errors.New("HD0001: sesame deployment directory is required")
	}
	if options.TenantID == "" {
		return nil, errors.New("HD0002: tenant id is required")
	}
	client, err := sesame.Start(ctx, sesame.Options{
		Binary:     options.Binary,
		Deployment: options.Deployment,
		Stderr:     options.Stderr,
	})
	if err != nil {
		return nil, fmt.Errorf("HD0003: start sesame engine: %w", err)
	}
	// Refuse to run against an engine that cannot answer the two questions
	// every request depends on, rather than discovering it on the first
	// authenticated call.
	if err := client.RequireOperations(ctx, "authorize.decide", "session.verify"); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("HD0004: sesame engine is missing a required operation: %w", err)
	}
	return &Engine{client: client, tenantID: options.TenantID}, nil
}

// Adopt wraps a client the caller already started. `heimdall init` needs it
// because the tenant must be bootstrapped before a tenant id exists to
// construct an Engine with. Close on the result does close the client.
func Adopt(client *sesame.Client, tenantID string) *Engine {
	return &Engine{client: client, tenantID: tenantID}
}

// TenantID is the single tenant every decision is scoped to.
func (e *Engine) TenantID() string { return e.tenantID }

// Close stops the engine.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.client == nil {
		return nil
	}
	client := e.client
	e.client = nil
	return client.Close()
}

// Decision is what the boundary needs to both answer the request and write an
// audit record: the outcome plus SESAME's own reasoning, unmodified.
type Decision struct {
	Outcome       Outcome
	ReasonCode    string
	PolicyVersion int64
	DecisionID    string
	// Err is set for Unavailable and carries the transport failure. It is
	// never rendered to a client; it goes to logs and to the audit record.
	Err error
}

// Decide asks the one question. Every path that is not an explicit "allow"
// from a live engine returns a non-Allow outcome — an unknown decision string
// is a Deny, a transport failure is Unavailable, and a closed engine is
// Unavailable. There is no branch that returns Allow on error.
func (e *Engine) Decide(ctx context.Context, principalID string, action Action, resource string) Decision {
	if !action.Valid() {
		// A route naming an action outside the vocabulary is a programming
		// error, and the safe reading of a programming error is deny.
		return Decision{Outcome: Deny, ReasonCode: "deny_unknown_action"}
	}
	if principalID == "" || resource == "" {
		return Decision{Outcome: Deny, ReasonCode: "deny_incomplete_request"}
	}

	e.mu.RLock()
	client := e.client
	e.mu.RUnlock()
	if client == nil {
		return Decision{Outcome: Unavailable, ReasonCode: "engine_closed", Err: errors.New("sesame engine is not running")}
	}

	ctx, cancel := context.WithTimeout(ctx, decideTimeout)
	defer cancel()

	decision, err := client.Decide(ctx, sesame.DecisionRequest{
		TenantID:    e.tenantID,
		PrincipalID: principalID,
		Action:      action.String(),
		Resource:    resource,
	}, nil)
	if err != nil {
		// A ProtocolError means the engine received the question and refused
		// it. That is an answer, and the answer is no. Anything else — a
		// dead pipe, a timeout, a killed process — means no answer at all.
		var protocolError *sesame.ProtocolError
		if errors.As(err, &protocolError) {
			return Decision{Outcome: Deny, ReasonCode: protocolError.Code, Err: err}
		}
		return Decision{Outcome: Unavailable, ReasonCode: "engine_unavailable", Err: err}
	}

	outcome := Deny
	if decision.Decision == "allow" {
		outcome = Allow
	}
	return Decision{
		Outcome:       outcome,
		ReasonCode:    decision.ReasonCode,
		PolicyVersion: decision.PolicyVersion,
		DecisionID:    decision.DecisionID,
	}
}

// Ping reports whether the engine is answering. It is the readiness probe: a
// control plane that cannot authorize is not ready, however healthy its own
// goroutines are.
//
// It deliberately does not probe with a fabricated session. Repeated bogus
// session.verify calls would write failed-verification events into SESAME's
// security ledger, making the health check indistinguishable from an attack.
func (e *Engine) Ping(ctx context.Context) error {
	e.mu.RLock()
	client := e.client
	e.mu.RUnlock()
	if client == nil {
		return errors.New("HD0007: sesame engine is not running")
	}

	ctx, cancel := context.WithTimeout(ctx, decideTimeout)
	defer cancel()
	return client.Ping(ctx)
}

// VerifySession resolves a presented session to a principal. It is the only
// way a principal enters the system; nothing downstream accepts a
// caller-supplied principal id.
func (e *Engine) VerifySession(ctx context.Context, sessionID, secret string) (sesame.Session, Outcome) {
	e.mu.RLock()
	client := e.client
	e.mu.RUnlock()
	if client == nil {
		return sesame.Session{}, Unavailable
	}

	ctx, cancel := context.WithTimeout(ctx, decideTimeout)
	defer cancel()

	session, err := client.SessionVerify(ctx, sessionID, secret)
	if err != nil {
		var protocolError *sesame.ProtocolError
		if errors.As(err, &protocolError) {
			return sesame.Session{}, Deny
		}
		return sesame.Session{}, Unavailable
	}
	if session.PrincipalID == "" || session.Status != "active" {
		return sesame.Session{}, Deny
	}
	return session, Allow
}

// SeedRoles creates the four shipped role bundles, once. Re-running it is
// safe: a role whose name already exists is left alone, so `heimdall init` is
// idempotent.
func (e *Engine) SeedRoles(ctx context.Context) ([]sesame.Role, error) {
	e.mu.RLock()
	client := e.client
	e.mu.RUnlock()
	if client == nil {
		return nil, errors.New("HD0005: sesame engine is not running")
	}

	roles := make([]sesame.Role, 0, len(RoleBundles))
	for _, bundle := range RoleBundles {
		role, err := client.RoleCreate(ctx, e.tenantID, bundle.Name, permissions(bundle.Actions))
		if err != nil {
			var protocolError *sesame.ProtocolError
			// SESAME roles are immutable and tenant-unique; an already-exists
			// answer means the bundle is seeded, which is the desired end
			// state. The engine has spelled this code both "conflict" and
			// "role_exists" across preview releases, so both are accepted —
			// this is exactly the kind of drift pinning the version was meant
			// to catch, and the live re-run of init is what caught it.
			if errors.As(err, &protocolError) &&
				(protocolError.Code == "conflict" || strings.HasSuffix(protocolError.Code, "_exists")) {
				continue
			}
			return nil, fmt.Errorf("HD0006: seed role %q: %w", bundle.Name, err)
		}
		roles = append(roles, role)
	}
	return roles, nil
}

// LoginRequest is one local login attempt. Nothing in it is stored here and
// the password is never logged; it exists only to be handed to SESAME.
type LoginRequest struct {
	Namespace  string
	Identifier string
	Password   string
	// TOTP is supplied when the principal has an activated authenticator.
	TOTP     string
	Lifetime time.Duration
}

// IssuedSession is the one copy of a session secret. It is returned to the
// caller and never persisted by HEIMDALL.
type IssuedSession struct {
	SessionID   string
	Secret      string
	PrincipalID string
	ExpiresAt   string
	Assurance   string
}

// Login runs SESAME's local authentication transaction end to end.
//
// Every step is an engine call: Argon2id verification, TOTP replay
// prevention, and session issuance all happen inside SESAME. This function
// sequences those calls and translates the outcome; it makes no
// authentication decision of its own, and there is no branch that issues a
// session the engine did not issue.
func (e *Engine) Login(ctx context.Context, request LoginRequest) (IssuedSession, Outcome) {
	e.mu.RLock()
	client := e.client
	e.mu.RUnlock()
	if client == nil {
		return IssuedSession{}, Unavailable
	}

	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()

	identifier := sesame.PrincipalIdentifier{
		Namespace: request.Namespace, Value: request.Identifier,
	}

	// authn.begin succeeds whether or not the identifier resolves, so this
	// call leaks nothing about which accounts exist.
	begun, err := client.AuthenticationBegin(ctx, e.tenantID, identifier)
	if err != nil {
		return IssuedSession{}, outcomeOf(err)
	}

	if _, err := client.AuthenticationVerifyPassword(ctx, begun.TransactionID, request.Password); err != nil {
		return IssuedSession{}, outcomeOf(err)
	}

	if request.TOTP != "" {
		if _, err := client.AuthenticationVerifyTOTP(ctx, begun.TransactionID, request.TOTP); err != nil {
			return IssuedSession{}, outcomeOf(err)
		}
	}

	// Complete fails when the transaction is not satisfied, which is how a
	// principal with an activated authenticator and no code supplied is
	// refused: the check lives in the engine, not in a branch here.
	issued, err := client.AuthenticationComplete(ctx, begun.TransactionID, request.Lifetime)
	if err != nil {
		return IssuedSession{}, outcomeOf(err)
	}
	return IssuedSession{
		SessionID:   issued.SessionID,
		Secret:      issued.Secret,
		PrincipalID: issued.PrincipalID,
		ExpiresAt:   issued.ExpiresAt,
		Assurance:   issued.Assurance,
	}, Allow
}

// RevokeSession ends a session.
func (e *Engine) RevokeSession(ctx context.Context, sessionID, reason string) error {
	e.mu.RLock()
	client := e.client
	e.mu.RUnlock()
	if client == nil {
		return errors.New("HD0008: sesame engine is not running")
	}
	ctx, cancel := context.WithTimeout(ctx, decideTimeout)
	defer cancel()
	return client.SessionRevoke(ctx, sessionID, reason)
}

// outcomeOf maps an engine error onto the trichotomy. A ProtocolError is the
// engine refusing; anything else is the engine not answering.
func outcomeOf(err error) Outcome {
	var protocolError *sesame.ProtocolError
	if errors.As(err, &protocolError) {
		return Deny
	}
	return Unavailable
}
