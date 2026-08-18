package docker_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/provider/conformance"
	"github.com/d31ma/heimdall/internal/provider/docker"
	"github.com/d31ma/heimdall/internal/provider/docker/dockertest"
	"github.com/d31ma/heimdall/internal/spec"
)

// supported is the spec the conformance harness deploys: two services, an
// ordering dependency, a healthcheck, a named volume, and a secret reference,
// all of which a standalone Engine can express.
func supported(revision string) spec.DeploySpec {
	rendered := spec.DeploySpec{
		App:      "checkout",
		Revision: revision,
		Services: []spec.Service{
			{
				Name:      "api",
				Image:     "ghcr.io/example/api:1.4.2",
				Command:   []string{"/bin/api"},
				Env:       []spec.EnvVar{{Key: "DATABASE_URL", Ref: "vault/checkout#database_url"}},
				Ports:     []spec.Port{{Published: 8000, Target: 8000, Protocol: "tcp"}},
				DependsOn: []string{"db"},
				Restart:   "unless-stopped",
				Healthcheck: &spec.Healthcheck{
					Test: []string{"CMD", "true"}, IntervalMS: 10000, Retries: 3,
				},
				Resources: &spec.Resources{CPUMillis: 500, MemoryMiB: 512},
			},
			{
				Name:    "db",
				Image:   "postgres:16.4-alpine",
				Volumes: []spec.Volume{{Source: "pgdata", Target: "/var/lib/postgresql/data"}},
			},
		},
	}
	rendered.Normalize()
	return rendered
}

// unsupported asks a standalone Engine for replicas, which it has no
// scheduler for.
func unsupported() spec.DeploySpec {
	rendered := spec.DeploySpec{
		App:      "checkout",
		Revision: "0000000",
		Services: []spec.Service{{Name: "api", Image: "nginx:1.27", Replicas: 3}},
	}
	rendered.Normalize()
	return rendered
}

func newProvider(engine *dockertest.Engine) (*docker.Provider, provider.Target) {
	adapter := &docker.Provider{
		SecretResolver: func(_ context.Context, ref string) (string, error) {
			return "resolved-value-for-" + ref, nil
		},
	}
	target := provider.Target{ID: "tgt-1", Provider: "docker", Region: "alpha", Endpoint: engine.URL()}
	return adapter, target
}

// TestConformance is the assertion that matters most in this package: the
// Docker adapter satisfies the same contract every future adapter must.
func TestConformance(t *testing.T) {
	engine := dockertest.New()
	t.Cleanup(engine.Close)
	adapter, target := newProvider(engine)

	conformance.Run(t, conformance.Harness{
		Provider:    adapter,
		Target:      target,
		Supported:   supported,
		Unsupported: unsupported,
		ApplyContext: func(ctx context.Context, want spec.DeploySpec) context.Context {
			return docker.WithApply(ctx, docker.ApplyOptions{Spec: want})
		},
		Reset: func(*testing.T) { engine.Reset() },
	})
}

func TestCapabilitiesRejectReplicasWithAnActionableCaveat(t *testing.T) {
	capabilities := (&docker.Provider{}).Capabilities()

	if capabilities.Of(provider.FeatureReplicas) != provider.Rejected {
		t.Error("a standalone Engine has no scheduler; replicas must be rejected, not silently run once")
	}
	caveat := capabilities.Caveats[provider.FeatureReplicas]
	if !strings.Contains(caveat, "Swarm") {
		t.Errorf("the caveat does not say what to do instead: %q", caveat)
	}
	// An unanswered feature must never read as supported.
	if capabilities.Of(provider.Feature("invented")) != provider.Rejected {
		t.Error("an unanswered feature did not default to rejected")
	}
}

func TestRejectionNamesEveryProblemAtOnce(t *testing.T) {
	engine := dockertest.New()
	t.Cleanup(engine.Close)
	adapter, target := newProvider(engine)

	bad := spec.DeploySpec{App: "checkout", Revision: "aaaaaaa", Services: []spec.Service{
		{Name: "api", Image: "nginx:1.27", Replicas: 3},
		{Name: "worker", Image: "nginx:1.27", Replicas: 5},
	}}
	bad.Normalize()

	_, err := adapter.Plan(context.Background(), target, bad)
	if err == nil {
		t.Fatal("planned an unsupported spec")
	}
	message := err.Error()
	// One round trip must surface both problems, not one per attempt.
	if !strings.Contains(message, "api") || !strings.Contains(message, "worker") {
		t.Fatalf("rejection does not name both services: %s", message)
	}
}

