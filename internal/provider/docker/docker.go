package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

// Labels HEIMDALL stamps on everything it creates. They are how Observe finds
// its own workloads, how drift is detected without a local database, and how
// a running container reports the commit that put it there — the product's
// headline claim, so it is a label and not a lookup table that can go stale.
const (
	LabelProject  = "dev.delma.heimdall.project"
	LabelApp      = "dev.delma.heimdall.app"
	LabelService  = "dev.delma.heimdall.service"
	LabelRevision = "dev.delma.heimdall.revision"
	LabelSpecHash = "dev.delma.heimdall.spec-hash"
	// LabelServiceHash is the per-service content hash. Comparing it is what
	// makes a plan cheap: no field-by-field inspection of a live container is
	// needed to know whether it matches the desired state.
	LabelServiceHash = "dev.delma.heimdall.service-hash"
	LabelManagedBy   = "dev.delma.heimdall.managed-by"
)

const managedBy = "heimdall"

// stopTimeout is how long a container gets to exit before the Engine kills
// it. Ten seconds matches Docker's own default.
const stopTimeout = 10 * time.Second

// Provider is the Docker Engine adapter.
type Provider struct {
	// Timeout bounds one Engine call. Zero uses a sensible default.
	Timeout time.Duration
	// SecretResolver supplies values for ${secret:...} references at apply
	// time only. It is nil until a resolver is configured, and a plan that
	// needs one fails rather than deploying a container with a missing
	// variable.
	SecretResolver func(ctx context.Context, ref string) (string, error)

	// engines caches one client per endpoint. A fresh transport per call
	// leaks its keep-alive socket when the transport is dropped — one
	// connection per reconcile or scrape poll, forever — which is how a
	// long-running control plane exhausted its host's ephemeral ports.
	// Bounded by the number of distinct target endpoints.
	mu      sync.Mutex
	engines map[string]*engine
}

func (p *Provider) Name() string { return "docker" }

func (p *Provider) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return 60 * time.Second
}

func (p *Provider) connect(target provider.Target) (*engine, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cached, ok := p.engines[target.Endpoint]; ok {
		return cached, nil
	}
	dialed, err := newEngine(target.Endpoint, p.timeout())
	if err != nil {
		return nil, err
	}
	if p.engines == nil {
		p.engines = map[string]*engine{}
	}
	p.engines[target.Endpoint] = dialed
	return dialed, nil
}

// Capabilities is the honest answer for a standalone Docker Engine. Swarm is
// a Phase 4 addition and its answers differ, which is exactly why this is a
// method and not a constant.
func (p *Provider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Provider: "docker",
		Support: map[provider.Feature]provider.Support{
			provider.FeatureMultiService: provider.Full,
			provider.FeatureSidecars:     provider.Full,
			provider.FeaturePorts:        provider.Full,
			provider.FeatureMultiPort:    provider.Full,
			provider.FeatureDependsOn:    provider.Full,
			provider.FeatureHealthcheck:  provider.Full,
			provider.FeatureNamedVolume:  provider.Full,
			provider.FeatureBindMount:    provider.Full,
			provider.FeatureResources:    provider.Full,
			provider.FeatureRestart:      provider.Full,
			provider.FeatureSecretRef:    provider.Full,
			// A standalone Engine has no scheduler, so replicas beyond one and
			// scaling to zero are Swarm features. Rejecting is the honest
			// answer; silently running one container when three were asked for
			// is the dishonest one.
			provider.FeatureReplicas:    provider.Rejected,
			provider.FeatureScaleToZero: provider.Rejected,
			provider.FeatureFileSecret:  provider.Rejected,
		},
		Caveats: map[provider.Feature]string{
			provider.FeatureReplicas:    "a standalone Engine runs one container per service; use Swarm or a cloud target for replicas",
			provider.FeatureScaleToZero: "a standalone Engine has no scale-to-zero; use Cloud Run or Container Apps",
			provider.FeatureFileSecret:  "a standalone Engine has no secrets API; use Swarm, or an environment ${secret:...} reference",
		},
	}
}

