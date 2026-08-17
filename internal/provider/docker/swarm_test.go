package docker_test

import (
	"context"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/provider/conformance"
	"github.com/d31ma/heimdall/internal/provider/docker"
	"github.com/d31ma/heimdall/internal/provider/docker/dockertest"
	"github.com/d31ma/heimdall/internal/spec"
)

func swarmSpec(revision string) spec.DeploySpec {
	deploy := spec.DeploySpec{
		App: "checkout", Revision: revision,
		Services: []spec.Service{
			{Name: "web", Image: "nginx:1.27", Replicas: 3,
				Ports: []spec.Port{{Published: 8080, Target: 80, Protocol: "tcp"}}},
			{Name: "worker", Image: "ghcr.io/example/worker:2", Replicas: 2, Wave: 1},
		},
	}
	deploy.Normalize()
	return deploy
}

func swarmHarness(t *testing.T) (conformance.Harness, *dockertest.Engine) {
	engine := dockertest.New()
	t.Cleanup(engine.Close)
	adapter := &docker.Swarm{}
	target := provider.Target{ID: "tgt-swarm", Provider: "swarm", Region: "conf", Endpoint: engine.URL()}

	return conformance.Harness{
		Provider: adapter,
		Target:   target,
		Supported: func(revision string) spec.DeploySpec {
			deploy := swarmSpec(revision)
			return deploy
		},
		Unsupported: func() spec.DeploySpec {
			// A bind mount names a node-local path; Swarm rejects it.
			deploy := spec.DeploySpec{
				App: "checkout", Revision: "r",
				Services: []spec.Service{{Name: "web", Image: "nginx:1",
					Volumes: []spec.Volume{{Source: "/etc/config", Target: "/config"}}}},
			}
			deploy.Normalize()
			return deploy
		},
		ApplyContext: func(ctx context.Context, want spec.DeploySpec) context.Context {
			return docker.WithApply(ctx, docker.ApplyOptions{Spec: want, Prune: true})
		},
		Reset: func(t *testing.T) {
			engine.Mu.Lock()
			engine.Services = map[string]*dockertest.Service{}
			engine.Mu.Unlock()
		},
	}, engine
}

func TestSwarmConformance(t *testing.T) {
	harness, _ := swarmHarness(t)
	conformance.Run(t, harness)
}

