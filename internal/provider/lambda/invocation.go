package lambda

import (
	"context"
	"encoding/json"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/sequence"
	"strconv"
)

type invocation struct {
	adapter  *Provider
	request  provider.InvocationRequest
	client   *awslambda.Client
	identity Identity
	live     *awslambda.GetFunctionOutput
	version  string
	response *awslambda.InvokeOutput
}

func (i *invocation) Invoke(ctx context.Context) (provider.Invocation, error) {
	i.identity = Identity{App: i.request.App, Service: i.request.Service}
	ctx, cancel := context.WithTimeout(ctx, (functionLimits{}).InvokeTimeout())
	defer cancel()
	err := (sequence.Steps{Actions: []func() error{i.payload, func() error { return i.connect(ctx) }, func() error { return i.read(ctx) }, i.ownership, i.immutable, func() error { return i.invoke(ctx) }, i.responseStatus, i.responsePayload}}).Run()
	return i.result(err)
}
func (i *invocation) payload() error {
	if len(i.request.Payload) > (functionLimits{}).PayloadBytes() || !json.Valid(i.request.Payload) {
		return (Diagnostic{}).Format((Diagnostic{}).InvocationCode(), "direct Lambda payload must be valid JSON within 6 MiB")
	}
	return nil
}
func (i *invocation) connect(ctx context.Context) error {
	client, err := i.adapter.client(ctx, i.request.Target)
	i.client = client
	return err
}
func (i *invocation) read(ctx context.Context) error {
	live, err := i.client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String(i.identity.Name())})
	i.live = live
	return err
}
func (i *invocation) ownership() error {
	owned := i.identity.Owns(i.live.Tags)
	return i.requireOwnership(owned)
}
func (i *invocation) requireOwnership(owned bool) error {
	if i.live.TagsError != nil || !owned {
		return (Diagnostic{}).Format((Diagnostic{}).OwnershipCode(), "Lambda function belongs outside this application")
	}
	return nil
}
func (i *invocation) immutable() error {
	i.version = i.live.Tags[(ownershipLabels{}).Version()]
	number, err := strconv.Atoi(i.version)
	return i.requireVersion(number, err)
}
func (invocation) requireVersion(number int, err error) error {
	if err != nil || number < 1 {
		return (Diagnostic{}).Format((Diagnostic{}).InvocationCode(), "no converged immutable Lambda version is available")
	}
	return nil
}
func (i *invocation) invoke(ctx context.Context) error {
	response, err := i.client.Invoke(ctx, &awslambda.InvokeInput{FunctionName: aws.String(i.identity.Name()), Qualifier: aws.String(i.version), InvocationType: types.InvocationTypeRequestResponse, Payload: i.request.Payload}, i.singleAttempt)
	i.response = response
	return err
}
func (invocation) singleAttempt(options *awslambda.Options) { options.RetryMaxAttempts = 1 }
func (i *invocation) responseStatus() error {
	if i.response.StatusCode != i.SuccessStatus() || aws.ToString(i.response.ExecutedVersion) != i.version {
		return (Diagnostic{}).Format((Diagnostic{}).InvocationCode(), "Lambda returned an unexpected invocation status or version")
	}
	return nil
}
func (i *invocation) responsePayload() error {
	if !json.Valid(i.response.Payload) {
		return (Diagnostic{}).Format((Diagnostic{}).InvocationCode(), "direct Lambda response is not valid JSON")
	}
	return nil
}
func (i *invocation) result(err error) (provider.Invocation, error) {
	if err != nil {
		return provider.Invocation{}, err
	}
	requestID, _ := awsmiddleware.GetRequestIDMetadata(i.response.ResultMetadata)
	return provider.Invocation{RequestID: requestID, Version: i.version, Payload: i.response.Payload, FunctionError: aws.ToString(i.response.FunctionError)}, nil
}
func (invocation) SuccessStatus() int32 { return 200 }
