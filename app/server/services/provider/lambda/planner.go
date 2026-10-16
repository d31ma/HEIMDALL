package lambda

import (
	"context"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type planner struct {
	adapter *Provider
	target  provider.Target
	want    spec.DeploySpec
	client  *awslambda.Client
	plan    provider.Plan
	live    map[string]FunctionState
	desired map[string]bool
}

func (p *planner) Plan(ctx context.Context) (provider.Plan, error) {
	p.desired = map[string]bool{}
	err := (sequence.Steps{Actions: []func() error{p.validate, p.prepare, func() error { return p.connect(ctx) }, func() error { return p.inventory(ctx) }, func() error { return p.services(ctx) }, p.prune, p.order}}).Run()
	return p.result(err)
}
func (p *planner) validate() error {
	return (RuntimeContract{Target: p.target, Spec: p.want}).Validate()
}
func (p *planner) prepare() error {
	hash, err := spec.Hash(p.want)
	p.plan = provider.Plan{Target: p.target.ID, App: provider.AppRef{Project: p.project(), App: p.want.App}, Revision: p.want.Revision, SpecHash: hash}
	return err
}
func (p *planner) project() string {
	if p.target.Project == "" {
		return (ownershipLabels{}).DefaultProject()
	}
	return p.target.Project
}
func (p *planner) connect(ctx context.Context) error {
	client, err := p.adapter.client(ctx, p.target)
	p.client = client
	return err
}
func (p *planner) inventory(ctx context.Context) error {
	live, err := p.adapter.functions(ctx, p.client, p.plan.App)
	p.live = live
	return err
}
func (p *planner) services(ctx context.Context) error {
	var err error
	for index := 0; index < len(p.want.Services) && err == nil; index++ {
		service := p.want.Services[index]
		p.desired[service.Name] = true
		err = (&servicePlan{planner: p, service: service}).Build(ctx)
	}
	return err
}
func (p *planner) prune() error {
	for name := range p.live {
		p.plan.Operations = append(p.plan.Operations, p.removal(name)...)
	}
	return nil
}
func (p *planner) removal(name string) []provider.Operation {
	if p.desired[name] {
		return nil
	}
	return []provider.Operation{{Kind: provider.OpDelete, Service: name, Wave: (functionLimits{}).PruneWave(), Prune: true, Reason: "function removed from desired state"}}
}
func (p *planner) order() error { (operationOrder{}).Sort(p.plan.Operations); return nil }
func (p *planner) result(err error) (provider.Plan, error) {
	if err != nil {
		return provider.Plan{}, err
	}
	return p.plan, nil
}
