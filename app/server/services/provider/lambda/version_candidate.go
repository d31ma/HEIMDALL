package lambda

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

type versionCandidate struct {
	publication   publication
	configuration types.FunctionConfiguration
}

func (candidate versionCandidate) Version(requestContext context.Context) (string, error) {
	eligible := candidate.eligible()
	found, failure := candidate.read(requestContext, eligible)
	matched := candidate.matches(found)
	return candidate.result(matched, failure)
}
func (candidate versionCandidate) eligible() bool {
	fields := []bool{(immutableVersion{value: aws.ToString(candidate.configuration.Version)}).Valid(), aws.ToString(candidate.configuration.CodeSha256) == aws.ToString(candidate.publication.ready.Configuration.CodeSha256), (FunctionState{}).matchesConfiguration(&candidate.configuration, candidate.publication.target, candidate.publication.service), (publicationEnvironment{}).Matches(candidate.publication.ready.Configuration, &candidate.configuration)}
	return (FunctionState{}).all(fields)
}
func (candidate versionCandidate) read(requestContext context.Context, eligible bool) (*awslambda.GetFunctionOutput, error) {
	if !eligible {
		return nil, nil
	}
	return candidate.publication.client.GetFunction(requestContext, &awslambda.GetFunctionInput{FunctionName: aws.String(candidate.publication.identity.Name()), Qualifier: candidate.configuration.Version})
}

func (candidate versionCandidate) matches(found *awslambda.GetFunctionOutput) bool {
	configuration := (FunctionState{}).configuration(found)
	fields := []bool{configuration.State == types.StateActive, aws.ToString(configuration.Version) == aws.ToString(candidate.configuration.Version), aws.ToString(configuration.CodeSha256) == aws.ToString(candidate.publication.ready.Configuration.CodeSha256), (FunctionState{}).matchesConfiguration(&configuration, candidate.publication.target, candidate.publication.service), (FunctionState{}).matchesImage(found, candidate.publication.service), (publicationEnvironment{}).Matches(candidate.publication.ready.Configuration, &configuration)}
	return (FunctionState{}).all(fields)
}
func (candidate versionCandidate) result(matched bool, failure error) (string, error) {
	switch {
	case failure != nil:
		return "", failure
	case matched:
		return aws.ToString(candidate.configuration.Version), nil
	}
	return "", nil
}
