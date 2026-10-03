package api

import (
	"encoding/json"

	"github.com/d31ma/heimdall/internal/agent"
	"github.com/d31ma/heimdall/internal/observe"
	"net/http"
	"strconv"
	"time"

	"github.com/d31ma/heimdall/internal/dispatch"
	"github.com/d31ma/heimdall/internal/enroll"
	"github.com/d31ma/heimdall/internal/store"
)

// The agent routes sit outside the SESAME boundary, and that is deliberate
// rather than an exception being carved out.
//
// An agent is not a principal. It holds no grant, takes no action of its own,
// and cannot ask for anything: it receives work that an already-authorized
// human sync produced, for exactly the one target its certificate names. The
// authorization decision was made when the sync was requested. Asking SESAME
// again here would be a second decision about a request nobody made.
//
// What replaces it is mTLS: `enroll.IdentityOf` reads the target from a
// verified client certificate, and reads it from VerifiedChains only, so a
// self-signed certificate proves nothing.

// maxPollWait bounds a long poll. Longer than this and proxies start dropping
// idle connections, which reads to an operator as a flaky agent.
const maxPollWait = 90 * time.Second

// agentIdentity authenticates an agent request, or writes the refusal.
func (s *Server) agentIdentity(w http.ResponseWriter, r *http.Request) (enroll.AgentIdentity, bool) {
	if s.Dispatcher == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"code": "HD0503", "message": "this control plane is not accepting agents",
		})
		return enroll.AgentIdentity{}, false
	}
	identity, ok := enroll.IdentityOf(r.TLS)
	if !ok {
		// No detail: the caller is unauthenticated, and "your certificate is
		// from the wrong CA" is more than it needs to know.
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"code": "HD0401", "message": "a client certificate issued by this control plane is required",
		})
		return enroll.AgentIdentity{}, false
	}
	return identity, true
}

type enrollRequest struct {
	Token string `json:"token"`
	CSR   string `json:"csr"`
}

// agentEnroll exchanges an enrollment token for a client certificate.
//
// It is the one agent route reachable without a certificate — an enrolling
// agent has none yet — and the token is what stands in for one. The agent has
// already pinned this control plane's certificate fingerprint before sending
// anything, so the exchange is not vulnerable to a machine in the middle.
func (s *Server) agentEnroll(w http.ResponseWriter, r *http.Request) {
	if s.Material == nil || s.Dispatcher == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"code": "HD0503", "message": "this control plane is not accepting agents",
		})
		return
	}

	var body enrollRequest
	if !decodeBody(w, r, &body) {
		return
	}

	issuer := &enroll.Issuer{
		Key:         s.Material.EnrollmentKey,
		URL:         s.PublicURL,
		Fingerprint: s.Material.ServerFingerprint(),
	}
	token, err := issuer.Verify(body.Token)
	if err != nil {
		// One refusal for every failure mode. The caller is unauthenticated,
		// and distinguishing "expired" from "forged" tells it which part of a
		// guess was right.
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"code": "HD0401", "message": "the enrollment token is not valid",
		})
		return
	}

	// The token names a target; the target must still exist. An enrollment
	// for a deleted target would produce an agent nothing can ever dispatch
	// to.
	target, err := store.In[store.Target](s.Store, store.Targets).Get(token.TargetID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"code": "HD0401", "message": "the enrollment token is not valid",
		})
		return
	}

	agentID := "agt-" + token.TargetID
	certificate, err := s.Material.IssueAgentCertificate([]byte(body.CSR), agentID, target.ID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400", "message": err.Error(),
		})
		return
	}

	// Record the agent on the target so an operator can see one was enrolled.
	if err := store.In[store.Target](s.Store, store.Targets).
		Patch(target.ID, map[string]any{"agent_id": agentID}); err != nil {
		s.fail(w, err)
		return
	}

	if s.Audit != nil {
		// Enrollment is a security event even though no principal made it:
		// something now holds a credential for this target.
		s.Audit.Record(Entry{
			Action: "agent:enroll", Resource: "target:" + target.ID,
			Outcome: "allow", ReasonCode: "allow_enrollment_token",
			Method: r.Method, Path: r.URL.Path, PrincipalID: agentID,
		})
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"agent_id":        agentID,
		"target_id":       target.ID,
		"certificate_pem": string(certificate),
		"ca_pem":          string(s.Material.CACertificatePEM()),
	})
}

// agentWork is the long poll. It returns 204 when nothing is waiting, which
// the agent answers by polling again — that empty round trip is what keeps
// the connection warm through proxies without a heartbeat protocol.
func (s *Server) agentWork(w http.ResponseWriter, r *http.Request) {
	identity, ok := s.agentIdentity(w, r)
	if !ok {
		return
	}

	wait := 30 * time.Second
	if requested, err := strconv.Atoi(r.URL.Query().Get("wait")); err == nil && requested > 0 {
		wait = time.Duration(requested) * time.Second
	}
	if wait > maxPollWait {
		wait = maxPollWait
	}

	// The target comes from the certificate, never from the request. An agent
	// that could name its own target could poll for another host's work.
	job, found := s.Dispatcher.Poll(r.Context(), identity.TargetID, wait)
	if !found {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// agentResult accepts a completed job.
func (s *Server) agentResult(w http.ResponseWriter, r *http.Request) {
	identity, ok := s.agentIdentity(w, r)
	if !ok {
		return
	}

	var outcome dispatch.Outcome
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes*32)).Decode(&outcome); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400", "message": "the result body is not valid",
		})
		return
	}

	// Complete checks the job belongs to this target, so an agent cannot
	// report on another host's deployment.
	if err := s.Dispatcher.Complete(identity.TargetID, outcome); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"code": "HD0403", "message": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "recorded"})
}

