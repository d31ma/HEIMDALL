package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/d31ma/sesame/clients/go/sesame"
)

// Device authorization (RFC 8628) for `heimdall login --device`: the CLI
// shows a short code, a person approves it in the web UI where they are
// already signed in, and the CLI receives a session. The point is that the
// password is typed into a browser and never into a terminal — a terminal is
// where shoulder-surfing, shell history, and SSH jump hosts live.
//
// As everywhere else, nothing here decides anything. SESAME generates the
// code, bounds the guessing, times the grant out, and enforces that approval
// proves a live session rather than naming a principal. These methods
// forward and translate.

// DeviceClientName is the OIDC client `heimdall init` registers for the
// device grant. One client, confidential — and that is not a contradiction:
// the "device" in the OAuth sense is the control plane itself, which starts
// the grant, polls the token endpoint, and introspects at the boundary. The
// CLI only ever talks to HEIMDALL's own routes and holds nothing but the
// resulting tokens, so the client secret never leaves the deployment
// directory. Confidential is also what makes introspection work: SESAME
// introspects a token only for the client it was issued to.
const DeviceClientName = "heimdall-cli"

// OIDCClients is what init persists: the client id and the only copy of its
// secret SESAME will ever return. It lives beside the TLS keys, 0600.
type OIDCClients struct {
	DeviceClientID     string `json:"device_client_id"`
	DeviceClientSecret string `json:"device_client_secret"`
}

// UseOIDCClients arms the device grant and the boundary's token path.
// Without it, only sessions authenticate — a control plane without its OIDC
// credential fails closed for token holders rather than guessing.
func (e *Engine) UseOIDCClients(clients OIDCClients) {
	e.apiClientID = clients.DeviceClientID
	e.apiClientSecret = clients.DeviceClientSecret
}

// VerifyBearer resolves either credential a caller may hold: a session pair
// ("ses_x.secret", from password login) or an OAuth access token (a JWT, from
// the device grant). Both end at SESAME; the only logic here is telling the
// two shapes apart, and a JWT's two dots against a session's one make that
// unambiguous.
func (e *Engine) VerifyBearer(ctx context.Context, bearer string) (sesame.Session, Outcome) {
	if strings.Count(bearer, ".") == 2 {
		return e.verifyAccessToken(ctx, bearer)
	}
	sessionID, secret, found := strings.Cut(bearer, ".")
	if !found || sessionID == "" || secret == "" {
		return sesame.Session{}, Deny
	}
	return e.VerifySession(ctx, sessionID, secret)
}

func (e *Engine) verifyAccessToken(ctx context.Context, token string) (sesame.Session, Outcome) {
	if e.client == nil {
		return sesame.Session{}, Unavailable
	}
	if e.apiClientID == "" || e.apiClientSecret == "" {
		// No introspection credential means no way to check, and fail closed
		// is the only honest answer.
		return sesame.Session{}, Deny
	}
	result, err := e.client.Introspect(ctx, e.apiClientID, e.apiClientSecret, token)
	if err != nil {
		return sesame.Session{}, Unavailable
	}
	if !result.Active || result.Subject == "" || result.TenantID != e.tenantID {
		return sesame.Session{}, Deny
	}
	// The introspection names the session the token was bound to at
	// approval; revoking that session already kills the token, because
	// SESAME checks it. What comes back here is the principal the boundary
	// authorizes.
	return sesame.Session{
		ID: result.SessionID, TenantID: result.TenantID, PrincipalID: result.Subject,
	}, Allow
}

// EnsureDeviceClient registers the device-grant client, or verifies the one
// a previous init registered. The result is persisted by init because the
// secret is returned exactly once — there is no list operation to find it
// again, which is deliberate on SESAME's part.
func (e *Engine) EnsureDeviceClient(ctx context.Context, stored OIDCClients) (OIDCClients, error) {
	if e.client == nil {
		return OIDCClients{}, fmt.Errorf("HD0150: no engine")
	}
	if stored.DeviceClientID != "" {
		// A re-run of init. Verify the stored client still exists rather
		// than trusting a config file that may have outlived its deployment.
		if _, err := e.client.ClientGet(ctx, stored.DeviceClientID); err != nil {
			return OIDCClients{}, fmt.Errorf("HD0150: the stored device client %s no longer exists: %w",
				stored.DeviceClientID, err)
		}
		return stored, nil
	}
	// The device grant never redirects, but registration requires at least
	// one URI. The loopback address is RFC 8252's answer for native apps: it
	// can never leave the machine the CLI runs on.
	registration, err := e.client.ClientRegister(ctx,
		e.tenantID, DeviceClientName, "confidential",
		[]string{"http://127.0.0.1/callback"}, []string{"openid", "offline_access"}, "first_party", nil)
	if err != nil {
		return OIDCClients{}, fmt.Errorf("HD0150: register the device client: %w", err)
	}
	return OIDCClients{
		DeviceClientID:     registration.Client.ID,
		DeviceClientSecret: registration.Secret,
	}, nil
}

