package docker

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"sort"
	"time"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

// Swarm deploys to a Docker Swarm through a manager's Engine API. It lives in
// this package because Swarm is the same Engine speaking two more endpoint
// families — /services and /tasks — not a different runtime: the transport,
// the labels, the hashing, and the log framing are all shared with the
// standalone adapter, and sharing the code is what keeps the two from
// disagreeing about any of them.
//
// The unit of desired state is the Swarm service. HEIMDALL never touches
// individual tasks: the scheduler owns placement and restarts, and reaching
// past it would be fighting the orchestrator it chose to use.
type Swarm struct {
	// Timeout bounds one Engine call; the endpoint comes from the target,
	// exactly as it does for the standalone Provider.
	Timeout        time.Duration
	SecretResolver func(ctx context.Context, ref string) (string, error)

	engines engineCache
}

func (s *Swarm) Name() string { return "swarm" }

func (s *Swarm) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return 2 * time.Minute
}

func (s *Swarm) connect(target provider.Target) (*engine, error) {
	return s.engines.get(target.Endpoint, s.timeout())
}

func (s *Swarm) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Provider: "swarm",
		Support: map[provider.Feature]provider.Support{
			provider.FeatureMultiService: provider.Full,
			provider.FeaturePorts:        provider.Full,
			provider.FeatureMultiPort:    provider.Full,
			provider.FeatureHealthcheck:  provider.Full,
			provider.FeatureNamedVolume:  provider.Full,
			provider.FeatureResources:    provider.Full,
			provider.FeatureRestart:      provider.Full,
			provider.FeatureSecretRef:    provider.Full,
			// File secrets are Swarm's own model — and content-hash naming
			// turns its immutable-secret rotation dance into a non-event.
			provider.FeatureFileSecret: provider.Full,
			// The scheduler is the whole point of Swarm.
			provider.FeatureReplicas: provider.Full,
			// depends_on orders creation, which Swarm honours only as wave
			// ordering — the scheduler starts tasks in its own time. Partial,
			// with the caveat saying exactly what is and is not promised.
			provider.FeatureDependsOn: provider.Partial,
			// A bind mount refers to a path on whichever node the scheduler
			// picks, which is a different file on every node. Rejecting beats
			// deploying something that works on one node in five.
			provider.FeatureBindMount: provider.Rejected,
			// Sidecar containers in one task do not exist in Swarm's model.
			provider.FeatureSidecars:    provider.Rejected,
			provider.FeatureScaleToZero: provider.Rejected,
		},
		Caveats: map[provider.Feature]string{
			provider.FeatureDependsOn:   "Swarm orders service creation by wave; task start order belongs to the scheduler",
			provider.FeatureBindMount:   "a bind mount names a node-local path, which is a different file on every node; use a named volume",
			provider.FeatureSidecars:    "one container per Swarm task; declare the sidecar as its own service",
			provider.FeatureScaleToZero: "Swarm keeps replicas running; use Cloud Run or Container Apps for scale to zero",
		},
	}
}

