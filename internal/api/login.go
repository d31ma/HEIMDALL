package api

import (
	"net/http"
	"time"

	"github.com/d31ma/heimdall/internal/auth"
)

// The login routes are the only unauthenticated mutating surface, and they
// contain no authentication logic: every step is a call into SESAME, which
// hashes with Argon2id, tracks the transaction, prevents TOTP replay, and
// issues the session. HEIMDALL forwards inputs and returns the result.
//
// Failures are deliberately uniform. SESAME's authn.begin succeeds whether or
// not the identifier resolves, so its result never reveals which accounts
// exist; returning a distinct message here would give back exactly what the
// engine was careful not to disclose.

type loginRequest struct {
	// Namespace defaults to "username". SESAME scopes identifiers by
	// namespace so an email login and a username login cannot collide.
	Namespace  string `json:"namespace,omitempty"`
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
	// TOTP is supplied when the principal has an activated authenticator.
	TOTP string `json:"totp,omitempty"`
}

type loginResponse struct {
	SessionID     string `json:"session_id"`
	SessionSecret string `json:"session_secret"`
	PrincipalID   string `json:"principal_id"`
	ExpiresAt     string `json:"expires_at"`
	Assurance     string `json:"assurance"`
}

const sessionLifetime = 12 * time.Hour

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.Engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"code": "HD0503", "message": "authorization engine unavailable",
		})
		return
	}

	var body loginRequest
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Identifier == "" || body.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400", "message": "identifier and password are required",
		})
		return
	}
	namespace := body.Namespace
	if namespace == "" {
		namespace = "username"
	}

	issued, outcome := s.Engine.Login(r.Context(), auth.LoginRequest{
		Namespace: namespace, Identifier: body.Identifier,
		Password: body.Password, TOTP: body.TOTP, Lifetime: sessionLifetime,
	})
	switch outcome {
	case auth.Unavailable:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"code": "HD0503", "message": "authorization engine unavailable",
		})
	case auth.Deny:
		// One message for every failure mode: wrong password, unknown
		// identifier, missing or wrong TOTP, suspended principal.
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"code": "HD0401", "message": "authentication failed",
		})
	default:
		writeJSON(w, http.StatusOK, loginResponse{
			SessionID:     issued.SessionID,
			SessionSecret: issued.Secret,
			PrincipalID:   issued.PrincipalID,
			ExpiresAt:     issued.ExpiresAt,
			Assurance:     issued.Assurance,
		})
	}
}

// logout revokes the presented session. It is authenticated only by the
// session it revokes, so a stolen token can be burned by whoever holds it —
// which is the desired property.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if s.Engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"code": "HD0503", "message": "authorization engine unavailable",
		})
		return
	}
	sessionID, secret, ok := bearerParts(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"code": "HD0401", "message": "a session is required",
		})
		return
	}
	if _, outcome := s.Engine.VerifySession(r.Context(), sessionID, secret); outcome != auth.Allow {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"code": "HD0401", "message": "session is not valid",
		})
		return
	}
	if err := s.Engine.RevokeSession(r.Context(), sessionID, "logout"); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"code": "HD0503", "message": "could not revoke the session",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}
