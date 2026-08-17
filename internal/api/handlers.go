package api

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/reconcile"
	"github.com/d31ma/heimdall/internal/store"
)

// maxRequestBytes bounds a request body. An API is untrusted input even when
// the caller is authorized.
const maxRequestBytes = 1 << 20

// decodeBody reads a bounded JSON body.
func decodeBody(w http.ResponseWriter, r *http.Request, into any) bool {
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxRequestBytes))
	// Reject unknown fields: a typo in a request body must be an error, not
	// a silently ignored setting that an operator believes took effect.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400", "message": "request body is not valid: " + err.Error(),
		})
		return false
	}
	return true
}

// fail maps an internal error onto a status. It never returns the raw error
// for a 500: a stack of wrapped errors can name internal paths, and the
// diagnostic code plus the log is the actionable pair.
func (s *Server) fail(w http.ResponseWriter, err error) {
	message := err.Error()
	switch {
	case strings.Contains(message, "HD0300"):
		// A capability rejection is the operator's compose file, not a server
		// fault, and the whole message is the actionable part.
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"code": "HD0300", "message": message,
		})
	case strings.HasPrefix(message, "HD02"):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": codeOf(message), "message": message})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"code": "HD0500", "message": message,
		})
	}
}

func codeOf(message string) string {
	if len(message) >= 6 && strings.HasPrefix(message, "HD") {
		return message[:6]
	}
	return "HD0500"
}

