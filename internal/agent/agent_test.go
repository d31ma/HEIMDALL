// These tests stand up a real TLS control plane, a real agent process
// (in-process, but the real code path), and a fake Docker Engine. Enrollment,
// mTLS, the long poll, the job, and the report all run for real.
//
// The one thing faked is the Docker Engine itself, so this runs on a machine
// with no daemon. scripts/e2e-docker.sh covers the live version.
package agent_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/d31ma/heimdall/internal/agent"
	"github.com/d31ma/heimdall/internal/dispatch"
	"github.com/d31ma/heimdall/internal/enroll"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/provider/docker"
	"github.com/d31ma/heimdall/internal/provider/docker/dockertest"
	"github.com/d31ma/heimdall/internal/spec"
)

const targetID = "tgt-1"

// controlPlane is the minimum of the real thing: the three agent routes over
// TLS with the real key material and the real dispatcher.
type controlPlane struct {
	server     *httptest.Server
	material   *enroll.Material
	dispatcher *dispatch.Dispatcher
	url        string
}

func newControlPlane(t *testing.T) *controlPlane {
	t.Helper()

	material, err := enroll.Ensure(t.TempDir(), []string{"127.0.0.1", "localhost"})
	if err != nil {
		t.Fatalf("key material: %v", err)
	}
	plane := &controlPlane{material: material, dispatcher: dispatch.New(20 * time.Second)}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/agent/enroll", plane.enroll(t))
	mux.HandleFunc("GET /api/v1/agent/work", plane.work)
	mux.HandleFunc("POST /api/v1/agent/result", plane.result)

	server := httptest.NewUnstartedServer(mux)
	server.TLS = material.ServerTLSConfig()
	server.StartTLS()
	t.Cleanup(server.Close)

	plane.server = server
	plane.url = server.URL
	return plane
}

func (c *controlPlane) issuer() *enroll.Issuer {
	return &enroll.Issuer{
		Key: c.material.EnrollmentKey, URL: c.url, Fingerprint: c.material.ServerFingerprint(),
	}
}

