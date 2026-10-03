package registry

import (
	"fmt"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/sequence"
	"github.com/d31ma/heimdall/internal/store"
)

type targetDefinitions struct {
	providers map[string]provider.Provider
	targets   []TargetDecl
	storage   *store.Store
	existing  map[string]store.Target
}

func (d *targetDefinitions) Validate() error {
	steps := d.steps()
	return (sequence.Steps{Actions: steps}).Run()
}
func (d *targetDefinitions) steps() []func() error {
	if d.providers == nil {
		return nil
	}
	return []func() error{d.readExisting, d.targetsValid}
}
func (d *targetDefinitions) targetsValid() error {
	var err error
	for index := 0; index < len(d.targets) && err == nil; index++ {
		err = d.target(d.targets[index])
	}
	return err
}
func (d targetDefinitions) target(declaration TargetDecl) error {
	adapter, known := d.providers[declaration.Provider]
	existing := d.existing[declaration.Project+"/"+declaration.Name]
	target := provider.Target{ID: existing.ID, AgentID: existing.AgentID, Project: declaration.Project, Provider: declaration.Provider, Region: declaration.Region, Endpoint: declaration.Endpoint, CredentialRef: declaration.CredentialRef, Config: declaration.Config}
	return (sequence.Steps{Actions: []func() error{func() error { return d.known(known, declaration.Provider) }, func() error { return (provider.TargetCheck{Adapter: adapter, Target: target}).Definition() }}}).Run()
}
func (targetDefinitions) known(known bool, name string) error {
	if !known {
		return fmt.Errorf("HD0272: registry target names unregistered provider %q", name)
	}
	return nil
}

func (d *targetDefinitions) readExisting() error {
	targets, err := store.In[store.Target](d.storage, store.Targets).Find(nil)
	d.index(targets)
	return err
}
func (d *targetDefinitions) index(targets []store.Target) {
	d.existing = map[string]store.Target{}
	for _, target := range targets {
		d.existing[target.Project+"/"+target.Name] = target
	}
}
