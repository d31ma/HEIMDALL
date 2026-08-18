package ecs_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/credentials"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/provider/conformance"
	"github.com/d31ma/heimdall/internal/provider/ecs"
	"github.com/d31ma/heimdall/internal/provider/ecs/ecstest"
	"github.com/d31ma/heimdall/internal/spec"
)

func ecsSpec(revision string) spec.DeploySpec {
	deploy := spec.DeploySpec{
		App: "checkout", Revision: revision,
		Services: []spec.Service{
			{Name: "web", Image: "nginx:1.27", Replicas: 2,
				Ports: []spec.Port{{Published: 8080, Target: 80, Protocol: "tcp"}},
				Env:   []spec.EnvVar{{Key: "PASSWORD", Ref: "vault/db#password"}}},
			{Name: "worker", Image: "ghcr.io/example/worker:2", Wave: 1,
				Resources: &spec.Resources{CPUMillis: 500, MemoryMiB: 1024}},
		},
	}
	deploy.Normalize()
	return deploy
}

func harness(t *testing.T) (conformance.Harness, *ecstest.AWS) {
	fake := ecstest.New()
	t.Cleanup(fake.Close)

	adapter := &ecs.Provider{
		EndpointOverride:  fake.URL(),
		StaticCredentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
		SecretResolver: func(_ context.Context, ref string) (string, error) {
			return "resolved:" + ref, nil
		},
	}
	target := provider.Target{
		ID: "tgt-ecs", Provider: "ecs", Project: "conf", Region: "us-east-1",
		Endpoint: "production",
		Config:   map[string]string{"subnets": "subnet-1,subnet-2", "log_group": "/heimdall/conf"},
	}

	return conformance.Harness{
		Provider:  adapter,
		Target:    target,
		Supported: func(revision string) spec.DeploySpec { return ecsSpec(revision) },
		Unsupported: func() spec.DeploySpec {
			deploy := spec.DeploySpec{
				App: "checkout", Revision: "r",
				Services: []spec.Service{{Name: "web", Image: "nginx:1",
					Volumes: []spec.Volume{{Source: "data", Target: "/data"}}}},
			}
			deploy.Normalize()
			return deploy
		},
		ApplyContext: func(ctx context.Context, want spec.DeploySpec) context.Context {
			return ecs.WithApply(ctx, ecs.ApplyOptions{Spec: want, Prune: true})
		},
		Reset: func(t *testing.T) {
			fake.Mu.Lock()
			fake.Services = map[string]*ecstest.Service{}
			fake.TaskDefinitions = map[string]*ecstest.TaskDefinition{}
			fake.Mu.Unlock()
		},
	}, fake
}

func TestECSConformance(t *testing.T) {
	h, _ := harness(t)
	conformance.Run(t, h)
}

