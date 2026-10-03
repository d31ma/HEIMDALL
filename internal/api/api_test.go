// These tests run against a real compiled `sesame` binary and a real
// deployment, not a fake. A stub would happily return "allow" and prove
// nothing about the one property that matters: that no path through the
// boundary reaches a handler without a live engine saying yes.
package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/d31ma/heimdall/internal/api"
	"github.com/d31ma/heimdall/internal/auth"
	"github.com/d31ma/heimdall/internal/store"
	"github.com/d31ma/sesame/clients/go/sesame"
)

type harness struct {
	engine  *auth.Engine
	client  *sesame.Client
	server  *httptest.Server
	storage *store.Store
	tenant  string
	audited []api.Entry
}

func (h *harness) Record(entry api.Entry) { h.audited = append(h.audited, entry) }

func sesameBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("SESAME_BINARY")
	if binary == "" {
		binary = "sesame"
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		t.Skipf("sesame binary not on PATH: %v", err)
	}
	return resolved
}

// newHarness builds a throwaway deployment under the test's temp directory.
// t.TempDir is a local filesystem, which is what FYLO requires — a root on a
// sync filesystem is refused by internal/store for the same reason.
func newHarness(t *testing.T) *harness {
	t.Helper()
	binary := sesameBinary(t)

	fylo := os.Getenv("FYLO_BINARY")
	if fylo == "" {
		resolved, err := exec.LookPath("fylo")
		if err != nil {
			t.Skipf("fylo binary not on PATH: %v", err)
		}
		fylo = resolved
	}

	deployment := filepath.Join(t.TempDir(), "sesame")
	initialize := exec.Command(binary, "init",
		"--deployment", deployment, "--fylo-binary", fylo, "--issuer", "https://localhost:8443")
	initialize.Stderr = os.Stderr
	if err := initialize.Run(); err != nil {
		t.Fatalf("sesame init: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	client, err := sesame.Start(ctx, sesame.Options{Binary: binary, Deployment: deployment})
	if err != nil {
		t.Fatalf("start sesame: %v", err)
	}

	bootstrap, err := client.TenantBootstrap(ctx, "heimdall-test")
	if err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

	engine := auth.Adopt(client, bootstrap.Tenant.ID)
	if _, err := engine.SeedRoles(ctx); err != nil {
		t.Fatalf("seed roles: %v", err)
	}

	// A real FYLO root under the test's temp directory. The handlers behind
	// the boundary are real, so the boundary is exercised against real
	// storage rather than against a stub that always succeeds.
	storage, err := store.Open(filepath.Join(t.TempDir(), "fylo-root"), "")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	h := &harness{engine: engine, client: client, tenant: bootstrap.Tenant.ID, storage: storage}
	h.server = httptest.NewServer((&api.Server{
		Engine: engine, Audit: h, Version: "test", Store: storage,
	}).Handler())

	t.Cleanup(func() {
		h.server.Close()
		_ = engine.Close()
		_ = storage.Close()
	})
	return h
}

// login creates a principal with a password and logs it in, returning the
// bearer value the middleware expects.
func (h *harness) login(t *testing.T, username string) (principalID, bearer string) {
	t.Helper()
	ctx := context.Background()
	identifier := sesame.PrincipalIdentifier{Namespace: "username", Value: username}

	principal, err := h.client.PrincipalCreate(ctx, h.tenant, "human", identifier)
	if err != nil {
		t.Fatalf("create principal: %v", err)
	}
	// A long random-looking constant, not a realistic secret: it exists only
	// to satisfy the engine's own strength rules inside a temp deployment.
	const password = "correct-horse-battery-staple-9271"
	if err := h.client.SetPassword(ctx, principal.ID, password); err != nil {
		t.Fatalf("set password: %v", err)
	}

	begun, err := h.client.AuthenticationBegin(ctx, h.tenant, identifier)
	if err != nil {
		t.Fatalf("begin authentication: %v", err)
	}
	if _, err := h.client.AuthenticationVerifyPassword(ctx, begun.TransactionID, password); err != nil {
		t.Fatalf("verify password: %v", err)
	}
	session, err := h.client.AuthenticationComplete(ctx, begun.TransactionID, time.Hour)
	if err != nil {
		t.Fatalf("complete authentication: %v", err)
	}
	return principal.ID, session.SessionID + "." + session.Secret
}

func (h *harness) get(t *testing.T, path, bearer string) (int, map[string]string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, h.server.URL+path, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("request %s: %v", path, err)
	}
	defer func() { _ = response.Body.Close() }()

	var body map[string]string
	_ = json.NewDecoder(response.Body).Decode(&body)
	return response.StatusCode, body
}

const (
	// appPath needs a reconcile engine, so it is used only where the
	// assertion is about authorization refusing before any handler runs.
	appPath = "/api/v1/projects/alpha/apps/checkout"
	// targetsPath reaches a handler that needs only the store, so it proves
	// the boundary lets an authorized request through.
	targetsPath = "/api/v1/targets"
)

// TestUngrantedPrincipalGets403WithReasonCode is the first half of the Phase 0
// authorization exit criterion.
func TestUngrantedPrincipalGets403WithReasonCode(t *testing.T) {
	h := newHarness(t)
	_, bearer := h.login(t, "nobody")

	status, body := h.get(t, appPath, bearer)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %v)", status, body)
	}
	if body["reason_code"] != "deny_no_grant" {
		t.Fatalf("reason_code = %q, want SESAME's deny_no_grant", body["reason_code"])
	}
}

