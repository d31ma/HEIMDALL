// These tests are the Phase 1 vertical slice end to end: a real git
// repository, a real FYLO store, the real render, diff, and Docker adapter
// code, and a fake only at the Docker Engine boundary.
//
// scripts/e2e-docker.sh runs the same shape against a live Engine. This runs
// everywhere, on every commit.
package reconcile_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/d31ma/heimdall/internal/agent"
	"github.com/d31ma/heimdall/internal/diff"
	"github.com/d31ma/heimdall/internal/dispatch"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/provider/docker"
	"github.com/d31ma/heimdall/internal/provider/docker/dockertest"
	"github.com/d31ma/heimdall/internal/reconcile"
	"github.com/d31ma/heimdall/internal/secrets"
	"github.com/d31ma/heimdall/internal/spec"
	"github.com/d31ma/heimdall/internal/store"
)

const baseCompose = `services:
  api:
    image: ghcr.io/example/api:1.4.2
    environment:
      LOG_LEVEL: info
      DATABASE_URL: "${secret:vault/checkout#database_url}"
    ports:
      - "8000:8000"
    depends_on:
      - db
  db:
    image: postgres:16.4-alpine
    volumes:
      - pgdata:/var/lib/postgresql/data
`

// world is everything one test needs: a git remote, a store, an engine, and a
// wired reconciler.
type world struct {
	t         *testing.T
	engine    *reconcile.Engine
	docker    *dockertest.Engine
	upstream  string
	appID     string
	storage   *store.Store
	runUpstrm func(args ...string)
	writeFile func(path, body string)
}

