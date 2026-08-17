// Package cloudrun deploys compose applications to Google Cloud Run.
//
// Mapping: one compose service becomes one Cloud Run service. Replicas map to
// minimum instances; scale to zero is native — it is the reason to pick this
// runtime — and holds the Full support the container runtimes reject.
//
// Cloud Run label keys forbid dots, so HEIMDALL's identity vocabulary is
// spelled with dashes here (dev-delma-heimdall-app). The translation lives in
// this package and nowhere else; everything outside still speaks the dotted
// form through provider.Instance and friends.
package cloudrun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	run "google.golang.org/api/run/v2"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

// Identity rides in annotations, not labels: Cloud Run's label alphabet
// forbids dots in keys and colons in values, and a sha256:... content hash
// that had to be mangled to fit would never compare equal to itself again —
// which reads as an update on every plan, forever. Annotations carry the
// dotted vocabulary verbatim, the same strings every other adapter stamps.
const (
	annotationManagedBy   = "dev.delma.heimdall.managed-by"
	annotationProject     = "dev.delma.heimdall.project"
	annotationApp         = "dev.delma.heimdall.app"
	annotationService     = "dev.delma.heimdall.service"
	annotationRevision    = "dev.delma.heimdall.revision"
	annotationServiceHash = "dev.delma.heimdall.service-hash"
	annotationSpecHash    = "dev.delma.heimdall.spec-hash"

	managedBy = "heimdall"
)

// Provider is the adapter. Target.Endpoint is the GCP project id;
// Target.Region is the Cloud Run location.
type Provider struct {
	SecretResolver func(ctx context.Context, ref string) (string, error)

	// EndpointOverride and NoAuth exist for the conformance suite's fake.
	EndpointOverride string
	NoAuth           bool
}

func (p *Provider) Name() string { return "cloudrun" }

func (p *Provider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Provider: "cloudrun",
		Support: map[provider.Feature]provider.Support{
			provider.FeatureMultiService: provider.Full,
			provider.FeatureSecretRef:    provider.Full,
			provider.FeatureFileSecret:   provider.Rejected,
			provider.FeatureResources:    provider.Full,
			// The whole reason to choose Cloud Run.
			provider.FeatureScaleToZero: provider.Full,
			// Replicas become minimum instances: the platform still scales
			// above them on demand.
			provider.FeatureReplicas: provider.Partial,
			// One serving port per service, chosen by the platform contract.
			provider.FeaturePorts:     provider.Partial,
			provider.FeatureMultiPort: provider.Rejected,
			provider.FeatureDependsOn: provider.Partial,
			// Startup probes exist; compose healthchecks translate loosely.
			provider.FeatureHealthcheck: provider.Partial,
			provider.FeatureRestart:     provider.Partial,
			provider.FeatureNamedVolume: provider.Rejected,
			provider.FeatureBindMount:   provider.Rejected,
			provider.FeatureSidecars:    provider.Rejected,
		},
		Caveats: map[provider.Feature]string{
			provider.FeatureFileSecret:  "Cloud Run secret volumes are future work; use an environment ${secret:...} reference",
			provider.FeatureReplicas:    "replicas set minimum instances; Cloud Run scales above them on demand",
			provider.FeaturePorts:       "Cloud Run serves one HTTP port per service; the published side of a mapping is ignored",
			provider.FeatureMultiPort:   "one serving port per Cloud Run service",
			provider.FeatureDependsOn:   "services are created in wave order; start order belongs to the platform",
			provider.FeatureHealthcheck: "compose healthchecks become startup probes where expressible",
			provider.FeatureRestart:     "Cloud Run restarts containers as part of serving; restart policies cannot be expressed",
			provider.FeatureNamedVolume: "Cloud Run containers have no durable volumes",
			provider.FeatureBindMount:   "there is no host to bind-mount",
			provider.FeatureSidecars:    "declare a sidecar as its own service",
		},
	}
}

func (p *Provider) client(ctx context.Context) (*run.Service, error) {
	options := []option.ClientOption{}
	if p.EndpointOverride != "" {
		options = append(options, option.WithEndpoint(p.EndpointOverride))
	}
	if p.NoAuth {
		options = append(options, option.WithoutAuthentication())
	}
	client, err := run.NewService(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("HD0363: build Cloud Run client: %w", err)
	}
	return client, nil
}

func gcpProject(target provider.Target) string { return target.Endpoint }

func parent(target provider.Target) string {
	return fmt.Sprintf("projects/%s/locations/%s", gcpProject(target), target.Region)
}

func projectOf(target provider.Target) string {
	if target.Project != "" {
		return target.Project
	}
	return "default"
}

// resourceID is the Cloud Run service id: lowercase, dashes, bounded.
func resourceID(app provider.AppRef, service string) string {
	return strings.ToLower(fmt.Sprintf("hd-%s-%s-%s", app.Project, app.App, service))
}

