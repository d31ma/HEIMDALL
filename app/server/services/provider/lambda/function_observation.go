package lambda

import (
	"context"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"io"
)

type functionObservation struct {
	adapter *Provider
	target  provider.Target
	ref     provider.FunctionRef
}

func (observation functionObservation) metrics(requestContext context.Context, window provider.Window) (provider.FunctionSeries, error) {
	return (metricObservation{observation: observation, window: window}).Read(requestContext)
}
func (observation functionObservation) logs(requestContext context.Context, filter provider.LogFilter) (io.ReadCloser, error) {
	return (logObservation{observation: observation, filter: filter}).Read(requestContext)
}
