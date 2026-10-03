package lambda

import (
	"context"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
	"strconv"
)

type publication struct {
	client   *awslambda.Client
	identity Identity
	ready    *awslambda.GetFunctionOutput
	target   provider.Target
	service  spec.Service
}

func (p publication) Version(ctx context.Context) (string, error) {
	return p.version(ctx)
}

func (p publication) version(ctx context.Context) (string, error) {
	published, err := p.client.PublishVersion(ctx, &awslambda.PublishVersionInput{FunctionName: aws.String(p.identity.Name()), RevisionId: p.ready.Configuration.RevisionId, CodeSha256: p.ready.Configuration.CodeSha256})
	if err == nil {
		return aws.ToString(published.Version), nil
	}
	var conflict *types.ResourceConflictException
	if !errors.As(err, &conflict) {
		return "", err
	}
	return p.existingVersion(ctx)
}

func (p publication) existingVersion(ctx context.Context) (string, error) {
	var marker *string
	for page := 0; page < 100; page++ {
		listed, err := p.client.ListVersionsByFunction(ctx, &awslambda.ListVersionsByFunctionInput{FunctionName: aws.String(p.identity.Name()), Marker: marker, MaxItems: aws.Int32(50)})
		if err != nil {
			return "", err
		}
		for _, config := range listed.Versions {
			version := aws.ToString(config.Version)
			if number, err := strconv.Atoi(version); err != nil || number < 1 || aws.ToString(config.CodeSha256) != aws.ToString(p.ready.Configuration.CodeSha256) || !(FunctionState{}).matchesConfiguration(&config, p.target, p.service) {
				continue
			}
			found, err := p.client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String(p.identity.Name()), Qualifier: aws.String(version)})
			if err != nil {
				return "", err
			}
			if found.Configuration != nil && found.Configuration.State == types.StateActive && (FunctionState{}).matchesImage(found, p.service) {
				return version, nil
			}
		}
		if aws.ToString(listed.NextMarker) == "" {
			break
		}
		if aws.ToString(listed.NextMarker) == aws.ToString(marker) {
			return "", (Diagnostic{}).Format((Diagnostic{}).ReadinessCode(), "Lambda version pagination did not advance")
		}
		marker = listed.NextMarker
	}
	return "", (Diagnostic{}).Format((Diagnostic{}).ReadinessCode(), "publication conflicted and no matching immutable version exists")
}