func intQuery(r *http.Request, name string, fallback int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

// appID resolves a project and app name to the stored application. Handlers
// receive an already-authorized request, so a miss here is a 404 and never an
// authorization decision.
func (s *Server) appID(w http.ResponseWriter, r *http.Request) (store.Application, bool) {
	project := r.PathValue("project")
	name := r.PathValue("app")

	applications, err := store.In[store.Application](s.Store, store.Applications).
		Find(store.Equals("name", name))
	if err != nil {
		s.fail(w, err)
		return store.Application{}, false
	}
	for _, application := range applications {
		if application.Project == project {
			return application, true
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{
		"code": "HD0404", "message": "no application " + project + "/" + name,
	})
	return store.Application{}, false
}

// ---------------------------------------------------------------------------
// Projects
// ---------------------------------------------------------------------------

// listProjects returns the projects that have applications.
//
// Projects are derived rather than stored: an application names its project,
// and nothing else creates one. A separate collection would be a second place
// for the same fact to live and disagree.
//
// Authorization is project:read on the tenant, not on each project. This
// route returns names and counts, never an application's contents — reading
// those still requires app:read on that project, which is where the real
// boundary is.
func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	applications, err := store.In[store.Application](s.Store, store.Applications).Find(nil)
	if err != nil {
		s.fail(w, err)
		return
	}

	counts := map[string]int{}
	for _, application := range applications {
		counts[application.Project]++
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)

	projects := make([]map[string]any, 0, len(names))
	for _, name := range names {
		projects = append(projects, map[string]any{"name": name, "applications": counts[name]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

// ---------------------------------------------------------------------------
// Applications
// ---------------------------------------------------------------------------

func (s *Server) listApps(w http.ResponseWriter, r *http.Request) {
	applications, err := store.In[store.Application](s.Store, store.Applications).
		Find(store.Equals("project", r.PathValue("project")))
	if err != nil {
		s.fail(w, err)
		return
	}
	if applications == nil {
		applications = []store.Application{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": applications})
}

func (s *Server) createApp(w http.ResponseWriter, r *http.Request) {
	var application store.Application
	if !decodeBody(w, r, &application) {
		return
	}
	// The project comes from the path, which is what was authorized. Taking
	// it from the body would let a caller create an application in a project
	// the decision never covered.
	application.Project = r.PathValue("project")
	application.CreatedAt = s.now()

	if application.Name == "" || application.RepoID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400", "message": "name and repo_id are required",
		})
		return
	}
	// Exactly one destination: a single target, or a group to fan out over.
	if (application.TargetID == "") == (application.GroupID == "") {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400", "message": "exactly one of target_id and group_id is required",
		})
		return
	}

	id, err := store.In[store.Application](s.Store, store.Applications).Put(application)
	if err != nil {
		s.fail(w, err)
		return
	}
	application.ID = id
	writeJSON(w, http.StatusCreated, application)
}

func (s *Server) getApp(w http.ResponseWriter, r *http.Request) {
	application, ok := s.appID(w, r)
	if !ok {
		return
	}
	summary, err := s.Reconcile.Status(r.Context(), application.ID)
	if err != nil {
		s.fail(w, err)
		return
	}

	// The desired topology, for the tree view: the diff summary says how each
	// service is doing, but only the rendered spec knows what depends on
	// what. Best effort — an application that has never rendered simply has
	// no topology yet.
	type topologyService struct {
		Name      string   `json:"name"`
		Image     string   `json:"image"`
		DependsOn []string `json:"depends_on,omitempty"`
		Wave      int      `json:"wave"`
		Replicas  int      `json:"replicas"`
	}
	var topology []topologyService
	if revisions, err := s.Reconcile.Revisions(application.ID, 1); err == nil && len(revisions) > 0 {
		for _, service := range revisions[0].Spec.Services {
			topology = append(topology, topologyService{
				Name: service.Name, Image: service.Image, DependsOn: service.DependsOn,
				Wave: service.Wave, Replicas: service.Replicas,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"application": application,
		"status":      summary,
		"topology":    topology,
	})
}

// refuseManaged is ADR 0010's one-authority rule at the API boundary: a
// document the registry declares is changed by a commit to the root
// repository, never by a route. The refusal names where the truth lives.
func (s *Server) refuseManaged(w http.ResponseWriter, managedBy string) bool {
	if managedBy != store.ManagedByRegistry {
		return false
	}
	message := "this document is declared by the root repository; change it there"
	if s.Registry != nil {
		if binding, found, err := s.Registry.Binding(); err == nil && found {
			message = "this document is declared by the root repository " + binding.URL + "; change it there"
		}
	}
	writeJSON(w, http.StatusConflict, map[string]string{"code": "HD0272", "message": message})
	return true
}

func (s *Server) updateApp(w http.ResponseWriter, r *http.Request) {
	application, ok := s.appID(w, r)
	if !ok {
		return
	}
	if refused := s.refuseManaged(w, application.ManagedBy); refused {
		return
	}
	var changes map[string]any
	if !decodeBody(w, r, &changes) {
		return
	}
	// Identity fields are not patchable: changing them would silently
	// re-point an application at a different project, and the authorization
	// decision was made against the old one.
	for _, immutable := range []string{"id", "project", "name", "created_at"} {
		delete(changes, immutable)
	}
	if err := store.In[store.Application](s.Store, store.Applications).Patch(application.ID, changes); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) deleteApp(w http.ResponseWriter, r *http.Request) {
	application, ok := s.appID(w, r)
	if !ok {
		return
	}
	if refused := s.refuseManaged(w, application.ManagedBy); refused {
		return
	}
	// Deleting the application record does not delete what is running. That
	// is prune's job, and prune is a separate action with a separate grant.
	if err := store.In[store.Application](s.Store, store.Applications).Delete(application.ID); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "deleted",
		"message": "the application record is gone; running containers were left in place — use prune to remove them",
	})
}

func (s *Server) suspendApp(w http.ResponseWriter, r *http.Request) {
	application, ok := s.appID(w, r)
	if !ok {
		return
	}
	if refused := s.refuseManaged(w, application.ManagedBy); refused {
		return
	}
	suspended := !application.Suspended
	if err := store.In[store.Application](s.Store, store.Applications).
		Patch(application.ID, map[string]any{"suspended": suspended}); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"suspended": suspended})
}

// ---------------------------------------------------------------------------
// Sync, rollback, prune
// ---------------------------------------------------------------------------

type syncRequest struct {
	DryRun   bool     `json:"dry_run,omitempty"`
	Services []string `json:"services,omitempty"`
	Prune    *bool    `json:"prune,omitempty"`
}