// TestGrantedPrincipalReachesHandler proves the boundary is not simply
// denying everything, which a 403-only test would not catch.
func TestGrantedPrincipalReachesHandler(t *testing.T) {
	h := newHarness(t)
	principal, bearer := h.login(t, "operator-one")

	role, err := h.client.RoleCreate(context.Background(), h.tenant, "reader",
		[]sesame.Permission{{Action: auth.TargetRead.String(), Resource: "*"}})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := h.client.GrantCreate(context.Background(), h.tenant, principal, role.ID); err != nil {
		t.Fatalf("grant role: %v", err)
	}

	status, body := h.get(t, targetsPath, bearer)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", status, body)
	}
}

// TestStoppedEngineGets503AndNeverBypasses is the second half. The
// distinction from 403 is deliberate: an outage must not read as a missing
// grant, or an operator spends it auditing their own permissions.
func TestStoppedEngineGets503AndNeverBypasses(t *testing.T) {
	h := newHarness(t)
	principal, bearer := h.login(t, "operator-two")

	role, err := h.client.RoleCreate(context.Background(), h.tenant, "reader",
		[]sesame.Permission{{Action: auth.TargetRead.String(), Resource: "*"}})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := h.client.GrantCreate(context.Background(), h.tenant, principal, role.ID); err != nil {
		t.Fatalf("grant role: %v", err)
	}
	// The principal is fully authorized. Only the engine is gone.
	if status, _ := h.get(t, targetsPath, bearer); status != http.StatusOK {
		t.Fatalf("precondition failed: authorized request returned %d", status)
	}

	if err := h.engine.Close(); err != nil {
		t.Fatalf("stop engine: %v", err)
	}

	status, body := h.get(t, targetsPath, bearer)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 — a stopped engine must never allow a request (body %v)", status, body)
	}
}

func TestMissingSessionGets401(t *testing.T) {
	h := newHarness(t)
	if status, body := h.get(t, appPath, ""); status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body %v)", status, body)
	}
	if status, body := h.get(t, appPath, "not-a-session.and-not-a-secret"); status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body %v)", status, body)
	}
}

