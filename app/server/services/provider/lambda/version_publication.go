package lambda

import (
	"context"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type versionPublication struct {
	publication publication
	result      *awslambda.PublishVersionOutput
	failure     error
	recovery    bool
	version     string
}

func (operation versionPublication) Version(requestContext context.Context) (string, error) {
	failure := (sequence.Steps{Actions: []func() error{
		operation.validateReady,
		func() error { return operation.publish(requestContext) },
		operation.classify,
		func() error { return operation.recover(requestContext) },
		operation.validateVersion,
	}}).Run()
	return operation.version, failure
}
func (operation *versionPublication) validateReady() error {
	if operation.publication.ready == nil || operation.publication.ready.Configuration == nil || aws.ToString(operation.publication.ready.Configuration.RevisionId) == "" || aws.ToString(operation.publication.ready.Configuration.CodeSha256) == "" {
		return (Diagnostic{}).Format((Diagnostic{}).ReadinessCode(), "native publication requires exact revision and code hash readback")
	}
	return nil
}
func (operation *versionPublication) publish(requestContext context.Context) error {
	ready := operation.publication.ready.Configuration
	operation.result, operation.failure = operation.publication.client.PublishVersion(requestContext, &awslambda.PublishVersionInput{FunctionName: aws.String(operation.publication.identity.Name()), RevisionId: ready.RevisionId, CodeSha256: ready.CodeSha256})
	operation.version = operation.resultVersion()
	return nil
}
func (operation versionPublication) resultVersion() string {
	if operation.result == nil {
		return ""
	}
	return aws.ToString(operation.result.Version)
}
func (operation *versionPublication) classify() error {
	var conflict *types.ResourceConflictException
	switch {
	case operation.failure == nil:
		return nil
	case errors.As(operation.failure, &conflict):
		operation.recovery = true
		return nil
	}
	return operation.failure
}
func (operation *versionPublication) recover(requestContext context.Context) error {
	if !operation.recovery {
		return nil
	}
	var failure error
	operation.version, failure = (versionInventory{publication: operation.publication}).Version(requestContext)
	return failure
}
func (operation *versionPublication) validateVersion() error {
	if !(immutableVersion{value: operation.version}).Valid() {
		return (Diagnostic{}).Format((Diagnostic{}).ReadinessCode(), "native publication did not return an immutable version")
	}
	return nil
}
