package lambda

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
	"io"
)

type logObservation struct {
	observation functionObservation
	filter      provider.LogFilter
	function    *awslambda.GetFunctionOutput
	group       string
	client      *cloudwatchlogs.Client
	streams     []string
	reader      io.ReadCloser
}

func (query logObservation) Read(requestContext context.Context) (io.ReadCloser, error) {
	requestContext, cancel := context.WithTimeout(requestContext, (functionLimits{}).ReadTimeout())
	defer cancel()
	failure := (sequence.Steps{Actions: []func() error{
		query.validate,
		func() error { return query.functionRead(requestContext) },
		func() error { return query.configure(requestContext) },
		query.groupName,
		func() error { return query.streamRead(requestContext) },
		func() error { return query.eventRead(requestContext) },
	}}).Run()
	return query.reader, failure
}
func (query logObservation) validate() error {
	if query.filter.Follow {
		return (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "Lambda function logs use bounded polling, not live follow")
	}
	return nil
}
func (query *logObservation) functionRead(requestContext context.Context) error {
	var failure error
	query.function, failure = (functionRead{observation: query.observation}).Read(requestContext)
	return failure
}
func (query *logObservation) configure(requestContext context.Context) error {
	configuration, failure := query.observation.adapter.configuration(requestContext, query.observation.target)
	if failure != nil {
		return failure
	}
	query.client = cloudwatchlogs.NewFromConfig(configuration, query.endpoint)
	return nil
}
func (query *logObservation) groupName() error {
	query.group = query.logGroup()
	return nil
}
func (query logObservation) logGroup() string {
	if query.function.Configuration != nil && query.function.Configuration.LoggingConfig != nil && aws.ToString(query.function.Configuration.LoggingConfig.LogGroup) != "" {
		return aws.ToString(query.function.Configuration.LoggingConfig.LogGroup)
	}
	return "/aws/lambda/" + (Identity{App: query.observation.ref.AppRef, Service: query.observation.ref.Service}).Name()
}
func (query logObservation) endpoint(options *cloudwatchlogs.Options) {
	if query.observation.adapter.EndpointOverride != "" {
		options.BaseEndpoint = aws.String(query.observation.adapter.EndpointOverride)
	}
}
func (query *logObservation) streamRead(requestContext context.Context) error {
	var failure error
	query.streams, failure = (logStreams{client: query.client, group: query.group, version: query.observation.ref.Version}).Read(requestContext)
	return failure
}
func (query *logObservation) eventRead(requestContext context.Context) error {
	var failure error
	query.reader, failure = (logEvents{client: query.client, group: query.group, streams: query.streams, filter: query.filter}).Read(requestContext)
	return failure
}
