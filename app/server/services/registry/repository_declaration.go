package registry

import (
	"github.com/d31ma/heimdall/app/server/repositories/store"
	"slices"
)

type repositoryDeclaration struct {
	engine      *Engine
	collection  *registryCollection[store.Repository]
	declaration RepoDecl
	key         string
	existing    store.Repository
	wanted      store.Repository
	found       bool
}

func (declaration *repositoryDeclaration) Apply() error {
	declaration.key = declaration.collection.Key(declaration.declaration.Project, declaration.declaration.Name)
	declaration.existing, declaration.found = declaration.collection.Mark(declaration.key)
	declaration.wanted = store.Repository{
		Project:          declaration.declaration.Project,
		Name:             declaration.declaration.Name,
		URL:              declaration.declaration.URL,
		DefaultRef:       declaration.declaration.DefaultRef,
		CredentialRef:    declaration.declaration.CredentialRef,
		RequireSignature: declaration.declaration.RequireSignature,
		WebhookSecretRef: declaration.declaration.WebhookSecretRef,
		ManagedBy:        store.ManagedByRegistry,
	}
	return (&registryChange{found: declaration.found, changed: declaration.changed(), create: declaration.create, update: declaration.patch}).Apply()
}

func (declaration *repositoryDeclaration) create() error {
	declaration.wanted.CreatedAt = declaration.engine.now()
	id, err := declaration.collection.Create(declaration.wanted, declaration.key)
	declaration.remember(id, err)
	return err
}

func (declaration *repositoryDeclaration) changed() bool {
	return slices.Contains([]bool{
		declaration.existing.URL != declaration.wanted.URL,
		declaration.existing.DefaultRef != declaration.wanted.DefaultRef,
		declaration.existing.CredentialRef != declaration.wanted.CredentialRef,
		declaration.existing.RequireSignature != declaration.wanted.RequireSignature,
		declaration.existing.WebhookSecretRef != declaration.wanted.WebhookSecretRef,
		declaration.existing.ManagedBy != store.ManagedByRegistry,
	}, true)
}

func (declaration *repositoryDeclaration) patch() error {
	err := declaration.collection.Update(declaration.existing.ID, map[string]any{
		"url":                declaration.wanted.URL,
		"default_ref":        declaration.wanted.DefaultRef,
		"credential_ref":     declaration.wanted.CredentialRef,
		"require_signature":  declaration.wanted.RequireSignature,
		"webhook_secret_ref": declaration.wanted.WebhookSecretRef,
		"managed_by":         store.ManagedByRegistry,
	}, declaration.key)
	declaration.remember(declaration.existing.ID, err)
	return err
}

func (declaration *repositoryDeclaration) remember(id string, err error) {
	if err != nil {
		return
	}
	declaration.wanted.ID = id
	declaration.collection.documents[declaration.key] = declaration.wanted
}
