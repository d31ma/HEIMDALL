package lambda

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/d31ma/heimdall/internal/sequence"
)

type functionRead struct {
	observation functionObservation
	client      *awslambda.Client
	current     *awslambda.GetFunctionOutput
	published   *awslambda.GetFunctionOutput
}

func (reading functionRead) Read(requestContext context.Context) (*awslambda.GetFunctionOutput, error) {
	failure := (sequence.Steps{Actions: []func() error{
		reading.validateVersion,
		func() error { return reading.configure(requestContext) },
		func() error { return reading.readCurrent(requestContext) },
		reading.validateOwnership,
		func() error { return reading.readPublished(requestContext) },
		reading.validatePublished,
	}}).Run()
	return reading.published, failure
}
func (reading functionRead) validateVersion() error {
	if !(immutableVersion{value: reading.observation.ref.Version}).Valid() {
		return (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "function observation requires an immutable version")
	}
	return nil
}
func (reading *functionRead) configure(requestContext context.Context) error {
	var failure error
	reading.client, failure = reading.observation.adapter.client(requestContext, reading.observation.target)
	return failure
}
func (reading *functionRead) readCurrent(requestContext context.Context) error {
	var failure error
	identity := Identity{App: reading.observation.ref.AppRef, Service: reading.observation.ref.Service}
	reading.current, failure = reading.client.GetFunction(requestContext, &awslambda.GetFunctionInput{FunctionName: aws.String(identity.Name())})
	return failure
}
func (reading *functionRead) validateOwnership() error {
	identity := Identity{App: reading.observation.ref.AppRef, Service: reading.observation.ref.Service}
	if reading.current.TagsError != nil || !identity.Owns(reading.current.Tags) {
		return (Diagnostic{}).Format((Diagnostic{}).OwnershipCode(), "function observation belongs outside this application")
	}
	return nil
}
func (reading *functionRead) readPublished(requestContext context.Context) error {
	var failure error
	identity := Identity{App: reading.observation.ref.AppRef, Service: reading.observation.ref.Service}
	reading.published, failure = reading.client.GetFunction(requestContext, &awslambda.GetFunctionInput{FunctionName: aws.String(identity.Name()), Qualifier: aws.String(reading.observation.ref.Version)})
	return failure
}
func (reading *functionRead) validatePublished() error {
	if reading.published.Configuration == nil || aws.ToString(reading.published.Configuration.Version) != reading.observation.ref.Version {
		return (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "native function readback did not identify the requested immutable version")
	}
	return nil
}
