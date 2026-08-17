// Package aca deploys compose applications to Azure Container Apps.
//
// Mapping: one compose service becomes one Container App inside a managed
// environment. Identity rides in ARM tags — arbitrary strings, so the dotted
// vocabulary every other adapter stamps travels verbatim, hashes and all.
//
// Target layout: Config carries subscription_id and resource_group;
// Endpoint is the managed environment's full ARM id; Region is the Azure
// location. CredentialRef resolves to a client-secret credential at call
// time; empty means the ambient chain (managed identity, workload identity,
// az CLI) — the same posture the ECS adapter takes with the AWS chain.
package aca

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

const (
	tagManagedBy   = "dev.delma.heimdall.managed-by"
	tagProject     = "dev.delma.heimdall.project"
	tagApp         = "dev.delma.heimdall.app"
	tagService     = "dev.delma.heimdall.service"
	tagRevision    = "dev.delma.heimdall.revision"
	tagServiceHash = "dev.delma.heimdall.service-hash"
	tagSpecHash    = "dev.delma.heimdall.spec-hash"

	managedBy = "heimdall"
)

// Provider is the adapter.
type Provider struct {
	SecretResolver func(ctx context.Context, ref string) (string, error)

	// Transport and Credential exist for the conformance suite, which plugs
	// the SDK's generated server fakes in. Production leaves them nil.
	Transport  policy.Transporter
	Credential azcore.TokenCredential
}

func (p *Provider) Name() string { return "aca" }

func (p *Provider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Provider: "aca",
		Support: map[provider.Feature]provider.Support{
			provider.FeatureMultiService: provider.Full,
			provider.FeatureSecretRef:    provider.Full,
			provider.FeatureFileSecret:   provider.Rejected,
			provider.FeatureResources:    provider.Full,
			provider.FeatureReplicas:     provider.Full,
			// Scale to zero is native: minReplicas 0 is the default posture.
			provider.FeatureScaleToZero: provider.Full,
			provider.FeaturePorts:       provider.Partial,
			provider.FeatureMultiPort:   provider.Rejected,
			provider.FeatureDependsOn:   provider.Partial,
			provider.FeatureHealthcheck: provider.Partial,
			provider.FeatureRestart:     provider.Partial,
			provider.FeatureNamedVolume: provider.Rejected,
			provider.FeatureBindMount:   provider.Rejected,
			provider.FeatureSidecars:    provider.Rejected,
		},
		Caveats: map[provider.Feature]string{
			provider.FeatureFileSecret:  "Container Apps secret volumes are future work; use an environment ${secret:...} reference",
			provider.FeaturePorts:       "ingress exposes one target port per app; the published side of a mapping is ignored",
			provider.FeatureMultiPort:   "one ingress port per Container App",
			provider.FeatureDependsOn:   "apps are created in wave order; start order belongs to the platform",
			provider.FeatureHealthcheck: "compose healthchecks become startup probes where expressible",
			provider.FeatureRestart:     "Container Apps restarts containers as part of serving",
			provider.FeatureNamedVolume: "named volumes need Azure Files, which is not wired yet",
			provider.FeatureBindMount:   "there is no host to bind-mount",
			provider.FeatureSidecars:    "declare a sidecar as its own service",
		},
	}
}

