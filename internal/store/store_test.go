package store_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/store"
)

func requireFylo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("fylo"); err != nil {
		t.Skipf("fylo binary not on PATH: %v", err)
	}
}

func TestOpenRefusesASyncFilesystem(t *testing.T) {
	// FYLO's atomicity depends on local rename semantics that a file-sync
	// client does not provide, and the failure mode is silent corruption
	// found much later. Refusing loudly is the cheap half of that trade.
	roots := []string{
		"/Users/someone/Dropbox/heimdall",
		"/Users/someone/Library/CloudStorage/Dropbox/heimdall",
		"/Users/someone/OneDrive/heimdall",
	}
	for _, root := range roots {
		_, err := store.Open(root, "")
		if err == nil {
			t.Fatalf("opened a root under a sync tree: %s", root)
		}
		if !strings.Contains(err.Error(), "HD0012") {
			t.Errorf("root %s failed with %v, want the HD0012 sync-filesystem refusal", root, err)
		}
	}
}

func TestOpenRequiresARoot(t *testing.T) {
	if _, err := store.Open("", ""); err == nil {
		t.Fatal("opened an empty root")
	}
}

func TestBootstrapIsIdempotent(t *testing.T) {
	requireFylo(t)
	root := filepath.Join(t.TempDir(), "fylo-root")

	first, err := store.Open(root, "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Re-running Bootstrap is what every boot does; it must not fail on
	// collections that already exist.
	if err := first.Bootstrap(); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second, err := store.Open(root, "")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = second.Close() }()
}

// TestCollectionsAreNamespaced guards the shared-root contract with SESAME: a
// collision would put HEIMDALL state and identity state in one document space.
func TestCollectionsAreNamespaced(t *testing.T) {
	if len(store.Collections) == 0 {
		t.Fatal("no collections declared")
	}
	seen := map[string]bool{}
	for _, name := range store.Collections {
		if !strings.HasPrefix(name, "hd-") {
			t.Errorf("collection %q is not namespaced hd-*", name)
		}
		if seen[name] {
			t.Errorf("collection %q is declared twice", name)
		}
		seen[name] = true
	}
	// A projection listed as authoritative would make a rebuild destroy data.
	for _, projection := range store.Projections {
		for _, authoritative := range store.Authoritative {
			if projection == authoritative {
				t.Errorf("%q is listed as both a projection and authoritative; "+
					"Rebuild drops projections, so this would delete real state", projection)
			}
		}
	}
}

