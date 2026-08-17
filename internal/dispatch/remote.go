package dispatch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

// Remote is a provider.Provider whose work runs on an agent.
//
// It exists so the reconciler never branches on "is this target behind an
// agent". A target with an agent resolves to a Remote and everything above —
// refresh, plan, diff, sync, rollback, the operation document — is the same
// code path it is for a directly reachable target. One reconcile loop, not
// two that drift.
type Remote struct {
	Dispatcher *Dispatcher
	Target     provider.Target
	// Capability is the answer the agent's adapter would give. It is supplied
	// by the control plane rather than asked over the wire because plan-time
	// rejection must work even when the agent is offline: telling an operator
	// their compose file is unsupported should not require a reachable host.
	Capability provider.Capabilities
	// Secrets and Registries are resolved control-plane side and travel with
	// a sync job. They are nil for read-only jobs, which is why an observe
	// never carries a credential.
	Secrets    func(ctx context.Context, ref string) (string, error)
	Registries provider.RegistryResolver
	// NewJobID makes a job id. It is injectable so tests are deterministic.
	NewJobID func() string
}

func (r *Remote) Name() string { return r.Capability.Provider }

func (r *Remote) Capabilities() provider.Capabilities { return r.Capability }

func (r *Remote) jobID() string {
	if r.NewJobID != nil {
		return r.NewJobID()
	}
	return fmt.Sprintf("job-%d", time.Now().UnixNano())
}

// Plan asks the agent to plan. The capability check runs here, before the
// round trip, so an unsupported compose file is rejected with the offending
// service even when the host is unreachable.
func (r *Remote) Plan(ctx context.Context, target provider.Target, want spec.DeploySpec) (provider.Plan, error) {
	if err := provider.Validate(r.Capability, want); err != nil {
		return provider.Plan{}, err
	}
	outcome, err := r.Dispatcher.Submit(ctx, Job{
		ID: r.jobID(), TargetID: target.ID, Kind: KindPlan,
		App:  provider.AppRef{Project: target.Region, App: want.App},
		Spec: want,
	})
	if err != nil {
		return provider.Plan{}, r.describe(target, err)
	}
	return outcome.Plan, nil
}

// Apply asks the agent to plan and apply. The agent re-plans locally rather
// than trusting a plan computed elsewhere: live state may have moved since,
// and the agent is the only process that can see it.
func (r *Remote) Apply(ctx context.Context, target provider.Target, plan provider.Plan) (provider.Result, error) {
	options, ok := applyOptions(ctx)
	if !ok {
		return provider.Result{}, errors.New(
			"HD0365: a remote apply needs the spec the plan was produced from; use dispatch.WithApply")
	}
	specHash, err := spec.Hash(options.Spec)
	if err != nil {
		return provider.Result{}, err
	}
	if specHash != plan.SpecHash {
		return provider.Result{}, fmt.Errorf(
			"HD0366: plan was produced for spec %s but apply was given %s; re-plan before applying",
			plan.SpecHash, specHash)
	}

	job := Job{
		ID: r.jobID(), TargetID: target.ID, Kind: KindSync,
		App: plan.App, Spec: options.Spec, Prune: options.Prune,
	}
	if job.Secrets, err = r.collectSecrets(ctx, options.Spec); err != nil {
		return provider.Result{}, err
	}
	if job.Registries, err = r.collectRegistries(ctx, options.Spec); err != nil {
		return provider.Result{}, err
	}

	outcome, err := r.Dispatcher.Submit(ctx, job)
	if err != nil {
		return outcome.Result, r.describe(target, err)
	}
	return outcome.Result, nil
}

func (r *Remote) Observe(ctx context.Context, target provider.Target, app provider.AppRef) (provider.LiveState, error) {
	outcome, err := r.Dispatcher.Submit(ctx, Job{
		ID: r.jobID(), TargetID: target.ID, Kind: KindObserve, App: app,
	})
	if err != nil {
		return provider.LiveState{}, r.describe(target, err)
	}
	return outcome.Live, nil
}

func (r *Remote) Instances(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Instance, error) {
	outcome, err := r.Dispatcher.Submit(ctx, Job{
		ID: r.jobID(), TargetID: target.ID, Kind: KindInstances, App: app,
	})
	if err != nil {
		return nil, r.describe(target, err)
	}
	return outcome.Instances, nil
}

// Metrics and Logs need a stream rather than a request and a reply, so they
// wait for the agent's stats and log channels in Phase 3. Returning a clear
// refusal is better than a silent empty series that reads as "this container
// is doing nothing".
// Metrics asks the agent for one live sample. The history behind it comes
// from the rollups the agent ships on its own cadence; this call is only the
// freshest edge, so a chart does not stop a minute short during a deploy.
func (r *Remote) Metrics(
	ctx context.Context,
	target provider.Target,
	instance provider.InstanceRef,
	_ provider.Window,
) (provider.Series, error) {
	outcome, err := r.Dispatcher.Submit(ctx, Job{
		ID: r.jobID(), TargetID: target.ID, Kind: KindMetrics,
		App: instance.AppRef, Service: instance.Service, Instance: instance.Instance,
	})
	if err != nil {
		return provider.Series{}, r.describe(target, err)
	}
	if outcome.Error != "" {
		return provider.Series{}, errors.New(outcome.Error)
	}
	return outcome.Series, nil
}

