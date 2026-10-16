package reconcile

import (
	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
)

type selectivePlan struct {
	plan       provider.Plan
	want       spec.DeploySpec
	selective  bool
	names      map[string]bool
	operations []provider.Operation
}

func (p *selectivePlan) Restrict() provider.Plan {
	p.names = p.serviceNames()
	p.restrictSelected()
	return p.plan
}

func (p *selectivePlan) serviceNames() map[string]bool {
	names := make(map[string]bool, len(p.want.Services))
	for _, service := range p.want.Services {
		names[service.Name] = true
	}
	return names
}

func (p *selectivePlan) restrictSelected() {
	if p.selective {
		p.plan.Operations = p.selectedOperations()
	}
}

func (p *selectivePlan) selectedOperations() []provider.Operation {
	p.operations = make([]provider.Operation, 0, len(p.plan.Operations))
	for _, operation := range p.plan.Operations {
		p.IncludeOperation(operation)
	}
	return p.operations
}

func (p *selectivePlan) IncludeOperation(operation provider.Operation) {
	p.includeOperation(operation)
}

func (p *selectivePlan) includeOperation(operation provider.Operation) {
	if p.names[operation.Service] {
		p.operations = append(p.operations, operation)
	}
}