func (s *Server) syncApp(w http.ResponseWriter, r *http.Request) {
	application, ok := s.appID(w, r)
	if !ok {
		return
	}
	var body syncRequest
	if r.ContentLength > 0 && !decodeBody(w, r, &body) {
		return
	}

	decision := DecisionFrom(r)
	operation, err := s.Reconcile.Sync(r.Context(), reconcile.Request{
		AppID:         application.ID,
		PrincipalID:   Principal(r),
		PolicyVersion: decision.PolicyVersion,
		ReasonCode:    decision.ReasonCode,
		DryRun:        body.DryRun,
		Services:      body.Services,
		Prune:         body.Prune,
	})
	// A sync that completed with per-service failures is still a real
	// operation record, so it is returned rather than swallowed by the error.
	if err != nil && operation.ID == "" {
		s.fail(w, err)
		return
	}
	status := http.StatusOK
	if operation.Phase == store.PhaseFailed {
		status = http.StatusConflict
	}
	writeJSON(w, status, operation)
}

type rollbackRequest struct {
	// Revision is a commit or a stored revision id. It must already have been
	// rendered for this application: a rollback re-applies a stored spec and
	// never re-reads git, so a force-push cannot change what it means.
	Revision string `json:"revision"`
}

func (s *Server) rollbackApp(w http.ResponseWriter, r *http.Request) {
	application, ok := s.appID(w, r)
	if !ok {
		return
	}
	var body rollbackRequest
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Revision == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400", "message": "revision is required",
		})
		return
	}

	decision := DecisionFrom(r)
	operation, err := s.Reconcile.Sync(r.Context(), reconcile.Request{
		AppID:         application.ID,
		PrincipalID:   Principal(r),
		PolicyVersion: decision.PolicyVersion,
		ReasonCode:    decision.ReasonCode,
		Revision:      body.Revision,
	})
	if err != nil && operation.ID == "" {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, operation)
}

func (s *Server) pruneApp(w http.ResponseWriter, r *http.Request) {
	application, ok := s.appID(w, r)
	if !ok {
		return
	}
	prune := true
	decision := DecisionFrom(r)
	operation, err := s.Reconcile.Sync(r.Context(), reconcile.Request{
		AppID:         application.ID,
		PrincipalID:   Principal(r),
		PolicyVersion: decision.PolicyVersion,
		ReasonCode:    decision.ReasonCode,
		Prune:         &prune,
	})
	if err != nil && operation.ID == "" {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, operation)
}

func (s *Server) appHistory(w http.ResponseWriter, r *http.Request) {
	application, ok := s.appID(w, r)
	if !ok {
		return
	}
	operations, err := s.Reconcile.History(application.ID, intQuery(r, "limit", 50))
	if err != nil {
		s.fail(w, err)
		return
	}
	revisions, err := s.Reconcile.Revisions(application.ID, intQuery(r, "limit", 50))
	if err != nil {
		s.fail(w, err)
		return
	}
	if operations == nil {
		operations = []store.Operation{}
	}
	// A revision carries its whole rendered spec, which is large and not what
	// a history view needs. Strip it; the detail view fetches one revision.
	summaries := make([]map[string]any, 0, len(revisions))
	for _, revision := range revisions {
		summaries = append(summaries, map[string]any{
			"id": revision.ID, "commit": revision.Commit, "spec_hash": revision.SpecHash,
			"author": revision.Author, "message": revision.Message,
			"signed": revision.Signed, "rendered_at": revision.RenderedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"operations": operations, "revisions": summaries})
}

// ---------------------------------------------------------------------------
// Observability
// ---------------------------------------------------------------------------

func (s *Server) instanceProvider(w http.ResponseWriter, r *http.Request) (provider.Provider, store.Target, store.Application, bool) {
	application, ok := s.appID(w, r)
	if !ok {
		return nil, store.Target{}, store.Application{}, false
	}
	target, err := store.In[store.Target](s.Store, store.Targets).Get(application.TargetID)
	if err != nil {
		s.fail(w, err)
		return nil, store.Target{}, store.Application{}, false
	}
	adapter, ok := s.Providers[target.Provider]
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"code": "HD0501", "message": "no adapter for provider " + target.Provider,
		})
		return nil, store.Target{}, store.Application{}, false
	}
	return adapter, target, application, true
}

