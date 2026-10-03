package reconcile

import (
	"context"
	"fmt"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/sequence"
	"github.com/d31ma/heimdall/internal/spec"
	"github.com/d31ma/heimdall/internal/store"
)

type functionInvocation struct {
	engine   *Engine
	request  ServiceInvocation
	resolved resolved
	revision store.Revision
	service  spec.Service
	invoker  provider.Invoker
	result   provider.Invocation
}

func (i *functionInvocation) Invoke(ctx context.Context) (provider.Invocation, error) {
	err := (sequence.Steps{Actions: []func() error{i.resolve, i.active, i.revisionFor, i.declared, i.desired, i.compatible, func() error { return i.invoke(ctx) }}}).Run()
	return i.response(err)
}
func (i *functionInvocation) resolve() error {
	resolved, err := i.engine.resolve(i.request.AppID)
	i.resolved = resolved
	return err
}
func (i *functionInvocation) active() error {
	if i.resolved.app.Suspended {
		return &InvocationError{Code: (InvocationError{}).SuspendedCode(), Message: fmt.Sprintf("application %s is suspended", i.resolved.app.Name)}
	}
	return nil
}
func (i *functionInvocation) revisionFor() error {
	revision, err := i.engine.latestRevision(i.resolved.app.ID)
	i.revision = revision
	return err
}
func (i *functionInvocation) declared() error {
	service, found := i.revision.Spec.Service(i.request.Service)
	i.service = service
	return i.requireFunction(found)
}
func (i *functionInvocation) requireFunction(found bool) error {
	if !found || i.service.Function == nil {
		return &InvocationError{Code: (InvocationError{}).ServiceCode(), Message: "the named service is not a declared function at the stored revision"}
	}
	return nil
}
func (i *functionInvocation) compatible() error {
	invoker, available := i.resolved.adapter.(provider.Invoker)
	i.invoker = invoker
	return i.requireInvoker(available)
}
func (i *functionInvocation) requireInvoker(available bool) error {
	if !available {
		return &InvocationError{Code: (InvocationError{}).UnsupportedCode(), Message: fmt.Sprintf("the %s adapter does not support direct function invocation", i.resolved.target.Provider)}
	}
	return nil
}
func (i *functionInvocation) invoke(ctx context.Context) error {
	result, err := i.invoker.Invoke(ctx, provider.InvocationRequest{Target: i.resolved.target.Ref(), App: i.resolved.app.AppRef(), Service: i.request.Service, Payload: i.request.Payload})
	i.result = result
	return err
}
func (i *functionInvocation) response(err error) (provider.Invocation, error) {
	if err != nil {
		return provider.Invocation{}, err
	}
	return i.result, nil
}

func (i *functionInvocation) desired() error {
	return (provider.DesiredCheck{Adapter: i.resolved.adapter, Target: i.resolved.target.Ref(), Spec: i.revision.Spec}).Validate()
}