func newWorld(t *testing.T) *world {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	if _, err := exec.LookPath("fylo"); err != nil {
		t.Skipf("fylo not on PATH: %v", err)
	}

	root := t.TempDir()
	upstream := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(upstream, "deploy"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	runUpstream := func(args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = upstream
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
			"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	writeFile := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(upstream, path), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	runUpstream("init", "--initial-branch=main")
	writeFile("deploy/compose.yaml", baseCompose)
	runUpstream("add", ".")
	runUpstream("commit", "-m", "initial deployment")

	storage, err := store.Open(filepath.Join(root, "fylo-root"), "")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	fake := dockertest.New()
	t.Cleanup(fake.Close)

	secrets := func(_ context.Context, ref string) (string, error) {
		return "resolved:" + ref, nil
	}
	adapter := &docker.Provider{SecretResolver: secrets}
	reconciler := &reconcile.Engine{
		Store:     storage,
		Providers: map[string]provider.Provider{"docker": adapter},
		ApplyContext: map[string]func(context.Context, reconcile.ApplyParams) context.Context{
			"docker": func(ctx context.Context, params reconcile.ApplyParams) context.Context {
				return docker.WithApply(ctx, docker.ApplyOptions{
					Spec: params.Spec, Prune: params.Prune, Registries: params.Registries,
				})
			},
		},
		Secrets:  secrets,
		CacheDir: filepath.Join(root, "git"),
	}

	repoID, err := store.In[store.Repository](storage, store.Repos).Put(store.Repository{
		Project: "alpha", Name: "checkout", URL: upstream, DefaultRef: "main",
	})
	if err != nil {
		t.Fatalf("store repository: %v", err)
	}
	targetID, err := store.In[store.Target](storage, store.Targets).Put(store.Target{
		Project: "alpha", Name: "local", Provider: "docker", Endpoint: fake.URL(),
	})
	if err != nil {
		t.Fatalf("store target: %v", err)
	}
	appID, err := store.In[store.Application](storage, store.Applications).Put(store.Application{
		Project: "alpha", Name: "checkout", RepoID: repoID, TargetID: targetID, Path: "deploy",
	})
	if err != nil {
		t.Fatalf("store application: %v", err)
	}

	return &world{
		t: t, engine: reconciler, docker: fake, upstream: upstream,
		appID: appID, storage: storage, runUpstrm: runUpstream, writeFile: writeFile,
	}
}

// commit writes a file and commits it, returning nothing: the reconciler
// resolves the ref itself, which is what a real sync does.
func (w *world) commit(path, body, message string) {
	w.t.Helper()
	w.writeFile(path, body)
	w.runUpstrm("add", ".")
	w.runUpstrm("commit", "-m", message)
}

func (w *world) sync(request reconcile.Request) store.Operation {
	w.t.Helper()
	request.AppID = w.appID
	if request.PrincipalID == "" {
		request.PrincipalID = "prn_test"
	}
	if request.PolicyVersion == 0 {
		request.PolicyVersion = 7
	}
	operation, err := w.engine.Sync(context.Background(), request)
	if err != nil && operation.Phase != store.PhaseFailed {
		w.t.Fatalf("sync: %v", err)
	}
	return operation
}

func (w *world) status() diff.Summary {
	w.t.Helper()
	summary, err := w.engine.Status(context.Background(), w.appID)
	if err != nil {
		w.t.Fatalf("status: %v", err)
	}
	return summary
}

// TestVerticalSlice is the Phase 1 exit criterion in one test: a compose
// repository deploys, drift is visible, and a one-service change syncs
// cleanly — with every step attributable to a principal.
func TestVerticalSlice(t *testing.T) {
	world := newWorld(t)

	// Nothing is deployed yet, so the application is out of sync.
	if before := world.status(); before.SyncStatus != diff.OutOfSync {
		t.Fatalf("an undeployed application reports %q", before.SyncStatus)
	}

	operation := world.sync(reconcile.Request{})
	if operation.Phase != store.PhaseSucceeded {
		t.Fatalf("sync failed: %s %v", operation.Message, operation.Failures)
	}
	if len(operation.Applied) != 2 {
		t.Fatalf("applied %d operations, want 2", len(operation.Applied))
	}
	// Attribution is not optional: this is what makes the audit answerable.
	if operation.PrincipalID != "prn_test" || operation.PolicyVersion != 7 {
		t.Errorf("operation is not attributable: %+v", operation)
	}
	if operation.Revision == "" || operation.SpecHash == "" {
		t.Errorf("operation does not record what it deployed: %+v", operation)
	}

	// And now it converges.
	after := world.status()
	if after.SyncStatus != diff.Synced {
		t.Fatalf("after a successful sync the status is %q", after.SyncStatus)
	}
	if after.Health != provider.Healthy {
		t.Errorf("health = %q", after.Health)
	}
	if after.Live != operation.Revision {
		t.Errorf("live revision %q does not match the deployed one %q", after.Live, operation.Revision)
	}
}

// TestOneServiceChangeSyncsCleanly is the second half of the exit criterion:
// changing one service must not disturb the other.
func TestOneServiceChangeSyncsCleanly(t *testing.T) {
	world := newWorld(t)
	world.sync(reconcile.Request{})

	dbBefore := containerFor(t, world.docker, "db")

	world.commit("deploy/compose.yaml",
		strings.Replace(baseCompose, "api:1.4.2", "api:1.5.0", 1), "bump the api image")

	// The diff must name the change before anything is applied.
	summary := world.status()
	if summary.SyncStatus != diff.OutOfSync {
		t.Fatalf("a new commit did not show as out of sync: %q", summary.SyncStatus)
	}
	found := false
	for _, service := range summary.Services {
		for _, change := range service.Changes {
			if service.Service == "api" && change.Field == "image" {
				found = true
				if change.Desired != "ghcr.io/example/api:1.5.0" || change.Live != "ghcr.io/example/api:1.4.2" {
					t.Errorf("image change = %+v", change)
				}
			}
		}
	}
	if !found {
		t.Fatalf("the diff does not name the changed image: %+v", summary.Services)
	}

	operation := world.sync(reconcile.Request{})
	if operation.Phase != store.PhaseSucceeded {
		t.Fatalf("sync failed: %s %v", operation.Message, operation.Failures)
	}
	if len(operation.Applied) != 1 || operation.Applied[0].Service != "api" {
		t.Fatalf("a one-service change applied %d operations: %+v", len(operation.Applied), operation.Applied)
	}
	// The untouched service must be the same container, not a recreated one.
	if dbAfter := containerFor(t, world.docker, "db"); dbAfter.ID != dbBefore.ID {
		t.Errorf("db was recreated (%s -> %s) by a change to api", dbBefore.ID, dbAfter.ID)
	}
}

// TestDriftIsVisible: something removed the container out of band, the way a
// tired operator with `docker rm -f` would.
func TestDriftIsVisible(t *testing.T) {
	world := newWorld(t)
	world.sync(reconcile.Request{})

	world.docker.RemoveByService("db")

	summary := world.status()
	if summary.SyncStatus != diff.OutOfSync {
		t.Fatalf("an out-of-band removal reports %q", summary.SyncStatus)
	}
	if summary.Health == provider.Healthy {
		t.Errorf("health is %q with a service missing", summary.Health)
	}

	// Re-syncing restores it. Phase 2 does this automatically; Phase 1 proves
	// the mechanism works when asked.
	if operation := world.sync(reconcile.Request{}); operation.Phase != store.PhaseSucceeded {
		t.Fatalf("restore failed: %s", operation.Message)
	}
	if world.status().SyncStatus != diff.Synced {
		t.Error("re-sync did not restore the removed service")
	}
}

func TestDryRunAppliesNothing(t *testing.T) {
	world := newWorld(t)

	operation := world.sync(reconcile.Request{DryRun: true})
	if operation.Phase != store.PhaseSucceeded {
		t.Fatalf("dry run failed: %s", operation.Message)
	}
	if len(operation.Operations) != 2 {
		t.Fatalf("dry run produced %d planned operations, want 2", len(operation.Operations))
	}
	if len(operation.Applied) != 0 {
		t.Fatalf("a dry run applied %d operations", len(operation.Applied))
	}
	if world.docker.Count() != 0 {
		t.Fatalf("a dry run created %d containers", world.docker.Count())
	}
}

// TestRollbackReAppliesAStoredRevision proves a rollback does not go back to
// git, so a force-push cannot change what rolling back means.
func TestRollbackReAppliesAStoredRevision(t *testing.T) {
	world := newWorld(t)
	first := world.sync(reconcile.Request{})

	world.commit("deploy/compose.yaml",
		strings.Replace(baseCompose, "api:1.4.2", "api:2.0.0", 1), "bump to 2.0.0")
	second := world.sync(reconcile.Request{})
	if second.Revision == first.Revision {
		t.Fatal("the second sync deployed the same revision")
	}
	if image := containerFor(t, world.docker, "api").Image; image != "ghcr.io/example/api:2.0.0" {
		t.Fatalf("api image = %q before rollback", image)
	}

	rollback := world.sync(reconcile.Request{Revision: first.Revision})
	if rollback.Phase != store.PhaseSucceeded {
		t.Fatalf("rollback failed: %s %v", rollback.Message, rollback.Failures)
	}
	if !rollback.Rollback {
		t.Error("the operation is not marked as a rollback")
	}
	if image := containerFor(t, world.docker, "api").Image; image != "ghcr.io/example/api:1.4.2" {
		t.Fatalf("api image = %q after rollback, want the original", image)
	}
}

func TestRollbackToAnUnrenderedRevisionIsRefused(t *testing.T) {
	world := newWorld(t)
	world.sync(reconcile.Request{})

	_, err := world.engine.Sync(context.Background(), reconcile.Request{
		AppID: world.appID, PrincipalID: "prn_test", Revision: "0000000000000000000000000000000000000000",
	})
	if err == nil {
		t.Fatal("rolled back to a revision that was never rendered")
	}
	if !strings.Contains(err.Error(), "never rendered") {
		t.Errorf("refusal does not explain why: %v", err)
	}
}

// TestSelectiveSyncCarriesDependencies: deploying a service without what it
// depends on produces a broken application, so the dependency comes along.
func TestSelectiveSyncCarriesDependencies(t *testing.T) {
	world := newWorld(t)

	operation := world.sync(reconcile.Request{Services: []string{"api"}})
	if operation.Phase != store.PhaseSucceeded {
		t.Fatalf("selective sync failed: %s %v", operation.Message, operation.Failures)
	}
	// api depends on db, so both are deployed.
	if world.docker.Count() != 2 {
		t.Fatalf("selective sync of api created %d containers, want api and its dependency db",
			world.docker.Count())
	}
}

func TestSelectiveSyncRejectsAnUnknownService(t *testing.T) {
	world := newWorld(t)
	_, err := world.engine.Sync(context.Background(), reconcile.Request{
		AppID: world.appID, PrincipalID: "prn_test", Services: []string{"nonexistent"},
	})
	if err == nil {
		t.Fatal("selective sync accepted a service the application does not declare")
	}
}

// TestFailedRenderLeavesOneFailedOperation is the crash-safety shape in
// miniature: whatever goes wrong, exactly one document records it.
func TestFailedRenderLeavesOneFailedOperation(t *testing.T) {
	world := newWorld(t)
	world.commit("deploy/compose.yaml", "services:\n  web:\n    image: nginx:1.27\n    networks: [frontend]\n",
		"add an unmodelled directive")

	_, err := world.engine.Sync(context.Background(), reconcile.Request{
		AppID: world.appID, PrincipalID: "prn_test",
	})
	if err == nil {
		t.Fatal("synced a compose file with an unmodelled directive")
	}
	if !strings.Contains(err.Error(), "networks") {
		t.Errorf("failure does not name the directive: %v", err)
	}

	history, historyErr := world.engine.History(world.appID, 10)
	if historyErr != nil {
		t.Fatalf("history: %v", historyErr)
	}
	if len(history) != 1 {
		t.Fatalf("a failed sync left %d operation documents, want exactly 1", len(history))
	}
	if history[0].Phase != store.PhaseFailed {
		t.Errorf("phase = %q, want failed", history[0].Phase)
	}
	if history[0].Message == "" {
		t.Error("the failed operation records no reason")
	}
	if history[0].FinishedAt.IsZero() {
		t.Error("the failed operation was never marked finished, so it looks stuck")
	}
}

func TestSuspendedApplicationRefusesToSync(t *testing.T) {
	world := newWorld(t)
	if err := store.In[store.Application](world.storage, store.Applications).
		Patch(world.appID, map[string]any{"suspended": true}); err != nil {
		t.Fatalf("suspend: %v", err)
	}

	if _, err := world.engine.Sync(context.Background(), reconcile.Request{
		AppID: world.appID, PrincipalID: "prn_test",
	}); err == nil {
		t.Fatal("a suspended application synced")
	}
}

// TestRevisionsAreReusedNotReRendered keeps a revision id stable across
// refreshes of the same commit.
func TestRevisionsAreReusedNotReRendered(t *testing.T) {
	world := newWorld(t)

	first, err := world.engine.Refresh(context.Background(), world.appID)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	second, err := world.engine.Refresh(context.Background(), world.appID)
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("the same commit produced two revisions: %s and %s", first.ID, second.ID)
	}

	revisions, err := world.engine.Revisions(world.appID, 10)
	if err != nil {
		t.Fatalf("revisions: %v", err)
	}
	if len(revisions) != 1 {
		t.Fatalf("stored %d revisions for one commit", len(revisions))
	}
}

// TestNoSecretValueIsEverPersisted walks every stored document and looks for
// the resolved value. It is the CI gate's assertion, made at runtime.
func TestNoSecretValueIsEverPersisted(t *testing.T) {
	world := newWorld(t)
	world.sync(reconcile.Request{})

	for _, collection := range []string{store.Revisions, store.Operations, store.Applications, store.LiveState} {
		raw, err := world.storage.DB().FindDocs(collection, map[string]any{})
		if err != nil {
			t.Fatalf("read %s: %v", collection, err)
		}
		if strings.Contains(render(raw), "resolved:") {
			t.Fatalf("a resolved secret value reached %s", collection)
		}
	}

	// It must, however, have reached the container.
	api := containerFor(t, world.docker, "api")
	if !strings.Contains(strings.Join(api.Env, " "), "resolved:vault/checkout#database_url") {
		t.Fatalf("the secret never reached the container: %v", api.Env)
	}
}

// render flattens a FYLO result to searchable text, so the secret scan below
// looks at everything a document holds rather than at fields it remembered to
// name.
func render(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// findContainer looks a service's container up without failing when it is
// absent. A standalone-Docker update is a remove-then-create (an image is
// immutable, so the old container is deleted before the new one exists), so
// a poll watching for the new revision must tolerate the window in between
// rather than treat it as a missing service. Callers asserting on a settled
// state use containerFor, which fails on absence.
func findContainer(engine *dockertest.Engine, service string) (dockertest.Container, bool) {
	engine.Mu.Lock()
	defer engine.Mu.Unlock()
	for _, container := range engine.Containers {
		if container.Labels[docker.LabelService] == service {
			return *container, true
		}
	}
	return dockertest.Container{}, false
}

func containerFor(t *testing.T, engine *dockertest.Engine, service string) dockertest.Container {
	t.Helper()
	engine.Mu.Lock()
	defer engine.Mu.Unlock()
	for _, container := range engine.Containers {
		if container.Labels[docker.LabelService] == service {
			return *container
		}
	}
	t.Fatalf("no container for service %q", service)
	return dockertest.Container{}
}

// TestPrivateRegistryCredentialsReachThePullAndNothingElse is the Phase 1
// blocking requirement: a private image deploys, and no credential value
// reaches any persisted document.
func TestPrivateRegistryCredentialsReachThePullAndNothingElse(t *testing.T) {
	world := newWorld(t)

	if _, err := store.In[store.Registry](world.storage, store.Registries).Put(store.Registry{
		Project: "alpha", Name: "ghcr", Server: "ghcr.io",
		Username: "deploy-bot", PasswordRef: "vault/registry#ghcr_token",
	}); err != nil {
		t.Fatalf("store registry: %v", err)
	}

	world.commit("deploy/compose.yaml", `services:
  api:
    image: ghcr.io/example/private-api:1.0.0
  public:
    image: nginx:1.27
`, "deploy a private image")

	if operation := world.sync(reconcile.Request{}); operation.Phase != store.PhaseSucceeded {
		t.Fatalf("sync failed: %s %v", operation.Message, operation.Failures)
	}

	// The private image was pulled with the credential; the public one was not.
	private := world.docker.AuthFor("ghcr.io/example/private-api:1.0.0")
	if private == nil {
		t.Fatal("the private image was pulled anonymously")
	}
	if private.Username != "deploy-bot" || private.Password != "resolved:vault/registry#ghcr_token" {
		t.Errorf("registry credential = %+v", private)
	}
	if public := world.docker.AuthFor("nginx:1.27"); public != nil {
		t.Errorf("a public image was pulled with a credential: %+v", public)
	}

	// And the resolved token exists nowhere durable.
	for _, collection := range []string{
		store.Registries, store.Revisions, store.Operations, store.Applications, store.LiveState,
	} {
		raw, err := world.storage.DB().FindDocs(collection, map[string]any{})
		if err != nil {
			t.Fatalf("read %s: %v", collection, err)
		}
		if strings.Contains(render(raw), "resolved:vault/registry") {
			t.Fatalf("a resolved registry credential reached %s", collection)
		}
	}
}

// TestTargetScopedRegistryBeatsAProjectWideOne: the more specific binding
// wins, so a per-host credential is not shadowed by a project default.
func TestTargetScopedRegistryBeatsAProjectWideOne(t *testing.T) {
	world := newWorld(t)

	application, err := store.In[store.Application](world.storage, store.Applications).Get(world.appID)
	if err != nil {
		t.Fatalf("read application: %v", err)
	}
	registries := store.In[store.Registry](world.storage, store.Registries)
	if _, err := registries.Put(store.Registry{
		Project: "alpha", Name: "shared", Server: "ghcr.io",
		Username: "project-wide", PasswordRef: "vault/registry#shared",
	}); err != nil {
		t.Fatalf("store project registry: %v", err)
	}
	if _, err := registries.Put(store.Registry{
		Project: "alpha", Name: "host", Server: "ghcr.io", TargetID: application.TargetID,
		Username: "target-scoped", PasswordRef: "vault/registry#host",
	}); err != nil {
		t.Fatalf("store target registry: %v", err)
	}

	world.commit("deploy/compose.yaml",
		"services:\n  api:\n    image: ghcr.io/example/private-api:1.0.0\n", "private image")
	if operation := world.sync(reconcile.Request{}); operation.Phase != store.PhaseSucceeded {
		t.Fatalf("sync failed: %s %v", operation.Message, operation.Failures)
	}

	auth := world.docker.AuthFor("ghcr.io/example/private-api:1.0.0")
	if auth == nil || auth.Username != "target-scoped" {
		t.Fatalf("the project-wide registry shadowed the target-scoped one: %+v", auth)
	}
}

// setPolicy writes a sync policy onto the world's application.
func (w *world) setPolicy(policy store.SyncPolicy) {
	w.t.Helper()
	if err := store.In[store.Application](w.storage, store.Applications).
		Patch(w.appID, map[string]any{"sync_policy": policy}); err != nil {
		w.t.Fatalf("set policy: %v", err)
	}
}

// tick runs exactly one pass of the auto-sync loop, which is what a test
// wants: Run's ticker would make the test wait on wall-clock time.
func (w *world) tick() {
	w.t.Helper()
	auto := &reconcile.Auto{
		Engine: w.engine,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	auto.Once(context.Background())
}

// TestSelfHealRestoresAnOutOfBandRemoval is the Phase 2 exit criterion: kill a
// container by hand and the loop puts it back, with nobody asking.
func TestSelfHealRestoresAnOutOfBandRemoval(t *testing.T) {
	world := newWorld(t)
	world.sync(reconcile.Request{})
	world.setPolicy(store.SyncPolicy{SelfHeal: true})

	world.docker.RemoveByService("db")
	if world.status().SyncStatus != diff.OutOfSync {
		t.Fatal("removing a container did not register as drift")
	}

	world.tick()

	if status := world.status().SyncStatus; status != diff.Synced {
		t.Fatalf("self-heal left the application %q", status)
	}
	// And it is attributable: the operation says the policy did it, not a
	// principal who was not there.
	operations, err := world.engine.History(world.appID, 1)
	if err != nil || len(operations) == 0 {
		t.Fatalf("history: %v", err)
	}
	if operations[0].ReasonCode != "self_heal" {
		t.Errorf("reason code = %q, want self_heal", operations[0].ReasonCode)
	}
	if operations[0].PrincipalID != "" {
		t.Errorf("an automated sync was attributed to principal %q", operations[0].PrincipalID)
	}
}

// TestAutoSyncFollowsANewCommit is the other half of the same loop.
func TestAutoSyncFollowsANewCommit(t *testing.T) {
	world := newWorld(t)
	world.sync(reconcile.Request{})
	world.setPolicy(store.SyncPolicy{Automated: true})

	world.writeFile("deploy/compose.yaml",
		strings.Replace(baseCompose, "ghcr.io/example/api:1.4.2", "ghcr.io/example/api:1.5.0", 1))
	world.runUpstrm("add", ".")
	world.runUpstrm("commit", "-m", "bump api")

	world.tick()

	summary := world.status()
	if summary.SyncStatus != diff.Synced {
		t.Fatalf("auto-sync left the application %q", summary.SyncStatus)
	}
	if operations, _ := world.engine.History(world.appID, 1); len(operations) == 0 ||
		operations[0].ReasonCode != "auto_sync" {
		t.Errorf("the new commit was not applied by auto-sync")
	}
}

// TestAutoSyncRespectsConsent: drift alone is not permission. An application
// that opted into neither policy is left exactly as it is, and one that opted
// into only self-heal does not silently start following git.
func TestAutoSyncRespectsConsent(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		policy store.SyncPolicy
	}{
		{"no policy at all", store.SyncPolicy{}},
		{"self-heal only, so a new commit is not its business", store.SyncPolicy{SelfHeal: true}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			world := newWorld(t)
			world.sync(reconcile.Request{})
			world.setPolicy(testCase.policy)

			world.writeFile("deploy/compose.yaml",
				strings.Replace(baseCompose, "ghcr.io/example/api:1.4.2", "ghcr.io/example/api:9.9.9", 1))
			world.runUpstrm("add", ".")
			world.runUpstrm("commit", "-m", "bump api")

			world.tick()

			if world.status().SyncStatus != diff.OutOfSync {
				t.Fatal("an application that did not opt in was synced anyway")
			}
		})
	}
}

// TestSuspendedApplicationIsNeverAutoSynced: suspend is the switch an operator
// reaches for during an incident, and it must beat every policy.
func TestSuspendedApplicationIsNeverAutoSynced(t *testing.T) {
	world := newWorld(t)
	world.sync(reconcile.Request{})
	world.setPolicy(store.SyncPolicy{Automated: true, SelfHeal: true})
	if err := store.In[store.Application](world.storage, store.Applications).
		Patch(world.appID, map[string]any{"suspended": true}); err != nil {
		t.Fatalf("suspend: %v", err)
	}

	world.docker.RemoveByService("db")
	world.tick()

	if world.status().SyncStatus == diff.Synced {
		t.Fatal("a suspended application was healed anyway")
	}
}

// remoteWorld turns the world's target into an agent-managed one and gives
// the test an agent it can switch on and off: the loop is the real
// Poll/Complete protocol against the real docker adapter, minus the TLS that
// internal/agent proves elsewhere.
func (w *world) remote() *dispatch.Dispatcher {
	w.t.Helper()
	dispatcher := dispatch.New(30 * time.Second)
	w.engine.Dispatcher = dispatcher
	app, err := store.In[store.Application](w.storage, store.Applications).Get(w.appID)
	if err != nil {
		w.t.Fatalf("get app: %v", err)
	}
	if err := store.In[store.Target](w.storage, store.Targets).
		Patch(app.TargetID, map[string]any{"agent_id": "agt-test"}); err != nil {
		w.t.Fatalf("patch target: %v", err)
	}
	dispatcher.SetDrain(w.engine.Resume, w.engine.Expire)
	return dispatcher
}

// runAgentOnce polls once and executes one job the way the real agent does.
func (w *world) runAgentOnce(dispatcher *dispatch.Dispatcher, targetID string) bool {
	w.t.Helper()
	job, got := dispatcher.Poll(context.Background(), targetID, 2*time.Second)
	if !got {
		return false
	}
	outcome := agent.ExecuteJob(context.Background(), w.docker.URL(), job)
	if err := dispatcher.Complete(targetID, outcome); err != nil {
		w.t.Fatalf("complete: %v", err)
	}
	return true
}

// TestOfflineSyncParksAndDrainsOnReconnect is the Phase 3 pending-actions
// exit criterion: a sync against an offline target parks, a second sync
// supersedes it, and the reconnecting agent converges to the newest desired
// revision — one sync, not a backlog.
func TestOfflineSyncParksAndDrainsOnReconnect(t *testing.T) {
	world := newWorld(t)
	dispatcher := world.remote()
	app, _ := store.In[store.Application](world.storage, store.Applications).Get(world.appID)

	// No agent has ever connected: the sync parks rather than failing.
	first, err := world.engine.Sync(context.Background(), reconcile.Request{AppID: world.appID})
	if err != nil {
		t.Fatalf("an offline sync should park, got: %v", err)
	}
	if first.Phase != store.PhasePending || !strings.Contains(first.Message, "parked") {
		t.Fatalf("first sync: phase=%s message=%q", first.Phase, first.Message)
	}

	// The repo moves on while the host is away.
	world.writeFile("deploy/compose.yaml",
		strings.Replace(baseCompose, "ghcr.io/example/api:1.4.2", "ghcr.io/example/api:2.0.0", 1))
	world.runUpstrm("add", ".")
	world.runUpstrm("commit", "-m", "bump while offline")

	// A second sync supersedes the first.
	second, err := world.engine.Sync(context.Background(), reconcile.Request{AppID: world.appID})
	if err != nil {
		t.Fatalf("second offline sync: %v", err)
	}
	operations := store.In[store.Operation](world.storage, store.Operations)
	closed, err := operations.Get(first.ID)
	if err != nil {
		t.Fatalf("get first operation: %v", err)
	}
	if closed.Phase != store.PhaseSuperseded || !strings.Contains(closed.Message, second.ID) {
		t.Fatalf("the first operation was not closed onto the second: phase=%s message=%q",
			closed.Phase, closed.Message)
	}

	// The agent reconnects. Its first poll wakes the drain; the drained sync
	// lands as a job it picks up on a following poll.
	deadline := time.Now().Add(20 * time.Second)
	worked := false
	for time.Now().Before(deadline) {
		if world.runAgentOnce(dispatcher, app.TargetID) {
			worked = true
			break
		}
	}
	if !worked {
		t.Fatal("no drained job ever reached the agent")
	}
	// Keep serving until the drain settles the parked operation.
	for time.Now().Before(deadline) {
		parked, err := operations.Get(second.ID)
		if err != nil {
			t.Fatalf("get parked operation: %v", err)
		}
		if parked.Phase == store.PhaseSuperseded {
			if !strings.Contains(parked.Message, "drained on reconnect") {
				t.Fatalf("parked operation closed oddly: %q", parked.Message)
			}
			break
		}
		world.runAgentOnce(dispatcher, app.TargetID)
	}

	// And the host converged to the newest revision, in one hop.
	if !strings.Contains(render(world.docker.Containers), "api:2.0.0") {
		t.Fatal("the host did not converge to the newest revision")
	}
	if strings.Contains(render(world.docker.Containers), "api:1.4.2") {
		t.Fatal("the host deployed the stale revision on its way to the new one")
	}
}

// TestDryRunAgainstAnOfflineTargetStillFails: there is nothing to defer, and
// "I could not look" must never read as a plan.
func TestDryRunAgainstAnOfflineTargetStillFails(t *testing.T) {
	world := newWorld(t)
	world.remote()

	if _, err := world.engine.Sync(context.Background(), reconcile.Request{
		AppID: world.appID, DryRun: true,
	}); err == nil {
		t.Fatal("a dry run against an offline target succeeded")
	}
}

// TestReparkRebuildsAfterRestart: the operation documents are durable; only
// the in-memory references die with the process.
func TestReparkRebuildsAfterRestart(t *testing.T) {
	world := newWorld(t)
	dispatcher := world.remote()
	app, _ := store.In[store.Application](world.storage, store.Applications).Get(world.appID)

	parked, err := world.engine.Sync(context.Background(), reconcile.Request{AppID: world.appID})
	if err != nil {
		t.Fatalf("park: %v", err)
	}

	// "Restart": a fresh dispatcher with empty parking, then Repark.
	restarted := dispatch.New(30 * time.Second)
	world.engine.Dispatcher = restarted
	restarted.SetDrain(world.engine.Resume, world.engine.Expire)
	if err := world.engine.Repark(); err != nil {
		t.Fatalf("repark: %v", err)
	}
	if entries := restarted.ParkedFor(app.TargetID); len(entries) != 1 || entries[0].OperationID != parked.ID {
		t.Fatalf("repark rebuilt %+v", entries)
	}
	_ = dispatcher
}

// TestFanOutHaltsOnFailure is the Phase 4 fan-out exit criterion: one
// application deploys to a 50-host group, and a failure on host 7 halts the
// rollout with the remaining hosts untouched.
func TestFanOutHaltsOnFailure(t *testing.T) {
	world := newWorld(t)

	// Fifty hosts, each its own fake Engine. Host 07 refuses every pull.
	const fleet = 50
	engines := make([]*dockertest.Engine, fleet)
	targets := store.In[store.Target](world.storage, store.Targets)
	for i := 0; i < fleet; i++ {
		engines[i] = dockertest.New()
		t.Cleanup(engines[i].Close)
		if i == 6 {
			engines[i].FailPull = "ghcr.io/example/api"
		}
		if _, err := targets.Put(store.Target{
			Project: "alpha", Name: fmt.Sprintf("host-%02d", i+1),
			Provider: "docker", Endpoint: engines[i].URL(),
			Tags: map[string]string{"fleet": "edge"},
		}); err != nil {
			t.Fatalf("create target %d: %v", i, err)
		}
	}
	groupID, err := store.In[store.TargetGroup](world.storage, store.TargetGroups).Put(store.TargetGroup{
		Project: "alpha", Name: "edge", Selector: map[string]string{"fleet": "edge"},
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	// Point the application at the group instead of its single target, and
	// deploy strictly one host at a time so "hosts 8–50 untouched" is exact.
	if err := store.In[store.Application](world.storage, store.Applications).Patch(world.appID, map[string]any{
		"group_id": groupID, "target_id": "",
		"sync_policy": store.SyncPolicy{MaxParallel: 1, FailureThreshold: 1},
	}); err != nil {
		t.Fatalf("patch app: %v", err)
	}

	umbrella, err := world.engine.Sync(context.Background(), reconcile.Request{AppID: world.appID})
	if err == nil {
		t.Fatal("a rollout with a failing host reported success")
	}
	if !strings.Contains(umbrella.Message, "halted") {
		t.Fatalf("umbrella message: %q", umbrella.Message)
	}
	if !strings.Contains(umbrella.Message, "6 deployed") || !strings.Contains(umbrella.Message, "43 of 50 hosts untouched") {
		t.Fatalf("the halt arithmetic is wrong: %q", umbrella.Message)
	}

	// Hosts 1–6 run the app; host 7 failed mid-apply; hosts 8–50 are
	// literally untouched — no containers, not even an attempted pull.
	for i := 0; i < 6; i++ {
		if engines[i].Count() == 0 {
			t.Fatalf("host-%02d should have deployed", i+1)
		}
	}
	for i := 7; i < fleet; i++ {
		if engines[i].Count() != 0 {
			t.Fatalf("host-%02d was touched after the halt", i+1)
		}
		engines[i].Mu.Lock()
		pulls := len(engines[i].Pulled)
		engines[i].Mu.Unlock()
		if pulls != 0 {
			t.Fatalf("host-%02d saw a pull after the halt", i+1)
		}
	}

	// Every member sync wrote its own operation, and the failed host's
	// operation names the pull that broke it.
	operations, err := world.engine.History(world.appID, 100)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	perTarget := 0
	for _, operation := range operations {
		if strings.HasPrefix(operation.TargetID, "group:") {
			continue
		}
		perTarget++
	}
	if perTarget != 7 {
		t.Fatalf("expected 7 per-target operations (6 ok + 1 failed), found %d", perTarget)
	}
}

// TestOutboundWebhookFiresOnSyncCompletion with a verifiable signature.
func TestOutboundWebhookFiresOnSyncCompletion(t *testing.T) {
	world := newWorld(t)

	received := make(chan *http.Request, 1)
	var body []byte
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		received <- r
	}))
	defer sink.Close()

	if _, err := store.In[reconcile.OutboundWebhook](world.storage, store.OutboundWebhooks).
		Put(reconcile.OutboundWebhook{
			Project: "alpha", URL: sink.URL, SecretRef: "local/hook",
		}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	world.engine.Secrets = func(_ context.Context, ref string) (string, error) {
		return "hook-secret", nil
	}

	world.sync(reconcile.Request{})

	select {
	case request := <-received:
		signature := request.Header.Get("X-Heimdall-Signature-256")
		mac := hmac.New(sha256.New, []byte("hook-secret"))
		mac.Write(body)
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if signature != want {
			t.Fatalf("signature %q, want %q", signature, want)
		}
		if !strings.Contains(string(body), `"event":"sync.succeeded"`) {
			t.Fatalf("payload: %s", body[:200])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the webhook never fired")
	}
}

// TestSOPSReferenceDecryptsFromTheApplyingRevision is the SwarmCD-style
// in-repo secret, end to end: an age key in the deployment directory, a
// sops-encrypted YAML committed beside the compose file, a ${secret:sops:...}
// reference — and the decrypted value inside the container, having existed
// only in the pipe between the sops binary and the provider call.
func TestSOPSReferenceDecryptsFromTheApplyingRevision(t *testing.T) {
	for _, tool := range []string{"sops", "age-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH: %v", tool, err)
		}
	}
	world := newWorld(t)

	// The deployment directory carries the age key, like every other key.
	deployment := t.TempDir()
	if err := os.MkdirAll(filepath.Join(deployment, "keys"), 0o700); err != nil {
		t.Fatalf("mkdir keys: %v", err)
	}
	keyPath := filepath.Join(deployment, "keys", "age.key")
	if out, err := exec.Command("age-keygen", "-o", keyPath).CombinedOutput(); err != nil {
		t.Fatalf("age-keygen: %v: %s", err, out)
	}
	keyFile, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read age key: %v", err)
	}
	var recipient string
	for _, line := range strings.Split(string(keyFile), "\n") {
		if rest, found := strings.CutPrefix(line, "# public key: "); found {
			recipient = strings.TrimSpace(rest)
		}
	}
	if recipient == "" {
		t.Fatal("age-keygen output named no public key")
	}

	// Encrypt outside the repo, commit only ciphertext.
	plain := filepath.Join(t.TempDir(), "secrets.yaml")
	if err := os.WriteFile(plain, []byte("database_url: postgres://user:hunter2@db:5432/checkout\n"), 0o600); err != nil {
		t.Fatalf("write plaintext: %v", err)
	}
	encrypted := filepath.Join(world.upstream, "deploy", "secrets.enc.yaml")
	if out, err := exec.Command("sops", "--encrypt", "--age", recipient, "--output", encrypted, plain).CombinedOutput(); err != nil {
		t.Fatalf("sops encrypt: %v: %s", err, out)
	}
	world.commit("deploy/compose.yaml", strings.Replace(baseCompose,
		"${secret:vault/checkout#database_url}",
		"${secret:sops/secrets.enc.yaml#database_url}", 1), "sops secret")

	// The real resolver replaces the stub, on the engine and the adapter.
	resolver := &secrets.Resolver{Deployment: deployment}
	world.engine.Secrets = resolver.Resolve
	world.engine.Providers["docker"].(*docker.Provider).SecretResolver = resolver.Resolve

	operation := world.sync(reconcile.Request{})
	if operation.Phase != store.PhaseSucceeded {
		t.Fatalf("sync: %s: %s", operation.Phase, operation.Message)
	}

	api := containerFor(t, world.docker, "api")
	joined := strings.Join(api.Env, " ")
	if !strings.Contains(joined, "DATABASE_URL=postgres://user:hunter2@db:5432/checkout") {
		t.Fatalf("the decrypted value never reached the container: %v", api.Env)
	}

	// The ciphertext, not the value, is what the stored revision carries.
	revisions, err := store.In[store.Revision](world.storage, store.Revisions).Find(nil)
	if err != nil {
		t.Fatalf("find revisions: %v", err)
	}
	for _, revision := range revisions {
		canonical, err := spec.Canonical(revision.Spec)
		if err != nil {
			t.Fatalf("canonical: %v", err)
		}
		if strings.Contains(string(canonical), "hunter2") {
			t.Fatal("a decrypted value entered a stored revision")
		}
	}
}

