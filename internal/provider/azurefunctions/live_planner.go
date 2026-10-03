package azurefunctions

import (
	"context"
	"sort"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

type livePlanner struct{ client *armClient }

func (p livePlanner) plan(ctx context.Context, target provider.Target, want spec.DeploySpec) (provider.Plan, error) {
	c := p.client
	hash, err := spec.Hash(want)
	if err != nil {
		return provider.Plan{}, err
	}
	project := target.Project
	if project == "" {
		project = "default"
	}
	plan := provider.Plan{Target: target.ID, App: provider.AppRef{Project: project, App: want.App}, Revision: want.Revision, SpecHash: hash}
	apps, err := c.list(ctx)
	if err != nil {
		return provider.Plan{}, err
	}
	live := map[string]*nativeApp{}
	for _, app := range apps {
		if (resourceIdentity{}).owned(app, plan.App) {
			service := app.tagValue("service")
			if live[service] != nil || (optionalValue[string]{}).read(app.Name) != (resourceIdentity{}).name(plan.App, service) {
				return provider.Plan{}, (&AdapterError{Code: "HD0474", Message: "duplicate or mismatched managed function resource identity"})
			}
			live[service] = app
		}
	}
	desired := map[string]bool{}
	for _, service := range want.Services {
		desired[service.Name] = true
		expected, err := (functionDefinition{config: c.config}).build(plan, service)
		if err != nil {
			return provider.Plan{}, err
		}
		operation := provider.Operation{Kind: provider.OpNoop, Service: service.Name, Wave: service.Wave}
		existing := live[service.Name]
		if existing == nil {
			collision, err := c.get(ctx, (resourceIdentity{}).name(plan.App, service.Name))
			if err != nil {
				return provider.Plan{}, err
			}
			if collision != nil {
				return provider.Plan{}, (&AdapterError{Code: "HD0474", Message: "function name collides with a resource outside this application's ownership"})
			}
			operation.Kind, operation.Reason = provider.OpCreate, "native function is absent"
		} else {
			health, err := (revisionReadback{client: c}).health(ctx, existing, expected)
			if err != nil {
				return provider.Plan{}, err
			}
			if health != provider.Healthy {
				operation.Kind, operation.Reason = provider.OpUpdate, "actual function configuration or ready revision differs"
			}
		}
		plan.Operations = append(plan.Operations, operation)
	}
	for service := range live {
		if !desired[service] {
			plan.Operations = append(plan.Operations, provider.Operation{Kind: provider.OpDelete, Service: service, Prune: true, Reason: "function is no longer declared"})
		}
	}
	sort.Slice(plan.Operations, func(i, j int) bool {
		if plan.Operations[i].Wave != plan.Operations[j].Wave {
			return plan.Operations[i].Wave < plan.Operations[j].Wave
		}
		return plan.Operations[i].Service < plan.Operations[j].Service
	})
	return plan, nil
}
