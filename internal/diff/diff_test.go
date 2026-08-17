package diff_test

import (
	"strings"
	"testing"

	"github.com/d31ma/heimdall/internal/diff"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

func specOf(services ...spec.Service) spec.DeploySpec {
	rendered := spec.DeploySpec{App: "checkout", Revision: "aaaaaaa", Services: services}
	rendered.Normalize()
	return rendered
}

func changeFor(t *testing.T, diffs []diff.ServiceDiff, service, field string) diff.Change {
	t.Helper()
	for _, entry := range diffs {
		if entry.Service != service {
			continue
		}
		for _, change := range entry.Changes {
			if change.Field == field {
				return change
			}
		}
	}
	t.Fatalf("no change for %s.%s in %+v", service, field, diffs)
	return diff.Change{}
}

func TestFieldLevelChanges(t *testing.T) {
	desired := specOf(spec.Service{
		Name: "api", Image: "nginx:1.28", Restart: "always",
		Env:   []spec.EnvVar{{Key: "LOG_LEVEL", Value: "debug"}, {Key: "NEW", Value: "1"}},
		Ports: []spec.Port{{Published: 8080, Target: 80, Protocol: "tcp"}},
	})
	deployed := specOf(spec.Service{
		Name: "api", Image: "nginx:1.27", Restart: "always",
		Env:   []spec.EnvVar{{Key: "LOG_LEVEL", Value: "info"}, {Key: "OLD", Value: "1"}},
		Ports: []spec.Port{{Published: 8080, Target: 80, Protocol: "tcp"}},
	})

	diffs := diff.Specs(desired, deployed)

	if change := changeFor(t, diffs, "api", "image"); change.Desired != "nginx:1.28" || change.Live != "nginx:1.27" {
		t.Errorf("image change = %+v", change)
	}
	if change := changeFor(t, diffs, "api", "env.LOG_LEVEL"); change.Kind != diff.Modified {
		t.Errorf("LOG_LEVEL change = %+v", change)
	}
	if change := changeFor(t, diffs, "api", "env.NEW"); change.Kind != diff.Added {
		t.Errorf("NEW change = %+v", change)
	}
	if change := changeFor(t, diffs, "api", "env.OLD"); change.Kind != diff.Removed {
		t.Errorf("OLD change = %+v", change)
	}
	// An unchanged field must not appear at all, or the view is noise.
	for _, entry := range diffs {
		for _, change := range entry.Changes {
			if change.Field == "restart" || change.Field == "ports" {
				t.Errorf("unchanged field reported: %+v", change)
			}
		}
	}
}

// TestSecretsAreShownAsReferences is the redaction guarantee. There is no
// value to leak because render never produced one, and the diff proves it by
// printing the reference.
func TestSecretsAreShownAsReferences(t *testing.T) {
	desired := specOf(spec.Service{
		Name: "api", Image: "nginx:1.27",
		Env: []spec.EnvVar{{Key: "DB", Ref: "vault/checkout#password_v2"}},
	})
	deployed := specOf(spec.Service{
		Name: "api", Image: "nginx:1.27",
		Env: []spec.EnvVar{{Key: "DB", Ref: "vault/checkout#password_v1"}},
	})

	change := changeFor(t, diff.Specs(desired, deployed), "api", "env.DB")
	if !change.Secret {
		t.Error("a reference-valued variable is not marked secret")
	}
	if !strings.HasPrefix(change.Desired, "${secret:") || !strings.HasPrefix(change.Live, "${secret:") {
		t.Errorf("a secret was not rendered as a reference: %+v", change)
	}
}

// TestLiteralToReferenceIsVisible: moving a variable into a secret manager is
// a real change an operator must see, not a silent one.
func TestLiteralToReferenceIsVisible(t *testing.T) {
	desired := specOf(spec.Service{Name: "api", Image: "n:1",
		Env: []spec.EnvVar{{Key: "DB", Ref: "vault/db#url"}}})
	deployed := specOf(spec.Service{Name: "api", Image: "n:1",
		Env: []spec.EnvVar{{Key: "DB", Value: "postgres://in-git"}}})

	change := changeFor(t, diff.Specs(desired, deployed), "api", "env.DB")
	if change.Kind != diff.Modified || !change.Secret {
		t.Errorf("change = %+v", change)
	}
}

func TestAddedAndRemovedServices(t *testing.T) {
	desired := specOf(spec.Service{Name: "api", Image: "n:1"}, spec.Service{Name: "new", Image: "n:1"})
	deployed := specOf(spec.Service{Name: "api", Image: "n:1"}, spec.Service{Name: "gone", Image: "n:1"})

	kinds := map[string]diff.ChangeKind{}
	for _, entry := range diff.Specs(desired, deployed) {
		kinds[entry.Service] = entry.Kind
	}
	if kinds["new"] != diff.Added {
		t.Errorf("new service kind = %q", kinds["new"])
	}
	if kinds["gone"] != diff.Removed {
		t.Errorf("removed service kind = %q", kinds["gone"])
	}
	if _, present := kinds["api"]; present {
		t.Error("an unchanged service appears in the diff")
	}
}

func TestOrderingIsNotAChange(t *testing.T) {
	// The same content written in a different order must produce no diff, or
	// every reformat reads as a deployment change.
	desired := specOf(spec.Service{Name: "api", Image: "n:1",
		Env:   []spec.EnvVar{{Key: "B", Value: "2"}, {Key: "A", Value: "1"}},
		Ports: []spec.Port{{Published: 90, Target: 90, Protocol: "tcp"}, {Published: 80, Target: 80, Protocol: "tcp"}}})
	deployed := specOf(spec.Service{Name: "api", Image: "n:1",
		Env:   []spec.EnvVar{{Key: "A", Value: "1"}, {Key: "B", Value: "2"}},
		Ports: []spec.Port{{Published: 80, Target: 80, Protocol: "tcp"}, {Published: 90, Target: 90, Protocol: "tcp"}}})

	if diffs := diff.Specs(desired, deployed); len(diffs) != 0 {
		t.Fatalf("reordering produced a diff: %+v", diffs)
	}
}

func TestReportSyncStatus(t *testing.T) {
	app := provider.AppRef{Project: "alpha", App: "checkout"}
	live := provider.LiveState{
		App: app, Revision: "aaaaaaa",
		Services: map[string]provider.ServiceState{"api": {Health: provider.Healthy}},
	}

	synced := diff.Report(app, "tgt", "aaaaaaa",
		provider.Plan{Operations: []provider.Operation{{Kind: provider.OpNoop, Service: "api"}}},
		live, false, nil)
	if synced.SyncStatus != diff.Synced {
		t.Errorf("all-noop plan reported %q", synced.SyncStatus)
	}

	drifted := diff.Report(app, "tgt", "bbbbbbb",
		provider.Plan{Operations: []provider.Operation{{Kind: provider.OpUpdate, Service: "api"}}},
		live, false, nil)
	if drifted.SyncStatus != diff.OutOfSync {
		t.Errorf("plan with work reported %q", drifted.SyncStatus)
	}

	// An unreadable target must never render as Synced.
	unknown := diff.Report(app, "tgt", "aaaaaaa", provider.Plan{}, provider.LiveState{}, true, nil)
	if unknown.SyncStatus != diff.Unknown {
		t.Fatalf("a failed live read reported %q, which would show a broken target as fine", unknown.SyncStatus)
	}
	if unknown.Health != provider.Missing {
		t.Errorf("health = %q, want Missing", unknown.Health)
	}
}

func TestReportFoldsHealthOntoServices(t *testing.T) {
	app := provider.AppRef{Project: "alpha", App: "checkout"}
	live := provider.LiveState{App: app, Services: map[string]provider.ServiceState{
		"api": {Health: provider.Degraded, Message: "exited with code 1"},
		"db":  {Health: provider.Healthy},
	}}
	summary := diff.Report(app, "tgt", "aaaaaaa",
		provider.Plan{Operations: []provider.Operation{{Kind: provider.OpNoop}}}, live, false,
		[]diff.ServiceDiff{{Service: "api", Kind: diff.Modified}})

	found := false
	for _, entry := range summary.Services {
		if entry.Service == "api" {
			found = true
			if entry.Health != provider.Degraded || entry.Message == "" {
				t.Errorf("health not folded onto the diff row: %+v", entry)
			}
		}
	}
	if !found {
		t.Fatal("api missing from the summary")
	}
	// A service that is live but not in the diff must still be listed.
	if len(summary.Services) != 2 {
		t.Fatalf("summary lists %d services, want 2", len(summary.Services))
	}
	if summary.Health != provider.Degraded {
		t.Errorf("rollup = %q", summary.Health)
	}
}
