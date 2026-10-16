package lambda

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
	"time"
)

type observer struct {
	adapter   *Provider
	target    provider.Target
	app       provider.AppRef
	client    *awslambda.Client
	functions map[string]FunctionState
	state     provider.LiveState
}

func (o *observer) Observe(ctx context.Context) (provider.LiveState, error) {
	o.state = provider.LiveState{App: o.app, Target: o.target.ID, ReadAt: time.Now().UTC(), Services: map[string]provider.ServiceState{}}
	err := (sequence.Steps{Actions: []func() error{func() error { return o.connect(ctx) }, func() error { return o.inventory(ctx) }, o.services}}).Run()
	return o.result(err)
}
func (o *observer) connect(ctx context.Context) error {
	client, err := o.adapter.client(ctx, o.target)
	o.client = client
	return err
}
func (o *observer) inventory(ctx context.Context) error {
	functions, err := o.adapter.functions(ctx, o.client, o.app)
	o.functions = functions
	return err
}
func (o *observer) services() error {
	for name, function := range o.functions {
		o.state.Services[name] = provider.ServiceState{Health: function.Health(), Image: o.image(o.deployed(function)), FunctionVersion: o.version(function), Message: "deployment readiness; execution environment inventory is not exposed by Lambda"}
		o.state.Revision = o.merge(o.state.Revision, function.Live.Tags[(ownershipLabels{}).Revision()])
		o.state.SpecHash = o.merge(o.state.SpecHash, function.Live.Tags[(ownershipLabels{}).SpecHash()])
	}
	return nil
}
func (observer) deployed(function FunctionState) *awslambda.GetFunctionOutput {
	if function.Published != nil {
		return function.Published
	}
	return function.Live
}
func (observer) image(function *awslambda.GetFunctionOutput) string {
	if function == nil || function.Code == nil {
		return ""
	}
	return aws.ToString(function.Code.ResolvedImageUri)
}
func (observer) version(function FunctionState) string {
	if function.Published == nil || function.Published.Configuration == nil {
		return ""
	}
	return aws.ToString(function.Published.Configuration.Version)
}
func (observer) merge(current, next string) string {
	switch {
	case current == "":
		return next
	case current != next:
		return (ownershipLabels{}).Mixed()
	}
	return current
}
func (o *observer) result(err error) (provider.LiveState, error) {
	if err != nil {
		return provider.LiveState{}, err
	}
	return o.state, nil
}
