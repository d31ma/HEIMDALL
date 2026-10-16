package docker

import (
	"context"
	"fmt"
	"time"

	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
)

// Neither a standalone Engine nor Swarm has a scheduler, so a scheduled
// service on a Docker target runs on the heimdall agent's clock. The agent
// survives a control-plane restart and a failover; a control-plane-owned cron
// would miss runs across the first and double-fire across the second.
//
// The adapter therefore does not schedule anything itself. It registers
// desired state with whatever Clock it was given, reads registrations back
// for planning and drift, and refuses at plan time when there is no clock —
// which is exactly the case "this Docker target has no agent".

// Registration is one schedule as the clock holds it.
type Registration struct {
	Cron        string    `json:"cron"`
	Timezone    string    `json:"timezone"`
	Revision    string    `json:"revision"`
	ServiceHash string    `json:"service_hash"`
	SpecHash    string    `json:"spec_hash"`
	LastRunAt   time.Time `json:"last_run_at,omitempty"`
	LastStatus  string    `json:"last_run_status,omitempty"`
	NextRunAt   time.Time `json:"next_run_at,omitempty"`
}

// Clock is the schedule owner for a Docker target: the agent implements it.
type Clock interface {
	// Registrations lists what is currently scheduled for one application.
	Registrations(ctx context.Context, app provider.AppRef) (map[string]Registration, error)
	// Register creates or replaces one registration.
	Register(ctx context.Context, app provider.AppRef, service spec.Service, revision, specHash string) error
	// Unregister removes one, and is a no-op when nothing is registered.
	Unregister(ctx context.Context, app provider.AppRef, service string) error
	// RunNow starts one execution immediately, whatever the schedule says.
	RunNow(ctx context.Context, app provider.AppRef, service string) error
}

// scheduleSupport is the shared plan/observe/apply behaviour for both Docker
// adapters, which differ in everything except this.
type scheduleSupport struct {
	clock Clock
}

// rejectScheduledWithoutAClock is the plan-time refusal named in the
// capability caveat: a Docker target with no agent cannot run a job, and
// deploying the container anyway — to run once, now, and never again — would
// be a silently different thing from what was asked for.
func (s scheduleSupport) rejectScheduledWithoutAClock(want spec.DeploySpec) error {
	if s.clock != nil {
		return nil
	}
	for _, service := range want.Services {
		if service.Schedule == nil {
			continue
		}
		return fmt.Errorf(
			"HD0340: service %s is scheduled, and this Docker target has no heimdall agent to own the clock; "+
				"enroll an agent on the host, or move the job to a runtime with its own scheduler",
			service.Name)
	}
	return nil
}

func (s scheduleSupport) registrations(ctx context.Context, app provider.AppRef) (map[string]Registration, error) {
	if s.clock == nil {
		return map[string]Registration{}, nil
	}
	return s.clock.Registrations(ctx, app)
}

// planScheduled produces the operation for one scheduled service.
func planScheduled(
	service spec.Service, serviceHash string, registrations map[string]Registration,
) provider.Operation {
	registered, found := registrations[service.Name]
	switch {
	case !found:
		return provider.Operation{
			Kind: provider.OpCreate, Service: service.Name, Wave: service.Wave, Reason: "not scheduled"}
	case registered.ServiceHash != serviceHash:
		return provider.Operation{
			Kind: provider.OpUpdate, Service: service.Name, Wave: service.Wave,
			Reason: "the registered schedule differs from the desired one"}
	default:
		return provider.Operation{Kind: provider.OpNoop, Service: service.Name, Wave: service.Wave}
	}
}

// scheduledState is what Observe reports for a job. Between runs there is no
// container to look at, so health is the health of the *registration*: a job
// registered and waiting for 03:00 is Healthy, whether or not it has ever run.
// Only a failed last run is Degraded.
//
// Waiting is deliberately not Progressing. A sync waits for convergence, and
// a nightly job deployed at 10:00 would otherwise hold that wait open until
// its first run — reporting a perfectly good deploy as a failed rollout.
func scheduledState(registration Registration) provider.ServiceState {
	state := provider.ServiceState{
		Health:        provider.Healthy,
		Schedule:      registration.Cron,
		LastRunAt:     registration.LastRunAt,
		LastRunStatus: registration.LastStatus,
		NextRunAt:     registration.NextRunAt,
		Message:       "scheduled " + registration.Cron + " (" + registration.Timezone + ")",
	}
	switch {
	case registration.LastStatus == "failed":
		state.Health = provider.Degraded
		state.Message = "the last run failed at " + registration.LastRunAt.Format(time.RFC3339)
	case registration.Cron == "":
		state.Message = "manual-only job; nothing is scheduled"
	case registration.LastRunAt.IsZero():
		state.Message += "; no run yet"
	}
	return state
}

// RunNow starts one out-of-band execution. On a Docker target the clock is
// the agent's, so this only ever runs inside the agent — the control plane
// reaches it through the dispatcher, never by evaluating a schedule itself.
func (p *Provider) RunNow(ctx context.Context, _ provider.Target, app provider.AppRef, service string) error {
	if p.Clock == nil {
		return fmt.Errorf("HD0344: %s has no agent clock on this target, so there is nothing to run", service)
	}
	return p.Clock.RunNow(ctx, app, service)
}

// RunNow on Swarm is the standalone adapter's, for the same reason: the agent
// owns the clock either way.
func (s *Swarm) RunNow(ctx context.Context, _ provider.Target, app provider.AppRef, service string) error {
	if s.Clock == nil {
		return fmt.Errorf("HD0344: %s has no agent clock on this target, so there is nothing to run", service)
	}
	return s.Clock.RunNow(ctx, app, service)
}