// Plan mirrors the standalone planner over services instead of containers.
func (s *Swarm) Plan(ctx context.Context, target provider.Target, want spec.DeploySpec) (provider.Plan, error) {
	app := provider.AppRef{Project: projectOf(target), App: want.App}

	if err := provider.Validate(s.Capabilities(), want); err != nil {
		return provider.Plan{}, err
	}
	specHash, err := spec.Hash(want)
	if err != nil {
		return provider.Plan{}, err
	}
	engine, err := s.connect(target)
	if err != nil {
		return provider.Plan{}, err
	}
	live, err := s.servicesFor(ctx, engine, app)
	if err != nil {
		return provider.Plan{}, err
	}

	plan := provider.Plan{Target: target.ID, App: app, Revision: want.Revision, SpecHash: specHash}

	desired := map[string]bool{}
	for _, service := range want.Services {
		desired[service.Name] = true
		serviceHash, err := hashService(service)
		if err != nil {
			return provider.Plan{}, err
		}
		existing, running := live[service.Name]
		switch {
		case !running:
			plan.Operations = append(plan.Operations, provider.Operation{
				Kind: provider.OpCreate, Service: service.Name, Wave: service.Wave,
				Reason: "not deployed",
			})
		case existing.Spec.Labels[LabelServiceHash] != serviceHash:
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
		if desired[name] {
			continue
		}
		plan.Operations = append(plan.Operations, provider.Operation{
			Kind: provider.OpDelete, Service: name, Prune: true,
			Reason: "no longer declared in the desired state",
		})
	}

	sort.SliceStable(plan.Operations, func(i, j int) bool {
		if plan.Operations[i].Wave != plan.Operations[j].Wave {
			return plan.Operations[i].Wave < plan.Operations[j].Wave
		}
		return plan.Operations[i].Service < plan.Operations[j].Service
	})
	return plan, nil
}

// Apply executes a plan wave by wave, exactly as the standalone adapter does.
func (s *Swarm) Apply(ctx context.Context, target provider.Target, plan provider.Plan) (provider.Result, error) {
	options, ok := applyOptions(ctx)
	if !ok {
		return provider.Result{}, fmt.Errorf("HD0330: apply called without options; use WithApply")
	}
	specHash, err := spec.Hash(options.Spec)
	if err != nil {
		return provider.Result{}, err
	}
	if specHash != plan.SpecHash {
		return provider.Result{}, fmt.Errorf(
			"HD0331: the plan was computed for spec %s but apply was asked to run %s; re-plan",
			plan.SpecHash, specHash)
	}

	engine, err := s.connect(target)
	if err != nil {
		return provider.Result{}, err
	}
	live, err := s.servicesFor(ctx, engine, plan.App)
	if err != nil {
		return provider.Result{}, err
	}
	services := map[string]spec.Service{}
	for _, service := range options.Spec.Services {
		services[service.Name] = service
	}

	// File secrets are ensured before any service is touched: a Swarm secret
	// is immutable, so each distinct value gets its own content-hash-named
	// object, and services reference the name their value hashes to. Rotation
	// is therefore an ordinary update — no rename dance — and stale versions
	// are pruned after the waves settle.
	secretRefs, usedSecrets, err := s.ensureSecrets(ctx, engine, plan, options.Spec)
	if err != nil {
		return provider.Result{}, err
	}

	result := provider.Result{}
	operations := append([]provider.Operation(nil), plan.Operations...)
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
			if err := s.execute(ctx, engine, plan, operation, services, live, secretRefs); err != nil {
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

	s.pruneSecrets(ctx, engine, plan, usedSecrets)
	return result, nil
}

// LabelSecret names the compose secret a managed Swarm secret carries, so
// pruning can tell this app's versions apart from everything else.
const LabelSecret = "dev.delma.heimdall.secret"

// ensureSecrets resolves every file secret in the spec and creates any
// missing content-hash-named Swarm secret. It returns each service's
// references and the set of secret names this revision uses.
func (s *Swarm) ensureSecrets(
	ctx context.Context,
	engine *engine,
	plan provider.Plan,
	want spec.DeploySpec,
) (map[string][]swarmSecretReference, map[string]bool, error) {
	references := map[string][]swarmSecretReference{}
	used := map[string]bool{}

	// One listing answers every lookup; an apply must not scale Engine calls
	// with secret count.
	existing := map[string]swarmSecret{}
	for _, service := range want.Services {
		if len(service.Secrets) > 0 {
			listed, err := engine.listSecrets(ctx, map[string][]string{
				"label": {LabelManagedBy + "=" + managedBy},
			})
			if err != nil {
				return nil, nil, err
			}
			for _, secret := range listed {
				existing[secret.Spec.Name] = secret
			}
			break
		}
	}

	for _, service := range want.Services {
		for _, mount := range service.Secrets {
			if s.SecretResolver == nil {
				return nil, nil, fmt.Errorf(
					"HD0337: service %q mounts secret %q but no secret resolver is configured",
					service.Name, mount.Name)
			}
			value, err := s.SecretResolver(ctx, mount.Ref)
			if err != nil {
				return nil, nil, fmt.Errorf("HD0337: resolve secret %q for service %q: %w",
					mount.Name, service.Name, err)
			}

			// The name is the content address: a rotated value is a new
			// object, and an unchanged value converges on the same one.
			digest := sha256.Sum256([]byte(value))
			name := fmt.Sprintf("%s-%s-%s-%s",
				plan.App.Project, plan.App.App, mount.Name, hex.EncodeToString(digest[:4]))
			if len(name) > 64 {
				return nil, nil, fmt.Errorf(
					"HD0337: Swarm limits secret names to 64 characters and %q is %d; shorten the project, app, or secret name",
					name, len(name))
			}

			identifier := ""
			if found, ok := existing[name]; ok {
				identifier = found.ID
			} else {
				identifier, err = engine.createSecret(ctx, swarmSecretSpec{
					Name: name,
					Labels: map[string]string{
						LabelManagedBy: managedBy,
						LabelProject:   plan.App.Project,
						LabelApp:       plan.App.App,
						LabelSecret:    mount.Name,
					},
					Data: base64.StdEncoding.EncodeToString([]byte(value)),
				})
				if err != nil {
					return nil, nil, fmt.Errorf("HD0337: create Swarm secret %q: %w", name, err)
				}
				existing[name] = swarmSecret{ID: identifier, Spec: swarmSecretSpec{Name: name}}
			}

			target := mount.Target
			if target == "" {
				target = mount.Name
			}
			used[name] = true
			references[service.Name] = append(references[service.Name], swarmSecretReference{
				SecretID:   identifier,
				SecretName: name,
				File:       swarmSecretFile{Name: target, UID: "0", GID: "0", Mode: 0o444},
			})
		}
	}
	return references, used, nil
}

// pruneSecrets deletes this application's managed secrets that no current
// service references. The Engine refuses to delete a secret a service still
// uses, which is exactly the safety wanted: a failed delete is left for the
// next sync rather than failing an apply that already succeeded.
func (s *Swarm) pruneSecrets(ctx context.Context, engine *engine, plan provider.Plan, used map[string]bool) {
	listed, err := engine.listSecrets(ctx, map[string][]string{
		"label": {
			LabelManagedBy + "=" + managedBy,
			LabelProject + "=" + plan.App.Project,
			LabelApp + "=" + plan.App.App,
		},
	})
	if err != nil {
		return
	}
	for _, secret := range listed {
		if used[secret.Spec.Name] {
			continue
		}
		_ = engine.deleteSecret(ctx, secret.ID)
	}
}

func (s *Swarm) execute(
	ctx context.Context,
	engine *engine,
	plan provider.Plan,
	operation provider.Operation,
	services map[string]spec.Service,
	live map[string]swarmService,
	secretRefs map[string][]swarmSecretReference,
) error {
	switch operation.Kind {
	case provider.OpDelete:
		if existing, ok := live[operation.Service]; ok {
			return engine.deleteService(ctx, existing.ID)
		}
		return nil
	case provider.OpCreate, provider.OpUpdate, provider.OpRestart:
		service, ok := services[operation.Service]
		if !ok {
			return fmt.Errorf("HD0332: the plan names service %q but the spec does not", operation.Service)
		}
		request, err := s.serviceSpec(ctx, plan, service)
		if err != nil {
			return err
		}
		request.TaskTemplate.ContainerSpec.Secrets = secretRefs[service.Name]
		if existing, running := live[operation.Service]; running {
			return engine.updateService(ctx, existing.ID, existing.Version, request)
		}
		return engine.createService(ctx, request)
	}
	return nil
}

// serviceSpec builds the Swarm service definition. Secrets resolve here, at
// apply time, into the task template's environment — the same one-way trip
// they make on the standalone adapter.
func (s *Swarm) serviceSpec(ctx context.Context, plan provider.Plan, service spec.Service) (swarmServiceSpec, error) {
	environment, err := (&Provider{SecretResolver: s.SecretResolver}).environment(ctx, service)
	if err != nil {
		return swarmServiceSpec{}, err
	}
	serviceHash, err := hashService(service)
	if err != nil {
		return swarmServiceSpec{}, err
	}

	labels := map[string]string{
		LabelManagedBy:   managedBy,
		LabelProject:     plan.App.Project,
		LabelApp:         plan.App.App,
		LabelService:     service.Name,
		LabelRevision:    plan.Revision,
		LabelServiceHash: serviceHash,
		LabelSpecHash:    plan.SpecHash,
	}

	replicas := uint64(service.Replicas)
	if replicas == 0 {
		replicas = 1
	}

	specification := swarmServiceSpec{
		Name:   fmt.Sprintf("%s-%s-%s", plan.App.Project, plan.App.App, service.Name),
		Labels: labels,
		TaskTemplate: swarmTaskTemplate{
			ContainerSpec: swarmContainerSpec{
				Image:  service.Image,
				Env:    environment,
				Labels: labels,
			},
		},
		Mode: swarmServiceMode{Replicated: &swarmReplicated{Replicas: replicas}},
	}
	for _, volume := range service.Volumes {
		if volume.Source == "" {
			continue
		}
		specification.TaskTemplate.ContainerSpec.Mounts = append(
			specification.TaskTemplate.ContainerSpec.Mounts,
			swarmMount{Type: "volume", Source: volume.Source, Target: volume.Target},
		)
	}
	for _, port := range service.Ports {
		specification.EndpointSpec.Ports = append(specification.EndpointSpec.Ports, swarmPortConfig{
			Protocol: port.Protocol, TargetPort: port.Target, PublishedPort: port.Published,
		})
	}
	return specification, nil
}

// Observe reads services and their tasks back.
func (s *Swarm) Observe(ctx context.Context, target provider.Target, app provider.AppRef) (provider.LiveState, error) {
	engine, err := s.connect(target)
	if err != nil {
		return provider.LiveState{}, err
	}
	live, err := s.servicesFor(ctx, engine, app)
	if err != nil {
		return provider.LiveState{}, err
	}

	state := provider.LiveState{App: app, Target: target.ID, ReadAt: time.Now().UTC(),
		Services: map[string]provider.ServiceState{}}
	for name, service := range live {
		tasks, err := engine.listTasks(ctx, service.ID)
		if err != nil {
			return provider.LiveState{}, err
		}
		running := 0
		for _, task := range tasks {
			if task.Status.State == "running" {
				running++
			}
		}
		desired := 1
		if service.Spec.Mode.Replicated != nil {
			desired = int(service.Spec.Mode.Replicated.Replicas)
		}
		health := provider.Healthy
		switch {
		case running == 0 && desired > 0:
			health = provider.Missing
		case running < desired:
			health = provider.Degraded
		}
		state.Services[name] = provider.ServiceState{
			Health: health, Replicas: desired, Ready: running,
			Image:   service.Spec.TaskTemplate.ContainerSpec.Image,
			Message: fmt.Sprintf("%d/%d tasks running", running, desired),
		}
		if revision := service.Spec.Labels[LabelRevision]; revision != "" {
			state.Revision = revision
		}
		if hash := service.Spec.Labels[LabelSpecHash]; hash != "" {
			state.SpecHash = hash
		}
	}
	return state, nil
}

// Instances lists tasks: each running task is one instance, on whatever node
// the scheduler placed it.
func (s *Swarm) Instances(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Instance, error) {
	engine, err := s.connect(target)
	if err != nil {
		return nil, err
	}
	live, err := s.servicesFor(ctx, engine, app)
	if err != nil {
		return nil, err
	}

	var instances []provider.Instance
	for name, service := range live {
		tasks, err := engine.listTasks(ctx, service.ID)
		if err != nil {
			return nil, err
		}
		for _, task := range tasks {
			health := provider.Healthy
			if task.Status.State != "running" {
				health = provider.Degraded
			}
			instances = append(instances, provider.Instance{
				Ref: provider.InstanceRef{
					AppRef: app, Service: name, Instance: task.ID,
				},
				Image:     service.Spec.TaskTemplate.ContainerSpec.Image,
				Status:    task.Status.State,
				Health:    health,
				StartedAt: task.CreatedAt,
				Revision:  service.Spec.Labels[LabelRevision],
				Node:      task.NodeID,
			})
		}
	}
	sort.Slice(instances, func(i, j int) bool { return instances[i].Ref.Instance < instances[j].Ref.Instance })
	return instances, nil
}

// Metrics: a manager cannot read another node's container stats over the
// Engine API. The honest answer names the path that works.
func (s *Swarm) Metrics(context.Context, provider.Target, provider.InstanceRef, provider.Window) (provider.Series, error) {
	return provider.Series{}, fmt.Errorf(
		"HD0335: Swarm task metrics require an agent on each node; enrol the nodes as Docker Engine targets for per-container metrics")
}

// Logs streams a service's logs — the manager aggregates its tasks' output,
// framed the same way container logs are.
func (s *Swarm) Logs(
	ctx context.Context,
	target provider.Target,
	instance provider.InstanceRef,
	filter provider.LogFilter,
) (io.ReadCloser, error) {
	engine, err := s.connect(target)
	if err != nil {
		return nil, err
	}
	live, err := s.servicesFor(ctx, engine, instance.AppRef)
	if err != nil {
		return nil, err
	}
	service, ok := live[instance.Service]
	if !ok {
		return nil, fmt.Errorf("HD0336: no service %q is deployed", instance.Service)
	}

	query := url.Values{}
	query.Set("stdout", "true")
	query.Set("stderr", "true")
	if filter.Tail > 0 {
		query.Set("tail", fmt.Sprint(filter.Tail))
	}
	if filter.Follow {
		query.Set("follow", "true")
	}
	return engine.serviceLogs(ctx, service.ID, query)
}

func (s *Swarm) Events(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Event, error) {
	engine, err := s.connect(target)
	if err != nil {
		return nil, err
	}
	raw, err := engine.engineEvents(ctx, time.Now().Add(-24*time.Hour), map[string][]string{
		"label": {LabelApp + "=" + app.App, LabelProject + "=" + app.Project},
	})
	if err != nil {
		return nil, err
	}
	events := make([]provider.Event, 0, len(raw))
	for _, event := range raw {
		events = append(events, provider.Event{
			At: time.Unix(0, event.TimeNano).UTC(), Type: event.Action,
			Service: event.Actor.Attributes[LabelService],
			Message: event.Type + " " + event.Action, Source: "swarm",
		})
	}
	return events, nil
}

// servicesFor lists this application's services, by the same labels the
// standalone adapter filters containers with.
func (s *Swarm) servicesFor(ctx context.Context, engine *engine, app provider.AppRef) (map[string]swarmService, error) {
	services, err := engine.listServices(ctx, map[string][]string{
		"label": {
			LabelManagedBy + "=" + managedBy,
			LabelProject + "=" + app.Project,
			LabelApp + "=" + app.App,
		},
	})
	if err != nil {
		return nil, err
	}
	byName := make(map[string]swarmService, len(services))
	for _, service := range services {
		name := service.Spec.Labels[LabelService]
		if name == "" {
			continue
		}
		byName[name] = service
	}
	return byName, nil
}