// Plan compares the desired spec to what the Engine is actually running and
// returns the operations that would close the gap. It mutates nothing.
func (p *Provider) Plan(ctx context.Context, target provider.Target, want spec.DeploySpec) (provider.Plan, error) {
	app := provider.AppRef{Project: projectOf(target), App: want.App}

	// Fail closed before touching the Engine: an unsupported directive must
	// be reported with the offending service, not discovered halfway through
	// an apply.
	if err := provider.Validate(p.Capabilities(), want); err != nil {
		return provider.Plan{}, err
	}

	specHash, err := spec.Hash(want)
	if err != nil {
		return provider.Plan{}, err
	}

	engine, err := p.connect(target)
	if err != nil {
		return provider.Plan{}, err
	}
	live, err := p.containersFor(ctx, engine, app)
	if err != nil {
		return provider.Plan{}, err
	}

	plan := provider.Plan{
		Target:   target.ID,
		App:      app,
		Revision: want.Revision,
		SpecHash: specHash,
	}

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
				Reason: "not running",
			})
		case existing.Labels[LabelServiceHash] != serviceHash:
			plan.Operations = append(plan.Operations, provider.Operation{
				Kind: provider.OpUpdate, Service: service.Name, Wave: service.Wave,
				Reason: describeChange(existing, service),
			})
		case existing.State != "running":
			plan.Operations = append(plan.Operations, provider.Operation{
				Kind: provider.OpRestart, Service: service.Name, Wave: service.Wave,
				Reason: "container is " + existing.State,
			})
		default:
			plan.Operations = append(plan.Operations, provider.Operation{
				Kind: provider.OpNoop, Service: service.Name, Wave: service.Wave,
			})
		}
	}

	// Anything HEIMDALL manages for this app that the spec no longer mentions
	// is a prune candidate. It is marked, never executed, unless the caller
	// opted in — the failure mode of getting this wrong is deleting something
	// real.
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

// ApplyOptions carries what Apply needs beyond the plan itself.
type ApplyOptions struct {
	// Spec is the desired state the plan was produced from. Apply refuses a
	// spec whose hash does not match the plan's, so a plan can never be
	// applied against a different revision than the one it was previewed on.
	Spec spec.DeploySpec
	// Prune executes the plan's prune operations. Off by default.
	Prune bool
	// Registries supplies pull credentials for this apply. It travels with
	// the apply rather than sitting on the Provider because one Provider
	// serves every application, and each has its own registries.
	Registries provider.RegistryResolver
}

// applyContext threads options through Apply without widening the Provider
// interface, which every adapter must implement.
type applyKey struct{}

// WithApply attaches the spec and options an apply needs.
func WithApply(ctx context.Context, options ApplyOptions) context.Context {
	return context.WithValue(ctx, applyKey{}, options)
}

func applyOptions(ctx context.Context) (ApplyOptions, bool) {
	options, ok := ctx.Value(applyKey{}).(ApplyOptions)
	return options, ok
}

// Apply executes a plan, wave by wave. A wave settles before the next starts,
// and a failed operation stops that wave's successors rather than racing on.
func (p *Provider) Apply(ctx context.Context, target provider.Target, plan provider.Plan) (provider.Result, error) {
	options, ok := applyOptions(ctx)
	if !ok {
		return provider.Result{}, errors.New("HD0320: apply requires the spec the plan was produced from; use docker.WithApply")
	}
	specHash, err := spec.Hash(options.Spec)
	if err != nil {
		return provider.Result{}, err
	}
	if specHash != plan.SpecHash {
		return provider.Result{}, fmt.Errorf(
			"HD0321: plan was produced for spec %s but apply was given %s; re-plan before applying",
			plan.SpecHash, specHash)
	}

	engine, err := p.connect(target)
	if err != nil {
		return provider.Result{}, err
	}
	live, err := p.containersFor(ctx, engine, plan.App)
	if err != nil {
		return provider.Result{}, err
	}

	result := provider.Result{OpID: plan.SpecHash, Failures: map[string]string{}}

	for _, wave := range plan.Waves() {
		for _, operation := range plan.Operations {
			if operation.Wave != wave || operation.Kind == provider.OpNoop {
				continue
			}
			if operation.Prune && !options.Prune {
				// Recorded as skipped rather than silently dropped: an
				// operator looking at the sync must see that a prune was
				// available and not taken.
				result.Failures[operation.Service] = "prune is not enabled for this application"
				continue
			}
			if err := p.execute(ctx, engine, target, plan, options.Spec, operation, live, options.Registries); err != nil {
				result.Failures[operation.Service] = err.Error()
				continue
			}
			result.Applied = append(result.Applied, operation)
		}
		// A wave that produced failures must not let the next wave start: the
		// next wave's services almost certainly depend on this one.
		if len(result.Failures) > 0 {
			break
		}
	}

	if len(result.Failures) == 0 {
		result.Failures = nil
	}
	return result, nil
}

