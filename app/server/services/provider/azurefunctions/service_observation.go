package azurefunctions

import (
	"context"

	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type serviceObservation struct {
	client    *armClient
	reference provider.AppRef
	deployed  *nativeApp
	state     *provider.LiveState
	context   context.Context
	service   string
	revision  *nativeRevision
}

func (o serviceObservation) read(ctx context.Context) error {
	o.context = ctx
	o.service = o.deployed.tagValue((resourceIdentity{}).serviceTag())
	return (sequence.Steps{Actions: []func() error{o.identity, o.readRevision, o.record}}).Run()
}
func (o *serviceObservation) identity() error {
	_, duplicate := o.state.Services[o.service]
	name := (optionalValue[string]{}).read(o.deployed.Name)
	expected := (resourceIdentity{}).name(o.reference, o.service)
	return (ownedFunctions{}).requireUnique(duplicate, name, expected)
}
func (o *serviceObservation) readRevision() error {
	revision, err := (revisionReadback{client: o.client}).inspect(o.context, o.deployed)
	o.revision = revision
	return err
}
func (o *serviceObservation) record() error {
	image := (revisionImage{}).read(o.revision)
	health := (revisionReadback{client: o.client}).compare(o.deployed, *o.deployed, o.revision)
	first := len(o.state.Services) == 0
	identity := resourceIdentity{}
	labels := observationLabels{}
	o.state.Revision = labels.merge(o.state.Revision, o.deployed.tagValue(identity.revisionTag()), first)
	o.state.SpecHash = labels.merge(o.state.SpecHash, o.deployed.tagValue(identity.specHashTag()), first)
	o.state.Services[o.service] = provider.ServiceState{Health: health, Image: image, FunctionVersion: (revisionImage{}).version(o.revision), Message: (revisionImage{}).message(o.revision)}
	return nil
}
