package lambda

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type readiness struct {
	client  *awslambda.Client
	name    string
	version string
	live    *awslambda.GetFunctionOutput
	done    bool
}

func (r *readiness) Wait(ctx context.Context) (*awslambda.GetFunctionOutput, error) {
	err := r.poll(ctx)
	return r.result(err)
}
func (r *readiness) poll(ctx context.Context) error {
	var err error
	for !r.done && err == nil {
		err = r.check(ctx)
	}
	return err
}
func (r *readiness) check(ctx context.Context) error {
	return (sequence.Steps{Actions: []func() error{func() error { return r.read(ctx) }, r.failed, r.ready, func() error { return r.pause(ctx) }}}).Run()
}
func (r *readiness) read(ctx context.Context) error {
	input := &awslambda.GetFunctionInput{FunctionName: aws.String(r.name), Qualifier: r.qualifier()}
	live, err := r.client.GetFunction(ctx, input)
	r.live = live
	return err
}
func (r *readiness) qualifier() *string {
	if r.version == "" {
		return nil
	}
	return aws.String(r.version)
}
func (r *readiness) failed() error {
	configuration := (FunctionState{}).configuration(r.live)
	return r.failure(configuration)
}
func (readiness) failure(configuration types.FunctionConfiguration) error {
	if configuration.State == types.StateFailed || configuration.LastUpdateStatus == types.LastUpdateStatusFailed {
		return (Diagnostic{}).Format((Diagnostic{}).ReadinessCode(), "Lambda deployment failed; inspect native function state and update diagnostics")
	}
	return nil
}
func (r *readiness) ready() error {
	configuration := (FunctionState{}).configuration(r.live)
	r.done = r.matches(configuration)
	return nil
}
func (r *readiness) matches(configuration types.FunctionConfiguration) bool {
	return configuration.State == types.StateActive && (r.version != "" || configuration.LastUpdateStatus == types.LastUpdateStatusSuccessful)
}
func (r *readiness) pause(ctx context.Context) error {
	steps := r.pauseSteps(ctx)
	return (sequence.Steps{Actions: steps}).Run()
}
func (r *readiness) pauseSteps(ctx context.Context) []func() error {
	if r.done {
		return nil
	}
	return []func() error{func() error { return (pollDelay{}).Wait(ctx) }}
}
func (r *readiness) result(err error) (*awslambda.GetFunctionOutput, error) {
	if err != nil {
		return nil, err
	}
	return r.live, nil
}