// TestWebhookNudgeIsScopedToTheRepository: a push nudge syncs only the
// applications of the repository that was pushed, from the Auto loop's own
// goroutine — someone else's webhook must not deploy this application.
func TestWebhookNudgeIsScopedToTheRepository(t *testing.T) {
	world := newWorld(t)
	world.sync(reconcile.Request{})
	world.setPolicy(store.SyncPolicy{Automated: true})
	world.commit("deploy/compose.yaml",
		strings.Replace(baseCompose, "api:1.4.2", "api:1.5.0", 1), "bump api")

	operations := func() int {
		stored, err := store.In[store.Operation](world.storage, store.Operations).Find(nil)
		if err != nil {
			t.Fatalf("find operations: %v", err)
		}
		return len(stored)
	}
	before := operations()

	auto := &reconcile.Auto{
		Engine:   world.engine,
		Interval: time.Hour, // the ticker must not fire during the test
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { auto.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	// A nudge for a repository this application does not use is a no-op.
	auto.Nudge("rep_someone_elses")
	time.Sleep(400 * time.Millisecond)
	if got := operations(); got != before {
		t.Fatalf("a foreign repository's nudge ran %d operations", got-before)
	}

	// The right repository's nudge syncs the moved revision.
	app, err := store.In[store.Application](world.storage, store.Applications).Get(world.appID)
	if err != nil {
		t.Fatalf("get app: %v", err)
	}
	auto.Nudge(app.RepoID)
	// The operation document exists before the apply finishes, so the
	// evidence is the running container, not the record.
	deadline := time.Now().Add(10 * time.Second)
	for {
		// The service is briefly absent mid-replace; that is the rollout, not
		// a failure. Only a settled container carrying the new image ends the
		// wait.
		if api, ok := findContainer(world.docker, "api"); ok && strings.Contains(api.Image, "1.5.0") {
			break
		}
		if time.Now().After(deadline) {
			api, _ := findContainer(world.docker, "api")
			t.Fatalf("the nudged sync never deployed the new revision: %q", api.Image)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if operations() == before {
		t.Fatal("the sync deployed but recorded no operation")
	}
}

// TestSecretHintsPutRotationIntoTheHash: re-encrypting a sops file must
// change the service's content hash — the hint is what turns a value-only
// commit into a planned update instead of a noop.
func TestSecretHintsPutRotationIntoTheHash(t *testing.T) {
	build := func(ciphertext string) spec.DeploySpec {
		deploy := spec.DeploySpec{
			App: "checkout", Revision: "r1",
			Services: []spec.Service{{
				Name: "api", Image: "ghcr.io/example/api:1",
				Secrets: []spec.SecretMount{
					{Name: "db", Ref: "sops/secrets.enc.yaml#db"},
					{Name: "vaulted", Ref: "aws-sm/eu-west-1/other"},
				},
			}},
		}
		deploy.Normalize()
		if err := reconcile.StampSecretHints(context.Background(), &deploy,
			func(context.Context, string) ([]byte, error) { return []byte(ciphertext), nil }); err != nil {
			t.Fatalf("stamp: %v", err)
		}
		return deploy
	}

	first := build("ciphertext-one")
	rotated := build("ciphertext-two")

	if first.Services[0].Secrets[0].ContentHint == "" {
		t.Fatal("the sops mount got no hint")
	}
	if first.Services[0].Secrets[1].ContentHint != "" {
		t.Fatal("a non-repo reference must not be hinted; its store has no revision to pin")
	}
	hashOne, err := spec.HashService(first.Services[0])
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	hashTwo, err := spec.HashService(rotated.Services[0])
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if hashOne == hashTwo {
		t.Fatal("re-encrypted ciphertext did not change the service hash; rotation would plan as a noop")
	}

	// A reference the repo cannot answer fails the stamp, loudly.
	broken := build("x")
	if err := reconcile.StampSecretHints(context.Background(), &broken,
		func(context.Context, string) ([]byte, error) { return nil, errors.New("no such file") }); err == nil {
		t.Fatal("an unanswerable sops reference must fail the refresh")
	}
}

// TestAdapterForDispatchesAgentTargets: the observability routes ask the
// engine which adapter an application's reads go through. They used to pick
// the local adapter by provider name, so an agent-managed target answered
// "docker engine unreachable at http://docker" from a control plane that
// has no such socket — while the same application's status, resolved here,
// read fine. Reads and writes resolve identically.
func TestAdapterForDispatchesAgentTargets(t *testing.T) {
	w := newWorld(t)

	// The local target resolves to the local adapter.
	adapter, target, err := w.engine.AdapterFor(w.appID)
	if err != nil {
		t.Fatalf("adapter for a local target: %v", err)
	}
	if _, remote := adapter.(*dispatch.Remote); remote {
		t.Fatal("a target with no agent must not resolve to the dispatcher")
	}
	if target.AgentID != "" {
		t.Fatalf("unexpected agent on the local target: %q", target.AgentID)
	}

	// Give the target an agent, and the same call must dispatch instead.
	w.engine.Dispatcher = dispatch.New(time.Second)
	if err := store.In[store.Target](w.storage, store.Targets).Patch(target.ID, map[string]any{
		"agent_id": "agt-1",
	}); err != nil {
		t.Fatalf("patch target: %v", err)
	}
	adapter, target, err = w.engine.AdapterFor(w.appID)
	if err != nil {
		t.Fatalf("adapter for an agent target: %v", err)
	}
	if _, remote := adapter.(*dispatch.Remote); !remote {
		t.Fatalf("an agent-managed target resolved to %T; reads would dial a runtime this host cannot see", adapter)
	}
	if target.AgentID != "agt-1" {
		t.Fatalf("target lost its agent: %+v", target)
	}
}
