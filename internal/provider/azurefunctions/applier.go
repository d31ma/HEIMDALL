package azurefunctions

import (
	"context"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/sequence"
)

type applier struct {
	provider *Provider
	context  context.Context
	target   provider.Target
	plan     provider.Plan
	options  ApplyOptions
	config   targetConfig
	client   *armClient
	value    provider.Result
}

func (p applier) apply(ctx context.Context, target provider.Target, plan provider.Plan) (provider.Result, error) {
	p.context, p.target, p.plan = ctx, target, plan
	err := (sequence.Steps{Actions: []func() error{p.readOptions, p.validateWorkload, p.validatePlan, p.parseTarget, p.createClient, p.deploy}}).Run()
	return p.result(err)
}
func (p *applier) readOptions() error {
	options, ok := p.context.Value(applyKey{}).(ApplyOptions)
	p.options = options
	return p.requireOptions(ok)
}
func (applier) requireOptions(ok bool) error {
	if !ok {
		return &AdapterError{Code: (functionContract{}).planCode(), Message: "apply requires ApplyOptions.WithContext"}
	}
	return nil
}
func (p *applier) validateWorkload() error { return (workloadValidator{}).validate(p.options.Spec) }
func (p *applier) validatePlan() error {
	return (planIdentity{}).validate(p.target, p.plan, p.options.Spec)
}
func (p *applier) parseTarget() error {
	config, err := (targetConfig{}).parse(p.target)
	p.config = config
	return err
}
func (p *applier) createClient() error {
	client, err := (clientFactory{provider: p.provider}).create(p.config)
	p.client = client
	return err
}
func (p *applier) deploy() error {
	value, err := (functionDeployment{client: p.client}).apply(p.context, p.plan, p.options)
	p.value = value
	return err
}
func (p applier) result(err error) (provider.Result, error) { return p.value, err }
