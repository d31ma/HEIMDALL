package lambda

import (
	"context"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/d31ma/heimdall/internal/provider"
)

type functionObservation struct {
	adapter *Provider
	target  provider.Target
	ref     provider.FunctionRef
}

func (o functionObservation) function(ctx context.Context) (*awslambda.GetFunctionOutput, error) {
	if version, err := strconv.Atoi(o.ref.Version); err != nil || version < 1 {
		return nil, (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "function observation requires an immutable version")
	}
	client, err := o.adapter.client(ctx, o.target)
	if err != nil {
		return nil, err
	}
	identity := Identity{App: o.ref.AppRef, Service: o.ref.Service}
	current, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String(identity.Name())})
	if err != nil {
		return nil, err
	}
	if current.TagsError != nil || !identity.Owns(current.Tags) {
		return nil, (Diagnostic{}).Format((Diagnostic{}).OwnershipCode(), "function observation belongs outside this application")
	}
	return client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String(identity.Name()), Qualifier: aws.String(o.ref.Version)})
}

func (o functionObservation) metrics(ctx context.Context, window provider.Window) (provider.FunctionSeries, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := o.function(ctx); err != nil {
		return provider.FunctionSeries{}, err
	}
	if window.To.IsZero() {
		window.To = time.Now().UTC()
	}
	if window.From.IsZero() {
		window.From = window.To.Add(-time.Hour)
	}
	if window.Step == 0 {
		window.Step = time.Minute
	}
	if !window.From.Before(window.To) || window.To.Sub(window.From) > 24*time.Hour || window.Step < time.Minute || window.Step > time.Hour || window.Step%time.Minute != 0 {
		return provider.FunctionSeries{}, (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "metric window must be at most 24 hours with whole-minute periods")
	}
	configuration, err := o.adapter.configuration(ctx, o.target)
	if err != nil {
		return provider.FunctionSeries{}, err
	}
	client := cloudwatch.NewFromConfig(configuration, func(options *cloudwatch.Options) {
		if o.adapter.EndpointOverride != "" {
			options.BaseEndpoint = aws.String(o.adapter.EndpointOverride)
		}
	})
	name := (Identity{App: o.ref.AppRef, Service: o.ref.Service}).Name()
	queries := []cwtypes.MetricDataQuery{}
	for _, metric := range []struct{ id, name, statistic string }{{"invocations", "Invocations", "Sum"}, {"errors", "Errors", "Sum"}, {"throttles", "Throttles", "Sum"}, {"duration", "Duration", "Average"}} {
		queries = append(queries, cwtypes.MetricDataQuery{Id: aws.String(metric.id), ReturnData: aws.Bool(true), MetricStat: &cwtypes.MetricStat{Period: aws.Int32(int32(window.Step / time.Second)), Stat: aws.String(metric.statistic), Metric: &cwtypes.Metric{Namespace: aws.String("AWS/Lambda"), MetricName: aws.String(metric.name), Dimensions: []cwtypes.Dimension{{Name: aws.String("FunctionName"), Value: aws.String(name)}, {Name: aws.String("Resource"), Value: aws.String(name + ":" + o.ref.Version)}}}}})
	}
	series := provider.FunctionSeries{Ref: o.ref}
	var token *string
	for page := 0; page < 20; page++ {
		result, err := client.GetMetricData(ctx, &cloudwatch.GetMetricDataInput{StartTime: aws.Time(window.From), EndTime: aws.Time(window.To), MetricDataQueries: queries, NextToken: token, MaxDatapoints: aws.Int32(10000), ScanBy: cwtypes.ScanByTimestampAscending})
		if err != nil {
			return provider.FunctionSeries{}, err
		}
		for _, metric := range result.MetricDataResults {
			if len(metric.Timestamps) != len(metric.Values) || metric.StatusCode != cwtypes.StatusCodeComplete && metric.StatusCode != cwtypes.StatusCodePartialData {
				return provider.FunctionSeries{}, (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "native Lambda metrics are incomplete or malformed")
			}
			samples := []provider.Sample{}
			for index, at := range metric.Timestamps {
				samples = append(samples, provider.Sample{At: at, Value: metric.Values[index]})
			}
			switch aws.ToString(metric.Id) {
			case "invocations":
				series.Invocations = append(series.Invocations, samples...)
			case "errors":
				series.Errors = append(series.Errors, samples...)
			case "throttles":
				series.Throttles = append(series.Throttles, samples...)
			case "duration":
				series.DurationMS = append(series.DurationMS, samples...)
			}
		}
		if aws.ToString(result.NextToken) == "" {
			o.order(&series)
			return series, nil
		}
		if aws.ToString(result.NextToken) == aws.ToString(token) {
			return provider.FunctionSeries{}, (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "native metric pagination did not advance")
		}
		token = result.NextToken
	}
	return provider.FunctionSeries{}, (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "native metric query exceeded 20 pages")
}

