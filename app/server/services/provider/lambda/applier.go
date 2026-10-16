package lambda

import (
	"context"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type applier struct {
	adapter *Provider
	target  provider.Target
	plan    provider.Plan
	options ApplyOptions
	client  *awslambda.Client
	result  provider.Result
}

func (a *applier) Apply(ctx context.Context) (provider.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, (functionLimits{}).ApplyTimeout())
	defer cancel()
	err := (sequence.Steps{Actions: []func() error{func() error { return a.optionsFrom(ctx) }, a.matchingPlan, a.validate, func() error { return a.connect(ctx) }, func() error { return a.operations(ctx) }}}).Run()
	return a.finalResult(err)
}
func (a *applier) optionsFrom(ctx context.Context) error {
	options, available := ctx.Value(applyKey{}).(ApplyOptions)
	a.options = options
	return a.requireOptions(available)
}
func (a *applier) requireOptions(available bool) error {
	if !available {
		return (Diagnostic{}).Format((Diagnostic{}).ApplyCode(), "Lambda apply requires ApplyOptions.WithContext")
	}
	return nil
}
func (a *applier) matchingPlan() error {
	hash, err := spec.Hash(a.options.Spec)
	project := a.project()
	return (sequence.Steps{Actions: []func() error{func() error { return err }, func() error { return a.planIdentity(hash, project) }}}).Run()
}
func (a *applier) project() string {
	if a.target.Project == "" {
		return (ownershipLabels{}).DefaultProject()
	}
	return a.target.Project
}
func (a *applier) planIdentity(hash, project string) error {
	if hash != a.plan.SpecHash || a.plan.Target != a.target.ID || a.plan.App.App != a.options.Spec.App || a.plan.App.Project != project {
		return (Diagnostic{}).Format((Diagnostic{}).ApplyCode(), "Lambda plan and apply target/spec differ; re-plan")
	}
	return nil
}
func (a *applier) validate() error {
	return (RuntimeContract{Target: a.target, Spec: a.options.Spec}).Validate()
}
func (a *applier) connect(ctx context.Context) error {
	client, err := a.adapter.client(ctx, a.target)
	a.client = client
	return err
}
func (a *applier) operations(ctx context.Context) error {
	var operationErr error
	for index := 0; index < len(a.plan.Operations) && operationErr == nil; index++ {
		operationErr = a.operation(ctx, a.plan.Operations[index])
	}
	return nil
}
func (a *applier) operation(ctx context.Context, operation provider.Operation) error {
	steps := a.operationSteps(ctx, operation)
	return (sequence.Steps{Actions: steps}).Run()
}
func (a *applier) operationSteps(ctx context.Context, operation provider.Operation) []func() error {
	if operation.Kind == provider.OpNoop || operation.Prune && !a.options.Prune {
		return nil
	}
	return []func() error{func() error { return a.execute(ctx, operation) }}
}
func (a *applier) execute(ctx context.Context, operation provider.Operation) error {
	err := (execution{adapter: a.adapter, client: a.client, request: executionRequest{Target: a.target, Plan: a.plan, Desired: a.options.Spec, Operation: operation}}).Run(ctx)
	a.record(operation, err)
	return err
}
func (a *applier) record(operation provider.Operation, err error) {
	if err != nil {
		a.result.Failures = map[string]string{operation.Service: err.Error()}
		return
	}
	a.result.Applied = append(a.result.Applied, operation)
}
func (a *applier) finalResult(err error) (provider.Result, error) {
	if err != nil {
		return provider.Result{}, err
	}
	return a.result, nil
}
