package azurefunctions

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/services/provider/conformance"
)

func functionSpec(revision string) spec.DeploySpec {
	want := spec.DeploySpec{App: "checkout", Revision: revision, Services: []spec.Service{
		{Name: "one", Image: "mcr.microsoft.com/example/function@sha256:" + strings.Repeat("a", 64), Function: &spec.Function{RuntimeContract: "azure-functions-v4", Invocation: "http"}},
		{Name: "two", Image: "mcr.microsoft.com/example/function@sha256:" + strings.Repeat("a", 64), Function: &spec.Function{RuntimeContract: "azure-functions-v4", Invocation: "http"}, Wave: 1},
	}}
	want.Normalize()
	return want
}

func TestConformanceThroughARMHTTP(t *testing.T) {
	adapter, server := newProtocolServer(t)
	conformance.Run(t, conformance.Harness{
		Provider: adapter, Target: testTarget(), Supported: functionSpec, FunctionSupported: functionSpec,
		Unsupported: func() spec.DeploySpec {
			want := functionSpec("unsupported")
			want.Services[0].Ports = []spec.Port{{Target: 80, Published: 8080, Protocol: "tcp"}}
			return want
		},
		ApplyContext: func(ctx context.Context, want spec.DeploySpec) context.Context {
			return WithApply(ctx, ApplyOptions{Spec: want, Prune: true})
		},
		Reset: server.reset, InstancesMayBeEmpty: true,
		ChangedImage: func(image string) string {
			return strings.Split(image, "@sha256:")[0] + "@sha256:" + strings.Repeat("b", 64)
		},
	})
}

func applySpec(t *testing.T, adapter *Provider, want spec.DeploySpec) provider.Plan {
	t.Helper()
	plan, err := adapter.Plan(context.Background(), testTarget(), want)
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Apply(WithApply(context.Background(), ApplyOptions{Spec: want, Prune: true}), testTarget(), plan)
	if err != nil || len(result.Failures) != 0 {
		t.Fatalf("apply: %v %+v", err, result.Failures)
	}
	return plan
}

func TestNativeKindStorageIngressAndWaveReadiness(t *testing.T) {
	adapter, server := newProtocolServer(t)
	want := functionSpec("native")
	applySpec(t, adapter, want)
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.bodies) != 2 {
		t.Fatalf("PUT count %d", len(server.bodies))
	}
	for _, body := range server.bodies {
		if body["kind"] != "functionapp" {
			t.Fatalf("wire kind: %v", body["kind"])
		}
		properties := body["properties"].(map[string]any)
		configuration := properties["configuration"].(map[string]any)
		ingress := configuration["ingress"].(map[string]any)
		if configuration["activeRevisionsMode"] != "Single" || ingress["external"] != false || ingress["targetPort"] != float64(80) || ingress["allowInsecure"] != false {
			t.Fatalf("unsafe wire configuration %+v", configuration)
		}
		template := properties["template"].(map[string]any)
		if template["scale"].(map[string]any)["minReplicas"] != float64(0) || template["scale"].(map[string]any)["allowScalingRuleOverride"] != false {
			t.Fatal("zero minimum was lost")
		}
		container := template["containers"].([]any)[0].(map[string]any)
		values := map[string]string{}
		for _, entry := range container["env"].([]any) {
			env := entry.(map[string]any)
			values[env["name"].(string)] = env["value"].(string)
		}
		if values["AzureWebJobsStorage__accountName"] != "functionstorage" || values["AzureWebJobsStorage__credential"] != "managedidentity" || values["AzureWebJobsStorage__clientId"] != testTarget().Config["identity_client_id"] || values["FUNCTIONS_EXTENSION_VERSION"] != "~4" {
			t.Fatalf("storage/runtime wire settings: %+v", values)
		}
	}
	first := resourceName(provider.AppRef{Project: "conf", App: "checkout"}, "one")
	second := resourceName(provider.AppRef{Project: "conf", App: "checkout"}, "two")
	gets := 0
	for _, call := range server.calls {
		if call == "GET:"+first {
			gets++
		}
		if call == "PUT:"+second && gets < 4 {
			t.Fatal("next wave started before first revision was ready")
		}
	}
}

