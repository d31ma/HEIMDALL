package main

import (
	"context"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/services/provider/aca"
	"github.com/d31ma/heimdall/app/server/services/provider/azurefunctions"
	"github.com/d31ma/heimdall/app/server/services/provider/cloudrun"
	"github.com/d31ma/heimdall/app/server/services/provider/docker"
	"github.com/d31ma/heimdall/app/server/services/provider/ecs"
	"github.com/d31ma/heimdall/app/server/services/provider/lambda"
	"github.com/d31ma/heimdall/app/server/services/reconcile"
)

type providerRegistry struct {
	secrets func(context.Context, string) (string, error)
}

func (r providerRegistry) Build() (map[string]provider.Provider, map[string]func(context.Context, reconcile.ApplyParams) context.Context) {
	dockerAdapter := &docker.Provider{SecretResolver: r.secrets}
	swarmAdapter := &docker.Swarm{SecretResolver: r.secrets}
	ecsAdapter := &ecs.Provider{SecretResolver: r.secrets}
	runAdapter := &cloudrun.Provider{SecretResolver: r.secrets}
	acaAdapter := &aca.Provider{SecretResolver: r.secrets}
	lambdaAdapter := &lambda.Provider{SecretResolver: r.secrets}
	functionsAdapter := &azurefunctions.Provider{}
	adapters := map[string]provider.Provider{dockerAdapter.Name(): dockerAdapter, swarmAdapter.Name(): swarmAdapter, ecsAdapter.Name(): ecsAdapter, runAdapter.Name(): runAdapter, acaAdapter.Name(): acaAdapter, lambdaAdapter.Name(): lambdaAdapter, functionsAdapter.Name(): functionsAdapter}
	contexts := map[string]func(context.Context, reconcile.ApplyParams) context.Context{dockerAdapter.Name(): r.dockerContext, swarmAdapter.Name(): r.dockerContext, ecsAdapter.Name(): r.ecsContext, runAdapter.Name(): r.cloudRunContext, acaAdapter.Name(): r.acaContext, lambdaAdapter.Name(): r.lambdaContext, functionsAdapter.Name(): r.azureFunctionsContext}
	return adapters, contexts
}
func (providerRegistry) dockerContext(ctx context.Context, params reconcile.ApplyParams) context.Context {
	return docker.WithApply(ctx, docker.ApplyOptions{Spec: params.Spec, Prune: params.Prune, Registries: params.Registries})
}
func (providerRegistry) ecsContext(ctx context.Context, params reconcile.ApplyParams) context.Context {
	return ecs.WithApply(ctx, ecs.ApplyOptions{Spec: params.Spec, Prune: params.Prune, Registries: params.Registries})
}
func (providerRegistry) cloudRunContext(ctx context.Context, params reconcile.ApplyParams) context.Context {
	return cloudrun.WithApply(ctx, cloudrun.ApplyOptions{Spec: params.Spec, Prune: params.Prune, Registries: params.Registries})
}
func (providerRegistry) acaContext(ctx context.Context, params reconcile.ApplyParams) context.Context {
	return aca.WithApply(ctx, aca.ApplyOptions{Spec: params.Spec, Prune: params.Prune, Registries: params.Registries})
}
func (providerRegistry) lambdaContext(ctx context.Context, params reconcile.ApplyParams) context.Context {
	return (lambda.ApplyOptions{Spec: params.Spec, Prune: params.Prune}).WithContext(ctx)
}
func (providerRegistry) azureFunctionsContext(ctx context.Context, params reconcile.ApplyParams) context.Context {
	return (azurefunctions.ApplyOptions{Spec: params.Spec, Prune: params.Prune}).WithContext(ctx)
}