// TestSwarmReplicasReachTheScheduler: the reason Swarm exists as a target.
func TestSwarmReplicasReachTheScheduler(t *testing.T) {
	harness, engine := swarmHarness(t)
	want := swarmSpec("rev-replicas")
	ctx := context.Background()

	plan, err := harness.Provider.Plan(ctx, harness.Target, want)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := harness.Provider.Apply(harness.ApplyContext(ctx, want), harness.Target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	instances, err := harness.Provider.Instances(ctx, harness.Target, plan.App)
	if err != nil {
		t.Fatalf("instances: %v", err)
	}
	// 3 web + 2 worker tasks.
	if len(instances) != 5 {
		t.Fatalf("the scheduler placed %d tasks, want 5", len(instances))
	}

	live, err := harness.Provider.Observe(ctx, harness.Target, plan.App)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	web := live.Services["web"]
	if web.Replicas != 3 || web.Ready != 3 || web.Health != provider.Healthy {
		t.Fatalf("web state: %+v", web)
	}
	_ = engine
}

// TestSwarmServiceLogsAreDemuxed: the service log endpoint frames output the
// same way container logs are, and the adapter must strip the frames.
func TestSwarmServiceLogsAreDemuxed(t *testing.T) {
	harness, _ := swarmHarness(t)
	want := swarmSpec("rev-logs")
	ctx := context.Background()

	plan, _ := harness.Provider.Plan(ctx, harness.Target, want)
	if _, err := harness.Provider.Apply(harness.ApplyContext(ctx, want), harness.Target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	swarm := harness.Provider.(*docker.Swarm)
	stream, err := swarm.Logs(ctx, harness.Target, provider.InstanceRef{
		AppRef: plan.App, Service: "web",
	}, provider.LogFilter{Tail: 10})
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	text, _ := io.ReadAll(stream)
	_ = stream.Close()
	if !strings.Contains(string(text), "service log line one") {
		t.Fatalf("log text was not demuxed: %q", text)
	}
	if strings.ContainsRune(string(text), 0) {
		t.Fatal("frame headers leaked into the log text")
	}
}

// TestSwarmFileSecretRotation is the immutable-secret dance made a
// non-event: each value gets a content-hash-named Swarm secret, rotation is
// an ordinary service update, stale versions are pruned, and a version some
// service still references survives the prune because the Engine refuses.
func TestSwarmFileSecretRotation(t *testing.T) {
	harness, engine := swarmHarness(t)
	value := "hunter2"
	adapter := harness.Provider.(*docker.Swarm)
	adapter.SecretResolver = func(context.Context, string) (string, error) { return value, nil }

	secretSpec := func(revision, hint string) spec.DeploySpec {
		deploy := spec.DeploySpec{
			App: "checkout", Revision: revision,
			Services: []spec.Service{{
				Name: "api", Image: "ghcr.io/example/api:1", Replicas: 1,
				Secrets: []spec.SecretMount{{
					Name: "db_password", Ref: "sops/secrets.enc.yaml#db", ContentHint: hint,
				}},
			}},
		}
		deploy.Normalize()
		return deploy
	}
	ctx := context.Background()
	apply := func(want spec.DeploySpec) provider.Plan {
		t.Helper()
		plan, err := harness.Provider.Plan(ctx, harness.Target, want)
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		result, err := harness.Provider.Apply(harness.ApplyContext(ctx, want), harness.Target, plan)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if len(result.Failures) > 0 {
			t.Fatalf("apply failures: %v", result.Failures)
		}
		return plan
	}
	secretNames := func() []string {
		engine.Mu.Lock()
		defer engine.Mu.Unlock()
		var names []string
		for _, secret := range engine.SwarmSecrets {
			names = append(names, secret.Name)
		}
		sort.Strings(names)
		return names
	}
	referencedSecret := func() (id, name string) {
		engine.Mu.Lock()
		defer engine.Mu.Unlock()
		for _, service := range engine.Services {
			if name, _ := service.Spec["Name"].(string); name != "conf-checkout-api" {
				continue
			}
			template, _ := service.Spec["TaskTemplate"].(map[string]any)
			container, _ := template["ContainerSpec"].(map[string]any)
			references, _ := container["Secrets"].([]any)
			for _, raw := range references {
				reference, _ := raw.(map[string]any)
				id, _ = reference["SecretID"].(string)
				name, _ = reference["SecretName"].(string)
			}
		}
		return id, name
	}

	// First apply: one content-named secret, referenced by the service.
	apply(secretSpec("rev-1", "sha256:aaaaaaaaaaaa"))
	names := secretNames()
	if len(names) != 1 || !strings.HasPrefix(names[0], "conf-checkout-db_password-") {
		t.Fatalf("secrets after first apply: %v", names)
	}
	firstID, firstName := referencedSecret()
	if firstName != names[0] || firstID == "" {
		t.Fatalf("the service references %q (%s), want %q", firstName, firstID, names[0])
	}

	// A stranger keeps a handle on the old version: another service, outside
	// the prune's labels, references it.
	engine.Mu.Lock()
	engine.Services["foreign"] = &dockertest.Service{
		ID: "foreign", Version: 1,
		Spec: map[string]any{
			"Name": "someone-elses",
			"TaskTemplate": map[string]any{"ContainerSpec": map[string]any{
				"Secrets": []any{map[string]any{"SecretID": firstID, "SecretName": firstName}},
			}},
		},
	}
	engine.Mu.Unlock()

	// Rotation: new value, new hint. The hint difference is what makes the
	// service hash differ, so the plan updates rather than noops.
	value = "hunter3"
	plan := apply(secretSpec("rev-2", "sha256:bbbbbbbbbbbb"))
	updated := false
	for _, operation := range plan.Operations {
		if operation.Service == "api" && operation.Kind == provider.OpUpdate {
			updated = true
		}
	}
	if !updated {
		t.Fatalf("rotation did not plan an update: %+v", plan.Operations)
	}

	names = secretNames()
	if len(names) != 2 {
		t.Fatalf("secrets after rotation: %v (the in-use old version must survive the prune)", names)
	}
	_, nowReferenced := referencedSecret()
	if nowReferenced == firstName {
		t.Fatal("the service still references the pre-rotation secret")
	}

	// The stranger lets go; the next apply prunes the stale version.
	engine.Mu.Lock()
	delete(engine.Services, "foreign")
	engine.Mu.Unlock()
	apply(secretSpec("rev-3", "sha256:bbbbbbbbbbbb"))
	if names = secretNames(); len(names) != 1 || names[0] == firstName {
		t.Fatalf("stale secret survived an unobstructed prune: %v", names)
	}
}