func (s *Server) appInstances(w http.ResponseWriter, r *http.Request) {
	adapter, target, application, ok := s.instanceProvider(w, r)
	if !ok {
		return
	}
	instances, err := adapter.Instances(r.Context(), target.Ref(), application.AppRef())
	if err != nil {
		s.fail(w, err)
		return
	}
	if instances == nil {
		instances = []provider.Instance{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": instances})
}

func (s *Server) appMetrics(w http.ResponseWriter, r *http.Request) {
	adapter, target, application, ok := s.instanceProvider(w, r)
	if !ok {
		return
	}
	instanceID := r.URL.Query().Get("instance")
	if instanceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400", "message": "an instance query parameter is required",
		})
		return
	}

	// The service's metric selection comes from the latest revision's spec —
	// the heimdall.metrics label, resolved at render time. The developer's
	// choice is desired state in git like everything else, so the answer
	// honours whatever the last synced revision declared.
	serviceName := r.URL.Query().Get("service")
	var selection []string
	if revisions, err := s.Reconcile.Revisions(application.ID, 1); err == nil && len(revisions) > 0 {
		if service, ok := revisions[0].Spec.Service(serviceName); ok {
			selection = service.Metrics
		}
	}

	// History first: for a direct target the collector scraped this instance
	// into its ring and rollups; for an agent target the agent shipped the
	// rollups here. Either way this is where "24 hours of metrics" comes
	// from. The adapter path below answers one live sample when there is no
	// history yet.
	if s.Observe != nil {
		series, err := s.Observe.SeriesFor(provider.InstanceRef{
			AppRef: application.AppRef(), Service: serviceName, Instance: instanceID,
		}, provider.Window{})
		if err == nil && len(series.CPUPercent) > 0 {
			writeMetrics(w, series, selection)
			return
		}
	}
	series, err := adapter.Metrics(r.Context(), target.Ref(), provider.InstanceRef{
		AppRef: application.AppRef(), Service: serviceName, Instance: instanceID,
	}, provider.Window{})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeMetrics(w, series, selection)
}

// writeMetrics enforces the selection at the boundary and tells the page what
// was chosen, so unchosen panels are absent rather than empty. Enforcement
// lives here and not in the scrape: one Engine stats call carries every group
// anyway, so filtering collection would save nothing and add a second place
// the selection must be right.
func writeMetrics(w http.ResponseWriter, series provider.Series, selection []string) {
	if len(selection) > 0 {
		chosen := map[string]bool{}
		for _, name := range selection {
			chosen[name] = true
		}
		if !chosen["cpu"] {
			series.CPUPercent = nil
		}
		if !chosen["memory"] {
			series.MemoryBytes = nil
			series.MemoryLimit = 0
		}
		if !chosen["network"] {
			series.NetRxBytes = nil
			series.NetTxBytes = nil
		}
		if !chosen["block"] {
			series.BlockRead = nil
			series.BlockWrite = nil
		}
		if !chosen["pids"] {
			series.Pids = nil
		}
		if !chosen["throttling"] {
			series.CPUThrottled = nil
		}
		if !chosen["net_errors"] {
			series.NetErrors = nil
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": series, "metrics": selection})
}

// appLogs streams container logs. It is a plain chunked text response rather
// than SSE: a log line is not an event, and wrapping it in one would force
// every consumer to unwrap it again.
func (s *Server) appLogs(w http.ResponseWriter, r *http.Request) {
	adapter, target, application, ok := s.instanceProvider(w, r)
	if !ok {
		return
	}
	instanceID := r.URL.Query().Get("instance")
	if instanceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400", "message": "an instance query parameter is required",
		})
		return
	}

	follow := r.URL.Query().Get("follow") == "true"
	stream, err := adapter.Logs(r.Context(), target.Ref(), provider.InstanceRef{
		AppRef: application.AppRef(), Service: r.URL.Query().Get("service"), Instance: instanceID,
	}, provider.LogFilter{Tail: intQuery(r, "tail", 200), Follow: follow})
	if err != nil {
		s.fail(w, err)
		return
	}
	defer func() { _ = stream.Close() }()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Without this a proxy may buffer a followed stream until it ends, which
	// for a live tail is forever.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)
	buffer := make([]byte, 32<<10)
	for {
		n, err := stream.Read(buffer)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return
			}
			if canFlush {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
		if r.Context().Err() != nil {
			return
		}
	}
}

