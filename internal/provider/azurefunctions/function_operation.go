package azurefunctions

import (
	"context"
	"fmt"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/sequence"
	"github.com/d31ma/heimdall/internal/spec"
)

type functionOperation struct {
	client    *armClient
	context   context.Context
	plan      provider.Plan
	operation provider.Operation
	want      spec.DeploySpec
	name      string
	live      *nativeApp
	owned     bool
	service   string
}

func (o *functionOperation) run() error {
	o.name = (resourceIdentity{}).name(o.plan.App, o.operation.Service)
	return (sequence.Steps{Actions: []func() error{o.read, o.checkOwnership, o.mutate}}).Run()
}
func (o *functionOperation) read() error {
	live, err := o.client.get(o.context, o.name)
	o.live = live
	return err
}
func (o *functionOperation) checkOwnership() error {
	o.owned = (resourceIdentity{}).owned(o.live, o.plan.App)
	value := (appValues{}).read(o.live)
	o.service = value.tagValue((resourceIdentity{}).serviceTag())
	return o.requireOwnership()
}
func (o functionOperation) requireOwnership() error {
	if o.live != nil && (!o.owned || o.service != o.operation.Service) {
		return &AdapterError{Code: (functionContract{}).ownershipCode(), Message: "refuse mutation of a function outside this application's ownership"}
	}
	return nil
}
func (o *functionOperation) mutate() error {
	switch o.operation.Kind {
	case provider.OpDelete:
		return (functionRemoval{client: o.client, name: o.name, live: o.live}).remove(o.context)
	case provider.OpCreate, provider.OpUpdate:
		return (functionChange{client: o.client, name: o.name, plan: o.plan, want: o.want, serviceName: o.operation.Service}).apply(o.context)
	default:
		return &AdapterError{Code: (functionContract{}).planCode(), Message: fmt.Sprintf("unsupported function operation %q", o.operation.Kind)}
	}
}
