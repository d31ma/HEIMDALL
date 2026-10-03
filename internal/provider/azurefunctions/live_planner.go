package azurefunctions

import (
	"context"
	"sort"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/sequence"
	"github.com/d31ma/heimdall/internal/spec"
)

type livePlanner struct {
	client  *armClient
	context context.Context
	target  provider.Target
	want    spec.DeploySpec
	value   provider.Plan
	apps    []*nativeApp
	live    map[string]*nativeApp
	desired map[string]bool
}

func (p livePlanner) plan(ctx context.Context, target provider.Target, want spec.DeploySpec) (provider.Plan, error) {
	p.context, p.target, p.want = ctx, target, want
	p.desired = map[string]bool{}
	err := (sequence.Steps{Actions: []func() error{p.hash, p.list, p.index, p.services, p.prune, p.order}}).Run()
	return p.result(err)
}
func (p *livePlanner) hash() error {
	hash, err := spec.Hash(p.want)
	p.value = provider.Plan{Target: p.target.ID, App: provider.AppRef{Project: (resourceIdentity{}).project(p.target.Project), App: p.want.App}, Revision: p.want.Revision, SpecHash: hash}
	return err
}
func (p *livePlanner) list() error { apps, err := p.client.list(p.context); p.apps = apps; return err }
func (p *livePlanner) index() error {
	live, err := (ownedFunctions{}).read(p.apps, p.value.App)
	p.live = live
	return err
}
func (p *livePlanner) services() error {
	var err error
	for index := 0; index < len(p.want.Services) && err == nil; index++ {
		err = p.service(p.want.Services[index])
	}
	return err
}
func (p *livePlanner) service(service spec.Service) error {
	p.desired[service.Name] = true
	planning := servicePlan{client: p.client, plan: p.value, service: service, existing: p.live[service.Name]}
	operation, err := planning.read(p.context)
	p.appendOperation(operation, err)
	return err
}
func (p *livePlanner) appendOperation(operation provider.Operation, err error) {
	if err == nil {
		p.value.Operations = append(p.value.Operations, operation)
	}
}
func (p *livePlanner) prune() error {
	pruning := prunePlan{desired: p.desired}
	p.value.Operations = append(p.value.Operations, pruning.operations(p.live)...)
	return nil
}
func (p *livePlanner) order() error {
	sort.Slice(p.value.Operations, (operationOrder{operations: p.value.Operations}).less)
	return nil
}
func (p livePlanner) result(err error) (provider.Plan, error) {
	if err != nil {
		return provider.Plan{}, err
	}
	return p.value, nil
}
