package registry

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"path"
	"slices"
	"time"

	"github.com/d31ma/heimdall/internal/git"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/store"
)

// Engine reconciles the root repository's manifest against the hd-*
// collections. It writes registry documents and nothing else: no principal,
// no grant, no secret value, no agent — the authority boundary ADR 0010
// draws.
type Engine struct {
	Store    *store.Store
	CacheDir string
	Logger   *slog.Logger
	Now      func() time.Time
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

// Result summarises one registry sync.
type Result struct {
	Commit  string               `json:"commit"`
	Changes []provider.Operation `json:"changes"`
	// Skipped counts deletions the binding's prune setting withheld.
	Skipped int `json:"skipped"`
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
	result := Result{}
	change := func(kind provider.OperationKind, what string) {
		result.Changes = append(result.Changes, provider.Operation{Kind: kind, Service: what})
	}

	// --- projects ---------------------------------------------------------
	projects := store.In[store.Project](e.Store, store.Projects)
	existingProjects, err := projects.Find(nil)
	if err != nil {
		return result, err
	}
	projectByName := map[string]store.Project{}
	for _, project := range existingProjects {
		projectByName[project.Name] = project
	}
	declaredProjects := map[string]bool{}
	for _, decl := range manifest.Projects {
		declaredProjects[decl.Name] = true
		existing, ok := projectByName[decl.Name]
		switch {
		case !ok:
			id, err := projects.Put(store.Project{
				Name: decl.Name, Description: decl.Description,
				ManagedBy: store.ManagedByRegistry, CreatedAt: e.now(),
			})
			if err != nil {
				return result, err
			}
			projectByName[decl.Name] = store.Project{ID: id, Name: decl.Name, ManagedBy: store.ManagedByRegistry}
			change(provider.OpCreate, "project:"+decl.Name)
		case existing.Description != decl.Description || existing.ManagedBy != store.ManagedByRegistry:
			if err := projects.Patch(existing.ID, map[string]any{
				"description": decl.Description, "managed_by": store.ManagedByRegistry,
			}); err != nil {
				return result, err
			}
			change(provider.OpUpdate, "project:"+decl.Name)
		}
	}

	// --- repositories -----------------------------------------------------
	repos := store.In[store.Repository](e.Store, store.Repos)
	existingRepos, err := repos.Find(nil)
	if err != nil {
		return result, err
	}
	repoByName := map[string]store.Repository{}
	for _, repo := range existingRepos {
		repoByName[repo.Project+"/"+repo.Name] = repo
	}
	declaredRepos := map[string]bool{}
	for _, decl := range manifest.Repositories {
		key := decl.Project + "/" + decl.Name
		declaredRepos[key] = true
		existing, ok := repoByName[key]
		want := store.Repository{
			Project: decl.Project, Name: decl.Name, URL: decl.URL,
			DefaultRef: decl.DefaultRef, CredentialRef: decl.CredentialRef,
			RequireSignature: decl.RequireSignature, WebhookSecretRef: decl.WebhookSecretRef,
			ManagedBy: store.ManagedByRegistry,
		}
		switch {
		case !ok:
			want.CreatedAt = e.now()
			id, err := repos.Put(want)
			if err != nil {
				return result, err
			}
			want.ID = id
			repoByName[key] = want
			change(provider.OpCreate, "repository:"+key)
		case existing.URL != want.URL || existing.DefaultRef != want.DefaultRef ||
			existing.CredentialRef != want.CredentialRef ||
			existing.RequireSignature != want.RequireSignature ||
			existing.WebhookSecretRef != want.WebhookSecretRef ||
			existing.ManagedBy != store.ManagedByRegistry:
			if err := repos.Patch(existing.ID, map[string]any{
				"url": want.URL, "default_ref": want.DefaultRef,
				"credential_ref": want.CredentialRef, "require_signature": want.RequireSignature,
				"webhook_secret_ref": want.WebhookSecretRef, "managed_by": store.ManagedByRegistry,
			}); err != nil {
				return result, err
			}
			want.ID = existing.ID
			repoByName[key] = want
			change(provider.OpUpdate, "repository:"+key)
		}
	}

	// --- targets ----------------------------------------------------------
	targets := store.In[store.Target](e.Store, store.Targets)
	existingTargets, err := targets.Find(nil)
	if err != nil {
		return result, err
	}
	targetByName := map[string]store.Target{}
	for _, target := range existingTargets {
		targetByName[target.Project+"/"+target.Name] = target
	}
	declaredTargets := map[string]bool{}
	for _, decl := range manifest.Targets {
		key := decl.Project + "/" + decl.Name
		declaredTargets[key] = true
		existing, ok := targetByName[key]
		switch {
		case !ok:
			id, err := targets.Put(store.Target{
				Project: decl.Project, Name: decl.Name, Provider: decl.Provider,
				Region: decl.Region, Endpoint: decl.Endpoint, CredentialRef: decl.CredentialRef,
				Tags: decl.Tags, Config: decl.Config,
				ManagedBy: store.ManagedByRegistry, CreatedAt: e.now(),
			})
			if err != nil {
				return result, err
			}
			targetByName[key] = store.Target{ID: id, Project: decl.Project, Name: decl.Name}
			change(provider.OpCreate, "target:"+key)
		case existing.Provider != decl.Provider || existing.Region != decl.Region ||
			existing.Endpoint != decl.Endpoint || existing.CredentialRef != decl.CredentialRef ||
			!maps.Equal(existing.Config, decl.Config) || !maps.Equal(existing.Tags, decl.Tags) ||
			existing.ManagedBy != store.ManagedByRegistry:
			// An agent-enrolled target keeps its AgentID: enrollment is not
			// declarable (ADR 0007), so the sync never touches it.
			if err := targets.Patch(existing.ID, map[string]any{
				"provider": decl.Provider, "region": decl.Region,
				"endpoint": decl.Endpoint, "credential_ref": decl.CredentialRef,
				"config": decl.Config, "tags": decl.Tags,
				"managed_by": store.ManagedByRegistry,
			}); err != nil {
				return result, err
			}
			change(provider.OpUpdate, "target:"+key)
		}
	}

	// --- applications -----------------------------------------------------
	applications := store.In[store.Application](e.Store, store.Applications)
	existingApps, err := applications.Find(nil)
	if err != nil {
		return result, err
	}
	appByName := map[string]store.Application{}
	for _, app := range existingApps {
		appByName[app.Project+"/"+app.Name] = app
	}
	declaredApps := map[string]bool{}
	for _, decl := range manifest.Applications {
		key := decl.Project + "/" + decl.Name
		declaredApps[key] = true
		repo, ok := repoByName[decl.Project+"/"+decl.Repository]
		if !ok {
			return result, fmt.Errorf("HD0270: application %q resolves no repository", decl.Name)
		}
		target, ok := targetByName[decl.Project+"/"+decl.Target]
		if !ok {
			return result, fmt.Errorf("HD0270: application %q resolves no target", decl.Name)
		}
		policy := store.SyncPolicy{
			Automated: decl.SyncPolicy.Automated,
			SelfHeal:  decl.SyncPolicy.SelfHeal,
			Prune:     decl.SyncPolicy.Prune,
		}
		existing, ok := appByName[key]
		switch {
		case !ok:
			_, err := applications.Put(store.Application{
				Project: decl.Project, Name: decl.Name,
				RepoID: repo.ID, TargetID: target.ID,
				Path: decl.Path, Ref: decl.Ref, Overlays: decl.Overlays,
				Variables: decl.Variables, SyncPolicy: policy, Suspended: decl.Suspended,
				ManagedBy: store.ManagedByRegistry, CreatedAt: e.now(),
			})
			if err != nil {
				return result, err
			}
			change(provider.OpCreate, "application:"+key)
		case existing.RepoID != repo.ID || existing.TargetID != target.ID ||
			existing.Path != decl.Path || existing.Ref != decl.Ref ||
			!slices.Equal(existing.Overlays, decl.Overlays) ||
			!maps.Equal(existing.Variables, decl.Variables) ||
			existing.Suspended != decl.Suspended || existing.SyncPolicy != policy ||
			existing.ManagedBy != store.ManagedByRegistry:
			if err := applications.Patch(existing.ID, map[string]any{
				"repo_id": repo.ID, "target_id": target.ID,
				"path": decl.Path, "ref": decl.Ref,
				"overlays": decl.Overlays, "variables": decl.Variables,
				"sync_policy": policy, "suspended": decl.Suspended,
				"managed_by": store.ManagedByRegistry,
			}); err != nil {
				return result, err
			}
			change(provider.OpUpdate, "application:"+key)
		}
	}

	// --- prune-gated deletion, applications first -------------------------
	for key, app := range appByName {
		if declaredApps[key] || app.ManagedBy != store.ManagedByRegistry {
			continue
		}
		if !binding.Prune {
			result.Skipped++
			continue
		}
		if err := applications.Delete(app.ID); err != nil {
			return result, err
		}
		change(provider.OpDelete, "application:"+key)
	}
	for key, target := range targetByName {
		if declaredTargets[key] || target.ManagedBy != store.ManagedByRegistry {
			continue
		}
		if !binding.Prune {
			result.Skipped++
			continue
		}
		if err := targets.Delete(target.ID); err != nil {
			return result, err
		}
		change(provider.OpDelete, "target:"+key)
	}
	for key, repo := range repoByName {
		if declaredRepos[key] || repo.ManagedBy != store.ManagedByRegistry {
			continue
		}
		if !binding.Prune {
			result.Skipped++
			continue
		}
		if err := repos.Delete(repo.ID); err != nil {
			return result, err
		}
		change(provider.OpDelete, "repository:"+key)
	}
	for name, project := range projectByName {
		if declaredProjects[name] || project.ManagedBy != store.ManagedByRegistry {
			continue
		}
		if !binding.Prune {
			result.Skipped++
			continue
		}
		if err := projects.Delete(project.ID); err != nil {
			return result, err
		}
		change(provider.OpDelete, "project:"+name)
	}
	return result, nil
}
