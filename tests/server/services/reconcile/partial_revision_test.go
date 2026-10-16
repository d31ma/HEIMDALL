package reconcile_test

import (
	"strings"
	"testing"
	"time"

	"github.com/d31ma/heimdall/app/server/repositories/store"
	"github.com/d31ma/heimdall/app/server/services/provider/docker"
	"github.com/d31ma/heimdall/app/server/services/reconcile"
	"github.com/d31ma/heimdall/app/server/utils/diff"
)

func TestOneLaterServiceChangeSyncsCleanly(t *testing.T) {
	world := newWorld(t)
	world.engine.ConvergeTimeout = 2 * time.Second
	first := world.sync(reconcile.Request{})
	requirePartialSyncSucceeded(t, first)
	apiBefore := containerFor(t, world.docker, "api")
	dbBefore := containerFor(t, world.docker, "db")

	world.commit("deploy/compose.yaml",
		strings.Replace(baseCompose, "postgres:16.4-alpine", "postgres:16.5-alpine", 1), "bump the db image")
	operation := world.sync(reconcile.Request{})
	requirePartialSyncSucceeded(t, operation)
	requirePartialAppliedCount(t, operation)
	requirePartialAppliedService(t, operation)

	apiAfter := containerFor(t, world.docker, "api")
	if apiAfter.ID != apiBefore.ID {
		t.Errorf("api was recreated (%s -> %s) by a change to db", apiBefore.ID, apiAfter.ID)
	}
	dbAfter := containerFor(t, world.docker, "db")
	if dbAfter.ID == dbBefore.ID {
		t.Error("db was not replaced by its image change")
	}
	requirePartialContainerImage(t, dbAfter.Image)
	requirePartialContainerHash(t, dbAfter.Labels[docker.LabelSpecHash], operation.SpecHash)
	requirePartialContainerHash(t, apiAfter.Labels[docker.LabelSpecHash], first.SpecHash)
	requirePartialSyncStatus(t, world.status().SyncStatus)
}

func TestPartialRolloutRejectsHealthyStaleService(t *testing.T) {
	world := newWorld(t)
	world.engine.ConvergeTimeout = 2 * time.Second
	first := world.sync(reconcile.Request{})
	requirePartialSyncSucceeded(t, first)
	dbBefore := containerFor(t, world.docker, "db")
	world.commit("deploy/compose.yaml",
		strings.Replace(baseCompose, "postgres:16.4-alpine", "postgres:16.5-alpine", 1), "bump the db image")
	world.engine.Heartbeat = func() {
		container := containerFor(t, world.docker, "db")
		world.docker.Mu.Lock()
		world.docker.Containers[container.ID].Labels[docker.LabelSpecHash] = first.SpecHash
		world.docker.Containers[container.ID].Labels[docker.LabelServiceHash] = dbBefore.Labels[docker.LabelServiceHash]
		world.docker.Containers[container.ID].Labels[docker.LabelRevision] = first.Revision
		world.docker.Mu.Unlock()
	}

	operation := world.sync(reconcile.Request{})
	if operation.Phase != store.PhaseFailed {
		t.Fatalf("healthy db with stale deployment identity ended %s", operation.Phase)
	}
	if !strings.Contains(operation.Message, "HD0273") {
		t.Errorf("stale deployment failed without a convergence reason: %s", operation.Message)
	}
}

func TestPartialRolloutRejectsStaleServiceWithMatchingAggregate(t *testing.T) {
	world := newWorld(t)
	world.engine.ConvergeTimeout = 2 * time.Second
	first := world.sync(reconcile.Request{})
	requirePartialSyncSucceeded(t, first)
	dbBefore := containerFor(t, world.docker, "db")
	changed := strings.Replace(baseCompose, "api:1.4.2", "api:1.5.0", 1)
	changed = strings.Replace(changed, "postgres:16.4-alpine", "postgres:16.5-alpine", 1)
	world.commit("deploy/compose.yaml", changed, "change both services")
	world.engine.Heartbeat = func() {
		container := containerFor(t, world.docker, "db")
		world.docker.Mu.Lock()
		world.docker.Containers[container.ID].Labels[docker.LabelServiceHash] = dbBefore.Labels[docker.LabelServiceHash]
		world.docker.Containers[container.ID].Labels[docker.LabelSpecHash] = first.SpecHash
		world.docker.Containers[container.ID].Labels[docker.LabelRevision] = first.Revision
		world.docker.Mu.Unlock()
	}

	operation := world.sync(reconcile.Request{})
	if operation.Phase != store.PhaseFailed {
		t.Fatalf("stale db hidden by current api aggregate identity ended %s", operation.Phase)
	}
	if !strings.Contains(operation.Message, "HD0273") {
		t.Errorf("stale deployment failed without a convergence reason: %s", operation.Message)
	}
}

