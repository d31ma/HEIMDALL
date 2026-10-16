package lambda

import (
	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type RuntimeContract struct {
	Target provider.Target
	Spec   spec.DeploySpec
}

func (c RuntimeContract) Validate() error {
	return (sequence.Steps{Actions: []func() error{c.capabilities, c.declarations, c.target, c.services}}).Run()
}
func (c RuntimeContract) capabilities() error {
	return provider.Validate((&Provider{}).Capabilities(), c.Spec)
}
func (c RuntimeContract) target() error { return (TargetContract{Target: c.Target}).Validate() }
func (c RuntimeContract) declarations() error {
	var err error
	for index := 0; index < len(c.Spec.Services) && err == nil; index++ {
		err = c.declaration(c.Spec.Services[index])
	}
	return err
}
func (c RuntimeContract) declaration(service spec.Service) error {
	if service.Function == nil {
		return (serviceContract{service: service}).reject(provider.FeatureFunction, "declare lambda-runtime-api with direct json invocation")
	}
	return nil
}
func (c RuntimeContract) services() error {
	var err error
	for index := 0; index < len(c.Spec.Services) && err == nil; index++ {
		err = (serviceContract{target: c.Target, service: c.Spec.Services[index]}).Validate()
	}
	return err
}