func (c *controlPlane) enroll(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Token string `json:"token"`
			CSR   string `json:"csr"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		token, err := c.issuer().Verify(body.Token)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "the enrollment token is not valid"})
			return
		}
		certificate, err := c.material.IssueAgentCertificate([]byte(body.CSR), "agt-1", token.TargetID)
		if err != nil {
			t.Errorf("issue: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"agent_id": "agt-1", "target_id": token.TargetID,
			"certificate_pem": string(certificate), "ca_pem": string(c.material.CACertificatePEM()),
		})
	}
}

func (c *controlPlane) work(w http.ResponseWriter, r *http.Request) {
	identity, ok := enroll.IdentityOf(r.TLS)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	job, found := c.dispatcher.Poll(r.Context(), identity.TargetID, 2*time.Second)
	if !found {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	_ = json.NewEncoder(w).Encode(job)
}

func (c *controlPlane) result(w http.ResponseWriter, r *http.Request) {
	identity, ok := enroll.IdentityOf(r.TLS)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var outcome dispatch.Outcome
	if err := json.NewDecoder(r.Body).Decode(&outcome); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := c.dispatcher.Complete(identity.TargetID, outcome); err != nil {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func sampleSpec() spec.DeploySpec {
	rendered := spec.DeploySpec{
		App: "checkout", Revision: "abc1234",
		Services: []spec.Service{
			{Name: "web", Image: "nginx:1.27", Ports: []spec.Port{{Published: 8080, Target: 80, Protocol: "tcp"}}},
			{Name: "db", Image: "ghcr.io/example/postgres:16",
				Env: []spec.EnvVar{{Key: "PASSWORD", Ref: "vault/db#password"}}},
		},
	}
	rendered.Normalize()
	return rendered
}

// enrolledAgent runs the real enrollment and returns a started agent loop
// against a fake Docker Engine.
func enrolledAgent(t *testing.T, plane *controlPlane) (*dockertest.Engine, agent.Credentials) {
	t.Helper()

	token, err := plane.issuer().Issue(targetID, time.Hour)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	dir := t.TempDir()
	credentials, err := agent.Enroll(context.Background(), dir, token)
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if credentials.TargetID != targetID {
		t.Fatalf("enrolled for target %q, want %q", credentials.TargetID, targetID)
	}

	// Reloading proves the credentials were persisted, not just returned.
	if reloaded, err := agent.Load(dir); err != nil || reloaded.AgentID != credentials.AgentID {
		t.Fatalf("credentials did not round-trip: %v %+v", err, reloaded)
	}

	engine := dockertest.New()
	t.Cleanup(engine.Close)
	return engine, credentials
}

func startAgent(t *testing.T, plane *controlPlane, credentials agent.Credentials, engine *dockertest.Engine) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	var running sync.WaitGroup
	running.Add(1)
	go func() {
		defer running.Done()
		loop := &agent.Agent{
			Credentials: credentials,
			Endpoint:    engine.URL(),
			PollWait:    time.Second,
			MaxBackoff:  200 * time.Millisecond,
		}
		if err := loop.Run(ctx); err != nil && ctx.Err() == nil {
			t.Errorf("agent stopped: %v", err)
		}
	}()
	t.Cleanup(func() { cancel(); running.Wait() })

	// Wait for the first poll to land. A sync issued before any agent has
	// ever connected fails immediately by design — "no agent has ever
	// connected" is the right answer to that — so a test must not race it.
	waitForAgent(t, plane, credentials.TargetID)
	return cancel
}

func waitForAgent(t *testing.T, plane *controlPlane, target string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, ever := plane.dispatcher.LastSeen(target); ever {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the agent never polled for work")
}

// TestEnrollAndDeploy is the whole agent path: enrol over a pinned
// connection, connect with the issued certificate, receive a sync job, and
// create containers on the local Engine.
func TestEnrollAndDeploy(t *testing.T) {
	plane := newControlPlane(t)
	engine, credentials := enrolledAgent(t, plane)
	startAgent(t, plane, credentials, engine)

	remote := &dispatch.Remote{
		Dispatcher: plane.dispatcher,
		Capability: (&docker.Provider{}).Capabilities(),
		Secrets: func(_ context.Context, ref string) (string, error) {
			return "resolved:" + ref, nil
		},
	}
	target := provider.Target{ID: targetID, Provider: "docker", Region: "alpha"}
	want := sampleSpec()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	plan, err := remote.Plan(ctx, target, want)
	if err != nil {
		t.Fatalf("remote plan: %v", err)
	}
	if !plan.Changes() {
		t.Fatal("planning against an empty host reported no changes")
	}

	result, err := remote.Apply(
		dispatch.WithApply(ctx, dispatch.ApplyOptions{Spec: want}), target, plan)
	if err != nil {
		t.Fatalf("remote apply: %v", err)
	}
	if len(result.Failures) > 0 {
		t.Fatalf("apply reported failures: %v", result.Failures)
	}
	if engine.Count() != 2 {
		t.Fatalf("the agent created %d containers, want 2", engine.Count())
	}

	// And the control plane can read the state back through the same agent.
	live, err := remote.Observe(ctx, target, plan.App)
	if err != nil {
		t.Fatalf("remote observe: %v", err)
	}
	if live.Revision != "abc1234" {
		t.Fatalf("live revision = %q", live.Revision)
	}
	if live.Rollup() != provider.Healthy {
		t.Fatalf("rollup = %s", live.Rollup())
	}
}

// TestSecretsReachTheContainerAndNowhereElse: the resolved value travels with
// the job over mTLS, is used, and is not written down at either end.
func TestSecretsReachTheContainerAndNowhereElse(t *testing.T) {
	plane := newControlPlane(t)
	engine, credentials := enrolledAgent(t, plane)
	startAgent(t, plane, credentials, engine)

	remote := &dispatch.Remote{
		Dispatcher: plane.dispatcher,
		Capability: (&docker.Provider{}).Capabilities(),
		Secrets: func(_ context.Context, ref string) (string, error) {
			return "s3cret-for-" + ref, nil
		},
		Registries: func(_ context.Context, image string) (*provider.RegistryCredential, error) {
			if strings.HasPrefix(image, "ghcr.io/") {
				return &provider.RegistryCredential{
					Server: "ghcr.io", Username: "bot", Password: "registry-token",
				}, nil
			}
			return nil, nil
		},
	}
	target := provider.Target{ID: targetID, Provider: "docker", Region: "alpha"}
	want := sampleSpec()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	plan, err := remote.Plan(ctx, target, want)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := remote.Apply(
		dispatch.WithApply(ctx, dispatch.ApplyOptions{Spec: want}), target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Collect under the lock and release it before anything else: AuthFor
	// takes the same mutex, and holding it across that call deadlocks.
	var environment []string
	engine.Mu.Lock()
	for _, container := range engine.Containers {
		environment = append(environment, container.Env...)
	}
	engine.Mu.Unlock()

	found := false
	for _, env := range environment {
		if strings.HasPrefix(env, "PASSWORD=") {
			found = true
			if env != "PASSWORD=s3cret-for-vault/db#password" {
				t.Errorf("the secret did not arrive intact: %q", env)
			}
		}
	}
	if !found {
		t.Error("the secret never reached the container")
	}

	// The private image was pulled with the credential; the public one wasn't.
	if auth := engine.AuthFor("ghcr.io/example/postgres:16"); auth == nil || auth.Username != "bot" {
		t.Errorf("the private image was not pulled with a credential: %+v", auth)
	}
	if auth := engine.AuthFor("nginx:1.27"); auth != nil {
		t.Errorf("a public image was pulled with a credential: %+v", auth)
	}
}

// TestAnUnenrolledClientCannotPoll: the work route is reachable on the same
// listener as everything else, and mTLS is the only thing standing in front
// of it.
func TestAnUnenrolledClientCannotPoll(t *testing.T) {
	plane := newControlPlane(t)

	pinned, err := enroll.PinnedTLSConfig(plane.material.ServerFingerprint())
	if err != nil {
		t.Fatalf("pin: %v", err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: pinned}}

	response, err := client.Get(plane.url + "/api/v1/agent/work")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a client with no certificate polled for work: status %d", response.StatusCode)
	}
}

func TestEnrollmentRefusesAForgedToken(t *testing.T) {
	plane := newControlPlane(t)

	elsewhere := &enroll.Issuer{
		Key:         []byte("a-different-32-byte-signing-key!!"),
		URL:         plane.url,
		Fingerprint: plane.material.ServerFingerprint(),
	}
	forged, err := elsewhere.Issue(targetID, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if _, err := agent.Enroll(context.Background(), t.TempDir(), forged); err == nil {
		t.Fatal("a token signed with another key enrolled successfully")
	}
}

// TestEnrollmentRefusesAnImpostorControlPlane is the pin doing its job: the
// token names one fingerprint, and a server presenting another never receives
// it.
func TestEnrollmentRefusesAnImpostorControlPlane(t *testing.T) {
	real := newControlPlane(t)
	impostor := newControlPlane(t)

	// A genuine token for the real control plane, pointed at the impostor.
	token, err := real.issuer().Issue(targetID, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	var decoded map[string]any
	raw, err := decodeToken(token)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	decoded["url"] = impostor.url
	redirected := encodeToken(t, decoded)

	_, err = agent.Enroll(context.Background(), t.TempDir(), redirected)
	if err == nil {
		t.Fatal("the agent enrolled with a control plane whose certificate was not the pinned one")
	}
	if !strings.Contains(err.Error(), "fingerprint") && !strings.Contains(err.Error(), "certificate") {
		t.Errorf("the failure does not point at the pin: %v", err)
	}
}

func TestAgentRefusesWhenTheLocalEngineIsUnreachable(t *testing.T) {
	plane := newControlPlane(t)
	_, credentials := enrolledAgent(t, plane)

	loop := &agent.Agent{
		Credentials: credentials,
		// A port nothing is listening on.
		Endpoint: "tcp://127.0.0.1:1",
		PollWait: time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	err := loop.Run(ctx)
	if err == nil {
		t.Fatal("the agent started against an unreachable Docker Engine")
	}
	if !strings.Contains(err.Error(), "Docker Engine") {
		t.Errorf("the failure does not say what is wrong: %v", err)
	}
}

func TestSyncToATargetWithNoAgentSaysSo(t *testing.T) {
	plane := newControlPlane(t)
	remote := &dispatch.Remote{
		Dispatcher: plane.dispatcher,
		Capability: (&docker.Provider{}).Capabilities(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := remote.Observe(ctx, provider.Target{ID: "tgt-nobody"}, provider.AppRef{App: "checkout"})
	if err == nil {
		t.Fatal("observing a target with no agent succeeded")
	}
	// The message must tell an operator what to do, not just that it failed.
	if !strings.Contains(err.Error(), "heimdall enroll") {
		t.Errorf("the failure does not say how to fix it: %v", err)
	}
}

// TestRemoteRejectsUnsupportedFeaturesWithoutAnAgent proves plan-time
// rejection does not need a reachable host: telling an operator their compose
// file is unsupported should work while the host is down.
func TestRemoteRejectsUnsupportedFeaturesWithoutAnAgent(t *testing.T) {
	plane := newControlPlane(t)
	remote := &dispatch.Remote{
		Dispatcher: plane.dispatcher,
		Capability: (&docker.Provider{}).Capabilities(),
	}

	unsupported := spec.DeploySpec{App: "checkout", Revision: "abc1234",
		Services: []spec.Service{{Name: "web", Image: "nginx:1.27", Replicas: 5}}}
	unsupported.Normalize()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := remote.Plan(ctx, provider.Target{ID: "tgt-nobody"}, unsupported)
	if err == nil {
		t.Fatal("planned an unsupported spec")
	}
	var rejection *provider.RejectionError
	if !asRejection(err, &rejection) {
		t.Fatalf("the failure is not a capability rejection, so the host being down masked it: %v", err)
	}
}

// decodeToken and encodeToken let a test rewrite a token's public fields, so
// the pin can be exercised against a control plane the token was not for.
func decodeToken(encoded string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(encoded)
}

func encodeToken(t *testing.T, fields map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode token: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func asRejection(err error, target **provider.RejectionError) bool {
	return errors.As(err, target)
}

// TestObservabilityJobsRoundTrip: metrics, logs, and events reach an
// agent-managed container through the same rendezvous a sync does, so the
// instance drawer works for hosts the control plane cannot dial.
func TestObservabilityJobsRoundTrip(t *testing.T) {
	plane := newControlPlane(t)
	engine, credentials := enrolledAgent(t, plane)
	startAgent(t, plane, credentials, engine)

	// A running, labeled container for the observability calls to find.
	engine.Mu.Lock()
	engine.Containers["c-obs"] = &dockertest.Container{
		ID: "c-obs", Name: "/alpha-checkout-web", Image: "nginx:1", Running: true,
		Labels: map[string]string{
			"dev.delma.heimdall.managed-by": "heimdall",
			"dev.delma.heimdall.project":    "alpha",
			"dev.delma.heimdall.app":        "checkout",
			"dev.delma.heimdall.service":    "web",
			"dev.delma.heimdall.revision":   "abc1234",
		},
		Started: time.Now().Add(-time.Hour),
	}
	engine.Mu.Unlock()

	remote := &dispatch.Remote{
		Dispatcher: plane.dispatcher,
		Capability: (&docker.Provider{}).Capabilities(),
	}
	target := provider.Target{ID: targetID, Provider: "docker"}
	instance := provider.InstanceRef{
		AppRef:  provider.AppRef{Project: "alpha", App: "checkout"},
		Service: "web", Instance: "c-obs",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	series, err := remote.Metrics(ctx, target, instance, provider.Window{})
	if err != nil {
		t.Fatalf("remote metrics: %v", err)
	}
	if len(series.CPUPercent) == 0 || series.MemoryLimit == 0 {
		t.Fatalf("the live sample is empty: %+v", series)
	}

	stream, err := remote.Logs(ctx, target, instance, provider.LogFilter{Tail: 50})
	if err != nil {
		t.Fatalf("remote logs: %v", err)
	}
	logs, _ := io.ReadAll(stream)
	_ = stream.Close()
	if len(logs) == 0 {
		t.Fatal("the log tail came back empty")
	}

	// A follow is refused with direction, not accepted and wedged.
	if _, err := remote.Logs(ctx, target, instance, provider.LogFilter{Follow: true}); err == nil {
		t.Fatal("a followed stream was accepted over the rendezvous")
	}

	if _, err := remote.Events(ctx, target, provider.AppRef{Project: "alpha", App: "checkout"}); err != nil {
		t.Fatalf("remote events: %v", err)
	}
}