func TestPartialSelectiveSyncIgnoresUntouchedPlanChanges(t *testing.T) {
	world := newWorld(t)
	world.engine.ConvergeTimeout = 2 * time.Second
	requirePartialSyncSucceeded(t, world.sync(reconcile.Request{}))
	apiBefore := containerFor(t, world.docker, "api")
	changed := strings.Replace(baseCompose, "api:1.4.2", "api:1.5.0", 1)
	changed = strings.Replace(changed, "postgres:16.4-alpine", "postgres:16.5-alpine", 1)
	world.commit("deploy/compose.yaml", changed, "change both services")

	operation := world.sync(reconcile.Request{Services: []string{"db"}})
	requirePartialSyncSucceeded(t, operation)
	requirePartialAppliedCount(t, operation)
	requirePartialAppliedService(t, operation)
	apiAfter := containerFor(t, world.docker, "api")
	if apiAfter.ID != apiBefore.ID {
		t.Errorf("selective db sync recreated api (%s -> %s)", apiBefore.ID, apiAfter.ID)
	}
	requirePartialContainerImage(t, containerFor(t, world.docker, "db").Image)
	if world.status().SyncStatus != diff.OutOfSync {
		t.Error("unapplied api change should remain out of sync after selective db sync")
	}
}

func requirePartialSyncSucceeded(t *testing.T, operation store.Operation) {
	t.Helper()
	if operation.Phase != store.PhaseSucceeded {
		t.Fatalf("partial sync ended %s: %s %v", operation.Phase, operation.Message, operation.Failures)
	}
}

func requirePartialAppliedCount(t *testing.T, operation store.Operation) {
	t.Helper()
	if len(operation.Applied) != 1 {
		t.Fatalf("partial sync applied %d operations: %+v", len(operation.Applied), operation.Applied)
	}
}

func requirePartialAppliedService(t *testing.T, operation store.Operation) {
	t.Helper()
	if operation.Applied[0].Service != "db" {
		t.Errorf("partial sync applied %s instead of db", operation.Applied[0].Service)
	}
}

func requirePartialContainerImage(t *testing.T, image string) {
	t.Helper()
	if image != "postgres:16.5-alpine" {
		t.Errorf("db image = %q", image)
	}
}

func requirePartialContainerHash(t *testing.T, actual, expected string) {
	t.Helper()
	if actual != expected {
		t.Errorf("container spec hash = %q, want %q", actual, expected)
	}
}

func requirePartialSyncStatus(t *testing.T, status diff.SyncStatus) {
	t.Helper()
	if status != diff.Synced {
		t.Errorf("partial sync status = %q", status)
	}
}

func TestPartialSelectiveSyncWithPruneKeepsUntouchedServices(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		name := "apply"
		if dryRun {
			name = "dry-run"
		}
		t.Run(name, func(t *testing.T) {
			world := newWorld(t)
			world.engine.ConvergeTimeout = 2 * time.Second
			requirePartialSyncSucceeded(t, world.sync(reconcile.Request{}))
			apiBefore := containerFor(t, world.docker, "api")
			dbBefore := containerFor(t, world.docker, "db")
			changed := strings.Replace(baseCompose, "api:1.4.2", "api:1.5.0", 1)
			changed = strings.Replace(changed, "postgres:16.4-alpine", "postgres:16.5-alpine", 1)
			world.commit("deploy/compose.yaml", changed, "change both services")
			prune := true

			operation := world.sync(reconcile.Request{Services: []string{"db"}, Prune: &prune, DryRun: dryRun})
			requirePartialSyncSucceeded(t, operation)
			for _, planned := range operation.Operations {
				if planned.Service != "db" {
					t.Errorf("selective db sync planned %s for untouched %s", planned.Kind, planned.Service)
				}
			}
			if containerFor(t, world.docker, "api").ID != apiBefore.ID {
				t.Error("selective db sync with prune replaced or deleted api")
			}
			dbAfter := containerFor(t, world.docker, "db")
			if dryRun {
				if dbAfter.ID != dbBefore.ID || len(operation.Applied) != 0 {
					t.Error("selective dry run applied a runtime change")
				}
			} else {
				requirePartialAppliedCount(t, operation)
				requirePartialAppliedService(t, operation)
				requirePartialContainerImage(t, dbAfter.Image)
			}
			if world.status().SyncStatus != diff.OutOfSync {
				t.Error("unapplied api change should remain out of sync after selective db sync")
			}
		})
	}
}
