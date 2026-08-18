package registry_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d31ma/heimdall/internal/registry"
	"github.com/d31ma/heimdall/internal/store"
)

const manifestV1 = `
projects:
  - name: shop
repositories:
  - project: shop
    name: site
    url: /srv/git/site.git
    default_ref: main
targets:
  - project: shop
    name: local
    provider: docker
    endpoint: http://127.0.0.1:2375
applications:
  - project: shop
    name: site
    repository: site
    target: local
    path: deploy
    sync_policy:
      automated: true
      self_heal: true
`

// world is a real root repository and a real store.
type world struct {
	engine   *registry.Engine
	storage  *store.Store
	upstream string
	run      func(args ...string)
	binding  store.RootBinding
}

func newWorld(t *testing.T) *world {
	t.Helper()
	for _, tool := range []string{"git", "fylo"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH: %v", tool, err)
		}
	}
	root := t.TempDir()
	upstream := filepath.Join(root, "root-repo")
	if err := os.MkdirAll(upstream, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	run := func(args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = upstream
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(upstream, registry.ManifestFile), []byte(manifestV1), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	run("add", ".")
	run("commit", "-m", "registry v1")

	storage, err := store.Open(filepath.Join(root, "fylo-root"), "")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	engine := &registry.Engine{
		Store: storage, CacheDir: filepath.Join(root, "git"),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	binding, err := engine.Bind(store.RootBinding{URL: upstream, Ref: "main", BoundBy: "prn_admin"})
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	return &world{engine: engine, storage: storage, upstream: upstream, run: run, binding: binding}
}

func (w *world) commitManifest(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(w.upstream, registry.ManifestFile), []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	w.run("add", ".")
	w.run("commit", "-m", "registry change")
}

// TestRegistrySyncDeclaresAndConverges is ADR 0010 end to end: the manifest
// becomes documents, a re-run changes nothing, an edit updates in place, and
// deletion waits for prune.
func TestRegistrySyncDeclaresAndConverges(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	result, err := w.engine.Sync(ctx, "prn_admin")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	// 1 project + 1 repository + 1 target + 1 application.
	if len(result.Changes) != 4 {
		t.Fatalf("first sync made %d changes: %+v", len(result.Changes), result.Changes)
	}

	apps, err := store.In[store.Application](w.storage, store.Applications).Find(nil)
	if err != nil || len(apps) != 1 {
		t.Fatalf("applications: %v, %v", apps, err)
	}
	app := apps[0]
	if app.ManagedBy != store.ManagedByRegistry || !app.SyncPolicy.Automated || app.Path != "deploy" {
		t.Fatalf("application: %+v", app)
	}
	if app.RepoID == "" || app.TargetID == "" {
		t.Fatal("the application's names did not resolve to ids")
	}

	// Idempotence: the same manifest converges to zero changes.
	result, err = w.engine.Sync(ctx, "prn_admin")
	if err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	if len(result.Changes) != 0 {
		t.Fatalf("an unchanged manifest made %d changes: %+v", len(result.Changes), result.Changes)
	}

	// An edit updates in place.
	w.commitManifest(t, strings.Replace(manifestV1, "path: deploy", "path: deploy/prod", 1))
	result, err = w.engine.Sync(ctx, "prn_admin")
	if err != nil {
		t.Fatalf("sync after edit: %v", err)
	}
	if len(result.Changes) != 1 {
		t.Fatalf("edit made %d changes: %+v", len(result.Changes), result.Changes)
	}
	apps, _ = store.In[store.Application](w.storage, store.Applications).Find(nil)
	if apps[0].Path != "deploy/prod" {
		t.Fatalf("path = %q", apps[0].Path)
	}

	// Removing the application without prune withholds the deletion.
	withoutApp := strings.Split(manifestV1, "applications:")[0]
	w.commitManifest(t, withoutApp)
	result, err = w.engine.Sync(ctx, "prn_admin")
	if err != nil {
		t.Fatalf("sync without app: %v", err)
	}
	if result.Skipped == 0 {
		t.Fatalf("deletion was not withheld: %+v", result)
	}
	if apps, _ = store.In[store.Application](w.storage, store.Applications).Find(nil); len(apps) != 1 {
		t.Fatal("prune=false deleted an application")
	}

	// With prune, the undeclared application goes.
	w.binding.Prune = true
	if _, err := w.engine.Bind(w.binding); err != nil {
		t.Fatalf("rebind with prune: %v", err)
	}
	if _, err = w.engine.Sync(ctx, "prn_admin"); err != nil {
		t.Fatalf("pruning sync: %v", err)
	}
	if apps, _ = store.In[store.Application](w.storage, store.Applications).Find(nil); len(apps) != 0 {
		t.Fatal("prune=true left the undeclared application")
	}

	// The record: every sync above wrote one operation document.
	operations, err := store.In[store.Operation](w.storage, store.Operations).
		Find(store.Equals("reason_code", "registry_sync"))
	if err != nil || len(operations) != 5 {
		t.Fatalf("registry operations: %d, %v", len(operations), err)
	}
}

// TestRegistryAdoptsInteractiveDocuments: a document registered through the
// API becomes managed when the manifest declares it — visibly, as an update.
func TestRegistryAdoptsInteractiveDocuments(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	// An operator registered this repository interactively, earlier.
	if _, err := store.In[store.Repository](w.storage, store.Repos).Put(store.Repository{
		Project: "shop", Name: "site", URL: "/srv/git/site.git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("seed repo: %v", err)
	}

	if _, err := w.engine.Sync(ctx, "prn_admin"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	repos, _ := store.In[store.Repository](w.storage, store.Repos).Find(nil)
	if len(repos) != 1 || repos[0].ManagedBy != store.ManagedByRegistry {
		t.Fatalf("the declared repository was not adopted: %+v", repos)
	}
}

// TestManifestFailsClosed: an unknown field and a dangling reference are
// each named, and nothing is half-applied.
func TestManifestFailsClosed(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	w.commitManifest(t, manifestV1+"\nfleet:\n  - oops\n")
	if _, err := w.engine.Sync(ctx, "prn_admin"); err == nil || !strings.Contains(err.Error(), "fleet") {
		t.Fatalf("unknown section = %v, want it named", err)
	}

	w.commitManifest(t, strings.Replace(manifestV1, "target: local", "target: ghost", 1))
	if _, err := w.engine.Sync(ctx, "prn_admin"); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("dangling target = %v, want it named", err)
	}
	if apps, _ := store.In[store.Application](w.storage, store.Applications).Find(nil); len(apps) != 0 {
		t.Fatal("a failing manifest still created applications")
	}
}

// TestOverlayChangesPropagate: an overlay (or variable) edit in the
// manifest must patch the application document. The first live staging
// environment found the gap: an overlay removed from the manifest stayed on
// the document, and every render after the file's deletion failed with
// HD0240 — a silently dropped declaration, the exact thing the registry
// promises never to do.
func TestOverlayChangesPropagate(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	withOverlay := strings.Replace(manifestV1, "    path: deploy\n",
		"    path: deploy\n    overlays:\n      - compose.staging.yaml\n", 1)
	w.commitManifest(t, withOverlay)
	if _, err := w.engine.Sync(ctx, "prn_admin"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	apps, _ := store.In[store.Application](w.storage, store.Applications).Find(nil)
	if len(apps) != 1 || len(apps[0].Overlays) != 1 {
		t.Fatalf("overlay never landed: %+v", apps)
	}

	// Removing the overlay must patch the document, not leave it behind.
	w.commitManifest(t, manifestV1)
	result, err := w.engine.Sync(ctx, "prn_admin")
	if err != nil {
		t.Fatalf("sync after removal: %v", err)
	}
	if len(result.Changes) != 1 {
		t.Fatalf("overlay removal made %d changes: %+v", len(result.Changes), result.Changes)
	}
	apps, _ = store.In[store.Application](w.storage, store.Applications).Find(nil)
	if len(apps[0].Overlays) != 0 {
		t.Fatalf("the removed overlay survived on the document: %+v", apps[0].Overlays)
	}
}

// TestTargetConfigChangesPropagate: a target's config and tags are where
// every cloud detail lives — subnets, roles, the load-balancer group, a
// capacity provider. The diff used to compare neither, so a config edit in
// the manifest silently never reached the target document: exactly the
// dropped declaration the registry promises never to make. Found switching
// staging to Fargate Spot, which did nothing until this landed.
func TestTargetConfigChangesPropagate(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	if _, err := w.engine.Sync(ctx, "prn_admin"); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	targetHasKey := func() (store.Target, bool) {
		targets, _ := store.In[store.Target](w.storage, store.Targets).Find(nil)
		if len(targets) != 1 {
			t.Fatalf("expected one target, got %d", len(targets))
		}
		_, ok := targets[0].Config["capacity_provider"]
		return targets[0], ok
	}
	if _, ok := targetHasKey(); ok {
		t.Fatal("the base manifest declares no capacity_provider")
	}

	// Add a config key: the sync must patch the target.
	withConfig := strings.Replace(manifestV1,
		"    endpoint: http://127.0.0.1:2375\n",
		"    endpoint: http://127.0.0.1:2375\n    config:\n      capacity_provider: FARGATE_SPOT\n", 1)
	w.commitManifest(t, withConfig)
	result, err := w.engine.Sync(ctx, "prn_admin")
	if err != nil {
		t.Fatalf("sync after config add: %v", err)
	}
	if len(result.Changes) != 1 {
		t.Fatalf("a config change made %d changes: %+v", len(result.Changes), result.Changes)
	}
	if target, ok := targetHasKey(); !ok || target.Config["capacity_provider"] != "FARGATE_SPOT" {
		t.Fatalf("config never reached the target: %+v", target.Config)
	}

	// Idempotence: an unchanged manifest makes no further change.
	result, err = w.engine.Sync(ctx, "prn_admin")
	if err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	if len(result.Changes) != 0 {
		t.Fatalf("an unchanged config re-planned: %+v", result.Changes)
	}

	// Removing the key must patch it away, not leave it behind.
	w.commitManifest(t, manifestV1)
	if _, err := w.engine.Sync(ctx, "prn_admin"); err != nil {
		t.Fatalf("sync after config removal: %v", err)
	}
	if _, ok := targetHasKey(); ok {
		t.Fatal("the removed config key survived on the target")
	}
}