func (s *Server) appEvents(w http.ResponseWriter, r *http.Request) {
	adapter, target, application, ok := s.instanceProvider(w, r)
	if !ok {
		return
	}
	events, err := adapter.Events(r.Context(), target.Ref(), application.AppRef())
	if err != nil {
		s.fail(w, err)
		return
	}
	if events == nil {
		events = []provider.Event{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

// ---------------------------------------------------------------------------
// Targets, repositories, audit
// ---------------------------------------------------------------------------

func (s *Server) listTargets(w http.ResponseWriter, r *http.Request) {
	targets, err := store.In[store.Target](s.Store, store.Targets).Find(nil)
	if err != nil {
		s.fail(w, err)
		return
	}
	if targets == nil {
		targets = []store.Target{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": targets})
}

func (s *Server) createTarget(w http.ResponseWriter, r *http.Request) {
	var target store.Target
	if !decodeBody(w, r, &target) {
		return
	}
	if target.Name == "" || target.Provider == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400", "message": "name and provider are required",
		})
		return
	}
	if _, known := s.Providers[target.Provider]; !known {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400", "message": "no adapter for provider " + target.Provider,
		})
		return
	}
	target.CreatedAt = s.now()

	id, err := store.In[store.Target](s.Store, store.Targets).Put(target)
	if err != nil {
		s.fail(w, err)
		return
	}
	target.ID = id
	writeJSON(w, http.StatusCreated, target)
}

func (s *Server) listRepos(w http.ResponseWriter, r *http.Request) {
	repositories, err := store.In[store.Repository](s.Store, store.Repos).Find(nil)
	if err != nil {
		s.fail(w, err)
		return
	}
	if repositories == nil {
		repositories = []store.Repository{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"repositories": repositories})
}

func (s *Server) createRepo(w http.ResponseWriter, r *http.Request) {
	var repository store.Repository
	if !decodeBody(w, r, &repository) {
		return
	}
	if repository.Name == "" || repository.URL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400", "message": "name and url are required",
		})
		return
	}
	if repository.DefaultRef == "" {
		repository.DefaultRef = "main"
	}
	repository.CreatedAt = s.now()

	id, err := store.In[store.Repository](s.Store, store.Repos).Put(repository)
	if err != nil {
		s.fail(w, err)
		return
	}
	repository.ID = id
	writeJSON(w, http.StatusCreated, repository)
}

func (s *Server) readAudit(w http.ResponseWriter, r *http.Request) {
	records, err := store.In[store.AuditRecord](s.Store, store.Audit).Find(nil)
	if err != nil {
		s.fail(w, err)
		return
	}
	limit := intQuery(r, "limit", 100)
	// Find returns ascending TTID order, which is chronological. The most
	// recent records are the ones an operator wants.
	if len(records) > limit {
		records = records[len(records)-limit:]
	}
	if records == nil {
		records = []store.AuditRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": records})
}

// ---------------------------------------------------------------------------
// Registries
// ---------------------------------------------------------------------------
//
// A registry entry is a credential *binding*, which is exactly what
// secret:bind names, so creating one needs no new verb. Reading the list is
// target:read: it describes where a target may pull from. The action
// vocabulary is unchanged, and secret:bind finally has the route it was
// written for.

func (s *Server) listRegistries(w http.ResponseWriter, r *http.Request) {
	registries, err := store.In[store.Registry](s.Store, store.Registries).Find(nil)
	if err != nil {
		s.fail(w, err)
		return
	}
	if registries == nil {
		registries = []store.Registry{}
	}
	// The document holds a reference rather than a value, so there is nothing
	// to redact here — which is the property the whole design buys.
	writeJSON(w, http.StatusOK, map[string]any{"registries": registries})
}

func (s *Server) createRegistry(w http.ResponseWriter, r *http.Request) {
	var registry store.Registry
	if !decodeBody(w, r, &registry) {
		return
	}
	switch {
	case registry.Name == "" || registry.Server == "":
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400", "message": "name and server are required",
		})
		return
	case registry.PasswordRef == "":
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400",
			"message": "password_ref is required; HEIMDALL stores a reference to a credential, " +
				"never the credential itself",
		})
		return
	}
	registry.CreatedAt = s.now()

	id, err := store.In[store.Registry](s.Store, store.Registries).Put(registry)
	if err != nil {
		s.fail(w, err)
		return
	}
	registry.ID = id
	writeJSON(w, http.StatusCreated, registry)
}