func TestGeneratedScaleRulesArePlatformOwned(t *testing.T) {
	adapter, server := newProtocolServer(t)
	want := functionSpec("generated-rules")
	plan := applySpec(t, adapter, want)
	server.mu.Lock()
	deployed := server.apps[resourceName(plan.App, "one")]
	deployed.Properties.Template.Scale.Rules = []*armappcontainers.ScaleRule{{Name: pointer("functions-http"), HTTP: &armappcontainers.HTTPScaleRule{Metadata: map[string]*string{"concurrentRequests": pointer("100")}}}}
	deployed.Properties.Template.Scale.AllowScalingRuleOverride = pointer(false)
	ready := server.revisions[value(deployed.ID)+"/revisions/"+value(deployed.Properties.LatestReadyRevisionName)]
	ready.Properties.Template.Scale.Rules = deployed.Properties.Template.Scale.Rules
	server.mu.Unlock()
	unchanged, err := adapter.Plan(context.Background(), testTarget(), want)
	if err != nil || unchanged.Changes() {
		t.Fatalf("generated KEDA rules caused permanent drift: %+v %v", unchanged, err)
	}
	server.mu.Lock()
	deployed.Properties.Template.Scale.AllowScalingRuleOverride = nil
	server.mu.Unlock()
	unchanged, err = adapter.Plan(context.Background(), testTarget(), want)
	if err != nil || unchanged.Changes() {
		t.Fatalf("documented false default caused drift: %+v %v", unchanged, err)
	}
	server.mu.Lock()
	deployed.Properties.Template.Scale.AllowScalingRuleOverride = pointer(true)
	server.mu.Unlock()
	drift, err := adapter.Plan(context.Background(), testTarget(), want)
	if err != nil || drift.Operations[0].Kind != provider.OpUpdate {
		t.Fatalf("enabled scaling override escaped actual drift: %+v %v", drift, err)
	}
}

func TestUnknownTargetOptionsRejectBeforeARM(t *testing.T) {
	for _, key := range []string{"network", "concurrency", "workload_profile", "subscription_Id", ""} {
		t.Run(key, func(t *testing.T) {
			adapter, server := newProtocolServer(t)
			target := testTarget()
			target.Config[key] = "unsupported-value"
			_, err := adapter.Plan(context.Background(), target, functionSpec("unknown-option"))
			var diagnostic *AdapterError
			if !errors.As(err, &diagnostic) || diagnostic.Code != "HD0470" || !strings.Contains(diagnostic.Message, "unsupported azurefunctions target configuration") || strings.Contains(diagnostic.Message, "unsupported-value") || len(server.calls) != 0 {
				t.Fatalf("unknown option was silently discarded or reached ARM: %v calls=%v", err, server.calls)
			}
		})
	}
}

