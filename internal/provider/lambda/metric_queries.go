package lambda

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/d31ma/heimdall/internal/provider"
	"time"
)

type metricQueries struct {
	reference provider.FunctionRef
	window    provider.Window
}

func (queries metricQueries) Requests() []cwtypes.MetricDataQuery {
	names := functionMetricNames{}
	return []cwtypes.MetricDataQuery{
		queries.metric(names.InvocationsID(), "Invocations", names.Sum()),
		queries.metric(names.ErrorsID(), "Errors", names.Sum()),
		queries.metric(names.ThrottlesID(), "Throttles", names.Sum()),
		queries.metric(names.DurationID(), "Duration", names.Average()),
	}
}
func (queries metricQueries) metric(id, name, statistic string) cwtypes.MetricDataQuery {
	function := (Identity{App: queries.reference.AppRef, Service: queries.reference.Service}).Name()
	return cwtypes.MetricDataQuery{Id: aws.String(id), ReturnData: aws.Bool(true), MetricStat: &cwtypes.MetricStat{Period: aws.Int32(int32(queries.window.Step / time.Second)), Stat: aws.String(statistic), Metric: &cwtypes.Metric{Namespace: aws.String("AWS/Lambda"), MetricName: aws.String(name), Dimensions: []cwtypes.Dimension{{Name: aws.String("FunctionName"), Value: aws.String(function)}, {Name: aws.String("Resource"), Value: aws.String(function + ":" + queries.reference.Version)}}}}}
}