// ---------------------------------------------------------------------------
// Outbound webhooks
// ---------------------------------------------------------------------------

func (s *Server) createOutboundWebhook(w http.ResponseWriter, r *http.Request) {
	var subscriber reconcile.OutboundWebhook
	if !decodeBody(w, r, &subscriber) {
		return
	}
	subscriber.Project = r.PathValue("project")
	if subscriber.URL == "" || !strings.HasPrefix(subscriber.URL, "https://") &&
		!strings.HasPrefix(subscriber.URL, "http://") {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400", "message": "url must be an http(s) URL",
		})
		return
	}
	id, err := store.In[reconcile.OutboundWebhook](s.Store, store.OutboundWebhooks).Put(subscriber)
	if err != nil {
		s.fail(w, err)
		return
	}
	subscriber.ID = id
	writeJSON(w, http.StatusCreated, subscriber)
}

func (s *Server) listOutboundWebhooks(w http.ResponseWriter, r *http.Request) {
	subscribers, err := store.In[reconcile.OutboundWebhook](s.Store, store.OutboundWebhooks).
		Find(map[string]any{"project": r.PathValue("project")})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": subscribers})
}

// exportAudit streams both ledgers as NDJSON, oldest first: every
// authorization decision from hd-audit and every operation from
// hd-operations, each line tagged with its ledger. The correlation keys —
// principal_id and policy_version — appear in both, so "who did what, and
// why was it allowed" is one grep across one file.
func (s *Server) exportAudit(w http.ResponseWriter, r *http.Request) {
	auditRows, err := store.In[store.AuditRecord](s.Store, store.Audit).Find(nil)
	if err != nil {
		s.fail(w, err)
		return
	}
	operations, err := store.In[store.Operation](s.Store, store.Operations).Find(nil)
	if err != nil {
		s.fail(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", "attachment; filename=heimdall-audit.ndjson")
	encoder := json.NewEncoder(w)
	for _, row := range auditRows {
		_ = encoder.Encode(map[string]any{"ledger": "authorization", "entry": row})
	}
	for _, operation := range operations {
		_ = encoder.Encode(map[string]any{"ledger": "operations", "entry": operation})
	}
}

// ---------------------------------------------------------------------------
// Target groups
// ---------------------------------------------------------------------------

func (s *Server) createTargetGroup(w http.ResponseWriter, r *http.Request) {
	var body store.TargetGroup
	if !decodeBody(w, r, &body) {
		return
	}
	// The path is authoritative for the project: it is what was authorized,
	// and a body naming a different one would place the group outside it.
	body.Project = r.PathValue("project")
	if body.Project == "" || body.Name == "" || len(body.Selector) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400",
			"message": "project, name, and a non-empty selector are required; " +
				"an empty selector would match nothing and read as a broken group",
		})
		return
	}
	body.CreatedAt = s.now()

	id, err := store.In[store.TargetGroup](s.Store, store.TargetGroups).Put(body)
	if err != nil {
		s.fail(w, err)
		return
	}
	body.ID = id
	writeJSON(w, http.StatusCreated, body)
}

func (s *Server) listTargetGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := store.In[store.TargetGroup](s.Store, store.TargetGroups).
		Find(map[string]any{"project": r.PathValue("project")})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

// listGroupMembers resolves a group's membership now, by matching tags. It is
// never stored: a target retagged a second ago belongs to a different group a
// second later, with no write to either.
func (s *Server) listGroupMembers(w http.ResponseWriter, r *http.Request) {
	group, err := store.In[store.TargetGroup](s.Store, store.TargetGroups).Get(r.PathValue("group"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "HD0404", "message": "no such group"})
		return
	}
	targets, err := store.In[store.Target](s.Store, store.Targets).
		Find(map[string]any{"project": group.Project})
	if err != nil {
		s.fail(w, err)
		return
	}

	members := make([]store.Target, 0, len(targets))
	for _, target := range targets {
		if group.Matches(target) {
			members = append(members, target)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": group.Name, "targets": members})
}
