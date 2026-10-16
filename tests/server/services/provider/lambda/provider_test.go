package lambda_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/services/provider/conformance"
	"github.com/d31ma/heimdall/app/server/services/provider/lambda"
)

func functionSpec(revision string) spec.DeploySpec {
	want := spec.DeploySpec{App: "events", Revision: revision, Services: []spec.Service{{Name: "handler", Image: "000000000000.dkr.ecr.us-east-1.amazonaws.com/handler@sha256:" + strings.Repeat("a", 64), Function: &spec.Function{RuntimeContract: "lambda-runtime-api", Invocation: "json", TimeoutMS: 30000, Architecture: "amd64"}, Entrypoint: []string{"/runtime"}, Command: []string{"handler"}, Env: []spec.EnvVar{{Key: "PASSWORD", Ref: "vault/handler"}}}, {Name: "second", Image: "000000000000.dkr.ecr.us-east-1.amazonaws.com/second@sha256:" + strings.Repeat("a", 64), Function: &spec.Function{RuntimeContract: "lambda-runtime-api", Invocation: "json"}, Wave: 1}}}
	want.Normalize()
	return want
}

func harness(t *testing.T) (conformance.Harness, *protocol, *lambda.Provider) {
	fake := newProtocol(t)
	adapter := &lambda.Provider{EndpointOverride: fake.server.URL, StaticCredentials: credentials.NewStaticCredentialsProvider("test", "test", ""), SecretResolver: func(context.Context, string) (string, error) { return "resolved-test-secret", nil }}
	target := provider.Target{ID: "lambda-test", Provider: "lambda", Project: "conf", Region: "us-east-1", Config: map[string]string{"execution_role_arn": "arn:aws:iam::000000000000:role/existing-handler"}}
	h := conformance.Harness{Provider: adapter, Target: target, Supported: functionSpec, FunctionSupported: functionSpec, InstancesMayBeEmpty: true, ChangedImage: func(image string) string {
		return strings.TrimSuffix(image, strings.Repeat("a", 64)) + strings.Repeat("b", 64)
	}, Unsupported: func() spec.DeploySpec {
		want := functionSpec("unsupported")
		want.Services[0].Ports = []spec.Port{{Target: 80, Published: 80, Protocol: "tcp"}}
		return want
	}, ApplyContext: func(ctx context.Context, want spec.DeploySpec) context.Context {
		return (lambda.ApplyOptions{Spec: want, Prune: true}).WithContext(ctx)
	}, Reset: func(*testing.T) { fake.mu.Lock(); defer fake.mu.Unlock(); fake.reset() }}
	return h, fake, adapter
}

func deployed(t *testing.T) (conformance.Harness, *protocol, *lambda.Provider, provider.Plan) {
	t.Helper()
	h, fake, adapter := harness(t)
	want := functionSpec("initial")
	ctx := context.Background()
	plan, err := h.Provider.Plan(ctx, h.Target, want)
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.Provider.Apply(h.ApplyContext(ctx, want), h.Target, plan)
	if err != nil || len(result.Failures) > 0 {
		t.Fatalf("apply: %+v, %v", result, err)
	}
	return h, fake, adapter, plan
}

func TestLambdaConformance(t *testing.T) { h, _, _ := harness(t); conformance.Run(t, h) }

func TestLambdaRejectsOrdinaryImageBeforeClientAccess(t *testing.T) {
	adapter := &lambda.Provider{}
	want := spec.DeploySpec{App: "ordinary", Services: []spec.Service{{Name: "web", Image: "nginx:1"}}}
	_, err := adapter.Plan(context.Background(), provider.Target{}, want)
	var rejected *provider.RejectionError
	if !errors.As(err, &rejected) {
		t.Fatalf("expected native contract rejection, got %v", err)
	}
}

func TestLambdaRejectsUnimplementedTargetSettings(t *testing.T) {
	h, _, _ := harness(t)
	h.Target.Config["subnets"] = "subnet-existing"
	_, err := h.Provider.Plan(context.Background(), h.Target, functionSpec("unimplemented"))
	var diagnostic *lambda.Diagnostic
	if !errors.As(err, &diagnostic) || diagnostic.Code != "HD0410" || diagnostic.DiagnosticCode() != "HD0410" || !strings.Contains(diagnostic.Message, "subnets") {
		t.Fatalf("network setting did not produce a structured target diagnostic: %v", err)
	}
}

