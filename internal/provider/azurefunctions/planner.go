package azurefunctions

import (
	"context"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

type planner struct{ provider *Provider }

func (p planner) plan(ctx context.Context, target provider.Target, want spec.DeploySpec) (provider.Plan, error) {
	if err := (workloadValidator{}).validate(want); err != nil {
		return provider.Plan{}, err
	}
	config, err := (targetConfig{}).parse(target)
	if err != nil {
		return provider.Plan{}, err
	}
	client, err := (clientFactory{provider: p.provider}).create(config)
	if err != nil {
		return provider.Plan{}, err
	}
	return (livePlanner{client: client}).plan(ctx, target, want)
}