func (o functionObservation) order(series *provider.FunctionSeries) {
	for _, samples := range [][]provider.Sample{series.Invocations, series.Errors, series.Throttles, series.DurationMS} {
		sort.Slice(samples, func(i, j int) bool { return samples[i].At.Before(samples[j].At) })
	}
}

func (o functionObservation) logs(ctx context.Context, filter provider.LogFilter) (io.ReadCloser, error) {
	if filter.Follow {
		return nil, (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "Lambda function logs use bounded polling, not live follow")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	function, err := o.function(ctx)
	if err != nil {
		return nil, err
	}
	group := "/aws/lambda/" + (Identity{App: o.ref.AppRef, Service: o.ref.Service}).Name()
	if function.Configuration != nil && function.Configuration.LoggingConfig != nil && aws.ToString(function.Configuration.LoggingConfig.LogGroup) != "" {
		group = aws.ToString(function.Configuration.LoggingConfig.LogGroup)
	}
	configuration, err := o.adapter.configuration(ctx, o.target)
	if err != nil {
		return nil, err
	}
	client := cloudwatchlogs.NewFromConfig(configuration, func(options *cloudwatchlogs.Options) {
		if o.adapter.EndpointOverride != "" {
			options.BaseEndpoint = aws.String(o.adapter.EndpointOverride)
		}
	})
	streams, err := o.streams(ctx, client, group)
	if err != nil {
		return nil, err
	}
	if len(streams) == 0 {
		return io.NopCloser(strings.NewReader("")), nil
	}
	limit := filter.Tail
	if limit <= 0 || limit > 2000 {
		limit = 200
	}
	input := &cloudwatchlogs.FilterLogEventsInput{LogGroupName: aws.String(group), LogStreamNames: streams, Limit: aws.Int32(int32(limit))}
	if !filter.Since.IsZero() {
		input.StartTime = aws.Int64(filter.Since.UnixMilli())
	}
	var builder strings.Builder
	count := 0
	for page := 0; page < 20; page++ {
		result, err := client.FilterLogEvents(ctx, input)
		if err != nil {
			return nil, err
		}
		for _, event := range result.Events {
			message := aws.ToString(event.Message)
			if builder.Len()+len(message)+1 > 4*1024*1024 {
				return nil, (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "function log output exceeds 4 MiB")
			}
			builder.WriteString(message)
			builder.WriteByte('\n')
			count++
			if count >= limit {
				return io.NopCloser(strings.NewReader(builder.String())), nil
			}
		}
		if aws.ToString(result.NextToken) == "" {
			return io.NopCloser(strings.NewReader(builder.String())), nil
		}
		if aws.ToString(result.NextToken) == aws.ToString(input.NextToken) {
			return nil, (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "native log pagination did not advance")
		}
		input.NextToken = result.NextToken
	}
	return nil, (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "function log query exceeded 20 pages")
}

func (o functionObservation) streams(ctx context.Context, client *cloudwatchlogs.Client, group string) ([]string, error) {
	input := &cloudwatchlogs.DescribeLogStreamsInput{LogGroupName: aws.String(group), Limit: aws.Int32(50)}
	streams := []string{}
	for page := 0; page < 20; page++ {
		result, err := client.DescribeLogStreams(ctx, input)
		if err != nil {
			return nil, err
		}
		for _, stream := range result.LogStreams {
			if strings.Contains(aws.ToString(stream.LogStreamName), "/["+o.ref.Version+"]") {
				streams = append(streams, aws.ToString(stream.LogStreamName))
			}
		}
		if len(streams) > 100 {
			return nil, (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "version has more than 100 native log streams; narrow the native query")
		}
		if aws.ToString(result.NextToken) == "" {
			return streams, nil
		}
		if aws.ToString(result.NextToken) == aws.ToString(input.NextToken) {
			return nil, (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "native stream pagination did not advance")
		}
		input.NextToken = result.NextToken
	}
	return nil, (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "native stream inventory exceeded 20 pages")
}
