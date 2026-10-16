package lambda

import (
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
	"math"
)

type metricResult struct{ result cwtypes.MetricDataResult }

func (metric metricResult) Add(series *provider.FunctionSeries) error {
	return (sequence.Steps{Actions: []func() error{metric.validate, func() error { samples := metric.samples(); metric.store(series, samples); return nil }}}).Run()
}
func (metric metricResult) validate() error {
	return errors.Join(metric.status(), metric.lengths(), metric.identifier(), metric.values())
}
func (metric metricResult) status() error {
	if metric.result.StatusCode != cwtypes.StatusCodeComplete {
		return metric.invalid()
	}
	return nil
}
func (metric metricResult) lengths() error {
	if len(metric.result.Timestamps) != len(metric.result.Values) {
		return metric.invalid()
	}
	return nil
}
func (metric metricResult) identifier() error {
	names := functionMetricNames{}
	switch aws.ToString(metric.result.Id) {
	case names.InvocationsID(), names.ErrorsID(), names.ThrottlesID(), names.DurationID():
		return nil
	}
	return metric.invalid()
}
func (metric metricResult) values() error {
	var failure error
	for _, value := range metric.result.Values {
		failure = errors.Join(failure, metric.number(value))
	}
	return failure
}
func (metric metricResult) number(value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return metric.invalid()
	}
	return nil
}
func (metricResult) invalid() error {
	return (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "native Lambda metrics are incomplete or malformed")
}
func (metric metricResult) samples() []provider.Sample {
	samples := []provider.Sample{}
	for index, at := range metric.result.Timestamps {
		samples = append(samples, provider.Sample{At: at.UTC(), Value: metric.result.Values[index]})
	}
	return samples
}
func (metric metricResult) store(series *provider.FunctionSeries, samples []provider.Sample) {
	names := functionMetricNames{}
	switch aws.ToString(metric.result.Id) {
	case names.InvocationsID():
		series.Invocations = append(series.Invocations, samples...)
	case names.ErrorsID():
		series.Errors = append(series.Errors, samples...)
	case names.ThrottlesID():
		series.Throttles = append(series.Throttles, samples...)
	case names.DurationID():
		series.DurationMS = append(series.DurationMS, samples...)
	}
}
