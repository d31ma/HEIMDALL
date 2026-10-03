package azurefunctions

import (
	"context"

	"github.com/d31ma/heimdall/internal/provider"
)

type applier struct{ provider *Provider }

func (p applier) apply(ctx context.Context, target provider.Target, plan provider.Plan) (provider.Result, error) {
	options, ok := ctx.Value(applyKey{}).(ApplyOptions)
	if !ok {
		return provider.Result{}, (&AdapterError{Code: "HD0475", Message: "apply requires ApplyOptions.WithContext"})
	}
	if err := (workloadValidator{}).validate(options.Spec); err != nil {
		return provider.Result{}, err
	}
	if err := (planIdentity{}).validate(target, plan, options.Spec); err != nil {
		return provider.Result{}, err
	}
	config, err := (targetConfig{}).parse(target)
	if err != nil {
		return provider.Result{}, err
	}
	client, err := (clientFactory{provider: p.provider}).create(config)
	if err != nil {
		return provider.Result{}, err
	}
	return (functionDeployment{client: client}).apply(ctx, plan, options)
}