func TestAcceptedTemplateWithOldEqualRevisionNamesCannotConverge(t *testing.T) {
	adapter, server := newProtocolServer(t)
	want := functionSpec("old")
	oldImage := want.Services[0].Image
	initial := applySpec(t, adapter, want)
	name := resourceName(initial.App, "one")
	server.mu.Lock()
	oldRevision := value(server.apps[name].Properties.LatestReadyRevisionName)
	server.frozen = true
	server.mu.Unlock()
	want.Revision = "new"
	want.Services[0].Image = strings.Replace(want.Services[0].Image, strings.Repeat("a", 64), strings.Repeat("b", 64), 1)
	plan, err := adapter.Plan(context.Background(), testTarget(), want)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	result, err := adapter.Apply(WithApply(ctx, ApplyOptions{Spec: want}), testTarget(), plan)
	if !errors.Is(err, context.DeadlineExceeded) || len(result.Applied) != 0 {
		t.Fatalf("desired app template with equal old revision names falsely converged: %+v %v", result, err)
	}
	replan, err := adapter.Plan(context.Background(), testTarget(), want)
	if err != nil || replan.Operations[0].Kind != provider.OpUpdate {
		t.Fatalf("old immutable revision was hidden during planning: %+v %v", replan, err)
	}
	live, err := adapter.Observe(context.Background(), testTarget(), initial.App)
	if err != nil || live.Services["one"].Health != provider.Progressing || live.Services["one"].Image != oldImage || live.Services["one"].FunctionVersion != oldRevision {
		t.Fatalf("observation hid the actual old ready revision image or reported healthy: %+v %v", live, err)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	deployed := server.apps[name]
	if value(deployed.Properties.LatestRevisionName) != oldRevision || value(deployed.Properties.LatestReadyRevisionName) != oldRevision || value(deployed.Properties.Template.Containers[0].Image) != want.Services[0].Image {
		t.Fatal("fixture did not accept the new template while retaining both old names")
	}
	if len(server.bodies) != 3 {
		t.Fatal("a nonconverged revision crossed into the next wave")
	}
	read := false
	for _, call := range server.calls {
		read = read || call == "GET:"+oldRevision
	}
	if !read {
		t.Fatal("the real ARM pipeline did not read the immutable revision")
	}
}

func TestApplicationOnlyChangeReusesReadyRevisionAndImageRevertConverges(t *testing.T) {
	adapter, server := newProtocolServer(t)
	want := functionSpec("initial")
	initial := applySpec(t, adapter, want)
	name := resourceName(initial.App, "one")
	server.mu.Lock()
	oldRevision := value(server.apps[name].Properties.LatestReadyRevisionName)
	server.frozen = true
	server.mu.Unlock()
	want.Revision = "metadata-only"
	applySpec(t, adapter, want)
	server.mu.Lock()
	if value(server.apps[name].Properties.LatestReadyRevisionName) != oldRevision {
		t.Fatal("application-only fixture created a revision")
	}
	server.frozen = false
	server.mu.Unlock()
	want.Revision = "different-image"
	want.Services[0].Image = strings.Replace(want.Services[0].Image, strings.Repeat("a", 64), strings.Repeat("b", 64), 1)
	applySpec(t, adapter, want)
	want.Revision = "revert"
	want.Services[0].Image = strings.Replace(want.Services[0].Image, strings.Repeat("b", 64), strings.Repeat("a", 64), 1)
	applySpec(t, adapter, want)
	plan, err := adapter.Plan(context.Background(), testTarget(), want)
	if err != nil || plan.Changes() {
		t.Fatalf("revision-name independence broke image revert: %+v %v", plan, err)
	}
}

func TestReadyRevisionHealthThroughARM(t *testing.T) {
	mutations := map[string]func(*nativeRevision){
		"failed": func(revision *nativeRevision) {
			revision.Properties.ProvisioningState = pointer(armappcontainers.RevisionProvisioningStateFailed)
		},
		"unhealthy": func(revision *nativeRevision) {
			revision.Properties.HealthState = pointer(armappcontainers.RevisionHealthStateUnhealthy)
		},
		"inactive": func(revision *nativeRevision) { revision.Properties.Active = pointer(false) },
		"unknown-running": func(revision *nativeRevision) {
			revision.Properties.RunningState = pointer(armappcontainers.RevisionRunningStateUnknown)
		},
		"override": func(revision *nativeRevision) {
			revision.Properties.Template.Scale.AllowScalingRuleOverride = pointer(true)
		},
	}
	for key, mutate := range mutations {
		t.Run(key, func(t *testing.T) {
			adapter, server := newProtocolServer(t)
			want := functionSpec("readiness")
			initial := applySpec(t, adapter, want)
			server.mu.Lock()
			app := server.apps[resourceName(initial.App, "one")]
			mutate(server.revisions[value(app.ID)+"/revisions/"+value(app.Properties.LatestReadyRevisionName)])
			server.mu.Unlock()
			plan, err := adapter.Plan(context.Background(), testTarget(), want)
			if err != nil || plan.Operations[0].Kind != provider.OpUpdate {
				t.Fatalf("actual ready revision escaped planning: %+v %v", plan, err)
			}
		})
	}
	adapter, server := newProtocolServer(t)
	want := functionSpec("scale-zero")
	initial := applySpec(t, adapter, want)
	server.mu.Lock()
	for _, app := range server.apps {
		revision := server.revisions[value(app.ID)+"/revisions/"+value(app.Properties.LatestReadyRevisionName)]
		revision.Properties.HealthState = pointer(armappcontainers.RevisionHealthStateNone)
		revision.Properties.RunningState = pointer(armappcontainers.RevisionRunningStateStopped)
		revision.Properties.Replicas = pointer(int32(0))
	}
	server.mu.Unlock()
	plan, err := adapter.Plan(context.Background(), testTarget(), want)
	if err != nil || plan.Changes() {
		t.Fatalf("provisioned active zero replica revision caused perpetual drift: %+v %v", plan, err)
	}
	instances, err := adapter.Instances(context.Background(), testTarget(), initial.App)
	if err != nil || len(instances) != 0 {
		t.Fatalf("revision replica metadata became invented execution instances: %+v %v", instances, err)
	}
}

func TestTargetDiagnosticIsTypedAndActionable(t *testing.T) {
	adapter, server := newProtocolServer(t)
	target := testTarget()
	target.Config["storage_account_resource_id"] = "invalid"
	_, err := adapter.Plan(context.Background(), target, functionSpec("typed-error"))
	var diagnostic *AdapterError
	if !errors.As(err, &diagnostic) || diagnostic.Code != "HD0470" || !strings.Contains(diagnostic.Message, "Microsoft.Storage/storageAccounts") || len(server.calls) != 0 {
		t.Fatalf("missing typed target-reference diagnosis: %v", err)
	}
}

func TestRejectBeforeHTTP(t *testing.T) {
	mutations := map[string]func(*spec.Service){
		"ordinary":     func(service *spec.Service) { service.Function = nil },
		"timeout":      func(service *spec.Service) { service.Function.TimeoutMS = 1000 },
		"architecture": func(service *spec.Service) { service.Function.Architecture = "arm64" },
		"command":      func(service *spec.Service) { service.Command = []string{"serve"} },
		"rollout":      func(service *spec.Service) { service.Update = &spec.UpdateConfig{Order: "start-first"} },
		"reserved": func(service *spec.Service) {
			service.Env = []spec.EnvVar{{Key: "AzureWebJobsStorage", Value: "conflicting"}}
		},
		"mutable-image": func(service *spec.Service) { service.Image = "mcr.microsoft.com/example/function:latest" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			adapter, server := newProtocolServer(t)
			want := functionSpec("reject")
			mutate(&want.Services[0])
			_, err := adapter.Plan(context.Background(), testTarget(), want)
			var rejection *provider.RejectionError
			if !errors.As(err, &rejection) || len(server.calls) != 0 {
				t.Fatalf("expected located rejection before ARM, got %v, calls=%v", err, server.calls)
			}
		})
	}
}