type enrollTokenRequest struct {
	// Lifetime is a duration string such as "30m". Empty uses the default.
	Lifetime string `json:"lifetime,omitempty"`
}

// mintEnrollment issues an enrollment token for a target.
//
// It is an authorized route, unlike the three below: a human is asking for a
// credential that will let a host act on a target, and that is exactly the
// kind of request the boundary exists to decide. It is authorized as
// target:update because enrolling an agent changes what the target is.
//
// It also has to be a route rather than a local command: `heimdall serve`
// holds FYLO's exclusive root lock, so nothing else on the host can open the
// store while the control plane is running.
func (s *Server) mintEnrollment(w http.ResponseWriter, r *http.Request) {
	if s.Material == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"code": "HD0503", "message": "this control plane is not accepting agents",
		})
		return
	}
	if s.PublicURL == "" {
		writeJSON(w, http.StatusConflict, map[string]string{
			"code": "HD0409",
			"message": "no public URL is configured, so a token cannot be bound to this control plane; " +
				"set HD_PUBLIC_URL and restart",
		})
		return
	}

	target, err := store.In[store.Target](s.Store, store.Targets).Get(r.PathValue("target"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"code": "HD0404", "message": "no such target",
		})
		return
	}

	var body enrollTokenRequest
	if r.ContentLength > 0 && !decodeBody(w, r, &body) {
		return
	}
	lifetime := enroll.DefaultLifetime
	if body.Lifetime != "" {
		parsed, err := time.ParseDuration(body.Lifetime)
		if err != nil || parsed <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"code": "HD0400", "message": "lifetime must be a positive duration such as 30m",
			})
			return
		}
		lifetime = parsed
	}

	issuer := &enroll.Issuer{
		Key: s.Material.EnrollmentKey, URL: s.PublicURL, Fingerprint: s.Material.ServerFingerprint(),
	}
	token, err := issuer.Issue(target.ID, lifetime)
	if err != nil {
		s.fail(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"token":       token,
		"target_id":   target.ID,
		"target_name": target.Name,
		"url":         s.PublicURL,
		"fingerprint": s.Material.ServerFingerprint(),
		"expires_in":  lifetime.String(),
	})
}

// maxRollupBatch bounds one shipment. An agent batches a minute at a time,
// so hundreds means something is wrong, not busy.
const maxRollupBatch = 1024

// agentRollups ingests a batch of minute rollups from an agent's own scrape
// loop. The target comes from the client certificate; every rollup must name
// an application actually deployed to that target, or it is dropped — an
// agent must not be able to write history into another host's charts.
func (s *Server) agentRollups(w http.ResponseWriter, r *http.Request) {
	identity, ok := s.agentIdentity(w, r)
	if !ok {
		return
	}
	if s.Observe == nil || s.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"code": "HD0503", "message": "this control plane is not collecting metrics",
		})
		return
	}

	var body struct {
		Rollups []agent.WireRollup `json:"rollups"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes*8)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "HD0400", "message": "the rollup body is not valid",
		})
		return
	}
	if len(body.Rollups) > maxRollupBatch {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
			"code": "HD0413", "message": "too many rollups in one batch",
		})
		return
	}

	accepted := make([]observe.Rollup, 0, len(body.Rollups))
	// Resolved per batch: one lookup per distinct app, not per rollup.
	resolved := map[string]string{}
	for _, wire := range body.Rollups {
		key := wire.Project + "/" + wire.App
		appID, known := resolved[key]
		if !known {
			applications, err := store.In[store.Application](s.Store, store.Applications).
				Find(map[string]any{"project": wire.Project, "name": wire.App})
			if err != nil || len(applications) == 0 || applications[0].TargetID != identity.TargetID {
				// Unknown app, or an app on a different target. Dropped, not
				// erred: one bad label must not reject the batch.
				resolved[key] = ""
				continue
			}
			appID = applications[0].ID
			resolved[key] = appID
		}
		if appID == "" {
			continue
		}
		rollup := wire.Rollup
		rollup.AppID = appID
		accepted = append(accepted, rollup)
	}

	s.Observe.Ingest(accepted)
	writeJSON(w, http.StatusOK, map[string]any{"accepted": len(accepted)})
}

// mountAgentRoutes registers the three agent routes. They are listed here and
// nowhere else, so the set of routes outside the SESAME boundary stays
// countable.
func (s *Server) mountAgentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/agent/enroll", s.agentEnroll)
	mux.HandleFunc("GET /api/v1/agent/work", s.agentWork)
	mux.HandleFunc("POST /api/v1/agent/result", s.agentResult)
	mux.HandleFunc("POST /api/v1/agent/rollups", s.agentRollups)
}
