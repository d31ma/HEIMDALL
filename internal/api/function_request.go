package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/reconcile"
	"github.com/d31ma/heimdall/internal/sequence"
	"github.com/d31ma/heimdall/internal/store"
	"net/http"
	"time"
)

type functionRequest struct {
	server      *Server
	writer      http.ResponseWriter
	request     *http.Request
	input       invocationInput
	application store.Application
	result      provider.Invocation
}

func (f *functionRequest) Invoke() {
	ctx, cancel := context.WithTimeout(f.request.Context(), (functionLimits{}).WaitDuration())
	defer cancel()
	err := (sequence.Steps{Actions: []func() error{f.available, f.decode, f.payload, f.resolve, func() error { return f.invoke(ctx) }}}).Run()
	f.respond(err)
}
func (f *functionRequest) available() error {
	if f.server.Store == nil || f.server.Reconcile == nil {
		return &boundaryError{Code: (boundaryError{}).UnavailableCode(), Message: "the function control plane is unavailable"}
	}
	return nil
}
func (f *functionRequest) decode() error {
	return (&strictBody{request: f.request, writer: f.writer, until: time.Now().Add((functionLimits{}).WaitDuration()), limit: (functionLimits{}).EnvelopeBytes()}).Decode(&f.input)
}
func (f *functionRequest) payload() error {
	if len(f.input.Payload) > (functionLimits{}).PayloadBytes() || !json.Valid(f.input.Payload) {
		return &boundaryError{Code: (boundaryError{}).BadRequestCode(), Message: "payload must be JSON within 256 KiB"}
	}
	return nil
}
func (f *functionRequest) resolve() error {
	application, found := f.server.appID(f.writer, f.request)
	f.application = application
	return f.found(found)
}
func (functionRequest) found(found bool) error {
	if !found {
		return &responseWritten{}
	}
	return nil
}
func (f *functionRequest) invoke(ctx context.Context) error {
	result, err := f.server.Reconcile.InvokeService(ctx, reconcile.ServiceInvocation{AppID: f.application.ID, Service: f.request.PathValue("service"), Payload: f.input.Payload})
	f.result = result
	return err
}
func (f *functionRequest) respond(err error) {
	var written *responseWritten
	done := errors.As(err, &written)
	action := f.responseAction(err, done)
	action()
}
func (f *functionRequest) responseAction(err error, done bool) func() {
	switch {
	case done:
		return func() {}
	case err != nil:
		return func() { (functionError{}).Write(f.writer, err) }
	}
	return func() { writeJSON(f.writer, http.StatusOK, f.result) }
}
