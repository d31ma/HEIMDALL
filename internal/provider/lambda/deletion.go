package lambda

import (
	"context"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/d31ma/heimdall/internal/sequence"
)

type deletion struct {
	client *awslambda.Client
	name   string
	done   bool
}

func (d *deletion) Wait(ctx context.Context) error { return d.poll(ctx) }
func (d *deletion) poll(ctx context.Context) error {
	var err error
	for !d.done && err == nil {
		err = (sequence.Steps{Actions: []func() error{func() error { return d.read(ctx) }, func() error { return d.pause(ctx) }}}).Run()
	}
	return err
}
func (d *deletion) read(ctx context.Context) error {
	_, err := d.client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String(d.name)})
	var missing *types.ResourceNotFoundException
	d.done = errors.As(err, &missing)
	return d.readResult(err)
}
func (d *deletion) readResult(err error) error {
	if d.done {
		return nil
	}
	return err
}
func (d *deletion) pause(ctx context.Context) error {
	steps := d.pauseSteps(ctx)
	return (sequence.Steps{Actions: steps}).Run()
}
func (d *deletion) pauseSteps(ctx context.Context) []func() error {
	if d.done {
		return nil
	}
	return []func() error{func() error { return (pollDelay{}).Wait(ctx) }}
}
