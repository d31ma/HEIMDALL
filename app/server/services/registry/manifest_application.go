package registry

import (
	"github.com/d31ma/heimdall/app/server/repositories/store"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type manifestApplication struct {
	engine   *Engine
	binding  store.RootBinding
	manifest Manifest
	result   Result
}

func (a *manifestApplication) Apply() (Result, error) {
	err := (sequence.Steps{Actions: []func() error{a.targets, a.documents}}).Run()
	return a.result, err
}
func (a *manifestApplication) targets() error {
	return (&targetDefinitions{providers: a.engine.Providers, targets: a.manifest.Targets, storage: a.engine.Store}).Validate()
}
func (a *manifestApplication) documents() error {
	result, err := a.engine.applyDocuments(a.binding, a.manifest)
	a.result = result
	return err
}
