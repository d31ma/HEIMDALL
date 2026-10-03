package lambda

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/d31ma/heimdall/internal/sequence"
)

type metadata struct {
	client   *awslambda.Client
	identity Identity
	tags     map[string]string
	live     *awslambda.GetFunctionOutput
	done     bool
}

func (m *metadata) Wait(ctx context.Context) error { return m.poll(ctx) }
func (m *metadata) poll(ctx context.Context) error {
	var err error
	for !m.done && err == nil {
		err = (sequence.Steps{Actions: []func() error{func() error { return m.read(ctx) }, m.readable, m.matches, func() error { return m.pause(ctx) }}}).Run()
	}
	return err
}
func (m *metadata) read(ctx context.Context) error {
	live, err := m.client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String(m.identity.Name())})
	m.live = live
	return err
}
func (m *metadata) readable() error {
	if m.live.TagsError != nil {
		return (Diagnostic{}).Format((Diagnostic{}).ReadinessCode(), "Lambda revision metadata cannot be verified")
	}
	return nil
}
func (m *metadata) matches() error {
	fields := []bool{}
	for key, value := range m.tags {
		fields = append(fields, m.live.Tags[key] == value)
	}
	m.done = (FunctionState{}).all(fields)
	return nil
}
func (m *metadata) pause(ctx context.Context) error {
	steps := m.pauseSteps(ctx)
	return (sequence.Steps{Actions: steps}).Run()
}
func (m *metadata) pauseSteps(ctx context.Context) []func() error {
	if m.done {
		return nil
	}
	return []func() error{func() error { return (pollDelay{}).Wait(ctx) }}
}
