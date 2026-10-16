package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/repositories/store"
	"github.com/d31ma/heimdall/app/server/services/api"
	"github.com/d31ma/heimdall/app/server/services/auth"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/services/provider/lambda"
	"github.com/d31ma/heimdall/app/server/services/reconcile"
	"github.com/d31ma/sesame/clients/go/sesame"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type functionSpy struct {
	*lambda.Provider
	requests       []provider.InvocationRequest
	failure        error
	metricRefs     []provider.FunctionRef
	logRefs        []provider.FunctionRef
	observed       int
	observeVersion string
}

func (s *functionSpy) Invoke(_ context.Context, request provider.InvocationRequest) (provider.Invocation, error) {
	s.requests = append(s.requests, request)
	return provider.Invocation{Version: "7", RequestID: "local-request", Payload: json.RawMessage(`{"answer":true}`), FunctionError: "Handled"}, s.failure
}

func (s *functionSpy) Observe(_ context.Context, _ provider.Target, app provider.AppRef) (provider.LiveState, error) {
	s.observed++
	return provider.LiveState{App: app, Services: map[string]provider.ServiceState{"handler": {FunctionVersion: s.observeVersion}}}, nil
}
func (s *functionSpy) FunctionMetrics(_ context.Context, _ provider.Target, ref provider.FunctionRef, _ provider.Window) (provider.FunctionSeries, error) {
	s.metricRefs = append(s.metricRefs, ref)
	return provider.FunctionSeries{Ref: ref, Invocations: []provider.Sample{{At: time.Now().UTC(), Value: 2}}}, nil
}
func (s *functionSpy) FunctionLogs(_ context.Context, _ provider.Target, ref provider.FunctionRef, _ provider.LogFilter) (io.ReadCloser, error) {
	s.logRefs = append(s.logRefs, ref)
	return io.NopCloser(strings.NewReader("native function log\n")), nil
}

