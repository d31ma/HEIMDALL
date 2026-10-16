package registry

import (
	"fmt"
	"github.com/d31ma/heimdall/app/server/repositories/store"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
	"maps"
	"slices"
)

type applicationDeclaration struct {
	engine       *Engine
	collection   *registryCollection[store.Application]
	repositories *registryCollection[store.Repository]
	targets      *registryCollection[store.Target]
	declaration  AppDecl
	key          string
	existing     store.Application
	found        bool
	repository   store.Repository
	target       store.Target
	policy       store.SyncPolicy
}

func (declaration *applicationDeclaration) Apply() error {
	declaration.key = declaration.collection.Key(declaration.declaration.Project, declaration.declaration.Name)
	declaration.existing, declaration.found = declaration.collection.Mark(declaration.key)
	declaration.policy = store.SyncPolicy{
		Automated: declaration.declaration.SyncPolicy.Automated,
		SelfHeal:  declaration.declaration.SyncPolicy.SelfHeal,
		Prune:     declaration.declaration.SyncPolicy.Prune,
	}
	return (sequence.Steps{Actions: []func() error{declaration.resolveRepository, declaration.resolveTarget, declaration.write}}).Run()
}

func (declaration *applicationDeclaration) create() error {
	wanted := store.Application{
		Project:    declaration.declaration.Project,
		Name:       declaration.declaration.Name,
		RepoID:     declaration.repository.ID,
		TargetID:   declaration.target.ID,
		Path:       declaration.declaration.Path,
		Ref:        declaration.declaration.Ref,
		Overlays:   declaration.declaration.Overlays,
		Variables:  declaration.declaration.Variables,
		SyncPolicy: declaration.policy,
		Suspended:  declaration.declaration.Suspended,
		ManagedBy:  store.ManagedByRegistry,
		CreatedAt:  declaration.engine.now(),
	}
	_, err := declaration.collection.Create(wanted, declaration.key)
	return err
}

func (declaration *applicationDeclaration) resolveRepository() error {
	repository, found := declaration.repositories.documents[declaration.repositories.Key(declaration.declaration.Project, declaration.declaration.Repository)]
	declaration.repository = repository
	if !found {
		return fmt.Errorf("HD0270: application %q resolves no repository", declaration.declaration.Name)
	}
	return nil
}

func (declaration *applicationDeclaration) resolveTarget() error {
	target, found := declaration.targets.documents[declaration.targets.Key(declaration.declaration.Project, declaration.declaration.Target)]
	declaration.target = target
	if !found {
		return fmt.Errorf("HD0270: application %q resolves no target", declaration.declaration.Name)
	}
	return nil
}

func (declaration *applicationDeclaration) write() error {
	return (&registryChange{found: declaration.found, changed: declaration.changed(), create: declaration.create, update: declaration.patch}).Apply()
}

func (declaration *applicationDeclaration) changed() bool {
	return slices.Contains([]bool{
		declaration.existing.RepoID != declaration.repository.ID,
		declaration.existing.TargetID != declaration.target.ID,
		declaration.existing.Path != declaration.declaration.Path,
		declaration.existing.Ref != declaration.declaration.Ref,
		!slices.Equal(declaration.existing.Overlays,
			declaration.declaration.Overlays),
		!maps.Equal(declaration.existing.Variables,
			declaration.declaration.Variables),
		declaration.existing.Suspended != declaration.declaration.Suspended,
		declaration.existing.SyncPolicy != declaration.policy,
		declaration.existing.ManagedBy != store.ManagedByRegistry,
	}, true)
}

func (declaration *applicationDeclaration) patch() error {
	return declaration.collection.Update(declaration.existing.ID, map[string]any{
		"repo_id":     declaration.repository.ID,
		"target_id":   declaration.target.ID,
		"path":        declaration.declaration.Path,
		"ref":         declaration.declaration.Ref,
		"overlays":    declaration.declaration.Overlays,
		"variables":   declaration.declaration.Variables,
		"sync_policy": declaration.policy,
		"suspended":   declaration.declaration.Suspended,
		"managed_by":  store.ManagedByRegistry,
	}, declaration.key)
}
