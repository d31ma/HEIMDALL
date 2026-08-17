package cloudrun_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/provider/cloudrun"
	"github.com/d31ma/heimdall/internal/provider/conformance"
	"github.com/d31ma/heimdall/internal/spec"
)

// fakeCloudRun is the Admin API v2 at the HTTP boundary: enough of
// projects.locations.services to exercise the adapter's real client, real
// URLs, and real error paths.
type fakeCloudRun struct {
	mu       sync.Mutex
	services map[string]map[string]any // full name → service
	nextID   int
	server   *httptest.Server
}

func newFakeCloudRun() *fakeCloudRun {
	fake := &fakeCloudRun{services: map[string]map[string]any{}}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.route))
	return fake
}

func (f *fakeCloudRun) route(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path

	switch {
	// GET /v2/projects/P/locations/L/services — list
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/services"):
		out := []map[string]any{}
		prefix := strings.TrimPrefix(strings.TrimSuffix(path, "/services"), "/v2/")
		for name, service := range f.services {
			if strings.HasPrefix(name, prefix) {
				out = append(out, service)
			}
		}
		f.writeJSON(w, map[string]any{"services": out})

	// POST /v2/projects/P/locations/L/services?serviceId=x — create
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/services"):
		var service map[string]any
		_ = json.NewDecoder(r.Body).Decode(&service)
		id := r.URL.Query().Get("serviceId")
		parent := strings.TrimPrefix(strings.TrimSuffix(path, "/services"), "/v2/")
		name := parent + "/services/" + id
		if _, exists := f.services[name]; exists {
			http.Error(w, `{"error":{"code":409,"message":"exists"}}`, http.StatusConflict)
			return
		}
		f.nextID++
		service["name"] = name
		service["latestReadyRevision"] = fmt.Sprintf("%s/revisions/rev-%05d", name, f.nextID)
		service["createTime"] = "2026-08-16T00:00:00Z"
		service["conditions"] = []map[string]any{{
			"type": "Ready", "state": "CONDITION_SUCCEEDED",
			"message": "service is ready", "lastTransitionTime": "2026-08-16T00:00:00Z",
		}}
		f.services[name] = service
		// The real API returns a long-running operation; the SDK's Do()
		// unwraps only the operation envelope, which is all the adapter reads.
		f.writeJSON(w, map[string]any{"name": name + "/operations/op", "done": true})

	// PATCH /v2/.../services/x — update
	case r.Method == http.MethodPatch:
		name := strings.TrimPrefix(path, "/v2/")
		existing, ok := f.services[name]
		if !ok {
			http.Error(w, `{"error":{"code":404,"message":"not found"}}`, http.StatusNotFound)
			return
		}
		var service map[string]any
		_ = json.NewDecoder(r.Body).Decode(&service)
		service["name"] = name
		service["latestReadyRevision"] = existing["latestReadyRevision"]
		service["createTime"] = existing["createTime"]
		service["conditions"] = existing["conditions"]
		f.services[name] = service
		f.writeJSON(w, map[string]any{"name": name + "/operations/op", "done": true})

	// DELETE /v2/.../services/x
	case r.Method == http.MethodDelete:
		name := strings.TrimPrefix(path, "/v2/")
		if _, ok := f.services[name]; !ok {
			http.Error(w, `{"error":{"code":404,"message":"not found"}}`, http.StatusNotFound)
			return
		}
		delete(f.services, name)
		f.writeJSON(w, map[string]any{"name": name + "/operations/op", "done": true})

	default:
		http.Error(w, `{"error":{"code":400,"message":"unhandled `+r.Method+` `+path+`"}}`, http.StatusBadRequest)
	}
}

func (f *fakeCloudRun) writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func runSpec(revision string) spec.DeploySpec {
	deploy := spec.DeploySpec{
		App: "checkout", Revision: revision,
		Services: []spec.Service{
			{Name: "web", Image: "gcr.io/example/web:1",
				Ports: []spec.Port{{Published: 8080, Target: 8080, Protocol: "tcp"}},
				Env:   []spec.EnvVar{{Key: "PASSWORD", Ref: "vault/db#password"}}},
			{Name: "worker", Image: "gcr.io/example/worker:2", Wave: 1,
				Resources: &spec.Resources{CPUMillis: 1000, MemoryMiB: 512}},
		},
	}
	deploy.Normalize()
	return deploy
}

func harness(t *testing.T) (conformance.Harness, *fakeCloudRun) {
	fake := newFakeCloudRun()
	t.Cleanup(fake.server.Close)

	adapter := &cloudrun.Provider{
		EndpointOverride: fake.server.URL,
		NoAuth:           true,
		SecretResolver: func(_ context.Context, ref string) (string, error) {
			return "resolved:" + ref, nil
		},
	}
	target := provider.Target{
		ID: "tgt-run", Provider: "cloudrun", Project: "conf",
		Region: "europe-west1", Endpoint: "my-gcp-project",
	}

	return conformance.Harness{
		Provider:  adapter,
		Target:    target,
		Supported: func(revision string) spec.DeploySpec { return runSpec(revision) },
		Unsupported: func() spec.DeploySpec {
			// Two published ports on one service: Cloud Run serves one.
			deploy := spec.DeploySpec{
				App: "checkout", Revision: "r",
				Services: []spec.Service{{Name: "web", Image: "gcr.io/example/web:1",
					Ports: []spec.Port{
						{Published: 80, Target: 8080, Protocol: "tcp"},
						{Published: 9090, Target: 9090, Protocol: "tcp"},
					}}},
			}
			deploy.Normalize()
			return deploy
		},
		ApplyContext: func(ctx context.Context, want spec.DeploySpec) context.Context {
			return cloudrun.WithApply(ctx, cloudrun.ApplyOptions{Spec: want, Prune: true})
		},
		Reset: func(t *testing.T) {
			fake.mu.Lock()
			fake.services = map[string]map[string]any{}
			fake.mu.Unlock()
		},
	}, fake
}

func TestCloudRunConformance(t *testing.T) {
	h, _ := harness(t)
	conformance.Run(t, h)
}

// TestScaleToZeroIsAccepted: the one feature this runtime holds that the
// container runtimes reject.
func TestScaleToZeroIsAccepted(t *testing.T) {
	h, _ := harness(t)
	deploy := runSpec("rev-zero")
	// Replicas 1 → min instances 0: the platform may scale to zero.
	if err := provider.Validate(h.Provider.Capabilities(), deploy); err != nil {
		t.Fatalf("a scale-to-zero-capable spec was rejected: %v", err)
	}
}

// TestSecretsResolveIntoTheServiceDefinition and never into a label.
func TestSecretsResolveIntoTheServiceDefinition(t *testing.T) {
	h, fake := harness(t)
	want := runSpec("rev-secrets")
	ctx := context.Background()

	plan, err := h.Provider.Plan(ctx, h.Target, want)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := h.Provider.Apply(h.ApplyContext(ctx, want), h.Target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	raw, _ := json.Marshal(fake.services)
	if !strings.Contains(string(raw), "resolved:vault/db#password") {
		t.Fatal("the secret never reached a container definition")
	}
	for _, service := range fake.services {
		labels, _ := json.Marshal(service["annotations"])
		if strings.Contains(string(labels), "resolved:") {
			t.Fatal("a secret value leaked into a label")
		}
	}
}