func TestFunctionInvocationUsesStoredScopeAndOneSESAMEDecision(t *testing.T) {
	h := newHarness(t)
	adapter := &functionSpy{Provider: &lambda.Provider{EndpointOverride: "https://must-not-open.example.invalid"}}
	adapters := map[string]provider.Provider{"lambda": adapter}
	repoID, err := store.In[store.Repository](h.storage, store.Repos).Put(store.Repository{Project: "alpha", Name: "source", URL: "local-source"})
	if err != nil {
		t.Fatal(err)
	}
	targetID, err := store.In[store.Target](h.storage, store.Targets).Put(store.Target{Project: "alpha", Name: "native", Provider: "lambda", Region: "us-east-1", Config: map[string]string{"execution_role_arn": "arn:aws:iam::123456789012:role/existing"}})
	if err != nil {
		t.Fatal(err)
	}
	appID, err := store.In[store.Application](h.storage, store.Applications).Put(store.Application{Project: "alpha", Name: "events", RepoID: repoID, TargetID: targetID})
	if err != nil {
		t.Fatal(err)
	}
	wanted := spec.DeploySpec{App: "events", Revision: "source-commit", Services: []spec.Service{{Name: "handler", Replicas: 1, Image: "123456789012.dkr.ecr.us-east-1.amazonaws.com/handler@sha256:" + strings.Repeat("a", 64), Function: &spec.Function{RuntimeContract: "lambda-runtime-api", Invocation: "json"}}, {Name: "ordinary", Replicas: 1, Image: "ordinary"}}}
	functionWanted := wanted
	functionWanted.Services = wanted.Services[:1]
	_, err = store.In[store.Revision](h.storage, store.Revisions).Put(store.Revision{AppID: appID, Commit: wanted.Revision, Spec: functionWanted, RenderedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	reconciler := &reconcile.Engine{Store: h.storage, Providers: adapters}
	h.server.Close()
	h.server = httptest.NewServer((&api.Server{Engine: h.engine, Audit: h, Store: h.storage, Providers: adapters, Reconcile: reconciler}).Handler())
	principal, bearer := h.login(t, "function-operator")
	_, ungranted := h.login(t, "function-ungranted")
	resource, err := auth.Resource("project", "alpha", "app", "events")
	if err != nil {
		t.Fatal(err)
	}
	role, err := h.client.RoleCreate(context.Background(), h.tenant, "function-invoker", []sesame.Permission{{Action: auth.AppRun.String(), Resource: resource}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.client.GrantCreate(context.Background(), h.tenant, principal, role.ID); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/projects/alpha/apps/events/services/handler/invoke"
	call := func(path, body, bearer string) (int, string) {
		t.Helper()
		before := len(h.audited)
		request, err := http.NewRequest(http.MethodPost, h.server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+bearer)
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if len(h.audited) != before+1 || h.audited[len(h.audited)-1].Action != auth.AppRun.String() {
			t.Fatalf("invocation did not make exactly one existing boundary decision")
		}
		return response.StatusCode, string(data)
	}
	if status, _ := call(path, `{"payload":{}}`, ungranted); status != http.StatusForbidden || len(adapter.requests) != 0 {
		t.Fatal("ungranted invocation reached provider")
	}
	if status, _ := call(strings.Replace(path, "alpha", "beta", 1), `{"payload":{}}`, bearer); status != http.StatusForbidden || len(adapter.requests) != 0 {
		t.Fatal("body-independent project authorization was bypassed")
	}
	status, body := call(path, `{"payload":{"transient_marker":"input"}}`, bearer)
	if status != http.StatusOK || !strings.Contains(body, `"version":"7"`) || !strings.Contains(body, `"function_error":"Handled"`) || len(adapter.requests) != 1 {
		t.Fatalf("wrong native invocation result: %d %s", status, body)
	}
	invoked := adapter.requests[0]
	if invoked.Target.ID != targetID || invoked.App.Project != "alpha" || invoked.App.App != "events" || invoked.Service != "handler" || !strings.Contains(string(invoked.Payload), "transient_marker") {
		t.Fatalf("request scope was not resolved from stored app")
	}
	revisions, err := store.In[store.Revision](h.storage, store.Revisions).Find(nil)
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := json.Marshal(revisions)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bytes), "transient_marker") {
		t.Fatal("input payload was persisted into revision data")
	}
	for _, body := range []string{`{"payload":{},"target":"caller-target"}`, `{"payload":{}} {}`} {
		status, _ := call(path, body, bearer)
		if status != http.StatusBadRequest || len(adapter.requests) != 1 {
			t.Fatal("ambiguous or caller-selected routing reached provider")
		}
	}
	if status, _ := call(strings.Replace(path, "handler/invoke", "missing/invoke", 1), `{"payload":{}}`, bearer); status != http.StatusBadRequest || len(adapter.requests) != 1 {
		t.Fatal("undeclared service reached provider")
	}
	if err := store.In[store.Application](h.storage, store.Applications).Patch(appID, map[string]any{"suspended": true}); err != nil {
		t.Fatal(err)
	}
	if status, _ := call(path, `{"payload":{}}`, bearer); status != http.StatusBadRequest || len(adapter.requests) != 1 {
		t.Fatal("suspended app reached provider")
	}
	if err := store.In[store.Application](h.storage, store.Applications).Patch(appID, map[string]any{"suspended": false}); err != nil {
		t.Fatal(err)
	}
	if err := store.In[store.Target](h.storage, store.Targets).Patch(targetID, map[string]any{"agent_id": "agent-existing"}); err != nil {
		t.Fatal(err)
	}
	if status, body := call(path, `{"payload":{}}`, bearer); status != http.StatusBadRequest || !strings.Contains(body, "HD0410") || len(adapter.requests) != 1 {
		t.Fatal("unsupported native agent route reached dispatcher/provider")
	}
	if err := store.In[store.Target](h.storage, store.Targets).Patch(targetID, map[string]any{"agent_id": ""}); err != nil {
		t.Fatal(err)
	}
	adapter.failure = errors.New("native SDK secret-value-marker")
	if status, body := call(path, `{"payload":{}}`, bearer); status != http.StatusInternalServerError || strings.Contains(body, "secret-value-marker") || len(adapter.requests) != 2 {
		t.Fatal("transport error was exposed or invocation retried")
	}
	adapter.failure = context.DeadlineExceeded
	if status, body := call(path, `{"payload":{}}`, bearer); status != http.StatusGatewayTimeout || !strings.Contains(body, "execution may continue") || len(adapter.requests) != 3 {
		t.Fatal("ambiguous deadline was retried or lost")
	}
	tenantResource, err := auth.Resource("tenant", "self")
	if err != nil {
		t.Fatal(err)
	}
	createRole, err := h.client.RoleCreate(context.Background(), h.tenant, "function-targets", []sesame.Permission{{Action: auth.TargetCreate.String(), Resource: tenantResource}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.client.GrantCreate(context.Background(), h.tenant, principal, createRole.ID); err != nil {
		t.Fatal(err)
	}
	createTarget := func(body string) (int, string) {
		t.Helper()
		before := len(h.audited)
		request, err := http.NewRequest(http.MethodPost, h.server.URL+"/api/v1/targets", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+bearer)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if len(h.audited) != before+1 || h.audited[len(h.audited)-1].Action != auth.TargetCreate.String() {
			t.Fatal("target creation did not retain its one boundary decision")
		}
		return response.StatusCode, string(data)
	}
	beforeTargets, err := store.In[store.Target](h.storage, store.Targets).Find(nil)
	if err != nil {
		t.Fatal(err)
	}
	invalid := `{"name":"invalid-direct","project":"alpha","provider":"lambda","region":"us-east-1","agent_id":"agent-existing","config":{"execution_role_arn":"arn:aws:iam::123456789012:role/existing"}}`
	if status, body := createTarget(invalid); status != http.StatusBadRequest || !strings.Contains(body, "HD0410") {
		t.Fatalf("native routing was not rejected before storage: %d %s", status, body)
	}
	afterTargets, err := store.In[store.Target](h.storage, store.Targets).Find(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterTargets) != len(beforeTargets) {
		t.Fatal("invalid native target was persisted")
	}
	valid := strings.Replace(strings.Replace(invalid, "invalid-direct", "new-direct", 1), `,"agent_id":"agent-existing"`, "", 1)
	if status, body := createTarget(valid); status != http.StatusCreated || !strings.Contains(body, `"id":`) {
		t.Fatalf("pure pre-storage validation required an invented ID: %d %s", status, body)
	}

	observationRole, err := h.client.RoleCreate(context.Background(), h.tenant, "function-observer", []sesame.Permission{{Action: auth.ObserveMetrics.String(), Resource: resource}, {Action: auth.ObserveLogs.String(), Resource: resource}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.client.GrantCreate(context.Background(), h.tenant, principal, observationRole.ID); err != nil {
		t.Fatal(err)
	}
	get := func(suffix, token string) (int, string) {
		t.Helper()
		request, err := http.NewRequest(http.MethodGet, h.server.URL+strings.TrimSuffix(path, "invoke")+suffix, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, string(data)
	}
	if status, _ := get("metrics?version=7", ungranted); status != http.StatusForbidden || len(adapter.metricRefs) != 0 {
		t.Fatal("denied native metrics reached provider")
	}
	if status, _ := get("logs?version=7", ungranted); status != http.StatusForbidden || len(adapter.logRefs) != 0 {
		t.Fatal("denied native logs reached provider")
	}
	adapter.observeVersion = "7"
	if status, body := get("metrics?window=15m", bearer); status != http.StatusOK || !strings.Contains(body, `"invocations"`) || len(adapter.metricRefs) != 1 || adapter.metricRefs[0].Version != "7" || adapter.observed != 1 {
		t.Fatalf("metrics did not use actual observed version: %d %s", status, body)
	}
	if status, body := get("logs?version=6", bearer); status != http.StatusOK || body != "native function log\n" || len(adapter.logRefs) != 1 || adapter.logRefs[0].Version != "6" || adapter.logRefs[0].Service != "handler" || adapter.logRefs[0].Project != "alpha" || adapter.observed != 1 {
		t.Fatalf("logs lost native service/version scope: %d %s", status, body)
	}
	if status, _ := get("logs?since=invalid&version=7", bearer); status != http.StatusBadRequest || len(adapter.logRefs) != 1 {
		t.Fatal("invalid native log timestamp reached provider")
	}
	if status, _ := get("logs?follow=true&version=7", bearer); status != http.StatusBadRequest || len(adapter.logRefs) != 1 {
		t.Fatal("unsupported follow was silently accepted")
	}
	adapter.observeVersion = ""
	if status, body := get("metrics", bearer); status != http.StatusConflict || !strings.Contains(body, "HD0284") || len(adapter.metricRefs) != 1 {
		t.Fatalf("missing observed version was invented: %d %s", status, body)
	}

}