func (p *Provider) execute(
	ctx context.Context,
	engine *engine,
	target provider.Target,
	plan provider.Plan,
	want spec.DeploySpec,
	operation provider.Operation,
	live map[string]containerSummary,
	registries provider.RegistryResolver,
) error {
	existing, running := live[operation.Service]

	switch operation.Kind {
	case provider.OpDelete:
		if !running {
			return nil
		}
		if err := engine.stopContainer(ctx, existing.ID, stopTimeout); err != nil {
			return err
		}
		return engine.removeContainer(ctx, existing.ID)

	case provider.OpRestart:
		if !running {
			return fmt.Errorf("HD0322: %s is not running and cannot be restarted", operation.Service)
		}
		return engine.startContainer(ctx, existing.ID)

	case provider.OpCreate, provider.OpUpdate:
		service, ok := want.Service(operation.Service)
		if !ok {
			return fmt.Errorf("HD0323: %s is not in the applied spec", operation.Service)
		}
		// Replace rather than mutate. A container's image, env, and mounts are
		// immutable in the Engine, so an update is a delete and a create; doing
		// it in that order keeps the port free.
		if running {
			if err := engine.stopContainer(ctx, existing.ID, stopTimeout); err != nil {
				return err
			}
			if err := engine.removeContainer(ctx, existing.ID); err != nil {
				return err
			}
		}
		return p.createAndStart(ctx, engine, target, plan, service, registries)

	default:
		return fmt.Errorf("HD0324: unsupported operation %q", operation.Kind)
	}
}

func (p *Provider) createAndStart(
	ctx context.Context,
	engine *engine,
	target provider.Target,
	plan provider.Plan,
	service spec.Service,
	registries provider.RegistryResolver,
) error {
	credential, err := registryAuthFor(ctx, registries, service.Image)
	if err != nil {
		return err
	}
	if err := engine.pullImage(ctx, service.Image, credential); err != nil {
		return err
	}

	serviceHash, hashErr := hashService(service)
	if hashErr != nil {
		return hashErr
	}

	request := createContainerRequest{
		Image:      service.Image,
		Cmd:        service.Command,
		Entrypoint: service.Entrypoint,
		Labels: map[string]string{
			LabelManagedBy:   managedBy,
			LabelProject:     plan.App.Project,
			LabelApp:         plan.App.App,
			LabelService:     service.Name,
			LabelRevision:    plan.Revision,
			LabelSpecHash:    plan.SpecHash,
			LabelServiceHash: serviceHash,
		},
	}
	for _, label := range service.Labels {
		request.Labels[label.Key] = label.Value
	}

	// Secrets are resolved here and nowhere else: in process, at apply time,
	// straight into the create call. No resolved value is returned, stored,
	// or logged.
	if request.Env, err = p.environment(ctx, service); err != nil {
		return err
	}

	if len(service.Ports) > 0 {
		request.ExposedPorts = map[string]struct{}{}
		request.HostConfig.PortBindings = map[string][]enginePortBind{}
		for _, port := range service.Ports {
			key := fmt.Sprintf("%d/%s", port.Target, port.Protocol)
			request.ExposedPorts[key] = struct{}{}
			request.HostConfig.PortBindings[key] = []enginePortBind{
				{HostPort: strconv.Itoa(port.Published)},
			}
		}
	}

	for _, volume := range service.Volumes {
		if !strings.HasPrefix(volume.Source, "/") && !strings.HasPrefix(volume.Source, ".") {
			// A named volume must exist before a container can mount it, and
			// creating it is idempotent.
			if err := engine.createVolume(ctx, volume.Source, map[string]string{
				LabelManagedBy: managedBy,
				LabelProject:   plan.App.Project,
				LabelApp:       plan.App.App,
			}); err != nil {
				return err
			}
		}
		bind := volume.Source + ":" + volume.Target
		if volume.ReadOnly {
			bind += ":ro"
		}
		request.HostConfig.Binds = append(request.HostConfig.Binds, bind)
	}

	if service.Healthcheck != nil {
		request.Healthcheck = &engineHealthcheck{
			Test:        service.Healthcheck.Test,
			Interval:    int64(service.Healthcheck.IntervalMS) * int64(time.Millisecond),
			Timeout:     int64(service.Healthcheck.TimeoutMS) * int64(time.Millisecond),
			StartPeriod: int64(service.Healthcheck.StartPerdMS) * int64(time.Millisecond),
			Retries:     service.Healthcheck.Retries,
		}
	}
	if service.Restart != "" {
		request.HostConfig.RestartPolicy = &engineRestartPolicy{Name: service.Restart}
	}
	if service.Resources != nil {
		request.HostConfig.Memory = int64(service.Resources.MemoryMiB) * (1 << 20)
		request.HostConfig.NanoCPUs = int64(service.Resources.CPUMillis) * 1_000_000
	}

	id, err := engine.createContainer(ctx, containerName(plan.App, service.Name), request)
	if err != nil {
		return err
	}
	return engine.startContainer(ctx, id)
}

