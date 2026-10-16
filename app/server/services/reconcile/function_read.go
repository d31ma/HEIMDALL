package reconcile

import (
	"context"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
	"io"
)

type functionRead struct {
	engine    *Engine
	request   ServiceObservation
	selection *functionInvocation
	observer  provider.FunctionObserver
	ref       provider.FunctionRef
	live      provider.LiveState
	series    provider.FunctionSeries
	logs      io.ReadCloser
}

func (f *functionRead) Metrics(ctx context.Context) (provider.FunctionSeries, error) {
	f.prepare()
	err := (sequence.Steps{Actions: []func() error{func() error { return f.scope(ctx) }, func() error { return f.metrics(ctx) }}}).Run()
	return f.metricResult(err)
}
func (f *functionRead) Logs(ctx context.Context) (io.ReadCloser, error) {
	f.prepare()
	err := (sequence.Steps{Actions: []func() error{func() error { return f.scope(ctx) }, func() error { return f.logData(ctx) }}}).Run()
	return f.logResult(err)
}
func (f *functionRead) prepare() {
	f.selection = &functionInvocation{engine: f.engine, request: ServiceInvocation{AppID: f.request.AppID, Service: f.request.Service}}
}
func (f *functionRead) scope(ctx context.Context) error {
	return (sequence.Steps{Actions: []func() error{f.selection.resolve, f.selection.revisionFor, f.selection.declared, f.selection.desired, f.support, func() error { return f.version(ctx) }, f.observedVersion}}).Run()
}
func (f *functionRead) support() error {
	observer, available := f.selection.resolved.adapter.(provider.FunctionObserver)
	f.observer = observer
	return f.requireObserver(available)
}
func (functionRead) requireObserver(available bool) error {
	if !available {
		return &InvocationError{Code: (InvocationError{}).ObservationCode(), Message: "this adapter has no qualified function/version telemetry mapping; configure and qualify its native logging/telemetry scope"}
	}
	return nil
}
func (f *functionRead) version(ctx context.Context) error {
	f.ref = provider.FunctionRef{AppRef: f.selection.resolved.app.AppRef(), Service: f.request.Service, Version: f.request.Version}
	steps := f.versionSteps(ctx)
	return (sequence.Steps{Actions: steps}).Run()
}
func (f *functionRead) versionSteps(ctx context.Context) []func() error {
	if f.ref.Version != "" {
		return nil
	}
	return []func() error{func() error { return f.observe(ctx) }, f.readyVersion}
}
func (f *functionRead) observe(ctx context.Context) error {
	live, err := f.selection.resolved.adapter.Observe(ctx, f.selection.resolved.target.Ref(), f.selection.resolved.app.AppRef())
	f.live = live
	return err
}
func (f *functionRead) readyVersion() error {
	f.ref.Version = f.live.Services[f.request.Service].FunctionVersion
	return nil
}
func (f *functionRead) observedVersion() error {
	if f.ref.Version == "" {
		return &InvocationError{Code: (InvocationError{}).VersionCode(), Message: "no immutable native function version has been observed"}
	}
	return nil
}
func (f *functionRead) metrics(ctx context.Context) error {
	series, err := f.observer.FunctionMetrics(ctx, f.selection.resolved.target.Ref(), f.ref, f.request.Window)
	f.series = series
	return err
}
func (f *functionRead) logData(ctx context.Context) error {
	logs, err := f.observer.FunctionLogs(ctx, f.selection.resolved.target.Ref(), f.ref, f.request.Filter)
	f.logs = logs
	return err
}
func (f *functionRead) metricResult(err error) (provider.FunctionSeries, error) {
	if err != nil {
		return provider.FunctionSeries{}, err
	}
	return f.series, nil
}
func (f *functionRead) logResult(err error) (io.ReadCloser, error) {
	return f.logs, err
}