// TestEveryDecisionIsAudited proves the audit record carries what the plan
// requires: a principal, a reason code, and a policy version.
func TestEveryDecisionIsAudited(t *testing.T) {
	h := newHarness(t)
	principal, bearer := h.login(t, "auditee")

	if status, _ := h.get(t, appPath, bearer); status != http.StatusForbidden {
		t.Fatalf("precondition failed: expected a denial to audit, got %d", status)
	}
	if len(h.audited) != 1 {
		t.Fatalf("recorded %d audit entries, want exactly 1", len(h.audited))
	}
	entry := h.audited[0]
	switch {
	case entry.PrincipalID != principal:
		t.Errorf("principal_id = %q, want %q", entry.PrincipalID, principal)
	case entry.Action != auth.AppRead.String():
		t.Errorf("action = %q, want %q", entry.Action, auth.AppRead)
	case entry.Resource != "project:alpha:app:checkout":
		t.Errorf("resource = %q", entry.Resource)
	case entry.ReasonCode == "":
		t.Error("reason_code is empty")
	case entry.PolicyVersion == 0:
		t.Error("policy_version is zero")
	}
}

// TestDeviceAuthorizationEndToEnd runs the whole RFC 8628 flow against the
// real engine: start, poll while pending, approve with a real session, poll
// again and receive a session. This is the CLI's login path with the browser
// half played by direct calls.
func TestDeviceAuthorizationEndToEnd(t *testing.T) {
	h := newHarness(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	clients, err := h.engine.EnsureDeviceClient(ctx, auth.OIDCClients{})
	if err != nil {
		t.Fatalf("register device client: %v", err)
	}
	if clients.DeviceClientSecret == "" {
		t.Fatal("no client secret was returned; introspection would be impossible")
	}
	// A re-run of init passes the stored result back and gets it verified.
	again, err := h.engine.EnsureDeviceClient(ctx, clients)
	if err != nil || again.DeviceClientID != clients.DeviceClientID {
		t.Fatalf("re-run with stored client: %+v vs %+v, err %v", clients, again, err)
	}
	// A stored id whose client is gone must fail loudly, not re-register a
	// second client the CLI half-uses.
	if _, err := h.engine.EnsureDeviceClient(ctx, auth.OIDCClients{DeviceClientID: "cli_gone"}); err == nil {
		t.Fatal("a dangling stored client id was accepted")
	}
	h.engine.UseOIDCClients(clients)

	grant, err := h.engine.DeviceStart(ctx)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if grant.UserCode == "" || grant.DeviceCode == "" {
		t.Fatalf("grant is missing codes: %+v", grant)
	}

	// Nobody has approved, so the poll is pending — not an error, not a
	// denial.
	poll, err := h.engine.DevicePoll(ctx, grant.DeviceCode)
	if err != nil || !poll.Pending {
		t.Fatalf("pre-approval poll: pending=%v err=%v", poll.Pending, err)
	}

	// A person signs in and approves the code — with it typed the sloppy way
	// a person types: lowercased, dash dropped.
	_, bearer := h.login(t, "approver")
	sessionID, secret, _ := strings.Cut(bearer, ".")
	sloppy := strings.ToLower(strings.ReplaceAll(grant.UserCode, "-", ""))
	if err := h.engine.DeviceApprove(ctx, sloppy, sessionID, secret); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// The next poll carries tokens.
	poll, err = h.engine.DevicePoll(ctx, grant.DeviceCode)
	if err != nil {
		t.Fatalf("post-approval poll: %v", err)
	}
	if poll.Pending || poll.Denied {
		t.Fatalf("post-approval poll did not settle: %+v", poll)
	}
	if poll.Tokens.AccessToken == "" {
		t.Fatal("no access token in the token result")
	}
	if poll.Tokens.RefreshToken == "" {
		t.Fatal("no refresh token; the CLI would die every five minutes")
	}

	// The boundary accepts the access token: introspection resolves it to
	// the approver's principal. This is the whole point of the flow.
	session, outcome := h.engine.VerifyBearer(ctx, poll.Tokens.AccessToken)
	if outcome != auth.Allow {
		t.Fatalf("the access token does not verify at the boundary: %v", outcome)
	}
	if session.PrincipalID == "" {
		t.Fatal("introspection returned no principal")
	}

	// A refresh rotates the pair, and the new access token still verifies.
	refreshed, err := h.engine.DeviceRefresh(ctx, poll.Tokens.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refreshed.AccessToken == "" || refreshed.RefreshToken == poll.Tokens.RefreshToken {
		t.Fatalf("the refresh did not rotate: %+v", refreshed)
	}
	if _, outcome := h.engine.VerifyBearer(ctx, refreshed.AccessToken); outcome != auth.Allow {
		t.Fatalf("the refreshed token does not verify: %v", outcome)
	}

	// A garbage token is a deny, not an error page.
	if _, outcome := h.engine.VerifyBearer(ctx, "eyJhb.garbage.token"); outcome != auth.Deny {
		t.Fatalf("a forged token gave %v, want deny", outcome)
	}
}

// TestDeviceDenialStopsTheFlow: a refused device must be told access_denied,
// and a forged session must not be able to approve anything.
func TestDeviceDenialStopsTheFlow(t *testing.T) {
	h := newHarness(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	clients, err := h.engine.EnsureDeviceClient(ctx, auth.OIDCClients{})
	if err != nil {
		t.Fatalf("register device client: %v", err)
	}
	h.engine.UseOIDCClients(clients)
	grant, err := h.engine.DeviceStart(ctx)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// A forged session cannot approve. The session is proved, not named.
	if err := h.engine.DeviceApprove(ctx, grant.UserCode, "ses_forged", "not-a-secret"); err == nil {
		t.Fatal("a forged session approved a device")
	}

	if err := h.engine.DeviceDeny(ctx, grant.UserCode); err != nil {
		t.Fatalf("deny: %v", err)
	}
	poll, err := h.engine.DevicePoll(ctx, grant.DeviceCode)
	if err != nil || !poll.Denied {
		t.Fatalf("post-denial poll: denied=%v pending=%v err=%v", poll.Denied, poll.Pending, err)
	}
}

// TestSCIMGroupGrantsOperatorThroughProvisioningAlone is the Phase 5 exit
// criterion: a directory group grants operator on a project through SCIM
// alone, with no HEIMDALL-side user administration. The "IdP" here is this
// test holding a provisioning token, doing exactly what Okta or Entra does:
// create a user, create a group containing them.
func TestSCIMGroupGrantsOperatorThroughProvisioningAlone(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// An administrator declares the mapping first — the one HEIMDALL-side
	// act, and it is about authorization shape, not users.
	mappingID, err := store.In[api.GroupMapping](h.storage, store.GroupMappings).Put(api.GroupMapping{
		Project: "alpha", GroupName: "platform-operators", Role: "operator",
	})
	if err != nil {
		t.Fatalf("declare mapping: %v", err)
	}

	// The IdP is registered and holds a bearer token.
	registration, err := h.client.ProvisioningClientRegister(ctx, h.tenant, "okta", "username", true)
	if err != nil {
		t.Fatalf("register provisioning client: %v", err)
	}
	token, _ := registration["token"].(string)
	if token == "" {
		t.Fatalf("no token in %v", registration)
	}

	scim := func(method, path, token, body string) (int, map[string]any) {
		t.Helper()
		request, err := http.NewRequest(method, h.server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("scim call: %v", err)
		}
		defer func() { _ = response.Body.Close() }()
		var decoded map[string]any
		_ = json.NewDecoder(response.Body).Decode(&decoded)
		return response.StatusCode, decoded
	}

	// No token, no service.
	if status, _ := scim("POST", "/scim/v2/Users", "", `{"userName":"eve"}`); status != http.StatusUnauthorized {
		t.Fatalf("an unauthenticated SCIM call answered %d", status)
	}

	// The IdP provisions a user…
	status, user := scim("POST", "/scim/v2/Users", token,
		`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"casey","active":true}`)
	if status != http.StatusCreated {
		t.Fatalf("user create: %d %v", status, user)
	}
	userID, _ := user["id"].(string)
	if userID == "" {
		t.Fatalf("no user id in %v", user)
	}

	// …and a group containing them, named what the mapping names.
	status, group := scim("POST", "/scim/v2/Groups", token, fmt.Sprintf(
		`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:Group"],"displayName":"platform-operators","members":[{"value":%q}]}`,
		userID))
	if status != http.StatusCreated {
		t.Fatalf("group create: %d %v", status, group)
	}
	t.Logf("group document: %v", group)

	// The mapping is now enforced: the SESAME grant exists and is recorded.
	mapping, err := store.In[api.GroupMapping](h.storage, store.GroupMappings).Get(mappingID)
	if err != nil {
		t.Fatalf("read mapping: %v", err)
	}
	if mapping.GrantID == "" || mapping.GroupID == "" {
		t.Fatalf("the mapping was not enforced: %+v", mapping)
	}

	// And the provisioned principal is an operator on alpha — and only on
	// alpha. This is the whole point.
	principal := principalOfSCIMUser(t, user)
	if decision := h.engine.Decide(ctx, principal, auth.AppSync, "project:alpha:app:checkout"); decision.Outcome != auth.Allow {
		t.Fatalf("the provisioned user cannot sync on alpha: %s (%s)", decision.Outcome, decision.ReasonCode)
	}
	if decision := h.engine.Decide(ctx, principal, auth.AppSync, "project:beta:app:checkout"); decision.Outcome == auth.Allow {
		t.Fatal("the group grant leaked outside its project")
	}
	// Operator, not admin: no app:create came with it.
	if decision := h.engine.Decide(ctx, principal, auth.AppCreate, "project:alpha:app:checkout"); decision.Outcome == auth.Allow {
		t.Fatal("the operator mapping granted admin actions")
	}
}

// principalOfSCIMUser digs the engine principal id out of a SCIM user
// document, wherever the engine put it.
func principalOfSCIMUser(t *testing.T, user map[string]any) string {
	t.Helper()
	if id, ok := user["id"].(string); ok && strings.HasPrefix(id, "prn_") {
		return id
	}
	if raw, ok := user["externalId"].(string); ok && strings.HasPrefix(raw, "prn_") {
		return raw
	}
	t.Fatalf("no principal id in the SCIM user document: %v", user)
	return ""
}

// TestManagedApplicationRefusesInteractiveMutation is ADR 0010's one-authority
// rule at the boundary: an application the registry declares is changed by a
// commit to the root repository, and the API says so.
func TestManagedApplicationRefusesInteractiveMutation(t *testing.T) {
	h := newHarness(t)
	principal, bearer := h.login(t, "admin-two")

	role, err := h.client.RoleCreate(context.Background(), h.tenant, "app-admin",
		[]sesame.Permission{
			{Action: auth.AppUpdate.String(), Resource: "*"},
			{Action: auth.AppDelete.String(), Resource: "*"},
		})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := h.client.GrantCreate(context.Background(), h.tenant, principal, role.ID); err != nil {
		t.Fatalf("grant role: %v", err)
	}

	if _, err := store.In[store.Application](h.storage, store.Applications).Put(store.Application{
		Project: "alpha", Name: "checkout", RepoID: "rep_x", TargetID: "tgt_x", Path: "deploy",
		ManagedBy: store.ManagedByRegistry,
	}); err != nil {
		t.Fatalf("seed application: %v", err)
	}

	patch := func(method string) (int, map[string]string) {
		t.Helper()
		request, err := http.NewRequest(method, h.server.URL+appPath, strings.NewReader(`{"path":"elsewhere"}`))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		request.Header.Set("Authorization", "Bearer "+bearer)
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer func() { _ = response.Body.Close() }()
		var body map[string]string
		_ = json.NewDecoder(response.Body).Decode(&body)
		return response.StatusCode, body
	}

	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		status, body := patch(method)
		if status != http.StatusConflict || body["code"] != "HD0272" {
			t.Fatalf("%s managed app = %d %v, want 409 HD0272", method, status, body)
		}
		if !strings.Contains(body["message"], "root repository") {
			t.Fatalf("the refusal does not say where the truth lives: %v", body)
		}
	}
}
