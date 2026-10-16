package azurefunctions

import (
	"context"

	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type servicePlan struct {
	client    *armClient
	plan      provider.Plan
	service   spec.Service
	existing  *nativeApp
	context   context.Context
	expected  nativeApp
	collision *nativeApp
	health    provider.Health
}

func (p servicePlan) read(ctx context.Context) (provider.Operation, error) {
	p.context = ctx
	err := (sequence.Steps{Actions: []func() error{p.define, p.inspect}}).Run()
	return p.result(err)
}
func (p *servicePlan) define() error {
	expected, err := (functionDefinition{config: p.client.config}).build(p.plan, p.service)
	p.expected = expected
	return err
}
func (p *servicePlan) inspect() error {
	if p.existing == nil {
		return p.checkAbsent()
	}
	return p.checkPresent()
}
func (p *servicePlan) checkAbsent() error {
	return (sequence.Steps{Actions: []func() error{p.readCollision, p.requireAbsent}}).Run()
}
func (p *servicePlan) readCollision() error {
	collision, err := p.client.get(p.context, (resourceIdentity{}).name(p.plan.App, p.service.Name))
	p.collision = collision
	return err
}
func (p *servicePlan) requireAbsent() error {
	if p.collision != nil {
		return &AdapterError{Code: (functionContract{}).ownershipCode(), Message: "function name collides with a resource outside this application's ownership"}
	}
	return nil
}
func (p *servicePlan) checkPresent() error {
	health, err := (revisionReadback{client: p.client}).health(p.context, p.existing, p.expected)
	p.health = health
	return err
}
func (p servicePlan) result(err error) (provider.Operation, error) { return p.operation(), err }
func (p servicePlan) operation() provider.Operation {
	operation := provider.Operation{Kind: provider.OpNoop, Service: p.service.Name, Wave: p.service.Wave}
	switch {
	case p.existing == nil:
		operation.Kind, operation.Reason = provider.OpCreate, "native function is absent"
	case p.health != provider.Healthy:
		operation.Kind, operation.Reason = provider.OpUpdate, "actual function configuration or ready revision differs"
	}
	return operation
}
