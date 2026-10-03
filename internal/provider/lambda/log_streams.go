package lambda

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	logtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/d31ma/heimdall/internal/sequence"
	"strings"
)

type logStreams struct {
	client     *cloudwatchlogs.Client
	group      string
	version    string
	streams    []string
	result     *cloudwatchlogs.DescribeLogStreamsOutput
	pagination nativePagination
}

func (query logStreams) Read(requestContext context.Context) ([]string, error) {
	query.pagination = nativePagination{code: (Diagnostic{}).ObservationCode(), noun: "stream"}
	failure := query.pages(requestContext)
	return query.output(failure)
}
func (query *logStreams) pages(requestContext context.Context) error {
	var failure error
	for page := 0; page < (functionLimits{}).QueryPages() && !query.pagination.done && failure == nil; page++ {
		failure = query.page(requestContext)
	}
	return failure
}
func (query *logStreams) page(requestContext context.Context) error {
	return (sequence.Steps{Actions: []func() error{func() error { return query.fetch(requestContext) }, query.selectStreams, query.bound, query.advance}}).Run()
}
func (query *logStreams) fetch(requestContext context.Context) error {
	var failure error
	query.result, failure = query.client.DescribeLogStreams(requestContext, &cloudwatchlogs.DescribeLogStreamsInput{LogGroupName: aws.String(query.group), Limit: aws.Int32((functionLimits{}).PageItems()), NextToken: query.pagination.token})
	return failure
}
func (query *logStreams) selectStreams() error {
	for _, stream := range query.result.LogStreams {
		query.selectStream(stream)
	}
	return nil
}
func (query *logStreams) selectStream(stream logtypes.LogStream) {
	if strings.Contains(aws.ToString(stream.LogStreamName), "/["+query.version+"]") {
		query.streams = append(query.streams, aws.ToString(stream.LogStreamName))
	}
}
func (query *logStreams) bound() error {
	if len(query.streams) > (functionLimits{}).LogStreams() {
		return (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "version has more than 100 native log streams; narrow the native query")
	}
	return nil
}
func (query *logStreams) advance() error {
	return query.pagination.Advance(query.result.NextToken, aws.ToString(query.result.NextToken) == "")
}
func (query logStreams) output(failure error) ([]string, error) {
	switch {
	case failure != nil:
		return nil, failure
	case !query.pagination.done:
		return nil, (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "native stream inventory exceeded 20 pages")
	}
	return query.streams, nil
}
