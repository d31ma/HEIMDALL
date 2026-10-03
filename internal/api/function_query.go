package api

import (
	"context"
	"errors"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/reconcile"
	"github.com/d31ma/heimdall/internal/sequence"
	"github.com/d31ma/heimdall/internal/store"
	"log/slog"
	"net/http"
	"time"
)

type functionQuery struct {
	server      *Server
	writer      http.ResponseWriter
	request     *http.Request
	application store.Application
	window      provider.Window
	filter      provider.LogFilter
	series      provider.FunctionSeries
	logs        []byte
}

func (q *functionQuery) Metrics() {
	ctx, cancel := context.WithTimeout(q.request.Context(), (functionLimits{}).WaitDuration())
	defer cancel()
	err := (sequence.Steps{Actions: []func() error{q.available, q.resolve, q.metricWindow, func() error { return q.metrics(ctx) }}}).Run()
	action := q.metricResponse(err)
	action()
}
func (q *functionQuery) Logs() {
	ctx, cancel := context.WithTimeout(q.request.Context(), (functionLimits{}).WaitDuration())
	defer cancel()
	err := (sequence.Steps{Actions: []func() error{q.available, q.resolve, q.follow, q.logFilter, func() error { return q.readLogs(ctx) }}}).Run()
	action := q.logResponse(err)
	action()
}
func (q *functionQuery) available() error {
	if q.server.Store == nil || q.server.Reconcile == nil {
		return &boundaryError{Code: (boundaryError{}).UnavailableCode(), Message: "the function control plane is unavailable"}
	}
	return nil
}
func (q *functionQuery) resolve() error {
	application, found := q.server.appID(q.writer, q.request)
	q.application = application
	return (functionRequest{}).found(found)
}
func (q *functionQuery) metricWindow() error {
	window, err := metricsWindow(q.request)
	q.window = window
	return q.validWindow(err)
}
func (functionQuery) validWindow(err error) error {
	if err != nil {
		return &boundaryError{Code: (boundaryError{}).BadRequestCode(), Message: "the metrics time window is invalid"}
	}
	return nil
}
func (q *functionQuery) logFilter() error {
	q.filter.Tail = intQuery(q.request, "tail", q.DefaultTail())
	value := q.request.URL.Query().Get("since")
	since, err := time.Parse(time.RFC3339, value)
	return q.since(value, since, err)
}
func (q *functionQuery) since(value string, since time.Time, err error) error {
	switch {
	case value == "":
		return nil
	case err != nil:
		return &boundaryError{Code: (boundaryError{}).BadRequestCode(), Message: "since must be an RFC3339 timestamp"}
	}
	q.filter.Since = since.UTC()
	return nil
}
func (q *functionQuery) observation() reconcile.ServiceObservation {
	return reconcile.ServiceObservation{AppID: q.application.ID, Service: q.request.PathValue("service"), Version: q.request.URL.Query().Get("version"), Window: q.window, Filter: q.filter}
}
func (q *functionQuery) metrics(ctx context.Context) error {
	series, err := q.server.Reconcile.FunctionMetrics(ctx, q.observation())
	q.series = series
	return err
}
func (q *functionQuery) readLogs(ctx context.Context) error {
	reader, prior := q.server.Reconcile.FunctionLogs(ctx, q.observation())
	data, err := (&boundedLog{Reader: reader, Limit: q.LogBytes()}).Read(prior)
	q.logs = data
	return err
}
func (q *functionQuery) metricResponse(err error) func() {
	var written *responseWritten
	done := errors.As(err, &written)
	switch {
	case done:
		return func() {}
	case err != nil:
		return func() { (functionError{}).Write(q.writer, err) }
	}
	return func() { writeJSON(q.writer, http.StatusOK, q.series) }
}
func (q *functionQuery) logResponse(err error) func() {
	var written *responseWritten
	done := errors.As(err, &written)
	switch {
	case done:
		return func() {}
	case err != nil:
		return func() { (functionError{}).Write(q.writer, err) }
	}
	return q.writeLogs
}
func (q *functionQuery) writeLogs() {
	q.writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	q.writer.Header().Set("Cache-Control", "no-store")
	q.writer.WriteHeader(http.StatusOK)
	_, err := q.writer.Write(q.logs)
	q.writeResult(err)
}
func (functionQuery) writeResult(err error) {
	if err != nil {
		slog.Default().Debug("function log response could not be delivered")
		return
	}
}
func (functionQuery) DefaultTail() int { return 200 }
func (functionQuery) LogBytes() int    { return 4 * 1024 * 1024 }

func (q *functionQuery) follow() error {
	value := q.request.URL.Query().Get("follow")
	if value != "" && value != "false" {
		return &boundaryError{Code: (boundaryError{}).BadRequestCode(), Message: "native function log reads are finite; follow is unsupported"}
	}
	return nil
}
