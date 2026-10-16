package lambda

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	logtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
	"io"
	"strings"
)

type logEvents struct {
	client     *cloudwatchlogs.Client
	group      string
	streams    []string
	filter     provider.LogFilter
	input      *cloudwatchlogs.FilterLogEventsInput
	result     *cloudwatchlogs.FilterLogEventsOutput
	builder    strings.Builder
	count      int
	limit      int
	pagination nativePagination
}

func (query logEvents) Read(requestContext context.Context) (io.ReadCloser, error) {
	query.limit = query.lineLimit()
	query.input = &cloudwatchlogs.FilterLogEventsInput{LogGroupName: aws.String(query.group), LogStreamNames: query.streams, Limit: aws.Int32(int32(query.limit)), StartTime: query.startTime()}
	query.pagination = nativePagination{code: (Diagnostic{}).ObservationCode(), noun: "log", done: len(query.streams) == 0}
	failure := query.pages(requestContext)
	return query.output(failure)
}
func (query logEvents) lineLimit() int {
	if query.filter.Tail <= 0 || query.filter.Tail > (functionLimits{}).MaximumLogLines() {
		return (functionLimits{}).DefaultLogLines()
	}
	return query.filter.Tail
}
func (query logEvents) startTime() *int64 {
	if query.filter.Since.IsZero() {
		return nil
	}
	return aws.Int64(query.filter.Since.UnixMilli())
}
func (query *logEvents) pages(requestContext context.Context) error {
	var failure error
	for page := 0; page < (functionLimits{}).QueryPages() && !query.pagination.done && failure == nil; page++ {
		failure = query.page(requestContext)
	}
	return failure
}
func (query *logEvents) page(requestContext context.Context) error {
	return (sequence.Steps{Actions: []func() error{func() error { return query.fetch(requestContext) }, query.events, query.advance}}).Run()
}
func (query *logEvents) fetch(requestContext context.Context) error {
	var failure error
	query.input.NextToken = query.pagination.token
	query.result, failure = query.client.FilterLogEvents(requestContext, query.input)
	return failure
}
func (query *logEvents) events() error {
	var failure error
	for index := 0; index < len(query.result.Events) && query.count < query.limit && failure == nil; index++ {
		failure = query.appendEvent(query.result.Events[index])
	}
	return failure
}
func (query *logEvents) appendEvent(event logtypes.FilteredLogEvent) error {
	message := aws.ToString(event.Message)
	if query.builder.Len()+len(message)+1 > (functionLimits{}).LogBytes() {
		return (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "function log output exceeds 4 MiB")
	}
	query.builder.WriteString(message)
	query.builder.WriteByte('\n')
	query.count++
	return nil
}
func (query *logEvents) advance() error {
	return query.pagination.Advance(query.result.NextToken, query.count >= query.limit || aws.ToString(query.result.NextToken) == "")
}
func (query *logEvents) output(failure error) (io.ReadCloser, error) {
	switch {
	case failure != nil:
		return nil, failure
	case !query.pagination.done:
		return nil, (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "function log query exceeded 20 pages")
	}
	return io.NopCloser(strings.NewReader(query.builder.String())), nil
}
