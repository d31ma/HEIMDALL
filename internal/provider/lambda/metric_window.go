package lambda

import (
	"github.com/d31ma/heimdall/internal/provider"
	"time"
)

type metricWindow struct{ window provider.Window }

func (bounds metricWindow) Normalized() provider.Window {
	bounds.end()
	bounds.start()
	bounds.period()
	return bounds.window
}
func (bounds *metricWindow) end() {
	if bounds.window.To.IsZero() {
		bounds.window.To = time.Now().UTC()
	}
	bounds.window.To = bounds.window.To.UTC()
}
func (bounds *metricWindow) start() {
	if bounds.window.From.IsZero() {
		bounds.window.From = bounds.window.To.Add(-time.Hour)
	}
	bounds.window.From = bounds.window.From.UTC()
}
func (bounds *metricWindow) period() {
	if bounds.window.Step == 0 {
		bounds.window.Step = time.Minute
	}
}
func (bounds metricWindow) Validate() error { return bounds.validate() }
func (bounds metricWindow) validate() error {
	if !bounds.window.From.Before(bounds.window.To) || bounds.window.To.Sub(bounds.window.From) > (functionLimits{}).MetricWindow() || bounds.window.Step < time.Minute || bounds.window.Step > time.Hour || bounds.window.Step%time.Minute != 0 {
		return (Diagnostic{}).Format((Diagnostic{}).ObservationCode(), "metric window must be at most 24 hours with whole-minute periods")
	}
	return nil
}
