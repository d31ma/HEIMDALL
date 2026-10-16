package lambda

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
	"slices"
)

type metricObservation struct {
	observation functionObservation
	window      provider.Window
	client      *cloudwatch.Client
	input       *cloudwatch.GetMetricDataInput
	result      *cloudwatch.GetMetricDataOutput
	series      provider.FunctionSeries
	pagination  nativePagination
	seen        map[string]bool
}

func (query metricObservation) Read(requestContext context.Context) (provider.FunctionSeries, error) {
	requestContext, cancel := context.WithTimeout(requestContext, (functionLimits{}).ReadTimeout())
	defer cancel()
	query.window = (metricWindow{window: query.window}).Normalized()
	query.series = provider.FunctionSeries{Ref: query.observation.ref}
	query.seen = map[string]bool{}
	query.pagination = nativePagination{code: (Diagnostic{}).ObservationCode(), noun: "metric"}
	failure := (sequence.Steps{Actions: []func() error{
		func() error { return query.functionRead(requestContext) },
		func() error { return (metricWindow{window: query.window}).Validate() },
		func() error { return query.configure(requestContext) },
		func() error { return query.pages(requestContext) },
	}}).Run()
	return query.output(failure)
}
func (query metricObservation) functionRead(requestContext context.Context) error {
	_, failure := (functionRead{observation: query.observation}).Read(requestContext)
	return failure
}
func (query *metricObservation) configure(requestContext context.Context) error {
	configuration, failure := query.observation.adapter.configuration(requestContext, query.observation.target)
	if failure != nil {
		return failure
	}
	query.client = cloudwatch.NewFromConfig(configuration, query.endpoint)
	query.input = &cloudwatch.GetMetricDataInput{StartTime: aws.Time(query.window.From), EndTime: aws.Time(query.window.To), MetricDataQueries: (metricQueries{reference: query.observation.ref, window: query.window}).Requests(), MaxDatapoints: aws.Int32((functionLimits{}).MetricPoints()), ScanBy: cwtypes.ScanByTimestampAscending}
	return nil
}
func (query metricObservation) endpoint(options *cloudwatch.Options) {
	if query.observation.adapter.EndpointOverride != "" {
		options.BaseEndpoint = aws.String(query.observation.adapter.EndpointOverride)
	}
}
func (query *metricObservation) pages(requestContext context.Context) error {
	var failure error
	for page := 0; page < (functionLimits{}).QueryPages() && !query.pagination.done && failure == nil; page++ {
		failure = query.page(requestContext)
	}
	return failure
}
func (query *metricObservation) page(requestContext context.Context) error {
	return (sequence.Steps{Actions: []func() error{func() error { return query.fetch(requestContext) }, query.results, query.advance}}).Run()
}
func (query *metricObservation) fetch(requestContext context.Context) error {
	var failure error
	query.input.NextToken = query.pagination.token
	query.result, failure = query.client.GetMetricData(requestContext, query.input)
	return failure
}
func (query *metricObservation) results() error {
	var failure error
	for index := 0; index < len(query.result.MetricDataResults) && failure == nil; index++ {
		failure = query.addMetric(query.result.MetricDataResults[index])
	}
	return failure
}
func (query *metricObservation) addMetric(result cwtypes.MetricDataResult) error {
	failure := (metricResult{result: result}).Add(&query.series)
	query.seen[aws.ToString(result.Id)] = failure == nil
	return failure
}
func (query *metricObservation) advance() error {
	return query.pagination.Advance(query.result.NextToken, aws.ToString(query.result.NextToken) == "")
}
func (query *metricObservation) output(failure error) (provider.FunctionSeries, error) {
	switch {
	case failure != nil:
		return provider.FunctionSeries{}, failure
	case !query.pagination.done:
		return provider.FunctionSeries{}, (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "native metric query exceeded 20 pages")
	case len(query.seen) != (functionMetricNames{}).Count():
		return provider.FunctionSeries{}, (metricResult{}).invalid()
	}
	query.order()
	return query.series, nil
}
func (query *metricObservation) order() {
	compare := func(left, right provider.Sample) int { return left.At.Compare(right.At) }
	slices.SortFunc(query.series.Invocations, compare)
	slices.SortFunc(query.series.Errors, compare)
	slices.SortFunc(query.series.Throttles, compare)
	slices.SortFunc(query.series.DurationMS, compare)
}
