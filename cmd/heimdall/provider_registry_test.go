package main

import (
	"context"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/reconcile"
	"github.com/d31ma/heimdall/internal/spec"
	"testing"
)

func TestNativeFunctionRegistrationProvidesMatchingApplyContexts(t *testing.T) {
	adapters, contexts := providers(nil)
	for _, name := range []string{"docker", "swarm", "ecs", "cloudrun", "aca", "lambda", "azurefunctions"} {
		adapter, present := adapters[name]
		if !present || adapter.Name() != name || contexts[name] == nil {
			t.Fatalf("missing or mismatched registration for %s", name)
		}
	}
	for _, name := range []string{"lambda", "azurefunctions"} {
		target := provider.Target{ID: "target", Provider: name}
		ctx := contexts[name](context.Background(), reconcile.ApplyParams{Spec: spec.DeploySpec{App: "app"}})
		_, err := adapters[name].Apply(ctx, target, provider.Plan{Target: "different"})
		if err == nil {
			t.Fatalf("%s accepted a mismatched apply plan", name)
		}
	}
}
