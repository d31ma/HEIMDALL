package azurefunctions

import (
	"context"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/sequence"
	"github.com/d31ma/heimdall/internal/spec"
)

type planner struct {
	provider *Provider
	context  context.Context
	target   provider.Target
	want     spec.DeploySpec
	config   targetConfig
	client   *armClient
	value    provider.Plan
}

func (p planner) plan(ctx context.Context, target provider.Target, want spec.DeploySpec) (provider.Plan, error) {
	p.context, p.target, p.want = ctx, target, want
	err := (sequence.Steps{Actions: []func() error{p.validateWorkload, p.parseTarget, p.createClient, p.readPlan}}).Run()
	return p.result(err)
}
func (p planner) validateWorkload() error { return (workloadValidator{}).validate(p.want) }
func (p *planner) parseTarget() error {
	config, err := (targetConfig{}).parse(p.target)
	p.config = config
	return err
}
func (p *planner) createClient() error {
	client, err := (clientFactory{provider: p.provider}).create(p.config)
	p.client = client
	return err
}
func (p *planner) readPlan() error {
	value, err := (livePlanner{client: p.client}).plan(p.context, p.target, p.want)
	p.value = value
	return err
}
func (p planner) result(err error) (provider.Plan, error) {
	if err != nil {
		return provider.Plan{}, err
	}
	return p.value, nil
}