func (p *Provider) servicesFor(ctx context.Context, client *run.Service, target provider.Target, app provider.AppRef) (map[string]*run.GoogleCloudRunV2Service, error) {
	listed, err := client.Projects.Locations.Services.List(parent(target)).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("HD0364: list Cloud Run services: %w", err)
	}
	out := map[string]*run.GoogleCloudRunV2Service{}
	for _, service := range listed.Services {
		labels := service.Annotations
		if labels[annotationManagedBy] != managedBy ||
			labels[annotationProject] != app.Project || labels[annotationApp] != app.App {
			continue
		}
		if name := labels[annotationService]; name != "" {
			out[name] = service
		}
	}
	return out, nil
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
	client, err := p.client(ctx)
	if err != nil {
		return provider.Plan{}, err
	}
	live, err := p.servicesFor(ctx, client, target, app)
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
		case existing.Annotations[annotationServiceHash] != serviceHash:
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
		return provider.Result{}, fmt.Errorf("HD0365: apply called without options; use WithApply")
	}
	specHash, err := spec.Hash(options.Spec)
	if err != nil {
		return provider.Result{}, err
	}
	if specHash != plan.SpecHash {
		return provider.Result{}, fmt.Errorf(
			"HD0366: the plan was computed for spec %s but apply was asked to run %s; re-plan",
			plan.SpecHash, specHash)
	}

	client, err := p.client(ctx)
	if err != nil {
		return provider.Result{}, err
	}
	live, err := p.servicesFor(ctx, client, target, plan.App)
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
			if err := p.execute(ctx, client, target, plan, operation, services, live); err != nil {
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
	client *run.Service,
	target provider.Target,
	plan provider.Plan,
	operation provider.Operation,
	services map[string]spec.Service,
	live map[string]*run.GoogleCloudRunV2Service,
) error {
	switch operation.Kind {
	case provider.OpDelete:
		existing, ok := live[operation.Service]
		if !ok {
			return nil
		}
		_, err := client.Projects.Locations.Services.Delete(existing.Name).Context(ctx).Do()
		if isNotFound(err) {
			return nil
		}
		return err

	case provider.OpCreate, provider.OpUpdate, provider.OpRestart:
		service, ok := services[operation.Service]
		if !ok {
			return fmt.Errorf("HD0367: the plan names service %q but the spec does not", operation.Service)
		}
		definition, err := p.serviceDefinition(ctx, plan, service)
		if err != nil {
			return err
		}
		if existing, running := live[operation.Service]; running {
			_, err := client.Projects.Locations.Services.Patch(existing.Name, definition).Context(ctx).Do()
			return err
		}
		_, err = client.Projects.Locations.Services.Create(parent(target), definition).
			ServiceId(resourceID(plan.App, service.Name)).Context(ctx).Do()
		return err
	}
	return nil
}

// serviceDefinition builds the Cloud Run service. Secrets resolve here into
// the container environment.
func (p *Provider) serviceDefinition(ctx context.Context, plan provider.Plan, service spec.Service) (*run.GoogleCloudRunV2Service, error) {
	serviceHash, err := spec.HashService(service)
	if err != nil {
		return nil, err
	}

	var environment []*run.GoogleCloudRunV2EnvVar
	for _, env := range service.Env {
		value := env.Value
		if env.Ref != "" {
			if p.SecretResolver == nil {
				return nil, fmt.Errorf("HD0368: %s needs secret %q but no resolver is configured", service.Name, env.Ref)
			}
			resolved, err := p.SecretResolver(ctx, env.Ref)
			if err != nil {
				return nil, fmt.Errorf("HD0369: resolve secret %q for %s: %w", env.Ref, service.Name, err)
			}
			value = resolved
		}
		environment = append(environment, &run.GoogleCloudRunV2EnvVar{Name: env.Key, Value: value})
	}

	container := &run.GoogleCloudRunV2Container{
		Image: service.Image,
		Env:   environment,
	}
	if len(service.Ports) > 0 {
		container.Ports = []*run.GoogleCloudRunV2ContainerPort{
			{ContainerPort: int64(service.Ports[0].Target)},
		}
	}
	if service.Resources != nil {
		limits := map[string]string{}
		if service.Resources.CPUMillis > 0 {
			limits["cpu"] = fmt.Sprintf("%dm", service.Resources.CPUMillis)
		}
		if service.Resources.MemoryMiB > 0 {
			limits["memory"] = fmt.Sprintf("%dMi", service.Resources.MemoryMiB)
		}
		container.Resources = &run.GoogleCloudRunV2ResourceRequirements{Limits: limits}
	}

	minInstances := int64(0)
	if service.Replicas > 1 {
		minInstances = int64(service.Replicas)
	}

	return &run.GoogleCloudRunV2Service{
		Annotations: map[string]string{
			annotationManagedBy: managedBy, annotationProject: plan.App.Project,
			annotationApp: plan.App.App, annotationService: service.Name,
			annotationRevision:    plan.Revision,
			annotationServiceHash: serviceHash, annotationSpecHash: plan.SpecHash,
		},
		Template: &run.GoogleCloudRunV2RevisionTemplate{
			Containers: []*run.GoogleCloudRunV2Container{container},
			Scaling:    &run.GoogleCloudRunV2RevisionScaling{MinInstanceCount: minInstances},
		},
	}, nil
}

