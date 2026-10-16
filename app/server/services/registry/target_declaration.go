package registry

import (
	"github.com/d31ma/heimdall/app/server/repositories/store"
	"maps"
	"slices"
)

type targetDeclaration struct {
	engine      *Engine
	collection  *registryCollection[store.Target]
	declaration TargetDecl
	key         string
	existing    store.Target
	found       bool
}

func (declaration *targetDeclaration) Apply() error {
	declaration.key = declaration.collection.Key(declaration.declaration.Project, declaration.declaration.Name)
	declaration.existing, declaration.found = declaration.collection.Mark(declaration.key)
	return (&registryChange{found: declaration.found, changed: declaration.changed(), create: declaration.create, update: declaration.patch}).Apply()
}

func (declaration *targetDeclaration) create() error {
	wanted := store.Target{
		Project:       declaration.declaration.Project,
		Name:          declaration.declaration.Name,
		Provider:      declaration.declaration.Provider,
		Region:        declaration.declaration.Region,
		Endpoint:      declaration.declaration.Endpoint,
		CredentialRef: declaration.declaration.CredentialRef,
		Tags:          declaration.declaration.Tags,
		Config:        declaration.declaration.Config,
		ManagedBy:     store.ManagedByRegistry,
		CreatedAt:     declaration.engine.now(),
	}
	id, err := declaration.collection.Create(wanted, declaration.key)
	declaration.remember(id, err)
	return err
}

func (declaration *targetDeclaration) changed() bool {
	return slices.Contains([]bool{
		declaration.existing.Provider != declaration.declaration.Provider,
		declaration.existing.Region != declaration.declaration.Region,
		declaration.existing.Endpoint != declaration.declaration.Endpoint,
		declaration.existing.CredentialRef != declaration.declaration.CredentialRef,
		!maps.Equal(declaration.existing.Config,
			declaration.declaration.Config),
		!maps.Equal(declaration.existing.Tags,
			declaration.declaration.Tags),
		declaration.existing.ManagedBy != store.ManagedByRegistry,
	}, true)
}

func (declaration *targetDeclaration) patch() error {
	return declaration.collection.Update(declaration.existing.ID, map[string]any{
		"provider":       declaration.declaration.Provider,
		"region":         declaration.declaration.Region,
		"endpoint":       declaration.declaration.Endpoint,
		"credential_ref": declaration.declaration.CredentialRef,
		"config":         declaration.declaration.Config,
		"tags":           declaration.declaration.Tags,
		"managed_by":     store.ManagedByRegistry,
	}, declaration.key)
}

func (declaration *targetDeclaration) remember(id string, err error) {
	if err != nil {
		return
	}
	declaration.collection.documents[declaration.key] = store.Target{
		ID:      id,
		Project: declaration.declaration.Project,
		Name:    declaration.declaration.Name,
	}
}