// Logs returns a bounded tail. Not a stream: the agent reads the tail and
// reports it as one outcome, which is exactly the shape the UI's polling
// wants, and a follow would hold the rendezvous hostage.
func (r *Remote) Logs(
	ctx context.Context,
	target provider.Target,
	instance provider.InstanceRef,
	filter provider.LogFilter,
) (io.ReadCloser, error) {
	if filter.Follow {
		return nil, errors.New(
			"HD0367: an agent tail is bounded, not followed; poll with tail= instead")
	}
	outcome, err := r.Dispatcher.Submit(ctx, Job{
		ID: r.jobID(), TargetID: target.ID, Kind: KindLogs,
		App: instance.AppRef, Service: instance.Service, Instance: instance.Instance,
		Tail: filter.Tail,
	})
	if err != nil {
		return nil, r.describe(target, err)
	}
	if outcome.Error != "" {
		return nil, errors.New(outcome.Error)
	}
	return io.NopCloser(bytes.NewReader(outcome.Logs)), nil
}

func (r *Remote) Events(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Event, error) {
	outcome, err := r.Dispatcher.Submit(ctx, Job{
		ID: r.jobID(), TargetID: target.ID, Kind: KindEvents, App: app,
	})
	if err != nil {
		return nil, r.describe(target, err)
	}
	if outcome.Error != "" {
		return nil, errors.New(outcome.Error)
	}
	return outcome.Events, nil
}

// collectSecrets resolves every reference the spec carries, once, so the
// agent receives values rather than references it has no way to resolve.
func (r *Remote) collectSecrets(ctx context.Context, want spec.DeploySpec) (map[string]string, error) {
	references := map[string]bool{}
	for _, service := range want.Services {
		for _, env := range service.Env {
			if env.Ref != "" {
				references[env.Ref] = true
			}
		}
	}
	if len(references) == 0 {
		return nil, nil
	}
	if r.Secrets == nil {
		return nil, errors.New(
			"HD0368: this application uses ${secret:...} but no secret resolver is configured; " +
				"refusing to send the agent a spec it cannot complete")
	}
	resolved := make(map[string]string, len(references))
	for reference := range references {
		value, err := r.Secrets(ctx, reference)
		if err != nil {
			return nil, fmt.Errorf("HD0369: resolve secret %q: %w", reference, err)
		}
		resolved[reference] = value
	}
	return resolved, nil
}

func (r *Remote) collectRegistries(ctx context.Context, want spec.DeploySpec) (map[string]provider.RegistryCredential, error) {
	if r.Registries == nil {
		return nil, nil
	}
	credentials := map[string]provider.RegistryCredential{}
	for _, service := range want.Services {
		credential, err := r.Registries(ctx, service.Image)
		if err != nil {
			return nil, fmt.Errorf("HD0370: resolve registry credential for %s: %w", service.Image, err)
		}
		if credential != nil {
			credentials[credential.Server] = *credential
		}
	}
	if len(credentials) == 0 {
		return nil, nil
	}
	return credentials, nil
}

// describe turns "no agent" into something an operator can act on.
func (r *Remote) describe(target provider.Target, err error) error {
	if !errors.Is(err, ErrNoAgent) && !errors.Is(err, ErrAgentTimeout) {
		return err
	}
	seen, ever := r.Dispatcher.LastSeen(target.ID)
	switch {
	case !ever:
		// %w keeps ErrNoAgent detectable: the reconciler parks a sync on an
		// offline target rather than failing it, and it needs to tell
		// "offline" apart from "broken" without parsing text.
		return fmt.Errorf(
			"HD0371: no agent has ever connected for target %s; enrol one with `heimdall enroll --target %s`: %w",
			target.ID, target.ID, ErrNoAgent)
	case errors.Is(err, ErrAgentTimeout):
		return fmt.Errorf("HD0372: the agent for target %s took the job and did not report; last seen %s: %w",
			target.ID, seen.Format(time.RFC3339), ErrAgentTimeout)
	default:
		return fmt.Errorf("HD0373: the agent for target %s is not connected; last seen %s: %w",
			target.ID, seen.Format(time.RFC3339), ErrNoAgent)
	}
}

// ApplyOptions is what a remote apply needs beyond the plan. It mirrors the
// docker adapter's, because the reconciler treats both the same way.
type ApplyOptions struct {
	Spec  spec.DeploySpec
	Prune bool
}

type applyKey struct{}

// WithApply attaches the spec and options a remote apply needs.
func WithApply(ctx context.Context, options ApplyOptions) context.Context {
	return context.WithValue(ctx, applyKey{}, options)
}

func applyOptions(ctx context.Context) (ApplyOptions, bool) {
	options, ok := ctx.Value(applyKey{}).(ApplyOptions)
	return options, ok
}
