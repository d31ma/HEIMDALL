package api_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/d31ma/heimdall/internal/agent"
	"github.com/d31ma/heimdall/internal/api"
	"github.com/d31ma/heimdall/internal/dispatch"
	"github.com/d31ma/heimdall/internal/observe"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/store"
)

// identityFor forges the request-level result of a completed mTLS handshake.
// The TLS handshake itself is proven in internal/agent and internal/enroll;
// this test is about what the handler does with an identity, so the identity
// is constructed rather than negotiated.
func identityFor(targetID string) *tls.ConnectionState {
	leaf := &x509.Certificate{
		Subject: pkix.Name{
			CommonName:         "agt-" + targetID,
			Organization:       []string{"HEIMDALL agent"},
			OrganizationalUnit: []string{targetID},
		},
	}
	return &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{leaf}}}
}

type rollupWorld struct {
	server   *api.Server
	targetID string
	otherID  string
	appID    string
}

func newRollupWorld(t *testing.T) *rollupWorld {
	t.Helper()
	if _, err := exec.LookPath("fylo"); err != nil {
		t.Skipf("fylo binary not on PATH: %v", err)
	}
	storage, err := store.Open(filepath.Join(t.TempDir(), "fylo-root"), "")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	targets := store.In[store.Target](storage, store.Targets)
	mine, _ := targets.Put(store.Target{Project: "alpha", Name: "edge", Provider: "docker", AgentID: "agt-1"})
	other, _ := targets.Put(store.Target{Project: "alpha", Name: "core", Provider: "docker", AgentID: "agt-2"})
	appID, _ := store.In[store.Application](storage, store.Applications).Put(store.Application{
		Project: "alpha", Name: "site", TargetID: mine, RepoID: "r", Path: ".",
	})

	return &rollupWorld{
		targetID: mine, otherID: other, appID: appID,
		server: &api.Server{
			Version: "test", Store: storage,
			Dispatcher: dispatch.New(0),
			Observe: &observe.Collector{
				Store:  storage,
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			},
		},
	}
}

func (w *rollupWorld) ship(t *testing.T, callerTarget string, rollups []agent.WireRollup) (int, int) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"rollups": rollups})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/rollups", bytes.NewReader(body))
	request.TLS = identityFor(callerTarget)
	recorder := httptest.NewRecorder()
	w.server.Handler().ServeHTTP(recorder, request)

	var answer struct {
		Accepted int `json:"accepted"`
	}
	_ = json.NewDecoder(recorder.Body).Decode(&answer)
	return recorder.Code, answer.Accepted
}

func wireRollup(project, app string) agent.WireRollup {
	return agent.WireRollup{
		Project: project, App: app,
		Rollup: observe.Rollup{
			Service: "web", InstanceID: "c1",
			Minute: time.Now().UTC().Truncate(time.Minute),
			CPUAvg: 12, CPUMax: 30, MemAvg: 1 << 20, MemMax: 2 << 20,
		},
	}
}

// TestAgentRollupIngest: a batch from the right agent lands in hd-rollups
// with the application resolved, and answers metrics reads.
func TestAgentRollupIngest(t *testing.T) {
	w := newRollupWorld(t)

	status, accepted := w.ship(t, w.targetID, []agent.WireRollup{wireRollup("alpha", "site")})
	if status != http.StatusOK || accepted != 1 {
		t.Fatalf("ship: status=%d accepted=%d", status, accepted)
	}

	stored, err := store.In[observe.Rollup](w.server.Store, store.Rollups).Find(nil)
	if err != nil || len(stored) != 1 {
		t.Fatalf("stored rollups: %d err=%v", len(stored), err)
	}
	if stored[0].AppID != w.appID {
		t.Fatalf("the application was not resolved: %+v", stored[0])
	}

	// And the series endpoint's source answers from it.
	series, err := w.server.Observe.SeriesFor(provider.InstanceRef{Instance: "c1"}, provider.Window{})
	if err != nil || len(series.CPUPercent) == 0 {
		t.Fatalf("series from ingested rollups: %v %+v", err, series)
	}
}

// TestAgentCannotWriteAnotherHostsHistory: the target in the certificate is
// the only target whose applications a batch may name.
func TestAgentCannotWriteAnotherHostsHistory(t *testing.T) {
	w := newRollupWorld(t)

	// The app lives on w.targetID; the caller's certificate names otherID.
	status, accepted := w.ship(t, w.otherID, []agent.WireRollup{wireRollup("alpha", "site")})
	if status != http.StatusOK || accepted != 0 {
		t.Fatalf("a foreign agent's rollup was accepted: status=%d accepted=%d", status, accepted)
	}
	if stored, _ := store.In[observe.Rollup](w.server.Store, store.Rollups).Find(nil); len(stored) != 0 {
		t.Fatal("a foreign rollup reached the store")
	}

	// An unknown app is dropped the same way, without failing the batch.
	status, accepted = w.ship(t, w.targetID, []agent.WireRollup{
		wireRollup("alpha", "nonexistent"), wireRollup("alpha", "site"),
	})
	if status != http.StatusOK || accepted != 1 {
		t.Fatalf("mixed batch: status=%d accepted=%d, want the good one kept", status, accepted)
	}
}

// TestRollupIngestRequiresAnAgentIdentity: no certificate, no write.
func TestRollupIngestRequiresAnAgentIdentity(t *testing.T) {
	w := newRollupWorld(t)
	body, _ := json.Marshal(map[string]any{"rollups": []agent.WireRollup{wireRollup("alpha", "site")}})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/rollups", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	w.server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d without a certificate", recorder.Code)
	}
}

// TestRateLimitBoundsARunawayClient, and never health probes.
func TestRateLimitBoundsARunawayClient(t *testing.T) {
	w := newRollupWorld(t)
	handler := w.server.RateLimit(w.server.Handler())

	limited := 0
	for i := 0; i < 400; i++ {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
		request.RemoteAddr = "203.0.113.9:1234"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("400 instant requests from one client were never limited")
	}

	// A different client is unaffected by the noisy one.
	request := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	request.RemoteAddr = "203.0.113.10:1234"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code == http.StatusTooManyRequests {
		t.Fatal("one client's flood limited another client")
	}

	// Health is never limited: a load balancer that gets 429 from /healthz
	// removes the instance exactly when it is busiest.
	for i := 0; i < 300; i++ {
		probe := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		probe.RemoteAddr = "203.0.113.9:1234"
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, probe)
		if out.Code == http.StatusTooManyRequests {
			t.Fatal("a health probe was rate limited")
		}
	}
}