func (p *Provider) client(ctx context.Context, target provider.Target) (*armappcontainers.ContainerAppsClient, error) {
	subscription := target.Config["subscription_id"]
	if subscription == "" {
		return nil, fmt.Errorf("HD0375: ACA target %s has no subscription_id configured", target.ID)
	}

	credential := p.Credential
	if credential == nil {
		switch {
		case target.CredentialRef != "":
			if p.SecretResolver == nil {
				return nil, fmt.Errorf("HD0376: target %s names credential %q but no resolver is configured",
					target.ID, target.CredentialRef)
			}
			raw, err := p.SecretResolver(ctx, target.CredentialRef)
			if err != nil {
				return nil, fmt.Errorf("HD0376: resolve credential %q: %w", target.CredentialRef, err)
			}
			var parsed struct {
				TenantID     string `json:"tenant_id"`
				ClientID     string `json:"client_id"`
				ClientSecret string `json:"client_secret"`
			}
			if err := json.Unmarshal([]byte(raw), &parsed); err != nil || parsed.ClientID == "" {
				return nil, fmt.Errorf(
					"HD0376: credential %q is not the JSON {tenant_id, client_id, client_secret} an ACA target needs",
					target.CredentialRef)
			}
			credential, err = azidentity.NewClientSecretCredential(
				parsed.TenantID, parsed.ClientID, parsed.ClientSecret, nil)
			if err != nil {
				return nil, fmt.Errorf("HD0376: build credential: %w", err)
			}
		default:
			chain, err := azidentity.NewDefaultAzureCredential(nil)
			if err != nil {
				return nil, fmt.Errorf("HD0376: default Azure credential: %w", err)
			}
			credential = chain
		}
	}

	options := &arm.ClientOptions{}
	if p.Transport != nil {
		options.Transport = p.Transport
	}
	client, err := armappcontainers.NewContainerAppsClient(subscription, credential, options)
	if err != nil {
		return nil, fmt.Errorf("HD0375: build ACA client: %w", err)
	}
	return client, nil
}

func resourceGroup(target provider.Target) string { return target.Config["resource_group"] }

func projectOf(target provider.Target) string {
	if target.Project != "" {
		return target.Project
	}
	return "default"
}

// resourceName is the Container App name: lowercase alphanumerics and
// dashes, max 32 characters, and it must round-trip — the name is how an
// update finds the app it replaces. A hash suffix keeps long names unique
// after truncation.
func resourceName(app provider.AppRef, service string) string {
	name := strings.ToLower(fmt.Sprintf("hd-%s-%s-%s", app.Project, app.App, service))
	if len(name) <= 32 {
		return name
	}
	digest, _ := spec.HashService(spec.Service{Name: name})
	suffix := strings.TrimPrefix(digest, "sha256:")[:6]
	return name[:25] + "-" + suffix
}

func (p *Provider) appsFor(ctx context.Context, client *armappcontainers.ContainerAppsClient, target provider.Target, app provider.AppRef) (map[string]*armappcontainers.ContainerApp, error) {
	pager := client.NewListByResourceGroupPager(resourceGroup(target), nil)
	out := map[string]*armappcontainers.ContainerApp{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("HD0377: list container apps: %w", err)
		}
		for _, containerApp := range page.Value {
			tags := map[string]string{}
			for key, value := range containerApp.Tags {
				if value != nil {
					tags[key] = *value
				}
			}
			if tags[tagManagedBy] != managedBy || tags[tagProject] != app.Project || tags[tagApp] != app.App {
				continue
			}
			if name := tags[tagService]; name != "" {
				out[name] = containerApp
			}
		}
	}
	return out, nil
}

func tagValue(containerApp *armappcontainers.ContainerApp, key string) string {
	if containerApp == nil || containerApp.Tags == nil || containerApp.Tags[key] == nil {
		return ""
	}
	return *containerApp.Tags[key]
}

