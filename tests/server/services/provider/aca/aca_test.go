package aca_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3/fake"

	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/services/provider/aca"
	"github.com/d31ma/heimdall/app/server/services/provider/conformance"
)

// fakeARM backs the SDK's generated server fake with a map, which is all the
// adapter's plan/apply/observe cycle needs from Azure.
type fakeARM struct {
	mu   sync.Mutex
	apps map[string]armappcontainers.ContainerApp
	// jobs is the scheduled half: a Container Apps job is a different ARM
	// resource with its own client, so the fake needs its own map.
	jobs map[string]armappcontainers.Job
}

// jobsServer backs armappcontainers.JobsClient the same way server() backs
// ContainerAppsClient.
func (f *fakeARM) jobsServer() *fake.JobsServer {
	return &fake.JobsServer{
		BeginCreateOrUpdate: func(_ context.Context, _, name string, envelope armappcontainers.Job, _ *armappcontainers.JobsClientBeginCreateOrUpdateOptions) (
			resp azfake.PollerResponder[armappcontainers.JobsClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
			f.mu.Lock()
			defer f.mu.Unlock()
			envelope.Name = &name
			f.jobs[name] = envelope
			resp.SetTerminalResponse(http.StatusOK,
				armappcontainers.JobsClientCreateOrUpdateResponse{Job: envelope}, nil)
			return
		},
		BeginDelete: func(_ context.Context, _, name string, _ *armappcontainers.JobsClientBeginDeleteOptions) (
			resp azfake.PollerResponder[armappcontainers.JobsClientDeleteResponse], errResp azfake.ErrorResponder) {
			f.mu.Lock()
			defer f.mu.Unlock()
			delete(f.jobs, name)
			resp.SetTerminalResponse(http.StatusOK, armappcontainers.JobsClientDeleteResponse{}, nil)
			return
		},
		NewListByResourceGroupPager: func(_ string, _ *armappcontainers.JobsClientListByResourceGroupOptions) (
			resp azfake.PagerResponder[armappcontainers.JobsClientListByResourceGroupResponse]) {
			f.mu.Lock()
			defer f.mu.Unlock()
			page := armappcontainers.JobsClientListByResourceGroupResponse{}
			for name := range f.jobs {
				job := f.jobs[name]
				page.Value = append(page.Value, &job)
			}
			resp.AddPage(http.StatusOK, page, nil)
			return
		},
	}
}

func (f *fakeARM) server() *fake.ContainerAppsServer {
	return &fake.ContainerAppsServer{
		BeginCreateOrUpdate: func(_ context.Context, _, name string, envelope armappcontainers.ContainerApp, _ *armappcontainers.ContainerAppsClientBeginCreateOrUpdateOptions) (
			resp azfake.PollerResponder[armappcontainers.ContainerAppsClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
			f.mu.Lock()
			defer f.mu.Unlock()
			envelope.Name = &name
			state := armappcontainers.ContainerAppProvisioningStateSucceeded
			if envelope.Properties == nil {
				envelope.Properties = &armappcontainers.ContainerAppProperties{}
			}
			envelope.Properties.ProvisioningState = &state
			revision := name + "--rev1"
			envelope.Properties.LatestRevisionName = &revision
			f.apps[name] = envelope
			resp.SetTerminalResponse(http.StatusOK,
				armappcontainers.ContainerAppsClientCreateOrUpdateResponse{ContainerApp: envelope}, nil)
			return
		},
		BeginDelete: func(_ context.Context, _, name string, _ *armappcontainers.ContainerAppsClientBeginDeleteOptions) (
			resp azfake.PollerResponder[armappcontainers.ContainerAppsClientDeleteResponse], errResp azfake.ErrorResponder) {
			f.mu.Lock()
			defer f.mu.Unlock()
			delete(f.apps, name)
			resp.SetTerminalResponse(http.StatusOK, armappcontainers.ContainerAppsClientDeleteResponse{}, nil)
			return
		},
		NewListByResourceGroupPager: func(_ string, _ *armappcontainers.ContainerAppsClientListByResourceGroupOptions) (
			resp azfake.PagerResponder[armappcontainers.ContainerAppsClientListByResourceGroupResponse]) {
			f.mu.Lock()
			defer f.mu.Unlock()
			page := armappcontainers.ContainerAppsClientListByResourceGroupResponse{}
			for name := range f.apps {
				app := f.apps[name]
				page.Value = append(page.Value, &app)
			}
			resp.AddPage(http.StatusOK, page, nil)
			return
		},
	}
}

func acaSpec(revision string) spec.DeploySpec {
	deploy := spec.DeploySpec{
		App: "checkout", Revision: revision,
		Services: []spec.Service{
			{Name: "web", Image: "ghcr.io/example/web:1",
				Ports: []spec.Port{{Published: 8080, Target: 8080, Protocol: "tcp"}},
				Env:   []spec.EnvVar{{Key: "PASSWORD", Ref: "vault/db#password"}}},
			{Name: "worker", Image: "ghcr.io/example/worker:2", Wave: 1,
				Resources: &spec.Resources{CPUMillis: 500, MemoryMiB: 1024}},
		},
	}
	deploy.Normalize()
	return deploy
}

func harness(t *testing.T) (conformance.Harness, *fakeARM) {
	arm := &fakeARM{
		apps: map[string]armappcontainers.ContainerApp{},
		jobs: map[string]armappcontainers.Job{},
	}

	adapter := &aca.Provider{
		// One transport serving both clients, because the adapter has one
		// Transport field and now builds two clients from it.
		Transport: fake.NewServerFactoryTransport(&fake.ServerFactory{
			ContainerAppsServer: *arm.server(),
			JobsServer:          *arm.jobsServer(),
		}),
		Credential: &azfake.TokenCredential{},
		SecretResolver: func(_ context.Context, ref string) (string, error) {
			return "resolved:" + ref, nil
		},
	}
	target := provider.Target{
		ID: "tgt-aca", Provider: "aca", Project: "conf", Region: "westeurope",
		Endpoint: "/subscriptions/s/resourceGroups/g/providers/Microsoft.App/managedEnvironments/env",
		Config:   map[string]string{"subscription_id": "sub-1", "resource_group": "g"},
	}

	return conformance.Harness{
		Provider:  adapter,
		Target:    target,
		Supported: func(revision string) spec.DeploySpec { return acaSpec(revision) },
		Unsupported: func() spec.DeploySpec {
			deploy := spec.DeploySpec{
				App: "checkout", Revision: "r",
				Services: []spec.Service{{Name: "web", Image: "ghcr.io/example/web:1",
					Volumes: []spec.Volume{{Source: "data", Target: "/data"}}}},
			}
			deploy.Normalize()
			return deploy
		},
		ApplyContext: func(ctx context.Context, want spec.DeploySpec) context.Context {
			return aca.WithApply(ctx, aca.ApplyOptions{Spec: want, Prune: true})
		},
		Reset: func(t *testing.T) {
			arm.mu.Lock()
			arm.apps = map[string]armappcontainers.ContainerApp{}
			arm.jobs = map[string]armappcontainers.Job{}
			arm.mu.Unlock()
		},
	}, arm
}

func TestACAConformance(t *testing.T) {
	h, _ := harness(t)
	conformance.Run(t, h)
}

// TestSecretsResolveIntoTheAppAndNeverIntoATag.
func TestSecretsResolveIntoTheApp(t *testing.T) {
	h, arm := harness(t)
	want := acaSpec("rev-secrets")
	ctx := context.Background()

	plan, err := h.Provider.Plan(ctx, h.Target, want)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := h.Provider.Apply(h.ApplyContext(ctx, want), h.Target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	arm.mu.Lock()
	defer arm.mu.Unlock()
	raw, _ := json.Marshal(arm.apps)
	if !strings.Contains(string(raw), "resolved:vault/db#password") {
		t.Fatal("the secret never reached a container definition")
	}
	for _, app := range arm.apps {
		for _, value := range app.Tags {
			if value != nil && strings.Contains(*value, "resolved:") {
				t.Fatal("a secret value leaked into an ARM tag")
			}
		}
	}
}