func TestLambdaDetectsNativeDriftDespiteUnchangedTags(t *testing.T) {
	h, fake, _, _ := deployed(t)
	fake.mu.Lock()
	for _, config := range fake.functions {
		config["Timeout"] = float64(60)
	}
	fake.mu.Unlock()
	plan, err := h.Provider.Plan(context.Background(), h.Target, functionSpec("initial"))
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Changes() {
		t.Fatal("native timeout drift hidden by unchanged desired hash tag")
	}
}

func TestLambdaIdleInactiveFunctionDoesNotRedeploy(t *testing.T) {
	h, fake, _, plan := deployed(t)
	fake.mu.Lock()
	for _, config := range fake.functions {
		config["State"] = "Inactive"
	}
	for _, versions := range fake.versions {
		for _, config := range versions {
			config["State"] = "Inactive"
		}
	}
	fake.mu.Unlock()
	unchanged, err := h.Provider.Plan(context.Background(), h.Target, functionSpec("initial"))
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Changes() {
		t.Fatal("native idle suspension must not trigger a redeploy")
	}
	state, err := h.Provider.Observe(context.Background(), h.Target, plan.App)
	if err != nil {
		t.Fatal(err)
	}
	if state.Rollup() != provider.Suspended {
		t.Fatalf("idle state %s", state.Rollup())
	}
}

func TestFailedLambdaUpdateStopsBeforeNextWave(t *testing.T) {
	h, fake, _, _ := deployed(t)
	fake.mu.Lock()
	fake.failUpdate = true
	fake.mutations = nil
	fake.mu.Unlock()
	want := functionSpec("changed")
	want.Services[0].Function.TimeoutMS = 60000
	ctx := context.Background()
	plan, err := h.Provider.Plan(ctx, h.Target, want)
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.Provider.Apply(h.ApplyContext(ctx, want), h.Target, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Failures) != 1 {
		t.Fatalf("active old code must not conceal failed update: %+v", result)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.mutations) != 1 || !strings.HasPrefix(fake.mutations[0], "configure:") {
		t.Fatalf("failed wave continued: %v", fake.mutations)
	}
}

func TestLambdaWaitHonorsCancellation(t *testing.T) {
	h, fake, _, _ := deployed(t)
	want := functionSpec("changed")
	want.Services[0].Function.TimeoutMS = 60000
	plan, err := h.Provider.Plan(context.Background(), h.Target, want)
	if err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.pendingReads = 100
	fake.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Millisecond)
	defer cancel()
	result, err := h.Provider.Apply(h.ApplyContext(ctx, want), h.Target, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Failures) == 0 {
		t.Fatal("pending update returned success after cancellation")
	}
}

