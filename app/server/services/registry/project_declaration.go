package registry

import (
	"github.com/d31ma/heimdall/app/server/repositories/store"
	"slices"
)

type projectDeclaration struct {
	engine      *Engine
	collection  *registryCollection[store.Project]
	declaration ProjectDecl
	key         string
	existing    store.Project
	found       bool
}

func (declaration *projectDeclaration) Apply() error {
	declaration.key = declaration.declaration.Name
	declaration.existing, declaration.found = declaration.collection.Mark(declaration.key)
	return (&registryChange{found: declaration.found, changed: declaration.changed(), create: declaration.create, update: declaration.patch}).Apply()
}

func (declaration *projectDeclaration) create() error {
	wanted := store.Project{
		Name:        declaration.declaration.Name,
		Description: declaration.declaration.Description,
		ManagedBy:   store.ManagedByRegistry,
		CreatedAt:   declaration.engine.now(),
	}
	id, err := declaration.collection.Create(wanted, declaration.key)
	declaration.remember(id, err)
	return err
}

func (declaration *projectDeclaration) changed() bool {
	return slices.Contains([]bool{
		declaration.existing.Description != declaration.declaration.Description,
		declaration.existing.ManagedBy != store.ManagedByRegistry,
	}, true)
}

func (declaration *projectDeclaration) patch() error {
	return declaration.collection.Update(declaration.existing.ID, map[string]any{
		"description": declaration.declaration.Description,
		"managed_by":  store.ManagedByRegistry,
	}, declaration.key)
}

func (declaration *projectDeclaration) remember(id string, err error) {
	if err != nil {
		return
	}
	declaration.collection.documents[declaration.key] = store.Project{
		ID:        id,
		Name:      declaration.declaration.Name,
		ManagedBy: store.ManagedByRegistry,
	}
}