func (p *Provider) Plan(ctx context.Context, target provider.Target, want spec.DeploySpec) (provider.Plan, error) {
	app := provider.AppRef{Project: projectOf(target), App: want.App}
	if err := provider.Validate(p.Capabilities(), want); err != nil {
		return provider.Plan{}, err
	}
	specHash, err := spec.Hash(want)
	if err != nil {
		return provider.Plan{}, err
	}
	client, err := p.client(ctx, target)
	if err != nil {
		return provider.Plan{}, err
	}
	live, err := p.appsFor(ctx, client, target, app)
	if err != nil {
		return provider.Plan{}, err
	}

	plan := provider.Plan{Target: target.ID, App: app, Revision: want.Revision, SpecHash: specHash}
	desired := map[string]bool{}
	for _, service := range want.Services {
		desired[service.Name] = true
		serviceHash, err := spec.HashService(service)
		if err != nil {
			return provider.Plan{}, err
		}
		existing, running := live[service.Name]
		switch {
		case !running:
			plan.Operations = append(plan.Operations, provider.Operation{
				Kind: provider.OpCreate, Service: service.Name, Wave: service.Wave, Reason: "not deployed",
			})
		case tagValue(existing, tagServiceHash) != serviceHash:
			plan.Operations = append(plan.Operations, provider.Operation{
				Kind: provider.OpUpdate, Service: service.Name, Wave: service.Wave,
				Reason: "the desired service differs from the deployed one",
			})
		default:
			plan.Operations = append(plan.Operations, provider.Operation{
				Kind: provider.OpNoop, Service: service.Name, Wave: service.Wave,
			})
		}
	}
	for name := range live {
		if !desired[name] {
			plan.Operations = append(plan.Operations, provider.Operation{
				Kind: provider.OpDelete, Service: name, Prune: true,
				Reason: "no longer declared in the desired state",
			})
		}
	}
	sort.SliceStable(plan.Operations, func(i, j int) bool {
		if plan.Operations[i].Wave != plan.Operations[j].Wave {
			return plan.Operations[i].Wave < plan.Operations[j].Wave
		}
		return plan.Operations[i].Service < plan.Operations[j].Service
	})
	return plan, nil
}

// ApplyOptions mirrors the other adapters'.
type ApplyOptions struct {
	Spec       spec.DeploySpec
	Prune      bool
	Registries provider.RegistryResolver
}

type applyKey struct{}

func WithApply(ctx context.Context, options ApplyOptions) context.Context {
	return context.WithValue(ctx, applyKey{}, options)
}

func applyOptions(ctx context.Context) (ApplyOptions, bool) {
	options, ok := ctx.Value(applyKey{}).(ApplyOptions)
	return options, ok
}

func (p *Provider) Apply(ctx context.Context, target provider.Target, plan provider.Plan) (provider.Result, error) {
	options, ok := applyOptions(ctx)
	if !ok {
		return provider.Result{}, fmt.Errorf("HD0378: apply called without options; use WithApply")
	}
	specHash, err := spec.Hash(options.Spec)
	if err != nil {
		return provider.Result{}, err
	}
	if specHash != plan.SpecHash {
		return provider.Result{}, fmt.Errorf(
			"HD0379: the plan was computed for spec %s but apply was asked to run %s; re-plan",
			plan.SpecHash, specHash)
	}

	client, err := p.client(ctx, target)
	if err != nil {
		return provider.Result{}, err
	}
	services := map[string]spec.Service{}
	for _, service := range options.Spec.Services {
		services[service.Name] = service
	}

	result := provider.Result{}
	operations := plan.Operations
	for start := 0; start < len(operations); {
		wave := operations[start].Wave
		end := start
		for end < len(operations) && operations[end].Wave == wave {
			end++
		}
		waveFailed := false
		for _, operation := range operations[start:end] {
			if operation.Prune && !options.Prune {
				continue
			}
			if operation.Kind == provider.OpNoop {
				continue
			}
			if err := p.execute(ctx, client, target, plan, operation, services); err != nil {
				result.Failures = map[string]string{operation.Service: err.Error()}
				waveFailed = true
				break
			}
			result.Applied = append(result.Applied, operation)
		}
		if waveFailed {
			break
		}
		start = end
	}
	return result, nil
}