// DeviceGrant is what the CLI needs to run the flow.
type DeviceGrant struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int64  `json:"expires_in"`
	Interval        int64  `json:"interval"`
}

// DeviceStart begins the grant.
func (e *Engine) DeviceStart(ctx context.Context) (DeviceGrant, error) {
	if e.client == nil {
		return DeviceGrant{}, fmt.Errorf("HD0151: no engine")
	}
	clientID := e.apiClientID
	if clientID == "" {
		return DeviceGrant{}, fmt.Errorf("HD0151: no device client is configured")
	}
	// offline_access asks for a refresh token: the access token lives five
	// minutes, and a CLI that had to be re-approved every five minutes would
	// train people back onto passwords in terminals.
	raw, err := e.client.DeviceAuthorizationStart(ctx, clientID, []string{"openid", "offline_access"})
	if err != nil {
		return DeviceGrant{}, fmt.Errorf("HD0151: start device authorization: %w", err)
	}
	return DeviceGrant{
		DeviceCode:      stringField(raw, "device_code"),
		UserCode:        stringField(raw, "user_code"),
		VerificationURI: stringField(raw, "verification_uri"),
		ExpiresIn:       intField(raw, "expires_in"),
		Interval:        intField(raw, "interval"),
	}, nil
}

// DeviceLookup resolves a typed user code for the approval page: what client
// is asking, and whether the code is still live. The error for a bad code is
// SESAME's, uniform across wrong, expired, and never-existed.
func (e *Engine) DeviceLookup(ctx context.Context, userCode string) (map[string]any, error) {
	if e.client == nil {
		return nil, fmt.Errorf("HD0152: no engine")
	}
	return e.client.DeviceAuthorizationLookup(ctx, e.tenantID, normalizeUserCode(userCode))
}

// DeviceApprove binds the caller's session to the waiting device. The session
// is proved to SESAME, not named: a caller that could merely assert a
// principal could attach any device to anyone.
func (e *Engine) DeviceApprove(ctx context.Context, userCode, sessionID, sessionSecret string) error {
	if e.client == nil {
		return fmt.Errorf("HD0153: no engine")
	}
	_, err := e.client.DeviceAuthorizationApprove(ctx,
		e.tenantID, normalizeUserCode(userCode), sessionID, sessionSecret)
	if err != nil {
		return fmt.Errorf("HD0153: approve device: %w", err)
	}
	return nil
}

// DeviceDeny records a refusal, so the polling CLI stops promptly instead of
// timing out.
func (e *Engine) DeviceDeny(ctx context.Context, userCode string) error {
	if e.client == nil {
		return fmt.Errorf("HD0154: no engine")
	}
	if _, err := e.client.DeviceAuthorizationDeny(ctx, e.tenantID, normalizeUserCode(userCode)); err != nil {
		return fmt.Errorf("HD0154: deny device: %w", err)
	}
	return nil
}

// DeviceTokens is what an approved device holds: an access token the
// boundary introspects, and a refresh token to replace it. There is no
// session secret — the session belongs to the approver's browser, and the
// tokens are bound to it on SESAME's side.
type DeviceTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int64  `json:"expires_in"`
}

// DevicePollResult is one answer to the CLI's poll.
type DevicePollResult struct {
	// Pending invites another poll. Everything else is final.
	Pending bool
	// Denied covers refusal, expiry, and never-existed — SESAME deliberately
	// collapses them so the token endpoint cannot be used to probe codes.
	Denied bool
	Tokens DeviceTokens
}

// DevicePoll exchanges a device code for tokens, once. The caller owns the
// polling cadence; SESAME owns the outcome.
func (e *Engine) DevicePoll(ctx context.Context, deviceCode string) (DevicePollResult, error) {
	if e.client == nil {
		return DevicePollResult{}, fmt.Errorf("HD0155: no engine")
	}

	var tokens DeviceTokens
	err := e.client.Request(ctx, "oidc.token", map[string]any{
		"grant_type":    "urn:ietf:params:oauth:grant-type:device_code",
		"device_code":   deviceCode,
		"client_id":     e.apiClientID,
		"client_secret": e.apiClientSecret,
	}, &tokens)
	if err != nil {
		message := err.Error()
		switch {
		case strings.Contains(message, "authorization_pending"), strings.Contains(message, "slow_down"):
			return DevicePollResult{Pending: true}, nil
		case strings.Contains(message, "access_denied"), strings.Contains(message, "expired_token"):
			return DevicePollResult{Denied: true}, nil
		}
		return DevicePollResult{}, fmt.Errorf("HD0155: device token exchange: %w", err)
	}
	return DevicePollResult{Tokens: tokens}, nil
}

// DeviceRefresh trades a refresh token for a new pair. SESAME rotates the
// refresh token on every use, so the returned one replaces what was sent.
func (e *Engine) DeviceRefresh(ctx context.Context, refreshToken string) (DeviceTokens, error) {
	if e.client == nil {
		return DeviceTokens{}, fmt.Errorf("HD0157: no engine")
	}
	var tokens DeviceTokens
	if err := e.client.Request(ctx, "oidc.token", map[string]any{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     e.apiClientID,
		"client_secret": e.apiClientSecret,
	}, &tokens); err != nil {
		return DeviceTokens{}, fmt.Errorf("HD0157: refresh: %w", err)
	}
	return tokens, nil
}

