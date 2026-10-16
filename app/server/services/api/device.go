package api

import (
	"net/http"
	"strings"
)

// Device authorization routes. Like login and logout they sit outside the
// authorized table — start and token are how a session comes to exist, and
// approve acts on the caller's own session by proving it to SESAME. No
// business decision is made here, so there is nothing for Decide to decide.

// deviceStart begins the grant. Unauthenticated: the CLI holds nothing yet.
func (s *Server) deviceStart(w http.ResponseWriter, r *http.Request) {
	if s.Engine == nil || !s.DeviceEnabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"code": "HD0503", "message": "device authorization is not configured on this control plane",
		})
		return
	}
	grant, err := s.Engine.DeviceStart(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, grant)
}

type deviceTokenRequest struct {
	DeviceCode string `json:"device_code"`
}

// deviceToken is the CLI's poll. 200 carries the session; 202 means keep
// polling; 401 is every terminal refusal, deliberately indistinct.
func (s *Server) deviceToken(w http.ResponseWriter, r *http.Request) {
	if s.Engine == nil || !s.DeviceEnabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"code": "HD0503", "message": "device authorization is not configured on this control plane",
		})
		return
	}
	var body deviceTokenRequest
	if !decodeBody(w, r, &body) {
		return
	}
	if body.DeviceCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400", "message": "device_code is required",
		})
		return
	}

	result, err := s.Engine.DevicePoll(r.Context(), body.DeviceCode)
	switch {
	case err != nil:
		s.fail(w, err)
	case result.Pending:
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "authorization_pending"})
	case result.Denied:
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"code": "HD0401", "message": "the device was not approved",
		})
	default:
		writeJSON(w, http.StatusOK, result.Tokens)
	}
}

type deviceRefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// deviceRefresh trades a refresh token for a new pair. The refresh token
// rotates on every use; the old one is dead the moment this returns.
func (s *Server) deviceRefresh(w http.ResponseWriter, r *http.Request) {
	if s.Engine == nil || !s.DeviceEnabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"code": "HD0503", "message": "device authorization is not configured on this control plane",
		})
		return
	}
	var body deviceRefreshRequest
	if !decodeBody(w, r, &body) {
		return
	}
	if body.RefreshToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400", "message": "refresh_token is required",
		})
		return
	}
	tokens, err := s.Engine.DeviceRefresh(r.Context(), body.RefreshToken)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"code": "HD0401", "message": "the refresh token is not valid",
		})
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

type deviceCodeRequest struct {
	UserCode string `json:"user_code"`
}

// bearerParts reads the caller's session from the Authorization header. The
// approval routes prove it to SESAME rather than resolving it here — this is
// the one flow where the session itself is the thing being spent.
func bearerParts(r *http.Request) (sessionID, secret string, ok bool) {
	raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	sessionID, secret, found := strings.Cut(raw, ".")
	return sessionID, secret, found && sessionID != "" && secret != ""
}

// deviceLookup shows an approver what they are approving. It requires a
// session — the approval page is behind login — but reveals only what SESAME
// chooses to return for a live code.
func (s *Server) deviceLookup(w http.ResponseWriter, r *http.Request) {
	if s.Engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"code": "HD0503", "message": "authorization engine unavailable",
		})
		return
	}
	if _, _, ok := bearerParts(r); !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"code": "HD0401", "message": "a session is required",
		})
		return
	}
	var body deviceCodeRequest
	if !decodeBody(w, r, &body) {
		return
	}
	result, err := s.Engine.DeviceLookup(r.Context(), body.UserCode)
	if err != nil {
		// Uniform: wrong, expired, and never-existed all answer the same.
		writeJSON(w, http.StatusNotFound, map[string]string{
			"code": "HD0404", "message": "no pending device with that code",
		})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// deviceApprove binds the caller's session to the waiting device. The
// session is proved to SESAME, not merely presented: a forged bearer fails
// there, which is the only place it could fail honestly.
func (s *Server) deviceApprove(w http.ResponseWriter, r *http.Request) {
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
	var body deviceCodeRequest
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.Engine.DeviceApprove(r.Context(), body.UserCode, sessionID, secret); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"code": "HD0404", "message": "no pending device with that code",
		})
		return
	}
	if s.Audit != nil {
		// A person just attached their identity to a device. That is a
		// security event on par with enrollment.
		s.Audit.Record(Entry{
			Action: "auth:device_approve", Resource: "tenant:self",
			Outcome: "allow", ReasonCode: "device_approved",
			Method: r.Method, Path: r.URL.Path,
		})
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

// deviceDeny records a refusal so the CLI stops polling promptly rather than
// timing out ten minutes later.
func (s *Server) deviceDeny(w http.ResponseWriter, r *http.Request) {
	if s.Engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"code": "HD0503", "message": "authorization engine unavailable",
		})
		return
	}
	if _, _, ok := bearerParts(r); !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"code": "HD0401", "message": "a session is required",
		})
		return
	}
	var body deviceCodeRequest
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.Engine.DeviceDeny(r.Context(), body.UserCode); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"code": "HD0404", "message": "no pending device with that code",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "denied"})
}
