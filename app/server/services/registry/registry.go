package registry

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"time"

	"github.com/d31ma/heimdall/app/server/helpers/git"
	"github.com/d31ma/heimdall/app/server/repositories/store"
	"github.com/d31ma/heimdall/app/server/services/provider"
)

// Engine reconciles the root repository's manifest against the hd-*
// collections. It writes registry documents and nothing else: no principal,
// no grant, no secret value, no agent — the authority boundary ADR 0010
// draws.
type Engine struct {
	Store     *store.Store
	Providers map[string]provider.Provider
	CacheDir  string
	Logger    *slog.Logger
	Now       func() time.Time
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now().UTC()
}

func (e *Engine) log() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}

// Binding returns the root binding, if one exists.
func (e *Engine) Binding() (store.RootBinding, bool, error) {
	bindings, err := store.In[store.RootBinding](e.Store, store.RootRepo).Find(nil)
	if err != nil || len(bindings) == 0 {
		return store.RootBinding{}, false, err
	}
	return bindings[0], true, nil
}

// Bind records the root repository. It is the one interactive act: at most
// one binding exists, and re-binding replaces it.
func (e *Engine) Bind(binding store.RootBinding) (store.RootBinding, error) {
	if binding.URL == "" {
		return store.RootBinding{}, fmt.Errorf("HD0271: a root binding needs a repository url")
	}
	collection := store.In[store.RootBinding](e.Store, store.RootRepo)
	existing, _, err := e.Binding()
	if err != nil {
		return store.RootBinding{}, err
	}
	if existing.ID != "" {
		if err := collection.Delete(existing.ID); err != nil {
			return store.RootBinding{}, err
		}
	}
	binding.BoundAt = e.now()
	id, err := collection.Put(binding)
	if err != nil {
		return store.RootBinding{}, err
	}
	binding.ID = id
	return binding, nil
}

// Unbind removes the binding. Managed documents keep their managed_by stamp
// deliberately: unbinding must not silently hand a fleet of applications
// back to interactive mutation — rebinding restores the authority, and an
// operator who truly wants them interactive re-registers them.
func (e *Engine) Unbind() error {
	existing, found, err := e.Binding()
	if err != nil || !found {
		return err
	}
	return store.In[store.RootBinding](e.Store, store.RootRepo).Delete(existing.ID)
}

// SyncIfBound is the Auto loop's entry: nothing bound is nothing to do,
// and a failing sync is a log line, not a dead loop.
func (e *Engine) SyncIfBound(ctx context.Context) {
	_, found, err := e.Binding()
	if err != nil || !found {
		return
	}
	if _, err := e.Sync(ctx, ""); err != nil {
		e.log().Warn("registry sync failed; the manifest and the store disagree until it succeeds", "error", err)
	}
}

// Sync reads the manifest at the bound ref and closes the gap to the
// collections. One operation document records the whole pass, exactly as a
// workload sync records one.
func (e *Engine) Sync(ctx context.Context, principalID string) (Result, error) {
	binding, found, err := e.Binding()
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{}, fmt.Errorf("HD0271: no root repository is bound; `registry bind` is the one interactive act")
	}

	mirror := git.Repo{Dir: path.Join(e.CacheDir, "root-"+binding.ID), URL: binding.URL}
	if err := git.Open(ctx, mirror); err != nil {
		return Result{}, err
	}
	ref := binding.Ref
	if ref == "" {
		ref = "HEAD"
	}
	commit, err := git.Resolve(ctx, mirror, ref)
	if err != nil {
		return Result{}, err
	}
	if binding.RequireSignature {
		if err := git.VerifySignature(ctx, mirror, commit.SHA); err != nil {
			return Result{}, err
		}
	}
	raw, err := git.ReadFile(ctx, mirror, commit.SHA, path.Join(binding.Path, ManifestFile))
	if err != nil {
		return Result{}, err
	}
	manifest, err := Parse(raw)
	if err != nil {
		return Result{}, err
	}

	operations := store.In[store.Operation](e.Store, store.Operations)
	record := store.Operation{
		App: "registry", Phase: store.PhaseApplying,
		Revision: commit.SHA, PrincipalID: principalID,
		ReasonCode: "registry_sync", StartedAt: e.now(),
	}
	recordID, err := operations.Put(record)
	if err != nil {
		return Result{}, err
	}

	result, err := e.apply(binding, manifest)
	result.Commit = commit.SHA

	changes := map[string]any{
		"operations": result.Changes, "finished_at": e.now(),
	}
	if err != nil {
		changes["phase"] = string(store.PhaseFailed)
		changes["message"] = err.Error()
	} else {
		changes["phase"] = string(store.PhaseSucceeded)
		changes["message"] = fmt.Sprintf("registry synced: %d changes, %d deletions withheld by prune=false",
			len(result.Changes), result.Skipped)
	}
	if patchErr := operations.Patch(recordID, changes); patchErr != nil {
		e.log().Error("registry operation patch lost", "operation", recordID, "error", patchErr)
	}
	return result, err
}

// apply is the diff-and-close half. Order matters: projects, then
// repositories and targets, then applications, so every name an application
// resolves already exists. Deletion runs the other way and is prune-gated.
func (e *Engine) apply(binding store.RootBinding, manifest Manifest) (Result, error) {
	return (&manifestApplication{engine: e, binding: binding, manifest: manifest}).Apply()
}

func (e *Engine) applyDocuments(binding store.RootBinding, manifest Manifest) (Result, error) {
	return (&registryDocuments{engine: e, binding: binding, manifest: manifest}).Apply()
}