// registryAuthFor resolves pull credentials for one image. A resolver that
// errors fails the apply: pulling anonymously after a credential lookup
// failed turns a misconfiguration into a confusing 404 from the registry.
func registryAuthFor(ctx context.Context, registries provider.RegistryResolver, image string) (*registryAuth, error) {
	if registries == nil {
		return nil, nil
	}
	credential, err := registries(ctx, image)
	if err != nil {
		return nil, fmt.Errorf("HD0327: resolve registry credential for %s: %w", image, err)
	}
	if credential == nil {
		return nil, nil
	}
	return &registryAuth{
		Username: credential.Username, Password: credential.Password,
		ServerAddress: credential.Server,
	}, nil
}

// environment builds the container's env, resolving secret references and
// nothing else.
func (p *Provider) environment(ctx context.Context, service spec.Service) ([]string, error) {
	if len(service.Env) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(service.Env))
	for _, env := range service.Env {
		if env.Ref == "" {
			out = append(out, env.Key+"="+env.Value)
			continue
		}
		if p.SecretResolver == nil {
			return nil, fmt.Errorf(
				"HD0325: %s needs secret %q but no secret resolver is configured; "+
					"refusing to start a container with the variable missing",
				service.Name, env.Ref)
		}
		value, err := p.SecretResolver(ctx, env.Ref)
		if err != nil {
			// The reference is safe to name; the value is not, and errors from
			// a resolver are wrapped rather than interpolated for that reason.
			return nil, fmt.Errorf("HD0326: resolve secret %q for %s: %w", env.Ref, service.Name, err)
		}
		out = append(out, env.Key+"="+value)
	}
	return out, nil
}

// Observe reads live state back from the Engine.
func (p *Provider) Observe(ctx context.Context, target provider.Target, app provider.AppRef) (provider.LiveState, error) {
	engine, err := p.connect(target)
	if err != nil {
		return provider.LiveState{}, err
	}
	containers, err := p.containersFor(ctx, engine, app)
	if err != nil {
		return provider.LiveState{}, err
	}

	state := provider.LiveState{
		App:      app,
		Target:   target.ID,
		ReadAt:   time.Now().UTC(),
		Services: map[string]provider.ServiceState{},
	}

	names := make([]string, 0, len(containers))
	for name := range containers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		summary := containers[name]
		inspected, err := engine.inspectContainer(ctx, summary.ID)
		if err != nil {
			if isNotFound(err) {
				// It disappeared between the list and the inspect. That is
				// drift, and reporting it as Missing is more accurate than
				// failing the whole read.
				state.Services[name] = provider.ServiceState{Health: provider.Missing, Message: "container disappeared during read"}
				continue
			}
			return provider.LiveState{}, err
		}

		ready := 0
		if inspected.State.Running {
			ready = 1
		}
		state.Services[name] = provider.ServiceState{
			Health:   healthOf(inspected),
			Replicas: 1,
			Ready:    ready,
			Image:    inspected.Config.Image,
			Message:  messageOf(inspected),
		}
		// Every container of one app carries the same revision; reading it
		// from any of them is how live state knows its own commit.
		if state.Revision == "" {
			state.Revision = summary.Labels[LabelRevision]
			state.SpecHash = summary.Labels[LabelSpecHash]
		}
	}
	return state, nil
}

