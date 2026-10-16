package cloudrun_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/services/provider/cloudrun"
)

func TestContainerCommandReachesCloudRun(t *testing.T) {
	h, fake := harness(t)
	want := runSpec("rev-command")
	want.Services[0].Entrypoint = []string{"/app/server"}
	want.Services[0].Command = []string{"--listen", "0.0.0.0:8080"}
	ctx := context.Background()
	plan, err := h.Provider.Plan(ctx, h.Target, want)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Provider.Apply(h.ApplyContext(ctx, want), h.Target, plan); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	service := fake.services["projects/my-gcp-project/locations/europe-west1/services/hd-conf-checkout-web"]
	template := service["template"].(map[string]any)
	container := template["containers"].([]any)[0].(map[string]any)
	if !reflect.DeepEqual(container["command"], []any{"/app/server"}) {
		t.Errorf("command = %v", container["command"])
	}
	if !reflect.DeepEqual(container["args"], []any{"--listen", "0.0.0.0:8080"}) {
		t.Errorf("args = %v", container["args"])
	}
}

func TestOrdinaryServiceKeepsZeroMinimumInstances(t *testing.T) {
	h, fake := harness(t)
	want := runSpec("rev-on-demand")
	ctx := context.Background()
	plan, err := h.Provider.Plan(ctx, h.Target, want)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Provider.Apply(h.ApplyContext(ctx, want), h.Target, plan); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	service := fake.services["projects/my-gcp-project/locations/europe-west1/services/hd-conf-checkout-web"]
	template := service["template"].(map[string]any)
	scaling := template["scaling"].(map[string]any)
	if minimum := scaling["minInstanceCount"]; minimum != nil && minimum != float64(0) {
		t.Errorf("ordinary service minimum = %v; on-demand must remain zero", minimum)
	}
}

func TestUDPIsRejectedBeforeCloudRunAccess(t *testing.T) {
	want := runSpec("rev-udp")
	want.Services[0].Ports[0].Protocol = "udp"
	adapter := &cloudrun.Provider{EndpointOverride: "http://127.0.0.1:1", NoAuth: true}
	_, err := adapter.Plan(context.Background(), provider.Target{}, want)
	var rejection *provider.RejectionError
	if !errors.As(err, &rejection) {
		t.Fatalf("expected plan rejection before network access, got %v", err)
	}
	if !strings.Contains(rejection.Error(), "web") || !strings.Contains(rejection.Error(), "udp") {
		t.Fatalf("rejection must locate the unsupported port: %v", rejection)
	}
}

func TestComposeHealthcheckIsRejectedBeforeCloudRunAccess(t *testing.T) {
	want := runSpec("rev-healthcheck")
	want.Services[0].Healthcheck = &spec.Healthcheck{Test: []string{"CMD", "/app/check"}}
	adapter := &cloudrun.Provider{EndpointOverride: "http://127.0.0.1:1", NoAuth: true}
	_, err := adapter.Plan(context.Background(), provider.Target{}, want)
	var rejection *provider.RejectionError
	if !errors.As(err, &rejection) {
		t.Fatalf("expected plan rejection before network access, got %v", err)
	}
	if !strings.Contains(rejection.Error(), "web") || !strings.Contains(rejection.Error(), "healthcheck") {
		t.Fatalf("rejection must locate the unsupported healthcheck: %v", rejection)
	}
}

func TestLiveImageDriftIsDetectedWithUnchangedAnnotations(t *testing.T) {
	h, fake := harness(t)
	want := runSpec("rev-image-drift")
	ctx := context.Background()
	plan, err := h.Provider.Plan(ctx, h.Target, want)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Provider.Apply(h.ApplyContext(ctx, want), h.Target, plan); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	service := fake.services["projects/my-gcp-project/locations/europe-west1/services/hd-conf-checkout-web"]
	template := service["template"].(map[string]any)
	container := template["containers"].([]any)[0].(map[string]any)
	container["image"] = "gcr.io/example/web:outside-git"
	fake.mu.Unlock()
	changed, err := h.Provider.Plan(ctx, h.Target, want)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Operations[0].Kind != provider.OpUpdate {
		t.Fatalf("image changed with original annotations, plan = %+v", changed)
	}
}

func TestMissingDeployedCommandRequiresAnUpdate(t *testing.T) {
	h, fake := harness(t)
	want := runSpec("rev-command-drift")
	want.Services[0].Entrypoint = []string{"/app/server"}
	want.Services[0].Command = []string{"--listen", "0.0.0.0:8080"}
	ctx := context.Background()
	plan, err := h.Provider.Plan(ctx, h.Target, want)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Provider.Apply(h.ApplyContext(ctx, want), h.Target, plan); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	service := fake.services["projects/my-gcp-project/locations/europe-west1/services/hd-conf-checkout-web"]
	template := service["template"].(map[string]any)
	container := template["containers"].([]any)[0].(map[string]any)
	delete(container, "command")
	delete(container, "args")
	fake.mu.Unlock()
	changed, err := h.Provider.Plan(ctx, h.Target, want)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Operations[0].Kind != provider.OpUpdate {
		t.Fatalf("command missing despite matching annotation, plan = %+v", changed)
	}
}
