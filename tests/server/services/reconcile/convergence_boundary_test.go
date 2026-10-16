package reconcile_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/d31ma/heimdall/app/server/repositories/store"
	"github.com/d31ma/heimdall/app/server/services/provider/docker"
	"github.com/d31ma/heimdall/app/server/services/reconcile"
)

func TestConvergenceRequiresCurrentPlanProof(t *testing.T) {
	cases := []struct {
		mode   string
		reason string
	}{
		{mode: "missing-proof", reason: "api has no current runtime plan proof"},
		{mode: "wrong-spec", reason: "different desired spec"},
		{mode: "wrong-target", reason: "different target"},
		{mode: "wrong-app", reason: "different application"},
		{mode: "duplicate-conflict", reason: "api still requires runtime update"},
		{mode: "unknown-operation", reason: "api still requires runtime unknown"},
		{mode: "read-error", reason: "synthetic planning read failed"},
		{mode: "blocked-read", reason: "context deadline exceeded"},
		{mode: "observe-read-error", reason: "synthetic observation read failed"},
		{mode: "missing-live", reason: "api is not running"},
		{mode: "degraded-live", reason: "api is Degraded (synthetic live probe failed)"},
	}
	for _, item := range cases {
		t.Run(item.mode, func(t *testing.T) {
			world := newWorld(t)
			world.engine.ConvergeTimeout = 2 * time.Second
			requirePartialSyncSucceeded(t, world.sync(reconcile.Request{}))
			adapter := &convergenceBoundaryProvider{Provider: world.engine.Providers["docker"].(*docker.Provider), mode: item.mode}
			world.engine.Providers["docker"] = adapter
			world.commit("deploy/compose.yaml", strings.Replace(baseCompose, "api:1.4.2", "api:1.5.0", 1), "change api")

			operation := world.sync(reconcile.Request{})
			if operation.Phase != store.PhaseFailed {
				t.Fatalf("unqualified %s planning proof ended %s", item.mode, operation.Phase)
			}
			if !strings.Contains(operation.Message, "HD0273") || !strings.Contains(operation.Message, item.reason) {
				t.Errorf("planning proof failed with %q, want convergence diagnostic containing %q", operation.Message, item.reason)
			}
			if adapter.readCount == 0 {
				t.Error("no post-apply runtime Plan read was made")
			}
		})
	}
}

func TestConvergenceAcceptsConsistentDuplicateNoopProof(t *testing.T) {
	world := newWorld(t)
	world.engine.ConvergeTimeout = 2 * time.Second
	requirePartialSyncSucceeded(t, world.sync(reconcile.Request{}))
	adapter := &convergenceBoundaryProvider{Provider: world.engine.Providers["docker"].(*docker.Provider), mode: "duplicate-noop"}
	world.engine.Providers["docker"] = adapter
	world.commit("deploy/compose.yaml", strings.Replace(baseCompose, "api:1.4.2", "api:1.5.0", 1), "change api")

	requirePartialSyncSucceeded(t, world.sync(reconcile.Request{}))
	if adapter.readCount == 0 {
		t.Error("no post-apply runtime Plan read was made")
	}
}

func TestConvergencePlanReadFollowsRequestCancellation(t *testing.T) {
	world := newWorld(t)
	world.engine.ConvergeTimeout = 2 * time.Second
	requirePartialSyncSucceeded(t, world.sync(reconcile.Request{}))
	started := make(chan struct{}, 1)
	adapter := &convergenceBoundaryProvider{Provider: world.engine.Providers["docker"].(*docker.Provider), mode: "blocked-read", readStarted: started}
	world.engine.Providers["docker"] = adapter
	world.commit("deploy/compose.yaml", strings.Replace(baseCompose, "api:1.4.2", "api:1.5.0", 1), "change api")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-started:
			cancel()
		case <-ctx.Done():
		}
	}()
	defer func() { cancel(); <-done }()

	operation, err := world.engine.Sync(ctx, reconcile.Request{AppID: world.appID, PrincipalID: "prn_test", PolicyVersion: 7})
	if operation.Phase != store.PhaseFailed {
		t.Fatalf("cancelled planning read ended %s", operation.Phase)
	}
	if err == nil || !strings.Contains(err.Error(), "HD0274") {
		t.Errorf("cancelled planning read returned %v, want request cancellation diagnostic", err)
	}
	if adapter.readCount != 1 {
		t.Errorf("cancelled read ran %d post-apply plans, want exactly one", adapter.readCount)
	}
}
