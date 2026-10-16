package main

import (
	"context"
	"path/filepath"

	hdsecrets "github.com/d31ma/heimdall/app/server/helpers/secrets"
	"github.com/d31ma/heimdall/app/server/services/dispatch"
	"github.com/d31ma/heimdall/app/server/services/reconcile"
	hdregistry "github.com/d31ma/heimdall/app/server/services/registry"
)

type serveReconciliation struct {
	application *serveApplication
	dispatcher  *dispatch.Dispatcher
	engine      *reconcile.Engine
	registry    *hdregistry.Engine
	auto        *reconcile.Auto
}

func (r *serveReconciliation) Prepare() {
	storage := r.application.authority.storage
	r.dispatcher = dispatch.New(0)
	resolver := &hdsecrets.Resolver{Deployment: storage.settings.deployment}
	secrets := resolver.Resolve
	adapters, applyContexts := providers(secrets)
	r.engine = &reconcile.Engine{
		Store:        storage.writer,
		Providers:    adapters,
		ApplyContext: applyContexts,
		Secrets:      secrets,
		Dispatcher:   r.dispatcher,
		CacheDir:     r.cacheDirectory(),
	}
}

func (r *serveReconciliation) Start(runContext context.Context) {
	storage := r.application.authority.storage
	r.registry = &hdregistry.Engine{
		Store: storage.writer, CacheDir: r.cacheDirectory(), Logger: storage.logger, Providers: r.engine.Providers,
	}
	r.auto = &reconcile.Auto{
		Engine: r.engine, Interval: autoInterval(), Logger: storage.logger,
		RegistrySync: r.registry.SyncIfBound,
	}
	go r.auto.Run(runContext)
}

func (r *serveReconciliation) cacheDirectory() string {
	return filepath.Join(r.application.authority.storage.settings.deployment, "git")
}
