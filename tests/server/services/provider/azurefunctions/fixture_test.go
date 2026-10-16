package azurefunctions

import (
	"context"
	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
)

func pointer[T any](v T) *T  { return &v }
func value[T any](v *T) T    { return (optionalValue[T]{}).read(v) }
func tag(name string) string { return (resourceIdentity{}).tag(name) }
func resourceName(app provider.AppRef, service string) string {
	return (resourceIdentity{}).name(app, service)
}
func definition(config targetConfig, plan provider.Plan, service spec.Service) (nativeApp, error) {
	return (functionDefinition{config: config}).build(plan, service)
}
func parseTarget(target provider.Target) (targetConfig, error) { return (targetConfig{}).parse(target) }

func WithApply(ctx context.Context, options ApplyOptions) context.Context {
	return options.WithContext(ctx)
}