func TestLambdaInvocationUsesPublishedVersionAndReportsHandlerFailure(t *testing.T) {
	h, fake, adapter, plan := deployed(t)
	fake.mu.Lock()
	fake.handlerFailure = true
	fake.mu.Unlock()
	result, err := adapter.Invoke(context.Background(), provider.InvocationRequest{Target: h.Target, App: plan.App, Service: "handler", Payload: []byte(`{"input":42}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != "1" || result.RequestID != "fixture-request" || result.FunctionError != "Unhandled" {
		t.Fatalf("invocation contract: %+v", result)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.lastQualifier != "1" || fake.invokeCalls != 1 || string(fake.lastInput) != `{"input":42}` {
		t.Fatalf("native invocation was changed or repeated: qualifier %s, count %d, payload %s", fake.lastQualifier, fake.invokeCalls, fake.lastInput)
	}
}

func TestLambdaDoesNotRetryAmbiguousInvocation(t *testing.T) {
	h, fake, adapter, plan := deployed(t)
	fake.mu.Lock()
	fake.invokeFailure = true
	fake.mu.Unlock()
	if _, err := adapter.Invoke(context.Background(), provider.InvocationRequest{Target: h.Target, App: plan.App, Service: "handler", Payload: []byte(`{}`)}); err == nil {
		t.Fatal("expected invocation transport failure")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.invokeCalls != 1 {
		t.Fatalf("non-idempotent invocation repeated %d times", fake.invokeCalls)
	}
}

func TestLambdaBoundsInvocationResponseBeforeSDKRead(t *testing.T) {
	h, fake, adapter, plan := deployed(t)
	fake.mu.Lock()
	fake.oversizedResponse = true
	fake.mu.Unlock()
	if _, err := adapter.Invoke(context.Background(), provider.InvocationRequest{Target: h.Target, App: plan.App, Service: "handler", Payload: []byte(`{}`)}); err == nil {
		t.Fatal("unbounded SDK response accepted")
	}
}

func TestLambdaSecretsNeverReachIdentityTags(t *testing.T) {
	_, fake, _, _ := deployed(t)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, tags := range fake.tags {
		for _, value := range tags {
			if strings.Contains(value, "resolved-test-secret") {
				t.Fatal("secret entered persisted identity")
			}
		}
	}
	found := false
	for _, config := range fake.functions {
		env, ok := config["Environment"].(map[string]any)
		if !ok {
			continue
		}
		vars, _ := env["Variables"].(map[string]any)
		if vars["PASSWORD"] == "resolved-test-secret" {
			found = true
		}
	}
	if !found {
		t.Fatal("environment secret never reached native SDK request")
	}
}

func TestLambdaPublicationConflictReusesOnlyMatchingImmutableVersion(t *testing.T) {
	h, fake, _, _ := deployed(t)
	fake.mu.Lock()
	fake.publicationConflict = true
	fake.mu.Unlock()
	want := functionSpec("metadata-change")
	want.Services[0].Labels = []spec.Label{{Key: "owner", Value: "team"}}
	ctx := context.Background()
	plan, err := h.Provider.Plan(ctx, h.Target, want)
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.Provider.Apply(h.ApplyContext(ctx, want), h.Target, plan)
	if err != nil || len(result.Failures) != 0 {
		t.Fatalf("unchanged native publication failed recovery: %+v %v", result, err)
	}
	unchanged, err := h.Provider.Plan(ctx, h.Target, want)
	if err != nil || unchanged.Changes() {
		t.Fatalf("recovered publication did not converge: %+v %v", unchanged, err)
	}
}

func TestLambdaPublicationConflictCannotReuseWrongConfiguration(t *testing.T) {
	h, fake, _, _ := deployed(t)
	fake.mu.Lock()
	fake.publicationConflict = true
	fake.mu.Unlock()
	want := functionSpec("timeout-change")
	want.Services[0].Function.TimeoutMS = 60000
	ctx := context.Background()
	plan, err := h.Provider.Plan(ctx, h.Target, want)
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.Provider.Apply(h.ApplyContext(ctx, want), h.Target, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Failures) == 0 {
		t.Fatal("wrong immutable function configuration was reused after publication conflict")
	}
}

func TestLambdaPlanRejectsExistingForeignResourceName(t *testing.T) {
	h, fake, _, _ := deployed(t)
	fake.mu.Lock()
	name := (lambda.Identity{App: provider.AppRef{Project: "conf", App: "events"}, Service: "handler"}).Name()
	fake.tags[name]["dev.delma.heimdall.managed-by"] = "other-control-plane"
	before := len(fake.mutations)
	fake.mu.Unlock()
	if _, err := h.Provider.Plan(context.Background(), h.Target, functionSpec("initial")); err == nil {
		t.Fatal("plan promised creation over an existing foreign resource name")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.mutations) != before {
		t.Fatal("ownership refusal mutated native state")
	}
}

func TestLambdaFunctionMetricsHaveNativeUnitsAndVersionDimensions(t *testing.T) {
	h, fake, adapter, plan := deployed(t)
	ref := provider.FunctionRef{AppRef: plan.App, Service: "handler", Version: "1"}
	series, err := adapter.FunctionMetrics(context.Background(), h.Target, ref, provider.Window{})
	if err != nil {
		t.Fatal(err)
	}
	if len(series.Invocations) != 1 || len(series.Errors) != 1 || len(series.Throttles) != 1 || len(series.DurationMS) != 1 {
		t.Fatalf("native function series %+v", series)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	name := (lambda.Identity{App: plan.App, Service: "handler"}).Name()
	for _, field := range []string{"AWS/Lambda", "FunctionName", "Resource", name + ":1", "Duration", "Average", "Invocations", "Sum"} {
		if !bytes.Contains(fake.metricInput, []byte(field)) {
			t.Fatalf("native metric request lacks %s", field)
		}
	}
}

func TestLambdaFunctionLogsReadOnlyRequestedNativeVersion(t *testing.T) {
	h, fake, adapter, plan := deployed(t)
	ref := provider.FunctionRef{AppRef: plan.App, Service: "handler", Version: "1"}
	reader, err := adapter.FunctionLogs(context.Background(), h.Target, ref, provider.LogFilter{Tail: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	output, err := io.ReadAll(reader)
	if err != nil || string(output) != "version one output\n" {
		t.Fatalf("native function logs %s, %v", output, err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	streams := fake.logInput["logStreamNames"].([]any)
	if len(streams) != 1 || streams[0] != "2026/10/03/[1]invocation" {
		t.Fatalf("wrong native version logs requested: %v", streams)
	}
}

func TestLambdaStopsAfterOwnershipChangesDuringUpdate(t *testing.T) {
	for _, phase := range []string{"configure", "code", "publish"} {
		t.Run(phase, func(t *testing.T) {
			h, fake, adapter, _ := deployed(t)
			want := functionSpec("ownership-race")
			want.Services[0].Function.TimeoutMS = 60000
			plan, err := adapter.Plan(context.Background(), h.Target, want)
			if err != nil {
				t.Fatal(err)
			}
			fake.mu.Lock()
			fake.ownershipLossAt, fake.mutations = phase, nil
			fake.mu.Unlock()
			result, err := adapter.Apply(h.ApplyContext(context.Background(), want), h.Target, plan)
			if err != nil || !strings.Contains(result.Failures["handler"], "ownership changed") {
				t.Fatalf("ownership loss was not reported: %+v %v", result, err)
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			for _, mutation := range fake.mutations {
				if strings.HasPrefix(mutation, "tag:") || strings.Contains(mutation, "second") || phase == "configure" && strings.HasPrefix(mutation, "code:") || phase != "publish" && strings.HasPrefix(mutation, "publish:") {
					t.Fatalf("mutation crossed ownership-loss boundary: %v", fake.mutations)
				}
			}
		})
	}
}

func TestLambdaTargetContractRejectsIgnoredRoutingBeforeSDKAccess(t *testing.T) {
	mutations := map[string]func(*provider.Target){
		"missing-id":     func(target *provider.Target) { target.ID = "" },
		"wrong-provider": func(target *provider.Target) { target.Provider = "cloudrun" },
		"missing-region": func(target *provider.Target) { target.Region = "" },
		"agent":          func(target *provider.Target) { target.AgentID = "agent-existing" },
		"endpoint":       func(target *provider.Target) { target.Endpoint = "https://handler.example.invalid" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			h, fake, adapter := harness(t)
			mutate(&h.Target)
			_, planErr := adapter.Plan(context.Background(), h.Target, functionSpec("invalid-target"))
			_, observeErr := adapter.Observe(context.Background(), h.Target, provider.AppRef{Project: "conf", App: "events"})
			_, invokeErr := adapter.Invoke(context.Background(), provider.InvocationRequest{Target: h.Target, App: provider.AppRef{Project: "conf", App: "events"}, Service: "handler", Payload: []byte(`{}`)})
			for _, err := range []error{planErr, observeErr, invokeErr} {
				var diagnostic *lambda.Diagnostic
				if !errors.As(err, &diagnostic) || diagnostic.Code != "HD0410" {
					t.Fatalf("ignored routing did not produce a structured refusal: %v", err)
				}
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if fake.requests != 0 {
				t.Fatalf("invalid target reached native SDK endpoint %d times", fake.requests)
			}
		})
	}
}