func healthOf(inspected containerInspect) provider.Health {
	// Lifecycle first, health probe second. The Engine keeps the last health
	// result after a container exits, so a stopped container can still report
	// "healthy" — trusting that would show a dead service as green.
	switch inspected.State.Status {
	case "created", "restarting":
		return provider.Progressing
	case "paused":
		return provider.Suspended
	case "removing":
		return provider.Missing
	case "running":
		if inspected.State.Health != nil {
			switch inspected.State.Health.Status {
			case "healthy", "none", "":
				return provider.Healthy
			case "starting":
				return provider.Progressing
			case "unhealthy":
				return provider.Degraded
			}
		}
		return provider.Healthy
	default:
		// exited or dead. A clean exit is still not the desired state for a
		// long-running service, so it is Degraded rather than Healthy.
		return provider.Degraded
	}
}

func messageOf(inspected containerInspect) string {
	if inspected.State.Running {
		return ""
	}
	if inspected.State.ExitCode != 0 {
		return fmt.Sprintf("exited with code %d", inspected.State.ExitCode)
	}
	return inspected.State.Status
}

// Instances lists the running units, one per service on a standalone Engine.
func (p *Provider) Instances(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Instance, error) {
	engine, err := p.connect(target)
	if err != nil {
		return nil, err
	}
	containers, err := p.containersFor(ctx, engine, app)
	if err != nil {
		return nil, err
	}

	instances := make([]provider.Instance, 0, len(containers))
	for name, summary := range containers {
		inspected, err := engine.inspectContainer(ctx, summary.ID)
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return nil, err
		}
		instances = append(instances, provider.Instance{
			Ref: provider.InstanceRef{
				AppRef: app, Service: name, Instance: summary.ID,
			},
			Image:     inspected.Config.Image,
			Status:    inspected.State.Status,
			Health:    healthOf(inspected),
			StartedAt: inspected.State.StartedAt,
			Restarts:  inspected.RestartCount,
			Revision:  summary.Labels[LabelRevision],
		})
	}
	sort.Slice(instances, func(i, j int) bool {
		return instances[i].Ref.Service < instances[j].Ref.Service
	})
	return instances, nil
}

// ManagedInstances lists every HEIMDALL-managed container on the Engine,
// with the application each belongs to. The agent's metrics loop uses it: an
// agent knows its host, not the control plane's application table, and the
// labels stamped at apply time are how a container names its owner.
func (p *Provider) ManagedInstances(ctx context.Context, target provider.Target) ([]provider.Instance, error) {
	engine, err := p.connect(target)
	if err != nil {
		return nil, err
	}
	summaries, err := engine.listContainers(ctx, map[string][]string{
		"label": {LabelManagedBy + "=" + managedBy},
	})
	if err != nil {
		return nil, err
	}

	instances := make([]provider.Instance, 0, len(summaries))
	for _, summary := range summaries {
		service := summary.Labels[LabelService]
		project := summary.Labels[LabelProject]
		app := summary.Labels[LabelApp]
		if service == "" || project == "" || app == "" {
			continue
		}
		instances = append(instances, provider.Instance{
			Ref: provider.InstanceRef{
				AppRef:  provider.AppRef{Project: project, App: app},
				Service: service, Instance: summary.ID,
			},
			Image:    summary.Image,
			Revision: summary.Labels[LabelRevision],
		})
	}
	sort.Slice(instances, func(i, j int) bool {
		return instances[i].Ref.Instance < instances[j].Ref.Instance
	})
	return instances, nil
}

// Metrics reads one live sample. The 24-hour history the UI charts comes from
// the agent's ring buffer and the rollups in hd-rollups — this adapter is the
// live tail, not a time-series database.
func (p *Provider) Metrics(
	ctx context.Context,
	target provider.Target,
	instance provider.InstanceRef,
	_ provider.Window,
) (provider.Series, error) {
	engine, err := p.connect(target)
	if err != nil {
		return provider.Series{}, err
	}
	sample, err := engine.containerStats(ctx, instance.Instance)
	if err != nil {
		return provider.Series{}, err
	}

	at := sample.Read
	if at.IsZero() {
		at = time.Now().UTC()
	}
	rx, tx := sample.network()
	read, write := sample.blockIO()

	return provider.Series{
		Ref:          instance,
		CPUPercent:   []provider.Sample{{At: at, Value: sample.cpuPercent()}},
		MemoryBytes:  []provider.Sample{{At: at, Value: float64(sample.memoryUsage())}},
		MemoryLimit:  sample.MemoryStats.Limit,
		NetRxBytes:   []provider.Sample{{At: at, Value: float64(rx)}},
		NetTxBytes:   []provider.Sample{{At: at, Value: float64(tx)}},
		BlockRead:    []provider.Sample{{At: at, Value: float64(read)}},
		BlockWrite:   []provider.Sample{{At: at, Value: float64(write)}},
		Pids:         []provider.Sample{{At: at, Value: float64(sample.PidsStats.Current)}},
		CPUThrottled: []provider.Sample{{At: at, Value: float64(sample.CPUStats.ThrottlingData.ThrottledPeriods)}},
		NetErrors:    []provider.Sample{{At: at, Value: float64(sample.netErrors())}},
	}, nil
}