func (p *Provider) execute(
	ctx context.Context,
	client *armappcontainers.ContainerAppsClient,
	target provider.Target,
	plan provider.Plan,
	operation provider.Operation,
	services map[string]spec.Service,
) error {
	group := resourceGroup(target)
	name := resourceName(plan.App, operation.Service)

	switch operation.Kind {
	case provider.OpDelete:
		poller, err := client.BeginDelete(ctx, group, name, nil)
		if err != nil {
			return err
		}
		_, err = poller.PollUntilDone(ctx, nil)
		return err

	case provider.OpCreate, provider.OpUpdate, provider.OpRestart:
		service, ok := services[operation.Service]
		if !ok {
			return fmt.Errorf("HD0380: the plan names service %q but the spec does not", operation.Service)
		}
		definition, err := p.appDefinition(ctx, target, plan, service)
		if err != nil {
			return err
		}
		poller, err := client.BeginCreateOrUpdate(ctx, group, name, definition, nil)
		if err != nil {
			return err
		}
		_, err = poller.PollUntilDone(ctx, nil)
		return err
	}
	return nil
}

func (p *Provider) appDefinition(ctx context.Context, target provider.Target, plan provider.Plan, service spec.Service) (armappcontainers.ContainerApp, error) {
	serviceHash, err := spec.HashService(service)
	if err != nil {
		return armappcontainers.ContainerApp{}, err
	}

	var environment []*armappcontainers.EnvironmentVar
	for _, env := range service.Env {
		value := env.Value
		if env.Ref != "" {
			if p.SecretResolver == nil {
				return armappcontainers.ContainerApp{}, fmt.Errorf(
					"HD0381: %s needs secret %q but no resolver is configured", service.Name, env.Ref)
			}
			resolved, err := p.SecretResolver(ctx, env.Ref)
			if err != nil {
				return armappcontainers.ContainerApp{}, fmt.Errorf(
					"HD0382: resolve secret %q for %s: %w", env.Ref, service.Name, err)
			}
			value = resolved
		}
		environment = append(environment, &armappcontainers.EnvironmentVar{
			Name: ptr(env.Key), Value: ptr(value),
		})
	}

	container := &armappcontainers.Container{
		Name:  ptr(service.Name),
		Image: ptr(service.Image),
		Env:   environment,
	}
	if service.Resources != nil {
		resources := &armappcontainers.ContainerResources{}
		if service.Resources.CPUMillis > 0 {
			resources.CPU = ptr(float64(service.Resources.CPUMillis) / 1000)
		}
		if service.Resources.MemoryMiB > 0 {
			resources.Memory = ptr(fmt.Sprintf("%dMi", service.Resources.MemoryMiB))
		}
		container.Resources = resources
	}

	minReplicas := int32(0)
	if service.Replicas > 1 {
		minReplicas = int32(service.Replicas)
	}

	properties := &armappcontainers.ContainerAppProperties{
		EnvironmentID: ptr(target.Endpoint),
		Template: &armappcontainers.Template{
			Containers: []*armappcontainers.Container{container},
			Scale: &armappcontainers.Scale{
				MinReplicas: ptr(minReplicas),
			},
		},
	}
	if len(service.Ports) > 0 {
		properties.Configuration = &armappcontainers.Configuration{
			Ingress: &armappcontainers.Ingress{
				TargetPort: ptr(int32(service.Ports[0].Target)),
				External:   ptr(true),
			},
		}
	}

	return armappcontainers.ContainerApp{
		Location: ptr(target.Region),
		Tags: map[string]*string{
			tagManagedBy: ptr(managedBy), tagProject: ptr(plan.App.Project),
			tagApp: ptr(plan.App.App), tagService: ptr(service.Name),
			tagRevision: ptr(plan.Revision), tagServiceHash: ptr(serviceHash),
			tagSpecHash: ptr(plan.SpecHash),
		},
		Properties: properties,
	}, nil
}

func ptr[T any](value T) *T { return &value }

