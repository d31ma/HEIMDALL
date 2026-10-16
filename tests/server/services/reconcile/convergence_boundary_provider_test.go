package reconcile_test

import (
	"context"
	"errors"

	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/services/provider/docker"
)

type convergenceBoundaryProvider struct {
	*docker.Provider
	active      bool
	mode        string
	readCount   int
	readStarted chan struct{}
}

func (p *convergenceBoundaryProvider) Apply(ctx context.Context, target provider.Target, plan provider.Plan) (provider.Result, error) {
	result, err := p.Provider.Apply(ctx, target, plan)
	p.active = true
	return result, err
}

func (p *convergenceBoundaryProvider) Plan(ctx context.Context, target provider.Target, want spec.DeploySpec) (provider.Plan, error) {
	plan, err := p.Provider.Plan(ctx, target, want)
	if err != nil || !p.active {
		return plan, err
	}
	p.readCount++
	switch p.mode {
	case "read-error":
		return provider.Plan{}, errors.New("synthetic planning read failed")
	case "blocked-read":
		if p.readStarted != nil {
			p.readStarted <- struct{}{}
		}
		<-ctx.Done()
		return provider.Plan{}, ctx.Err()
	case "missing-proof":
		plan.Operations = nil
	case "wrong-spec":
		plan.SpecHash = "different-spec"
	case "wrong-target":
		plan.Target = "different-target"
	case "wrong-app":
		plan.App.App = "different-app"
	case "duplicate-conflict":
		plan.Operations = append(plan.Operations, provider.Operation{Service: "api", Kind: provider.OpUpdate})
	case "unknown-operation":
		plan.Operations = append(plan.Operations, provider.Operation{Service: "api", Kind: provider.OperationKind("unknown")})
	case "duplicate-noop":
		plan.Operations = append(plan.Operations, provider.Operation{Service: "api", Kind: provider.OpNoop})
	}
	return plan, nil
}

func (p *convergenceBoundaryProvider) Observe(ctx context.Context, target provider.Target, app provider.AppRef) (provider.LiveState, error) {
	live, err := p.Provider.Observe(ctx, target, app)
	if err != nil || !p.active {
		return live, err
	}
	switch p.mode {
	case "observe-read-error":
		return provider.LiveState{}, errors.New("synthetic observation read failed")
	case "missing-live":
		delete(live.Services, "api")
	case "degraded-live":
		state := live.Services["api"]
		state.Health = provider.Degraded
		state.Message = "synthetic live probe failed"
		live.Services["api"] = state
	}
	return live, nil
}
