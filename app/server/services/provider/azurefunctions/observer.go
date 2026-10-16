package azurefunctions

import (
	"context"

	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type observer struct {
	provider *Provider
	client   *armClient
	context  context.Context
	target   provider.Target
	app      provider.AppRef
	config   targetConfig
	value    provider.LiveState
}

func (o observer) observe(ctx context.Context, target provider.Target, app provider.AppRef) (provider.LiveState, error) {
	o.context, o.target, o.app = ctx, target, app
	err := (sequence.Steps{Actions: []func() error{o.parseTarget, o.createClient, o.readState}}).Run()
	return o.result(err)
}
func (o *observer) parseTarget() error {
	config, err := (targetConfig{}).parse(o.target)
	o.config = config
	return err
}
func (o *observer) createClient() error {
	client, err := (clientFactory{provider: o.provider}).create(o.config)
	o.client = client
	return err
}
func (o *observer) readState() error {
	value, err := o.read(o.context, o.target.ID, o.app)
	o.value = value
	return err
}
func (o observer) result(err error) (provider.LiveState, error) {
	if err != nil {
		return provider.LiveState{}, err
	}
	return o.value, nil
}
func (o observer) read(ctx context.Context, target string, app provider.AppRef) (provider.LiveState, error) {
	observation := functionObservation{client: o.client, target: target, app: app}
	return observation.read(ctx)
}
func (o observer) instances(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Instance, error) {
	_, err := o.observe(ctx, target, app)
	return o.instanceResult(err)
}
func (observer) instanceResult(err error) ([]provider.Instance, error) {
	if err != nil {
		return nil, err
	}
	return []provider.Instance{}, nil
}
func (o observer) events(ctx context.Context, target provider.Target, app provider.AppRef) ([]provider.Event, error) {
	state, err := o.observe(ctx, target, app)
	return o.eventResult(state, err)
}
func (observer) eventResult(state provider.LiveState, err error) ([]provider.Event, error) {
	if err != nil {
		return nil, err
	}
	return []provider.Event{{At: state.ReadAt, Type: "deployment-observation", Message: "native deployment health " + string(state.Rollup()), Source: (functionContract{}).providerName()}}, nil
}
