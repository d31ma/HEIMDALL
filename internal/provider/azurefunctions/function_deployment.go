package azurefunctions

import (
	"context"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

type functionDeployment struct{ client *armClient }

func (d functionDeployment) apply(ctx context.Context, plan provider.Plan, options ApplyOptions) (provider.Result, error) {
	queue := operationQueue{client: d.client, context: ctx, plan: plan, options: options}
	return queue.run()
}
func (d functionDeployment) execute(ctx context.Context, plan provider.Plan, operation provider.Operation, want spec.DeploySpec) error {
	change := functionOperation{client: d.client, context: ctx, plan: plan, operation: operation, want: want}
	return change.run()
}
func (d functionDeployment) wait(ctx context.Context, name string, desired *nativeApp) error {
	convergence := functionConvergence{client: d.client, name: name, desired: desired}
	return convergence.wait(ctx)
}