// Logs opens a log stream. The caller closes it.
func (p *Provider) Logs(
	ctx context.Context,
	target provider.Target,
	instance provider.InstanceRef,
	filter provider.LogFilter,
) (io.ReadCloser, error) {
	engine, err := p.connect(target)
	if err != nil {
		return nil, err
	}
	if filter.Follow {
		// A followed stream must not be cut off by the per-call timeout.
		engine.client.Timeout = 0
	}

	query := url.Values{"stdout": {"true"}, "stderr": {"true"}, "timestamps": {"true"}}
	tail := filter.Tail
	if tail <= 0 {
		tail = 200
	}
	query.Set("tail", strconv.Itoa(tail))
	if !filter.Since.IsZero() {
		query.Set("since", strconv.FormatInt(filter.Since.Unix(), 10))
	}
	if filter.Follow {
		query.Set("follow", "true")
	}
	return engine.containerLogs(ctx, instance.Instance, query)
}

// Events maps Engine events onto HEIMDALL's unified shape.
func (p *Provider) Events(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Event, error) {
	engine, err := p.connect(target)
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
			At:      time.Unix(0, event.TimeNano).UTC(),
			Type:    event.Type + "." + event.Action,
			Service: event.Actor.Attributes[LabelService],
			Message: strings.TrimSpace(event.Action + " " + event.Actor.Attributes["image"]),
			Source:  "docker",
		})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })
	return events, nil
}

// Ping verifies the Engine is reachable and new enough. It is not part of the
// Provider interface; `heimdall doctor` and target validation call it.
func (p *Provider) Ping(ctx context.Context, target provider.Target) (string, error) {
	engine, err := p.connect(target)
	if err != nil {
		return "", err
	}
	version, err := engine.version(ctx)
	if err != nil {
		return "", err
	}
	return version.Version, nil
}

// containersFor returns this app's containers, keyed by service name. The
// label selector is the whole mechanism: HEIMDALL never touches a container
// it did not create.
func (p *Provider) containersFor(ctx context.Context, engine *engine, app provider.AppRef) (map[string]containerSummary, error) {
	summaries, err := engine.listContainers(ctx, map[string][]string{
		"label": {
			LabelManagedBy + "=" + managedBy,
			LabelProject + "=" + app.Project,
			LabelApp + "=" + app.App,
		},
	})
	if err != nil {
		return nil, err
	}
	byService := make(map[string]containerSummary, len(summaries))
	for _, summary := range summaries {
		service := summary.Labels[LabelService]
		if service == "" {
			continue
		}
		// If duplicates somehow exist, the most recently created wins, so a
		// half-finished previous apply cannot pin the plan to a stale one.
		if existing, ok := byService[service]; ok && existing.Created > summary.Created {
			continue
		}
		byService[service] = summary
	}
	return byService, nil
}

func containerName(app provider.AppRef, service string) string {
	return fmt.Sprintf("heimdall-%s-%s-%s", app.Project, app.App, service)
}

func projectOf(target provider.Target) string {
	if target.Project != "" {
		return target.Project
	}
	// Region carried the project before Target grew a Project field; reading
	// it keeps a target created by an older control plane deployable.
	if target.Region != "" {
		return target.Region
	}
	return "default"
}

// hashService content-addresses one service, so a plan can decide whether a
// running container matches without inspecting it field by field.
func hashService(service spec.Service) (string, error) {
	return spec.HashService(service)
}

// describeChange says what actually differs, so the plan an operator reads
// names the change rather than restating "update".
func describeChange(existing containerSummary, want spec.Service) string {
	if existing.Image != want.Image {
		return fmt.Sprintf("image %s -> %s", existing.Image, want.Image)
	}
	if existing.Labels[LabelRevision] != "" {
		return "configuration changed since revision " + existing.Labels[LabelRevision]
	}
	return "configuration changed"
}
