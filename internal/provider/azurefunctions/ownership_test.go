package azurefunctions

import (
	"context"
	"strings"
	"testing"

	"github.com/d31ma/heimdall/internal/spec"
)

func TestPruneRequiresOptInAndPreservesOtherOwners(t *testing.T) {
	adapter, server := newProtocolServer(t)
	want := functionSpec("prune")
	plan := applySpec(t, adapter, want)
	server.mu.Lock()
	foreign := *server.apps[resourceName(plan.App, "one")]
	foreign.Tags = map[string]*string{tag("managed-by"): pointer("heimdall"), tag("provider"): pointer("aca"), tag("project"): pointer("conf"), tag("app"): pointer("checkout"), tag("service"): pointer("foreign")}
	foreign.Name = pointer("foreign-resource")
	server.apps["foreign-resource"] = &foreign
	server.mu.Unlock()
	empty := spec.DeploySpec{App: "checkout", Revision: "removed"}
	empty.Normalize()
	prune, err := adapter.Plan(context.Background(), testTarget(), empty)
	if err != nil {
		t.Fatal(err)
	}
	if len(prune.Operations) != 2 {
		t.Fatalf("prune escaped ownership: %+v", prune.Operations)
	}
	result, err := adapter.Apply(WithApply(context.Background(), ApplyOptions{Spec: empty}), testTarget(), prune)
	if err != nil || len(result.Applied) != 0 {
		t.Fatalf("prune opt-in was ignored: %+v %v", result, err)
	}
	result, err = adapter.Apply(WithApply(context.Background(), ApplyOptions{Spec: empty, Prune: true}), testTarget(), prune)
	if err != nil || len(result.Failures) != 0 {
		t.Fatalf("prune failed: %+v %v", result, err)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.apps) != 1 || server.apps["foreign-resource"] == nil {
		t.Fatal("prune removed another provider's resource")
	}
}

func TestCollisionAndOwnershipChangeRefuseMutation(t *testing.T) {
	adapter, server := newProtocolServer(t)
	want := functionSpec("ownership")
	plan, err := adapter.Plan(context.Background(), testTarget(), want)
	if err != nil {
		t.Fatal(err)
	}
	config, err := parseTarget(testTarget())
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := definition(config, plan, want.Services[0])
	if err != nil {
		t.Fatal(err)
	}
	name := resourceName(plan.App, want.Services[0].Name)
	foreign.Name = pointer(name)
	foreign.Tags[tag("project")] = pointer("another-project")
	server.mu.Lock()
	server.apps[name] = &foreign
	server.mu.Unlock()
	if _, err := adapter.Plan(context.Background(), testTarget(), want); err == nil {
		t.Fatal("planning accepted an unowned name collision")
	}
	result, err := adapter.Apply(WithApply(context.Background(), ApplyOptions{Spec: want}), testTarget(), plan)
	if err != nil || result.Failures["one"] == "" {
		t.Fatalf("ownership changed after plan but apply mutated it: %+v %v", result, err)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.bodies) != 0 {
		t.Fatal("an unowned resource was overwritten")
	}
}

func TestTargetReferenceValidationBeforeARM(t *testing.T) {
	for _, key := range []string{"storage_account_resource_id", "identity_resource_id", "identity_client_id", "subscription_id"} {
		t.Run(key, func(t *testing.T) {
			adapter, server := newProtocolServer(t)
			target := testTarget()
			target.Config[key] = "invalid"
			if _, err := adapter.Plan(context.Background(), target, functionSpec("target")); err == nil {
				t.Fatal("invalid existing-resource reference accepted")
			}
			if len(server.calls) != 0 {
				t.Fatal("invalid target reached ARM")
			}
		})
	}
	adapter, server := newProtocolServer(t)
	target := testTarget()
	target.Config["storage_account_resource_id"] = strings.Replace(target.Config["storage_account_resource_id"], "000000000001", "000000000009", 1)
	if _, err := adapter.Plan(context.Background(), target, functionSpec("cross-subscription")); err == nil || len(server.calls) != 0 {
		t.Fatal("cross-subscription storage reference reached ARM")
	}
}

func TestPaginationCannotEscapeARMCollection(t *testing.T) {
	adapter, server := newProtocolServer(t)
	server.mu.Lock()
	server.nextLink = "https://invalid.example/steal?api-version=2026-07-01"
	server.mu.Unlock()
	if _, err := adapter.Plan(context.Background(), testTarget(), functionSpec("pagination")); err == nil {
		t.Fatal("cross-origin nextLink accepted")
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.calls) != 1 {
		t.Fatal("unsafe nextLink was followed")
	}
}