// normalizeUserCode forgives what a human does to a short code: lowercase it,
// add spaces, drop the dash. SESAME stores one canonical spelling.
func normalizeUserCode(code string) string {
	cleaned := strings.ToUpper(strings.TrimSpace(code))
	cleaned = strings.ReplaceAll(cleaned, " ", "")
	return cleaned
}

func stringField(m map[string]any, key string) string {
	value, _ := m[key].(string)
	return value
}

func intField(m map[string]any, key string) int64 {
	if value, ok := m[key].(float64); ok {
		return int64(value)
	}
	return 0
}

// SCIMCall is one SCIM resource operation the host received from an identity
// provider. The token is the IdP's, authenticated by SESAME per call.
type SCIMCall struct {
	Resource string // "Users" or "Groups"
	Method   string
	ID       string
	Token    string
	Body     string
}

// SCIM forwards one call to the matching engine operation. HEIMDALL never
// parses the SCIM body; a host that "understood" it would be a second
// implementation of a spec the engine already implements.
func (e *Engine) SCIM(ctx context.Context, call SCIMCall) (map[string]any, error) {
	if e.client == nil {
		return nil, fmt.Errorf("HD0160: no engine")
	}
	users := strings.EqualFold(call.Resource, "Users")
	if !users && !strings.EqualFold(call.Resource, "Groups") {
		return nil, fmt.Errorf("HD0160: unknown SCIM resource %q", call.Resource)
	}

	switch {
	case call.Method == "POST" && users:
		return e.client.SCIMUserCreate(ctx, call.Token, call.Body)
	case call.Method == "GET" && users && call.ID != "":
		return e.client.SCIMUserGet(ctx, call.Token, call.ID)
	case call.Method == "GET" && users:
		return e.client.SCIMUserList(ctx, call.Token, "", 1, 100)
	case call.Method == "PATCH" && users:
		return e.client.SCIMUserPatch(ctx, call.Token, call.ID, call.Body)
	case call.Method == "DELETE" && users:
		return e.client.SCIMUserDeprovision(ctx, call.Token, call.ID)
	case call.Method == "POST":
		return e.client.SCIMGroupCreate(ctx, call.Token, call.Body)
	case call.Method == "GET" && call.ID != "":
		return e.client.SCIMGroupGet(ctx, call.Token, call.ID)
	case call.Method == "GET":
		return e.client.SCIMGroupList(ctx, call.Token, "", 1, 100)
	case call.Method == "PATCH":
		return e.client.SCIMGroupPatch(ctx, call.Token, call.ID, call.Body)
	}
	return nil, fmt.Errorf("HD0160: unsupported SCIM method %s", call.Method)
}

// KnownBundle reports whether a role name is one of the shipped bundles.
func KnownBundle(name string) bool {
	for _, bundle := range RoleBundles {
		if bundle.Name == name {
			return true
		}
	}
	return false
}

// GrantRoleToGroup creates a project-scoped role from a shipped bundle and
// grants it to a SESAME group. The role is the bundle's actions bounded to
// one project's subtree — "operator, but only over project:alpha:*" — which
// is exactly what a directory-group mapping means.
//
// existingRoleID reuses a role a previous attempt created (SESAME roles are
// immutable and offer no lookup by name, so the caller remembers the id).
func (e *Engine) GrantRoleToGroup(ctx context.Context, groupID, bundleName, project, existingRoleID string) (roleID, grantID string, err error) {
	if e.client == nil {
		return "", "", fmt.Errorf("HD0161: no engine")
	}

	roleID = existingRoleID
	if roleID == "" {
		var actions []Action
		for _, bundle := range RoleBundles {
			if bundle.Name == bundleName {
				actions = bundle.Actions
			}
		}
		if actions == nil {
			return "", "", fmt.Errorf("HD0161: no role bundle named %q", bundleName)
		}
		scoped := make([]sesame.Permission, 0, len(actions))
		resource := "project:" + project + ":*"
		for _, action := range actions {
			scoped = append(scoped, sesame.Permission{Action: action.String(), Resource: resource})
		}
		// Named per group so two directory groups mapping to the same bundle
		// on the same project never collide on the tenant-unique role name.
		// SESAME's name alphabet has no @, so dashes join the parts.
		roleName := bundleName + "-" + project + "-" + strings.ToLower(strings.TrimPrefix(groupID, "grp_"))
		role, err := e.client.RoleCreate(ctx, e.tenantID, roleName, scoped)
		if err != nil {
			return "", "", fmt.Errorf("HD0161: create scoped role %q: %w", roleName, err)
		}
		roleID = role.ID
	}

	grant, err := e.client.GrantCreateForGroup(ctx, e.tenantID, groupID, roleID)
	if err != nil {
		return roleID, "", fmt.Errorf("HD0161: grant to group: %w", err)
	}
	return roleID, grant.ID, nil
}