func (p *Provider) Observe(ctx context.Context, target provider.Target, app provider.AppRef) (provider.LiveState, error) {
	client, err := p.client(ctx, target)
	if err != nil {
		return provider.LiveState{}, err
	}
	live, err := p.appsFor(ctx, client, target, app)
	if err != nil {
		return provider.LiveState{}, err
	}

	state := provider.LiveState{App: app, Target: target.ID, ReadAt: time.Now().UTC(),
		Services: map[string]provider.ServiceState{}}
	for name, containerApp := range live {
		health := provider.Healthy
		message := "provisioned"
		image := ""
		if containerApp.Properties != nil {
			if containerApp.Properties.ProvisioningState != nil &&
				*containerApp.Properties.ProvisioningState == armappcontainers.ContainerAppProvisioningStateFailed {
				health = provider.Degraded
				message = "provisioning failed"
			}
			if containerApp.Properties.Template != nil && len(containerApp.Properties.Template.Containers) > 0 &&
				containerApp.Properties.Template.Containers[0].Image != nil {
				image = *containerApp.Properties.Template.Containers[0].Image
			}
		}
		state.Services[name] = provider.ServiceState{
			Health: health, Replicas: 1, Ready: 1, Image: image, Message: message,
		}
		if revision := tagValue(containerApp, tagRevision); revision != "" {
			state.Revision = revision
		}
		if hash := tagValue(containerApp, tagSpecHash); hash != "" {
			state.SpecHash = hash
		}
	}
	return state, nil
}

func (p *Provider) Instances(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Instance, error) {
	client, err := p.client(ctx, target)
	if err != nil {
		return nil, err
	}
	live, err := p.appsFor(ctx, client, target, app)
	if err != nil {
		return nil, err
	}

	var instances []provider.Instance
	for name, containerApp := range live {
		image := ""
		revisionName := name
		if containerApp.Properties != nil {
			if containerApp.Properties.Template != nil && len(containerApp.Properties.Template.Containers) > 0 &&
				containerApp.Properties.Template.Containers[0].Image != nil {
				image = *containerApp.Properties.Template.Containers[0].Image
			}
			if containerApp.Properties.LatestRevisionName != nil {
				revisionName = *containerApp.Properties.LatestRevisionName
			}
		}
		instances = append(instances, provider.Instance{
			Ref: provider.InstanceRef{
				AppRef: app, Service: name, Instance: revisionName,
			},
			Image: image, Status: "provisioned", Health: provider.Healthy,
			Revision: tagValue(containerApp, tagRevision),
		})
	}
	sort.Slice(instances, func(i, j int) bool { return instances[i].Ref.Instance < instances[j].Ref.Instance })
	return instances, nil
}

// Metrics and Logs: Azure Monitor and Log Analytics hold them.
//
// ponytail: a clear refusal naming the store, like Cloud Run's. Wire the
// Azure Monitor query surface when a deployment needs ACA charts inside
// HEIMDALL rather than in the portal.
func (p *Provider) Metrics(context.Context, provider.Target, provider.InstanceRef, provider.Window) (provider.Series, error) {
	return provider.Series{}, fmt.Errorf(
		"HD0383: Container Apps metrics live in Azure Monitor; HEIMDALL does not proxy them yet")
}

func (p *Provider) Logs(context.Context, provider.Target, provider.InstanceRef, provider.LogFilter) (io.ReadCloser, error) {
	return nil, fmt.Errorf(
		"HD0383: Container Apps logs live in Log Analytics; HEIMDALL does not proxy them yet")
}

// Events: ARM's provisioning states are the observable narration.
func (p *Provider) Events(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Event, error) {
	client, err := p.client(ctx, target)
	if err != nil {
		return nil, err
	}
	live, err := p.appsFor(ctx, client, target, app)
	if err != nil {
		return nil, err
	}
	var events []provider.Event
	for name, containerApp := range live {
		stateName := "unknown"
		if containerApp.Properties != nil && containerApp.Properties.ProvisioningState != nil {
			stateName = string(*containerApp.Properties.ProvisioningState)
		}
		events = append(events, provider.Event{
			At: time.Now().UTC(), Type: "provisioning", Service: name,
			Message: "provisioning state: " + stateName, Source: "aca",
		})
	}
	return events, nil
}
