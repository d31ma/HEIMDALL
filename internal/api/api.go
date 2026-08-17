// Package api is the control plane's HTTP surface. Its whole security model
// is one rule: every route in the table below names exactly one action, and
// the authorization question is asked once, in middleware, before any handler
// runs. Handlers receive an already-authorized request and never ask again —
// no second check, and specifically no check inside a loop over resources.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/d31ma/heimdall/internal/auth"
	"github.com/d31ma/heimdall/internal/dispatch"
	"github.com/d31ma/heimdall/internal/enroll"
	"github.com/d31ma/heimdall/internal/observe"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/reconcile"
	"github.com/d31ma/heimdall/internal/registry"
	"github.com/d31ma/heimdall/internal/store"
)

// principalKey carries the authorized principal into handlers. It is
// unexported and typed so nothing outside this package can forge one.
type principalKey struct{}

// Principal returns the authorized principal for a request. It is only
// populated on routes that went through Authorize, which is every route in
// the table.
func Principal(r *http.Request) string {
	principal, _ := r.Context().Value(principalKey{}).(string)
	return principal
}

// Auditor records one completed authorization decision. Phase 0 writes to a
// log; a writer onto store.Audit replaces the sink in Phase 1 without
// touching this interface, because Entry is already the persisted shape.
type Auditor interface {
	Record(Entry)
}

// Entry is one mutating-call audit record. PolicyVersion and ReasonCode come
// straight from SESAME so that "why was this allowed in March" is answerable
// without replaying policy.
type Entry struct {
	PrincipalID   string `json:"principal_id"`
	Action        string `json:"action"`
	Resource      string `json:"resource"`
	Outcome       string `json:"outcome"`
	ReasonCode    string `json:"reason_code"`
	PolicyVersion int64  `json:"policy_version"`
	DecisionID    string `json:"decision_id,omitempty"`
	Method        string `json:"method"`
	Path          string `json:"path"`
}