func TestRebuildRequiresAFold(t *testing.T) {
	requireFylo(t)
	storage, err := store.Open(filepath.Join(t.TempDir(), "fylo-root"), "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = storage.Close() }()

	if err := storage.Rebuild(nil); err == nil {
		t.Fatal("rebuilt projections with no fold, which would silently empty them")
	}
}

// TestRebuildRegeneratesProjectionsFromOperations is the crash-safety property
// in miniature: authoritative operations survive, derived views are thrown
// away and reproduced.
func TestRebuildRegeneratesProjectionsFromOperations(t *testing.T) {
	requireFylo(t)
	storage, err := store.Open(filepath.Join(t.TempDir(), "fylo-root"), "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = storage.Close() }()

	if _, err := storage.DB().PutData(store.Operations, map[string]any{
		"app": "checkout", "revision": "a1b2c3d", "outcome": "synced",
	}); err != nil {
		t.Fatalf("write operation: %v", err)
	}

	fold := func(id string, operation map[string]any) (string, []map[string]any, error) {
		return store.LiveState, []map[string]any{{
			"app": operation["app"], "revision": operation["revision"],
		}}, nil
	}
	if err := storage.Rebuild(fold); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	rebuilt, err := storage.DB().FindDocs(store.LiveState, map[string]any{})
	if err != nil {
		t.Fatalf("read projection: %v", err)
	}
	rows, ok := rebuilt.(map[string]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("projection holds %v, want exactly one folded document", rebuilt)
	}

	// Running it twice must not double the projection.
	if err := storage.Rebuild(fold); err != nil {
		t.Fatalf("second rebuild: %v", err)
	}
	rebuilt, err = storage.DB().FindDocs(store.LiveState, map[string]any{})
	if err != nil {
		t.Fatalf("re-read projection: %v", err)
	}
	if rows, ok := rebuilt.(map[string]any); !ok || len(rows) != 1 {
		t.Fatalf("projection holds %v after a second rebuild, want exactly one", rebuilt)
	}
}

// TestGroupMembershipIsDerivedFromTags is the Phase 2 exit criterion: a tag
// change moves a host between groups with no edit to either group.
func TestGroupMembershipIsDerivedFromTags(t *testing.T) {
	eu := store.TargetGroup{Selector: map[string]string{"region": "eu"}}
	us := store.TargetGroup{Selector: map[string]string{"region": "us"}}

	host := store.Target{Tags: map[string]string{"region": "eu", "tier": "edge"}}
	if !eu.Matches(host) || us.Matches(host) {
		t.Fatal("a host did not start in the group its tags name")
	}

	// One retag, no write to either group.
	host.Tags["region"] = "us"
	if eu.Matches(host) || !us.Matches(host) {
		t.Fatal("retagging did not move the host between groups")
	}

	// Every pair must match, or a group widens the moment someone adds a
	// second selector key expecting it to narrow.
	both := store.TargetGroup{Selector: map[string]string{"region": "us", "tier": "core"}}
	if both.Matches(host) {
		t.Error("a selector matched a host missing one of its pairs")
	}

	// An empty selector matches nothing. Matching everything would silently
	// point a fleet-wide operation at the whole estate.
	if (store.TargetGroup{}).Matches(host) {
		t.Error("an empty selector matched a target")
	}
}

// TestFindNormalisesPlainQueries pins the fix for a real defect: FYLO
// ignores a plain {field: value} query rather than rejecting it, which
// turned "find this project's groups" into "find every group". Two of the
// first callers hit it.
func TestFindNormalisesPlainQueries(t *testing.T) {
	if _, err := exec.LookPath("fylo"); err != nil {
		t.Skipf("fylo binary not on PATH: %v", err)
	}
	storage, err := store.Open(filepath.Join(t.TempDir(), "fylo-root"), "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	targets := store.In[store.Target](storage, store.Targets)
	if _, err := targets.Put(store.Target{Project: "alpha", Name: "edge", Provider: "docker"}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := targets.Put(store.Target{Project: "beta", Name: "edge", Provider: "docker"}); err != nil {
		t.Fatalf("put: %v", err)
	}

	mine, err := targets.Find(map[string]any{"project": "alpha"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(mine) != 1 || mine[0].Project != "alpha" {
		t.Fatalf("a plain one-field query returned %d rows", len(mine))
	}

	// Two fields AND together.
	both, err := targets.Find(map[string]any{"project": "beta", "name": "edge"})
	if err != nil {
		t.Fatalf("find two fields: %v", err)
	}
	if len(both) != 1 || both[0].Project != "beta" {
		t.Fatalf("a two-field query returned %d rows", len(both))
	}
	if none, _ := targets.Find(map[string]any{"project": "alpha", "name": "core"}); len(none) != 0 {
		t.Fatalf("a mismatched two-field query returned %d rows", len(none))
	}
}

// TestPatchPersistsTypedNestedArrays pins the defect the UI audit found: a
// patch carrying a typed slice of structs (every sync's final phase patch
// carries []Operation) was silently rejected by FYLO's array rule, so the
// API returned "succeeded" while the store kept "planning" forever.
func TestPatchPersistsTypedNestedArrays(t *testing.T) {
	if _, err := exec.LookPath("fylo"); err != nil {
		t.Skipf("fylo binary not on PATH: %v", err)
	}
	storage, err := store.Open(filepath.Join(t.TempDir(), "fylo-root"), "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	operations := store.In[store.Operation](storage, store.Operations)
	id, err := operations.Put(store.Operation{
		AppID: "app-1", Project: "p", App: "a", Phase: store.PhasePlanning,
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	err = operations.Patch(id, map[string]any{
		"phase": string(store.PhaseSucceeded),
		"operations": []provider.Operation{
			{Kind: provider.OpCreate, Service: "web", Reason: "not running"},
			{Kind: provider.OpNoop, Service: "cache"},
		},
		"applied": []provider.Operation{{Kind: provider.OpCreate, Service: "web"}},
	})
	if err != nil {
		t.Fatalf("the typed-slice patch errored: %v", err)
	}

	stored, err := operations.Get(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Phase != store.PhaseSucceeded {
		t.Fatalf("stored phase = %q; the patch was silently dropped", stored.Phase)
	}
	if len(stored.Operations) != 2 || stored.Operations[0].Service != "web" {
		t.Fatalf("the nested array did not round-trip: %+v", stored.Operations)
	}
}