func (p *Provider) Observe(ctx context.Context, target provider.Target, app provider.AppRef) (provider.LiveState, error) {
	client, err := p.client(ctx)
	if err != nil {
		return provider.LiveState{}, err
	}
	live, err := p.servicesFor(ctx, client, target, app)
	if err != nil {
		return provider.LiveState{}, err
	}

	state := provider.LiveState{App: app, Target: target.ID, ReadAt: time.Now().UTC(),
		Services: map[string]provider.ServiceState{}}
	for name, service := range live {
		health := provider.Healthy
		message := "serving"
		// Reconciling==false with a terminal condition failure is degraded.
		if service.TerminalCondition != nil && service.TerminalCondition.State == "CONDITION_FAILED" {
			health = provider.Degraded
			message = service.TerminalCondition.Message
		}
		image := ""
		if service.Template != nil && len(service.Template.Containers) > 0 {
			image = service.Template.Containers[0].Image
		}
		state.Services[name] = provider.ServiceState{
			Health: health, Replicas: 1, Ready: 1, Image: image, Message: message,
		}
		if revision := service.Annotations[annotationRevision]; revision != "" {
			state.Revision = revision
		}
		if hash := service.Annotations[annotationSpecHash]; hash != "" {
			state.SpecHash = hash
		}
	}
	return state, nil
}

func (p *Provider) Instances(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Instance, error) {
	client, err := p.client(ctx)
	if err != nil {
		return nil, err
	}
	live, err := p.servicesFor(ctx, client, target, app)
	if err != nil {
		return nil, err
	}

	var instances []provider.Instance
	for name, service := range live {
		image := ""
		if service.Template != nil && len(service.Template.Containers) > 0 {
			image = service.Template.Containers[0].Image
		}
		created, _ := time.Parse(time.RFC3339, service.CreateTime)
		// Cloud Run abstracts individual instances away; the serving
		// revision is the observable unit.
		instances = append(instances, provider.Instance{
			Ref: provider.InstanceRef{
				AppRef: app, Service: name, Instance: service.LatestReadyRevision,
			},
			Image: image, Status: "serving", Health: provider.Healthy,
			StartedAt: created, Revision: service.Annotations[annotationRevision],
		})
	}
	sort.Slice(instances, func(i, j int) bool { return instances[i].Ref.Instance < instances[j].Ref.Instance })
	return instances, nil
}

// Metrics: Cloud Monitoring is the provider-native store. Wiring its query
// surface is real work with its own auth scopes; until a slice needs it the
// honest answer names the console.
//
// ponytail: returns a clear refusal, not an empty series. Add the
// monitoring.googleapis.com timeSeries query when a deployment needs charts
// for Cloud Run inside HEIMDALL rather than in Cloud Console.
func (p *Provider) Metrics(context.Context, provider.Target, provider.InstanceRef, provider.Window) (provider.Series, error) {
	return provider.Series{}, fmt.Errorf(
		"HD0370: Cloud Run metrics live in Cloud Monitoring; HEIMDALL does not proxy them yet")
}

// Logs: same position as metrics — Cloud Logging holds them.
func (p *Provider) Logs(context.Context, provider.Target, provider.InstanceRef, provider.LogFilter) (io.ReadCloser, error) {
	return nil, fmt.Errorf(
		"HD0370: Cloud Run logs live in Cloud Logging; HEIMDALL does not proxy them yet")
}

// Events reports the service conditions Cloud Run maintains.
func (p *Provider) Events(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Event, error) {
	client, err := p.client(ctx)
	if err != nil {
		return nil, err
	}
	live, err := p.servicesFor(ctx, client, target, app)
	if err != nil {
		return nil, err
	}
	var events []provider.Event
	for name, service := range live {
		for _, condition := range service.Conditions {
			at, _ := time.Parse(time.RFC3339, condition.LastTransitionTime)
			events = append(events, provider.Event{
				At: at, Type: condition.Type, Service: name,
				Message: condition.Message, Source: "cloudrun",
			})
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].At.After(events[j].At) })
	return events, nil
}

func isNotFound(err error) bool {
	var apiError *googleapi.Error
	if err == nil {
		return false
	}
	if ok := errorsAs(err, &apiError); ok {
		return apiError.Code == 404
	}
	return false
}

func errorsAs(err error, target **googleapi.Error) bool {
	return errors.As(err, target)
}