// Server holds the collaborators the routes need. It is deliberately not a
// god object: anything a handler needs in a later phase is added here and
// nowhere else.
type Server struct {
	Engine  *auth.Engine
	Audit   Auditor
	Version string

	// Store, Reconcile, and Providers are nil in the contract generator and
	// in the routing tests, which only need the table. Every handler that
	// uses one checks first, so a misconfigured server answers 503 rather
	// than panicking mid-request.
	Store     *store.Store
	Reconcile *reconcile.Engine
	Providers map[string]provider.Provider

	// Dispatcher and Material are set when this control plane accepts agents.
	// Both nil means it does not, and the agent routes answer 503 rather than
	// half-working.
	Dispatcher *dispatch.Dispatcher
	Material   *enroll.Material

	// Auto is the sync loop a webhook nudges. Nil disables the receiver.
	Auto *reconcile.Auto
	// Registry reconciles the root repository's manifest (ADR 0010). Nil
	// disables the registry routes.
	Registry *registry.Engine
	// Observe answers metrics with history when the collector is running.
	Observe *observe.Collector
	// Secrets resolves a webhook secret reference for the length of one
	// comparison. Nil means every webhook is refused.
	Secrets func(ctx context.Context, ref string) (string, error)
	// DeviceEnabled reports that the engine holds an OIDC client for the
	// device grant. False disables the device routes with a clear 503.
	DeviceEnabled bool

	// PublicURL is the address agents were told to connect to. It is part of
	// an enrollment token's signature, so a token minted for one control
	// plane cannot enrol against another.
	PublicURL string

	// Now is injectable so tests do not race the clock.
	Now func() time.Time
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

// decisionKey carries SESAME's answer into a handler, so an operation record
// can be stamped with the policy version the call was authorized under
// without a second decide call.
type decisionKey struct{}

// DecisionFrom returns the authorization decision for a request.
func DecisionFrom(r *http.Request) auth.Decision {
	decision, _ := r.Context().Value(decisionKey{}).(auth.Decision)
	return decision
}

// Route binds one method and path to exactly one action and one resource.
// resource derives the resource string from path values only — never from a
// body or a header, because a caller must not be able to name the thing it is
// being authorized against.
type Route struct {
	Method   string
	Pattern  string
	Action   auth.Action
	Resource func(*http.Request) (string, error)
	Handler  http.HandlerFunc
}

func project(r *http.Request) (string, error) {
	return auth.Resource("project", r.PathValue("project"))
}

func app(r *http.Request) (string, error) {
	return auth.Resource("project", r.PathValue("project"), "app", r.PathValue("app"))
}

func tenant(*http.Request) (string, error) {
	return auth.Resource("tenant", "self")
}

func targetResource(r *http.Request) (string, error) {
	return auth.Resource("target", r.PathValue("target"))
}

// Routes is the complete authorized surface. A test asserts every entry names
// a known action and a non-nil resolver, which is the mechanical half of
// "every route maps to exactly one action".
func (s *Server) Routes() []Route {
	return []Route{
		{"GET", "/api/v1/projects", auth.ProjectRead, tenant, s.listProjects},
		{"GET", "/api/v1/projects/{project}/apps", auth.AppRead, project, s.listApps},
		{"POST", "/api/v1/projects/{project}/apps", auth.AppCreate, project, s.createApp},
		{"GET", "/api/v1/projects/{project}/apps/{app}", auth.AppRead, app, s.getApp},
		{"PATCH", "/api/v1/projects/{project}/apps/{app}", auth.AppUpdate, app, s.updateApp},
		{"DELETE", "/api/v1/projects/{project}/apps/{app}", auth.AppDelete, app, s.deleteApp},
		{"GET", "/api/v1/projects/{project}/apps/{app}/history", auth.AppRead, app, s.appHistory},
		{"POST", "/api/v1/projects/{project}/apps/{app}/sync", auth.AppSync, app, s.syncApp},
		{"POST", "/api/v1/projects/{project}/apps/{app}/rollback", auth.AppRollback, app, s.rollbackApp},
		{"POST", "/api/v1/projects/{project}/apps/{app}/prune", auth.AppPrune, app, s.pruneApp},
		{"POST", "/api/v1/projects/{project}/apps/{app}/suspend", auth.AppSuspend, app, s.suspendApp},
		{"GET", "/api/v1/projects/{project}/apps/{app}/instances", auth.AppRead, app, s.appInstances},
		{"GET", "/api/v1/projects/{project}/apps/{app}/metrics", auth.ObserveMetrics, app, s.appMetrics},
		{"GET", "/api/v1/projects/{project}/apps/{app}/logs", auth.ObserveLogs, app, s.appLogs},
		{"GET", "/api/v1/projects/{project}/apps/{app}/events", auth.ObserveEvents, app, s.appEvents},
		{"GET", "/api/v1/targets", auth.TargetRead, tenant, s.listTargets},
		{"POST", "/api/v1/targets", auth.TargetCreate, tenant, s.createTarget},
		{"POST", "/api/v1/targets/{target}/enroll", auth.TargetUpdate, targetResource, s.mintEnrollment},
		{"POST", "/api/v1/projects/{project}/target-groups", auth.TargetCreate, project, s.createTargetGroup},
		{"GET", "/api/v1/projects/{project}/target-groups", auth.TargetRead, project, s.listTargetGroups},
		{"GET", "/api/v1/target-groups/{group}/targets", auth.TargetRead, tenant, s.listGroupMembers},
		{"POST", "/api/v1/projects/{project}/group-mappings", auth.ProjectGrant, project, s.createGroupMapping},
		{"GET", "/api/v1/projects/{project}/group-mappings", auth.ProjectRead, project, s.listGroupMappings},
		{"GET", "/api/v1/registries", auth.TargetRead, tenant, s.listRegistries},
		{"POST", "/api/v1/registries", auth.SecretBind, tenant, s.createRegistry},
		{"GET", "/api/v1/repos", auth.RepoRead, tenant, s.listRepos},
		{"GET", "/api/v1/registry", auth.RegistryRead, tenant, s.registryStatus},
		{"POST", "/api/v1/registry/bind", auth.RegistryBind, tenant, s.registryBind},
		{"DELETE", "/api/v1/registry/bind", auth.RegistryBind, tenant, s.registryUnbind},
		{"POST", "/api/v1/registry/sync", auth.RegistrySync, tenant, s.registrySync},
		{"POST", "/api/v1/repos", auth.RepoCreate, tenant, s.createRepo},
		{"GET", "/api/v1/audit", auth.AuditRead, tenant, s.readAudit},
		{"GET", "/api/v1/audit/export", auth.AuditExport, tenant, s.exportAudit},
		{"POST", "/api/v1/projects/{project}/outbound-webhooks", auth.SecretBind, project, s.createOutboundWebhook},
		{"GET", "/api/v1/projects/{project}/outbound-webhooks", auth.ProjectRead, project, s.listOutboundWebhooks},
	}
}

// Handler builds the mux. Routes outside the SESAME boundary are enumerated
// here and nowhere else: liveness, readiness, version, the two login routes,
// and the three agent routes, which authenticate with mTLS instead.
// Everything else goes through Authorize.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		// Readiness is the engine's readiness. A control plane that cannot
		// authorize is not ready, however healthy its own goroutines are.
		if s.Engine == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "HD0503", "status": "engine_unavailable"})
			return
		}
		if err := s.Engine.Ping(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "HD0503", "status": "engine_unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /api/v1/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"version": s.Version})
	})
	// Login and logout are unauthenticated by necessity — they are how a
	// session comes to exist — and contain no authentication logic: every
	// step is a SESAME call.
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
	// Device authorization (RFC 8628): start and token are how a CLI session
	// comes to exist; approve, deny, and lookup act on the caller's own
	// session by proving it to SESAME. See device.go.
	mux.HandleFunc("POST /api/v1/auth/device/start", s.deviceStart)
	mux.HandleFunc("POST /api/v1/auth/device/token", s.deviceToken)
	mux.HandleFunc("POST /api/v1/auth/device/refresh", s.deviceRefresh)
	mux.HandleFunc("POST /api/v1/auth/device/lookup", s.deviceLookup)
	mux.HandleFunc("POST /api/v1/auth/device/approve", s.deviceApprove)
	mux.HandleFunc("POST /api/v1/auth/device/deny", s.deviceDeny)

	// Agent routes authenticate with mTLS rather than a session; see the
	// comment at the top of agent.go for why that is not an exception being
	// carved out of the boundary.
	s.mountAgentRoutes(mux)

	// A forge holds no session and never will. The receiver authenticates the
	// message with an HMAC instead, and can do nothing but nudge the loop; see
	// webhook.go.
	mux.HandleFunc("POST /api/v1/webhooks/{repo}", s.webhook)

	// The SCIM host. An identity provider holds no session; its bearer token
	// is authenticated by SESAME on every operation. See scim.go.
	for _, method := range []string{"POST", "GET", "PATCH", "DELETE"} {
		mux.HandleFunc(method+" /scim/v2/{resource}", s.scim)
		mux.HandleFunc(method+" /scim/v2/{resource}/{id}", s.scim)
	}

	for _, route := range s.Routes() {
		mux.Handle(route.Method+" "+route.Pattern, s.Authorize(route))
	}
	return mux
}