func TestActualDriftWithUnchangedTags(t *testing.T) {
	mutations := map[string]func(*nativeApp){
		"kind":    func(app *nativeApp) { app.Kind = "" },
		"image":   func(app *nativeApp) { app.Properties.Template.Containers[0].Image = pointer("unexpected") },
		"storage": func(app *nativeApp) { app.Properties.Template.Containers[0].Env[1].Value = pointer("wrongstorage") },
		"ingress": func(app *nativeApp) { app.Properties.Configuration.Ingress.External = pointer(true) },
		"revision-mode": func(app *nativeApp) {
			app.Properties.Configuration.ActiveRevisionsMode = pointer(armappcontainers.ActiveRevisionsModeMultiple)
		},
		"environment": func(app *nativeApp) { app.Properties.EnvironmentID = pointer("unexpected") },
		"identity":    func(app *nativeApp) { app.Identity.UserAssignedIdentities = nil },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			adapter, server := newProtocolServer(t)
			want := functionSpec("drift")
			plan := applySpec(t, adapter, want)
			server.mu.Lock()
			mutate(server.apps[resourceName(plan.App, "one")])
			server.mu.Unlock()
			replan, err := adapter.Plan(context.Background(), testTarget(), want)
			if err != nil || replan.Operations[0].Kind != provider.OpUpdate {
				t.Fatalf("actual drift was hidden: %+v %v", replan, err)
			}
		})
	}
}