func TestApplyStampsTheRevisionOntoEveryContainer(t *testing.T) {
	engine := dockertest.New()
	t.Cleanup(engine.Close)
	adapter, target := newProvider(engine)

	ctx := context.Background()
	want := supported("abc1234")
	plan, err := adapter.Plan(ctx, target, want)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := adapter.Apply(docker.WithApply(ctx, docker.ApplyOptions{Spec: want}), target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	engine.Mu.Lock()
	defer engine.Mu.Unlock()
	if len(engine.Containers) != 2 {
		t.Fatalf("created %d containers, want 2", len(engine.Containers))
	}
	for _, container := range engine.Containers {
		if container.Labels[docker.LabelRevision] != "abc1234" {
			t.Errorf("%s carries revision %q", container.Name, container.Labels[docker.LabelRevision])
		}
		if container.Labels[docker.LabelManagedBy] != "heimdall" {
			t.Errorf("%s is not labelled as managed by heimdall, so Observe would not find it", container.Name)
		}
		if container.Labels[docker.LabelServiceHash] == "" {
			t.Errorf("%s has no service hash, so every plan would report a change", container.Name)
		}
	}
}

// TestSecretsResolveOnlyAtApply is the end of the secret path: the value
// exists in the container's environment and nowhere else.
func TestSecretsResolveOnlyAtApply(t *testing.T) {
	engine := dockertest.New()
	t.Cleanup(engine.Close)
	adapter, target := newProvider(engine)

	ctx := context.Background()
	want := supported("abc1234")
	plan, err := adapter.Plan(ctx, target, want)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	// The plan is stored and shown to operators. It must not carry the value.
	for _, operation := range plan.Operations {
		if strings.Contains(operation.Reason, "resolved-value") {
			t.Fatalf("a resolved secret leaked into the plan: %+v", operation)
		}
	}

	if _, err := adapter.Apply(docker.WithApply(ctx, docker.ApplyOptions{Spec: want}), target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	engine.Mu.Lock()
	defer engine.Mu.Unlock()
	found := false
	for _, container := range engine.Containers {
		for _, env := range container.Env {
			if strings.HasPrefix(env, "DATABASE_URL=") {
				found = true
				if !strings.Contains(env, "resolved-value-for-vault/checkout#database_url") {
					t.Errorf("secret was not resolved into the container: %q", env)
				}
			}
		}
	}
	if !found {
		t.Error("DATABASE_URL never reached the container")
	}
}

func TestApplyWithoutASecretResolverRefusesRatherThanStartingIncomplete(t *testing.T) {
	engine := dockertest.New()
	t.Cleanup(engine.Close)

	// No resolver configured, but the spec needs one.
	adapter := &docker.Provider{}
	target := provider.Target{ID: "tgt-1", Provider: "docker", Region: "alpha", Endpoint: engine.URL()}

	ctx := context.Background()
	want := supported("abc1234")
	plan, err := adapter.Plan(ctx, target, want)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	result, err := adapter.Apply(docker.WithApply(ctx, docker.ApplyOptions{Spec: want}), target, plan)
	if err != nil {
		t.Fatalf("apply returned a hard error rather than a per-service failure: %v", err)
	}
	if result.Failures["api"] == "" {
		t.Fatal("a service needing an unavailable secret was started anyway")
	}
	if !strings.Contains(result.Failures["api"], "secret resolver") {
		t.Errorf("failure does not explain the cause: %q", result.Failures["api"])
	}
}

// TestPruneRequiresOptIn guards the operation whose failure mode is deleting
// something real.
func TestPruneRequiresOptIn(t *testing.T) {
	engine := dockertest.New()
	t.Cleanup(engine.Close)
	adapter, target := newProvider(engine)
	ctx := context.Background()

	both := supported("abc1234")
	plan, err := adapter.Plan(ctx, target, both)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := adapter.Apply(docker.WithApply(ctx, docker.ApplyOptions{Spec: both}), target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Drop a service from the desired state.
	fewer := both
	fewer.Services = []spec.Service{both.Services[0]}
	fewer.Revision = "def5678"
	fewer.Normalize()

	prunePlan, err := adapter.Plan(ctx, target, fewer)
	if err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	var pruneOperation *provider.Operation
	for i := range prunePlan.Operations {
		if prunePlan.Operations[i].Prune {
			pruneOperation = &prunePlan.Operations[i]
		}
	}
	if pruneOperation == nil {
		t.Fatal("removing a service produced no prune operation")
	}

	// Without opt-in the container must survive, and the skip must be visible.
	result, err := adapter.Apply(docker.WithApply(ctx, docker.ApplyOptions{Spec: fewer}), target, prunePlan)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if result.Failures[pruneOperation.Service] == "" {
		t.Error("the skipped prune was not reported; an operator would think it happened")
	}
	if engine.Count() != 2 {
		t.Fatalf("prune ran without opt-in: %d containers remain", engine.Count())
	}

	// With opt-in it is removed.
	if _, err := adapter.Apply(
		docker.WithApply(ctx, docker.ApplyOptions{Spec: fewer, Prune: true}), target, prunePlan,
	); err != nil {
		t.Fatalf("apply with prune: %v", err)
	}
	if engine.Count() != 1 {
		t.Fatalf("prune did not remove the container: %d remain", engine.Count())
	}
}

// TestOutOfBandRemovalIsDrift is the self-heal precondition: the adapter must
// notice a container someone deleted behind its back.
func TestOutOfBandRemovalIsDrift(t *testing.T) {
	engine := dockertest.New()
	t.Cleanup(engine.Close)
	adapter, target := newProvider(engine)
	ctx := context.Background()

	want := supported("abc1234")
	plan, err := adapter.Plan(ctx, target, want)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := adapter.Apply(docker.WithApply(ctx, docker.ApplyOptions{Spec: want}), target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	engine.RemoveByService("db")

	drifted, err := adapter.Plan(ctx, target, want)
	if err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	if !drifted.Changes() {
		t.Fatal("an out-of-band removal produced no operations")
	}
	for _, operation := range drifted.Operations {
		if operation.Service == "db" {
			if operation.Kind != provider.OpCreate {
				t.Errorf("db operation is %q, want create", operation.Kind)
			}
			return
		}
	}
	t.Error("no operation targets the removed service")
}

// TestStoppedContainerIsRestartedNotRecreated keeps a self-heal from pulling
// and recreating when starting the existing container would do.
func TestStoppedContainerIsRestartedNotRecreated(t *testing.T) {
	engine := dockertest.New()
	t.Cleanup(engine.Close)
	adapter, target := newProvider(engine)
	ctx := context.Background()

	want := supported("abc1234")
	plan, _ := adapter.Plan(ctx, target, want)
	if _, err := adapter.Apply(docker.WithApply(ctx, docker.ApplyOptions{Spec: want}), target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	engine.StopByService("db")

	drifted, err := adapter.Plan(ctx, target, want)
	if err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	for _, operation := range drifted.Operations {
		if operation.Service == "db" && operation.Kind != provider.OpRestart {
			t.Errorf("db operation is %q, want restart", operation.Kind)
		}
	}
}

func TestObserveRollsUpHealth(t *testing.T) {
	engine := dockertest.New()
	t.Cleanup(engine.Close)
	adapter, target := newProvider(engine)
	ctx := context.Background()

	want := supported("abc1234")
	plan, _ := adapter.Plan(ctx, target, want)
	if _, err := adapter.Apply(docker.WithApply(ctx, docker.ApplyOptions{Spec: want}), target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	live, err := adapter.Observe(ctx, target, plan.App)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if live.Rollup() != provider.Healthy {
		t.Fatalf("rollup = %s, want Healthy", live.Rollup())
	}

	engine.StopByService("db")
	live, err = adapter.Observe(ctx, target, plan.App)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	// One stopped service must drag the whole application down; an app that
	// reports Healthy with a dead database is worse than no signal.
	if live.Rollup() == provider.Healthy {
		t.Fatalf("rollup is Healthy with a stopped service: %+v", live.Services)
	}
}

func TestFailedPullIsAFailureNotASuccess(t *testing.T) {
	engine := dockertest.New()
	t.Cleanup(engine.Close)
	adapter, target := newProvider(engine)
	ctx := context.Background()

	// The Engine reports a pull failure inside a 200 stream. Reading only the
	// status code would call this a success.
	engine.FailPull = "postgres"

	want := supported("abc1234")
	plan, _ := adapter.Plan(ctx, target, want)
	result, err := adapter.Apply(docker.WithApply(ctx, docker.ApplyOptions{Spec: want}), target, plan)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if result.Failures["db"] == "" {
		t.Fatal("a failed pull was reported as a successful apply")
	}
	if !strings.Contains(result.Failures["db"], "manifest unknown") {
		t.Errorf("failure does not carry the engine's message: %q", result.Failures["db"])
	}
}

func TestMetricsMatchDockerStatsArithmetic(t *testing.T) {
	engine := dockertest.New()
	t.Cleanup(engine.Close)
	adapter, target := newProvider(engine)
	ctx := context.Background()

	want := supported("abc1234")
	plan, _ := adapter.Plan(ctx, target, want)
	if _, err := adapter.Apply(docker.WithApply(ctx, docker.ApplyOptions{Spec: want}), target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}
	instances, err := adapter.Instances(ctx, target, plan.App)
	if err != nil {
		t.Fatalf("instances: %v", err)
	}

	series, err := adapter.Metrics(ctx, target, instances[0].Ref, provider.Window{})
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	// 100ms of 1000ms system time across 2 CPUs is 20%.
	if len(series.CPUPercent) != 1 || series.CPUPercent[0].Value != 20 {
		t.Errorf("cpu = %+v, want a single 20%% sample", series.CPUPercent)
	}
	// Usage minus the page cache, the way `docker stats` reports it.
	if len(series.MemoryBytes) != 1 || series.MemoryBytes[0].Value != float64(150<<20) {
		t.Errorf("memory = %+v, want 150MiB after subtracting inactive_file", series.MemoryBytes)
	}
	if series.NetRxBytes[0].Value != 1024 || series.BlockWrite[0].Value != 8192 {
		t.Errorf("network or block io wrong: %+v %+v", series.NetRxBytes, series.BlockWrite)
	}
}

func TestLogsAreDemultiplexed(t *testing.T) {
	engine := dockertest.New()
	t.Cleanup(engine.Close)
	adapter, target := newProvider(engine)
	ctx := context.Background()

	want := supported("abc1234")
	plan, _ := adapter.Plan(ctx, target, want)
	if _, err := adapter.Apply(docker.WithApply(ctx, docker.ApplyOptions{Spec: want}), target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}
	instances, _ := adapter.Instances(ctx, target, plan.App)

	stream, err := adapter.Logs(ctx, target, instances[0].Ref, provider.LogFilter{Tail: 10})
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	defer func() { _ = stream.Close() }()

	body, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("read logs: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "listening on :8000") || !strings.Contains(text, "ready") {
		t.Fatalf("log payload lost: %q", text)
	}
	// The 8-byte frame headers must not survive into what an operator reads.
	if strings.ContainsRune(text, '\x01') {
		t.Fatalf("stream frame headers leaked into the log text: %q", text)
	}
}

func TestHeimdallNeverTouchesForeignContainers(t *testing.T) {
	engine := dockertest.New()
	t.Cleanup(engine.Close)
	adapter, target := newProvider(engine)

	// A container nobody labelled. It must be invisible to every read and
	// survive every apply.
	engine.Inject(&dockertest.Container{
		ID: "foreign", Name: "someone-elses-database", Image: "postgres:15",
		Labels: map[string]string{"app": "not-ours"}, Running: true,
	})

	ctx := context.Background()
	want := supported("abc1234")
	plan, err := adapter.Plan(ctx, target, want)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	for _, operation := range plan.Operations {
		if operation.Service == "someone-elses-database" {
			t.Fatal("planned an operation against an unmanaged container")
		}
	}
	if _, err := adapter.Apply(
		docker.WithApply(ctx, docker.ApplyOptions{Spec: want, Prune: true}), target, plan,
	); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if _, alive := engine.Get("foreign"); !alive {
		t.Fatal("an unmanaged container was removed")
	}
}

// TestConnectionsAreReusedAcrossPolls: every reconcile and observe poll used
// to build a fresh transport whose keep-alive socket was never returned — a
// long-running control plane leaked one connection per poll until the host
// ran out of ephemeral ports. The adapter now holds one client per endpoint.
func TestConnectionsAreReusedAcrossPolls(t *testing.T) {
	fake := dockertest.New()
	t.Cleanup(fake.Close)
	adapter := &docker.Provider{}
	target := provider.Target{ID: "tgt", Provider: "docker", Project: "p", Endpoint: fake.URL()}
	app := provider.AppRef{Project: "p", App: "web"}

	ctx := context.Background()
	for i := 0; i < 30; i++ {
		if _, err := adapter.Observe(ctx, target, app); err != nil {
			t.Fatalf("observe poll %d: %v", i, err)
		}
	}
	// A shared pool may hold a few connections on a slow runner; the leak
	// this regresses opens exactly one per poll.
	if fake.Connections() > 10 {
		t.Fatalf("30 polls opened %d connections; the transport is not being reused", fake.Connections())
	}

	swarm := &docker.Swarm{}
	before := fake.Connections()
	empty := spec.DeploySpec{App: "web", Revision: "r1"}
	empty.Normalize()
	for i := 0; i < 30; i++ {
		if _, err := swarm.Plan(ctx, target, empty); err != nil {
			t.Fatalf("swarm plan poll %d: %v", i, err)
		}
	}
	if fake.Connections()-before > 10 {
		t.Fatalf("30 swarm polls opened %d connections", fake.Connections()-before)
	}
}
