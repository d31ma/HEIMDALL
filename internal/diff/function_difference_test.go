package diff_test

import (
	"github.com/d31ma/heimdall/internal/diff"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
	"testing"
)

func TestFunctionContractChangesAreVisibleInServiceDiff(t *testing.T) {
	desired := spec.DeploySpec{App: "events", Services: []spec.Service{{Name: "handler", Function: &spec.Function{RuntimeContract: "lambda-runtime-api", Invocation: "json", TimeoutMS: 30000, Architecture: "amd64"}}}}
	live := spec.DeploySpec{App: "events", Services: []spec.Service{{Name: "handler", Function: &spec.Function{RuntimeContract: "lambda-runtime-api", Invocation: "json", TimeoutMS: 1000, Architecture: "amd64"}}}}
	differences := diff.Specs(desired, live)
	if len(differences) != 1 || len(differences[0].Changes) != 1 || differences[0].Changes[0].Field != "function" {
		t.Fatalf("missing native contract drift: %#v", differences)
	}
}

func TestSummaryCarriesObservedFunctionVersion(t *testing.T) {
	desired := spec.DeploySpec{App: "events", Services: []spec.Service{{Name: "handler"}}}
	live := provider.LiveState{App: provider.AppRef{Project: "alpha", App: "events"}, Services: map[string]provider.ServiceState{"handler": {Health: provider.Healthy, FunctionVersion: "7"}}}
	summary := diff.Report(live.App, "target", "revision", provider.Plan{}, live, false, diff.Specs(desired, desired))
	if len(summary.Services) != 1 || summary.Services[0].FunctionVersion != "7" {
		t.Fatalf("lost observed native version: %#v", summary)
	}
}
