package registry

import (
	"github.com/d31ma/heimdall/app/server/repositories/store"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type registryDocuments struct {
	engine   *Engine
	binding  store.RootBinding
	manifest Manifest
	result   Result
}

func (documents *registryDocuments) Apply() (Result, error) {
	projects := &registryCollection[store.Project]{
		collection: store.In[store.Project](documents.engine.Store, store.Projects),
		result:     &documents.result, prefix: "project:", prune: documents.binding.Prune,
	}
	repositories := &registryCollection[store.Repository]{
		collection: store.In[store.Repository](documents.engine.Store, store.Repos),
		result:     &documents.result, prefix: "repository:", prune: documents.binding.Prune,
	}
	targets := &registryCollection[store.Target]{
		collection: store.In[store.Target](documents.engine.Store, store.Targets),
		result:     &documents.result, prefix: "target:", prune: documents.binding.Prune,
	}
	applications := &registryCollection[store.Application]{
		collection: store.In[store.Application](documents.engine.Store, store.Applications),
		result:     &documents.result, prefix: "application:", prune: documents.binding.Prune,
	}
	projectPass := &registryDeclarations[ProjectDecl, store.Project]{
		collection: projects, declarations: documents.manifest.Projects,
		key: func(project store.Project) string { return project.Name },
		write: func(declaration ProjectDecl) error {
			return (&projectDeclaration{engine: documents.engine, collection: projects, declaration: declaration}).Apply()
		},
	}
	repositoryPass := &registryDeclarations[RepoDecl, store.Repository]{
		collection: repositories, declarations: documents.manifest.Repositories,
		key: func(repository store.Repository) string { return repositories.Key(repository.Project, repository.Name) },
		write: func(declaration RepoDecl) error {
			return (&repositoryDeclaration{engine: documents.engine, collection: repositories, declaration: declaration}).Apply()
		},
	}
	targetPass := &registryDeclarations[TargetDecl, store.Target]{
		collection: targets, declarations: documents.manifest.Targets,
		key: func(target store.Target) string { return targets.Key(target.Project, target.Name) },
		write: func(declaration TargetDecl) error {
			return (&targetDeclaration{engine: documents.engine, collection: targets, declaration: declaration}).Apply()
		},
	}
	applicationPass := &registryDeclarations[AppDecl, store.Application]{
		collection: applications, declarations: documents.manifest.Applications,
		key: func(application store.Application) string {
			return applications.Key(application.Project, application.Name)
		},
		write: func(declaration AppDecl) error {
			return (&applicationDeclaration{
				engine: documents.engine, collection: applications,
				repositories: repositories, targets: targets, declaration: declaration,
			}).Apply()
		},
	}
	err := (sequence.Steps{Actions: []func() error{
		projectPass.Apply, repositoryPass.Apply, targetPass.Apply, applicationPass.Apply,
		func() error {
			return applications.Prune(
				func(application store.Application) string { return application.ID },
				func(application store.Application) bool { return application.ManagedBy == store.ManagedByRegistry },
			)
		},
		func() error {
			return targets.Prune(
				func(target store.Target) string { return target.ID },
				func(target store.Target) bool { return target.ManagedBy == store.ManagedByRegistry },
			)
		},
		func() error {
			return repositories.Prune(
				func(repository store.Repository) string { return repository.ID },
				func(repository store.Repository) bool { return repository.ManagedBy == store.ManagedByRegistry },
			)
		},
		func() error {
			return projects.Prune(
				func(project store.Project) string { return project.ID },
				func(project store.Project) bool { return project.ManagedBy == store.ManagedByRegistry },
			)
		},
	}}).Run()
	return documents.result, err
}