// Authorize is the boundary. It resolves the session to a principal, asks
// SESAME once, records the decision, and only then calls the handler.
func (s *Server) Authorize(route Route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Engine == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"code": "HD0503", "message": "authorization engine unavailable",
			})
			return
		}

		bearer, ok := bearerValue(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"code": "HD0401", "message": "a session is required",
			})
			return
		}

		// Either credential form: a session pair from password login, or an
		// access token from the device grant. Both are verified by SESAME.
		session, outcome := s.Engine.VerifyBearer(r.Context(), bearer)
		switch outcome {
		case auth.Unavailable:
			// The engine is down. This is not the caller's fault and must not
			// look like one, or an operator will spend the outage checking
			// their grants.
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"code": "HD0503", "message": "authorization engine unavailable",
			})
			return
		case auth.Deny:
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"code": "HD0401", "message": "session is not valid",
			})
			return
		}

		resource, err := route.Resource(r)
		if err != nil {
			// A resource that will not build is a request that cannot be
			// authorized, so it is refused rather than authorized against
			// some fallback.
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"code": "HD0400", "message": "resource identifier is not well formed",
			})
			return
		}

		decision := s.Engine.Decide(r.Context(), session.PrincipalID, route.Action, resource)

		if s.Audit != nil {
			s.Audit.Record(Entry{
				PrincipalID:   session.PrincipalID,
				Action:        route.Action.String(),
				Resource:      resource,
				Outcome:       decision.Outcome.String(),
				ReasonCode:    decision.ReasonCode,
				PolicyVersion: decision.PolicyVersion,
				DecisionID:    decision.DecisionID,
				Method:        r.Method,
				Path:          r.URL.Path,
			})
		}

		switch decision.Outcome {
		case auth.Allow:
			if s.Store == nil {
				// Authorized, but the server was built without its storage.
				// Refusing is honest; a nil dereference is not.
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{
					"code": "HD0503", "message": "control plane storage is unavailable",
				})
				return
			}
			ctx := context.WithValue(r.Context(), principalKey{}, session.PrincipalID)
			ctx = context.WithValue(ctx, decisionKey{}, decision)
			route.Handler(w, r.WithContext(ctx))
		case auth.Deny:
			// SESAME's reason code is passed through verbatim. It is the
			// difference between "you have no grant" and "your session
			// assurance is too low", and guessing at it here would be
			// inventing authorization logic.
			writeJSON(w, http.StatusForbidden, map[string]string{
				"code": "HD0403", "reason_code": decision.ReasonCode,
			})
		default:
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"code": "HD0503", "message": "authorization engine unavailable",
			})
		}
	})
}

// bearerSession splits an "Authorization: Bearer <session-id>.<secret>"
// header. The secret is never logged and never leaves this call chain.
func bearerValue(r *http.Request) (string, bool) {
	value, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	trimmed := strings.TrimSpace(value)
	return trimmed, found && trimmed != ""
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