func TestStaleRevisionCancellationAndFailedProvisioning(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel-stale", true: "failed"}[failed], func(t *testing.T) {
			adapter, server := newProtocolServer(t)
			want := functionSpec("pending")
			plan, err := adapter.Plan(context.Background(), testTarget(), want)
			if err != nil {
				t.Fatal(err)
			}
			server.mu.Lock()
			server.stalled, server.failed = true, failed
			server.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
			defer cancel()
			result, err := adapter.Apply(WithApply(ctx, ApplyOptions{Spec: want}), testTarget(), plan)
			if failed {
				if err != nil || result.Failures["one"] == "" {
					t.Fatalf("failed provisioning claimed success: %+v %v", result, err)
				}
			} else if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("stale ready revision must respect cancellation: %+v %v", result, err)
			}
			server.mu.Lock()
			defer server.mu.Unlock()
			if len(server.bodies) != 1 {
				t.Fatal("failure/cancellation crossed into the next wave")
			}
		})
	}
}

func TestTargetDefinitionValidationIsPureAndAllowsUnassignedID(t *testing.T) {
	adapter := &Provider{}
	target := testTarget()
	target.ID = ""
	if err := adapter.ValidateTargetDefinition(target); err != nil {
		t.Fatalf("valid unstored target rejected: %v", err)
	}
	if _, err := parseTarget(target); err == nil {
		t.Fatal("operation accepted an unassigned target ID")
	}
	target.Config["concurrency"] = "value-must-not-appear"
	err := adapter.ValidateTargetDefinition(target)
	var diagnostic *AdapterError
	if !errors.As(err, &diagnostic) || diagnostic.DiagnosticCode() != "HD0470" || strings.Contains(diagnostic.DiagnosticMessage(), "value-must-not-appear") {
		t.Fatalf("pure configuration validation lost safe diagnostic: %v", err)
	}
}

func TestAdapterDiagnosticMessageExcludesCause(t *testing.T) {
	cause := errors.New("private SDK detail")
	diagnostic := &AdapterError{Code: "HD0473", Message: "safe adapter message", Cause: cause}
	if diagnostic.DiagnosticCode() != "HD0473" || diagnostic.DiagnosticMessage() != "safe adapter message" || !errors.Is(diagnostic, cause) {
		t.Fatal("diagnostic projection must preserve a safe message and wrapped cause")
	}
}

func TestMissingReadyRevisionLeavesVersionAndImageUnobserved(t *testing.T) {
	adapter, server := newProtocolServer(t)
	plan := applySpec(t, adapter, functionSpec("missing-readback"))
	server.mu.Lock()
	server.revisions = map[string]*nativeRevision{}
	server.mu.Unlock()
	state, err := adapter.Observe(context.Background(), testTarget(), plan.App)
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range state.Services {
		if service.FunctionVersion != "" || service.Image != "" || service.Health != provider.Progressing {
			t.Fatalf("absent immutable revision was invented: %+v", service)
		}
	}
}
