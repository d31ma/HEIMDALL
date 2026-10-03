package azurefunctions

import (
	"context"
	"time"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/sequence"
)

type functionObservation struct {
	client  *armClient
	context context.Context
	target  string
	app     provider.AppRef
	apps    []*nativeApp
	state   provider.LiveState
	err     error
}

func (o functionObservation) read(ctx context.Context) (provider.LiveState, error) {
	o.context = ctx
	o.state = provider.LiveState{App: o.app, Target: o.target, ReadAt: time.Now().UTC(), Services: map[string]provider.ServiceState{}}
	err := (sequence.Steps{Actions: []func() error{o.list, o.services}}).Run()
	return o.result(err)
}
func (o *functionObservation) list() error {
	apps, err := o.client.list(o.context)
	o.apps = apps
	return err
}
func (o *functionObservation) services() error {
	for index := 0; index < len(o.apps) && o.err == nil; index++ {
		o.err = o.service(o.apps[index])
	}
	return o.err
}
func (o *functionObservation) service(app *nativeApp) error {
	owned := (resourceIdentity{}).owned(app, o.app)
	return o.observeOwned(app, owned)
}
func (o *functionObservation) observeOwned(app *nativeApp, owned bool) error {
	if !owned {
		return nil
	}
	return o.observeService(app)
}
func (o *functionObservation) observeService(app *nativeApp) error {
	observation := serviceObservation{client: o.client, reference: o.app, deployed: app, state: &o.state}
	return observation.read(o.context)
}
func (o functionObservation) result(err error) (provider.LiveState, error) {
	if err != nil {
		return provider.LiveState{}, err
	}
	return o.state, nil
}
