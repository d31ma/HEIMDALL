package diff_test

import (
	"testing"
	"time"

	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/utils/diff"
)

func TestReportKeepsMissingCreatesDistinctFromLiveAndPrunedServices(t *testing.T) {
	last := time.Unix(100, 0).UTC()
	next := time.Unix(200, 0).UTC()
	live := provider.LiveState{Revision: "current", Services: map[string]provider.ServiceState{
		"handler": {Health: provider.Healthy, FunctionVersion: "7", Schedule: "0 * * * *", LastRunAt: last, LastRunStatus: "succeeded", NextRunAt: next},
	}}
	plan := provider.Plan{Operations: []provider.Operation{
		{Kind: provider.OpCreate, Service: "api", Reason: "missing api"},
		{Kind: provider.OpCreate, Service: "handler", Reason: "already present"},
		{Kind: provider.OpCreate, Service: "pruned", Prune: true, Reason: "prune"},
		{Kind: provider.OpUpdate, Service: "updated", Reason: "update"},
		{Kind: provider.OpCreate, Service: "worker", Reason: "first reason"},
		{Kind: provider.OpCreate, Service: "worker", Reason: "later reason"},
	}}
	summary := diff.Report(diff.ReportInput{Target: "target", DesiredRevision: "desired", Plan: plan, Live: live,
		Services: []diff.ServiceDiff{{Service: "api", Kind: diff.Modified, Message: "existing explanation", Changes: []diff.Change{{Field: "image", Kind: diff.Modified}}}}})
	if summary.SyncStatus != diff.OutOfSync || summary.Health != provider.Missing || summary.Live != "current" || len(summary.Services) != 3 {
		t.Fatalf("missing-service summary: %#v", summary)
	}
	api, handler, worker := summary.Services[0], summary.Services[1], summary.Services[2]
	if api.Service != "api" || api.Kind != diff.Modified || api.Health != provider.Missing || api.Message != "existing explanation" || len(api.Changes) != 1 {
		t.Fatalf("lost existing diff evidence: %#v", api)
	}
	if handler.Service != "handler" || handler.Health != provider.Healthy || handler.FunctionVersion != "7" || handler.Schedule != "0 * * * *" || !handler.LastRunAt.Equal(last) || handler.LastRunStatus != "succeeded" || !handler.NextRunAt.Equal(next) {
		t.Fatalf("lost native live state or treated live create as missing: %#v", handler)
	}
	if worker.Service != "worker" || worker.Kind != diff.Added || worker.Health != provider.Missing || worker.Message != "first reason" {
		t.Fatalf("duplicated missing create or replaced first explanation: %#v", worker)
	}
}