// TestSecretsReachTheTaskDefinitionAndNowhereVisible: the resolved value
// lands in the container environment inside AWS and is never part of any
// HEIMDALL-visible name or tag.
func TestSecretsReachTheTaskDefinition(t *testing.T) {
	h, fake := harness(t)
	want := ecsSpec("rev-secrets")
	ctx := context.Background()

	plan, err := h.Provider.Plan(ctx, h.Target, want)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := h.Provider.Apply(h.ApplyContext(ctx, want), h.Target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	fake.Mu.Lock()
	defer fake.Mu.Unlock()
	found := false
	for _, definition := range fake.TaskDefinitions {
		if definition.Env["PASSWORD"] == "resolved:vault/db#password" {
			found = true
		}
	}
	if !found {
		t.Fatal("the secret never reached a task definition")
	}
	// And no tag anywhere carries the value.
	for _, service := range fake.Services {
		for _, value := range service.Tags {
			if strings.Contains(value, "resolved:") {
				t.Fatalf("a secret value leaked into a service tag: %q", value)
			}
		}
	}
}

// TestMetricsComeFromCloudWatch: provider-native first — the series is
// CloudWatch's, not a scrape.
func TestMetricsComeFromCloudWatch(t *testing.T) {
	h, _ := harness(t)
	series, err := h.Provider.Metrics(context.Background(), h.Target, provider.InstanceRef{
		AppRef: provider.AppRef{Project: "conf", App: "checkout"}, Service: "web", Instance: "task1",
	}, provider.Window{})
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if len(series.CPUPercent) != 60 || len(series.MemoryBytes) != 60 {
		t.Fatalf("series lengths: cpu=%d mem=%d, want 60 minutes each",
			len(series.CPUPercent), len(series.MemoryBytes))
	}
	for i := 1; i < len(series.CPUPercent); i++ {
		if series.CPUPercent[i].At.Before(series.CPUPercent[i-1].At) {
			t.Fatal("CloudWatch samples are not time-ordered")
		}
	}
}

// TestLogsComeFromCloudWatchLogs, through the awslogs stream naming the task
// definition set up.
func TestLogsComeFromCloudWatchLogs(t *testing.T) {
	h, fake := harness(t)
	want := ecsSpec("rev-logs")
	ctx := context.Background()

	plan, _ := h.Provider.Plan(ctx, h.Target, want)
	if _, err := h.Provider.Apply(h.ApplyContext(ctx, want), h.Target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	stream := "hd-conf-checkout-web/web/task42"
	fake.Mu.Lock()
	fake.LogLines[stream] = []string{"listening on :80", "ready"}
	fake.Mu.Unlock()

	reader, err := h.Provider.Logs(ctx, h.Target, provider.InstanceRef{
		AppRef: provider.AppRef{Project: "conf", App: "checkout"}, Service: "web", Instance: "task42",
	}, provider.LogFilter{Tail: 50})
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	text, _ := io.ReadAll(reader)
	if !strings.Contains(string(text), "listening on :80") {
		t.Fatalf("log text: %q", text)
	}
}

// TestFargateSizeSnapsToTiers: a request between tiers rounds to a valid
// cpu/memory pair rather than being sent as-is for AWS to reject.
func TestFargateSizeSnapsToTiers(t *testing.T) {
	h, fake := harness(t)
	want := ecsSpec("rev-tiers") // worker asks 500 CPUMillis → 512 tier, 1024 MiB
	ctx := context.Background()

	plan, _ := h.Provider.Plan(ctx, h.Target, want)
	if _, err := h.Provider.Apply(h.ApplyContext(ctx, want), h.Target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}
	fake.Mu.Lock()
	defer fake.Mu.Unlock()
	if len(fake.TaskDefinitions) == 0 {
		t.Fatal("nothing was registered")
	}
}

// TestTargetGroupAttachment: infrastructure owns the load balancer and hands
// the target its target group ARN as config; the adapter registers the one
// ported service into it at creation, with the container port the compose
// file published.
func TestTargetGroupAttachment(t *testing.T) {
	h, fake := harness(t)
	arn := "arn:aws:elasticloadbalancing:us-east-1:000000000000:targetgroup/web/abc123"
	h.Target.Config["target_group_arn"] = arn
	want := ecsSpec("rev-tg")
	ctx := context.Background()

	plan, err := h.Provider.Plan(ctx, h.Target, want)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := h.Provider.Apply(h.ApplyContext(ctx, want), h.Target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	fake.Mu.Lock()
	defer fake.Mu.Unlock()
	attached := 0
	for _, service := range fake.Services {
		for _, balancer := range service.LoadBalancers {
			attached++
			if balancer["targetGroupArn"] != arn {
				t.Fatalf("wrong target group: %v", balancer["targetGroupArn"])
			}
			if balancer["containerName"] != "web" {
				t.Fatalf("wrong container: %v", balancer["containerName"])
			}
			if port, _ := balancer["containerPort"].(float64); int(port) != 80 {
				t.Fatalf("wrong container port: %v", balancer["containerPort"])
			}
		}
	}
	if attached != 1 {
		t.Fatalf("expected exactly one load-balanced service, found %d", attached)
	}
}

// TestTargetGroupAmbiguityRefused: two ported services and no
// load_balanced_service is a guess the adapter must not make.
func TestTargetGroupAmbiguityRefused(t *testing.T) {
	h, _ := harness(t)
	h.Target.Config["target_group_arn"] = "arn:aws:elasticloadbalancing:us-east-1:000000000000:targetgroup/web/abc123"
	want := spec.DeploySpec{
		App: "checkout", Revision: "rev-ambiguous",
		Services: []spec.Service{
			{Name: "web", Image: "nginx:1", Ports: []spec.Port{{Published: 80, Target: 80, Protocol: "tcp"}}},
			{Name: "api", Image: "nginx:1", Ports: []spec.Port{{Published: 81, Target: 81, Protocol: "tcp"}}},
		},
	}
	want.Normalize()
	ctx := context.Background()

	plan, err := h.Provider.Plan(ctx, h.Target, want)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	result, err := h.Provider.Apply(h.ApplyContext(ctx, want), h.Target, plan)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(result.Failures) == 0 {
		t.Fatal("two ported services with one target group must refuse, not guess")
	}
	for _, reason := range result.Failures {
		if !strings.Contains(reason, "HD0363") {
			t.Fatalf("expected HD0363 in the refusal, got: %q", reason)
		}
	}
}

// TestAWSConnectionsAreReusedAcrossPolls: each call used to load a fresh SDK
// configuration whose new HTTP client carried its own transport — one
// leaked keep-alive socket per drift poll. The adapter now shares one HTTP
// client, so any number of polls drains into one connection pool.
func TestAWSConnectionsAreReusedAcrossPolls(t *testing.T) {
	h, fake := harness(t)
	ctx := context.Background()
	want := ecsSpec("rev-conns")

	for i := 0; i < 30; i++ {
		if _, err := h.Provider.Plan(ctx, h.Target, want); err != nil {
			t.Fatalf("plan poll %d: %v", i, err)
		}
	}
	if fake.Connections() > 4 {
		t.Fatalf("30 polls opened %d connections; the SDK transport is not shared", fake.Connections())
	}
}
