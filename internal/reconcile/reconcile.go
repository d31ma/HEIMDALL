// Package reconcile is the vertical slice: a commit becomes a rendered spec,
// a spec becomes a plan, and a plan becomes running containers — with one
// document recording the whole thing.
//
// Phase 1 runs a sync synchronously, on request. Auto-sync and self-heal in
// Phase 2 add a queue consumer in front of the same Sync call; nothing below
// changes when they do, which is the point of writing it this way.
package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/d31ma/heimdall/internal/diff"
	"github.com/d31ma/heimdall/internal/dispatch"
	"github.com/d31ma/heimdall/internal/git"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/render"
	"github.com/d31ma/heimdall/internal/secrets"
	"github.com/d31ma/heimdall/internal/spec"
	"github.com/d31ma/heimdall/internal/store"
)

// Engine performs refreshes and syncs.
type Engine struct {
	Store *store.Store
	// Providers is keyed by provider name. An application whose target names
	// an unregistered provider fails at plan time with that name, not with a
	// nil dereference.
	Providers map[string]provider.Provider
	// ApplyContext lets an adapter receive what its Apply needs beyond the
	// plan. It is keyed by provider name for the same reason.
	ApplyContext map[string]func(ctx context.Context, params ApplyParams) context.Context
	// Logger receives what must not be silent — above all a patch failure,
	// because a lost patch is how the API said "succeeded" while the store
	// kept "planning" forever. Nil falls back to slog's default.
	Logger *slog.Logger

	// Secrets resolves a ${secret:...} reference to a value, in process and at
	// apply time only. Nil means no secret manager is configured; a spec that
	// needs one then fails at apply rather than starting a container with the
	// variable missing.
	Secrets func(ctx context.Context, ref string) (string, error)
	// Dispatcher hands work to agents. When it is set, a target naming an
	// agent resolves to a remote provider instead of a local one, and every
	// path above — refresh, plan, diff, sync, rollback — is unchanged.
	Dispatcher *dispatch.Dispatcher
	// CacheDir holds git mirrors. It must be on a local filesystem.
	CacheDir string
	// Now is injectable so tests do not race the clock.
	Now func() time.Time
}

// patchOperation advances an operation's state machine and refuses to be
// silent about failure: the document is the record an operator reads, and a
// record that quietly stopped advancing is worse than a loud error.
func (e *Engine) patchOperation(id string, changes map[string]any) {
	if err := store.In[store.Operation](e.Store, store.Operations).Patch(id, changes); err != nil {
		logger := e.Logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Error("operation patch lost; the stored record is now behind the truth",
			"operation", id, "error", err)
	}
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now().UTC()
}

// Request is one sync or dry run.
type Request struct {
	AppID       string
	PrincipalID string
	// PolicyVersion and ReasonCode come from the authorization decision at
	// the boundary and are stamped into the operation document, so "why was
	// this allowed in March" is answerable without replaying policy.
	PolicyVersion int64
	ReasonCode    string
	// DryRun produces a plan and applies nothing.
	DryRun bool
	// Services restricts the sync to a subset. Empty means all.
	Services []string
	// Revision overrides the application's ref, which is how a rollback
	// re-applies a stored revision without touching git.
	Revision string
	// TargetOverride deploys to this target instead of the application's
	// own. Fan-out sets it for each member of a group; nothing else does.
	TargetOverride string
	// Prune overrides the application's policy for this one sync. Nil uses
	// the policy.
	Prune *bool
}

// ApplyParams is everything an adapter's Apply needs beyond the plan. It is
// provider-neutral on purpose: an adapter translates it into its own options
// type, and the reconciler never learns an adapter's shape.
type ApplyParams struct {
	Spec  spec.DeploySpec
	Prune bool
	// Registries resolves pull credentials for this application on this
	// target, at apply time only.
	Registries provider.RegistryResolver
}

// resolved is everything one operation needs, gathered once.
type resolved struct {
	app        store.Application
	repository store.Repository
	target     store.Target
	adapter    provider.Provider
	// remote is true when the adapter forwards to an agent. Only the apply
	// context differs; every other call site is identical.
	remote bool
}

func (e *Engine) resolve(appID string) (resolved, error) {
	return e.resolveFor(appID, "")
}

func (e *Engine) resolveFor(appID, targetOverride string) (resolved, error) {
	var out resolved

	app, err := store.In[store.Application](e.Store, store.Applications).Get(appID)
	if err != nil {
		return out, err
	}
	repository, err := store.In[store.Repository](e.Store, store.Repos).Get(app.RepoID)
	if err != nil {
		return out, fmt.Errorf("HD0250: application %s names repository %s: %w", app.Name, app.RepoID, err)
	}
	targetID := app.TargetID
	if targetOverride != "" {
		targetID = targetOverride
	}
	if targetID == "" {
		return out, fmt.Errorf(
			"HD0267: application %s deploys to group %s; this operation needs a single target",
			app.Name, app.GroupID)
	}
	target, err := store.In[store.Target](e.Store, store.Targets).Get(targetID)
	if err != nil {
		return out, fmt.Errorf("HD0251: application %s names target %s: %w", app.Name, targetID, err)
	}
	adapter, ok := e.Providers[target.Provider]
	if !ok {
		return out, fmt.Errorf("HD0252: no adapter registered for provider %q", target.Provider)
	}

	context := resolved{app: app, repository: repository, target: target, adapter: adapter}

	// A target behind an agent is not reachable from here. It resolves to a
	// provider that round-trips through the dispatcher, so nothing above this
	// line knows the difference.
	if target.AgentID != "" {
		if e.Dispatcher == nil {
			return out, fmt.Errorf(
				"HD0261: target %s is managed by an agent, but this control plane accepts none", target.Name)
		}
		context.adapter = &dispatch.Remote{
			Dispatcher: e.Dispatcher,
			Target:     target.Ref(),
			// The capability answer comes from the local adapter of the same
			// kind, so plan-time rejection works with the agent offline.
			Capability: adapter.Capabilities(),
			Secrets:    e.Secrets,
			Registries: e.registryResolver(context),
		}
		context.remote = true
	}
	return context, nil
}

// StampSecretHints digests the ciphertext behind every in-repo (sops) file
// secret into the spec, before hashing. The hint is what makes rotation
// plan: a re-encrypted secret changes the ciphertext, the hint, the service
// hash, and therefore the plan — without it, a value-only commit would plan
// as a noop and never reach the runtime. Only encrypted bytes are hashed;
// nothing secret enters the document. A reference the repo cannot answer
// fails the refresh: a revision that cannot name its own secrets is not a
// revision to store.
func StampSecretHints(ctx context.Context, rendered *spec.DeploySpec, read func(context.Context, string) ([]byte, error)) error {
	for si := range rendered.Services {
		for mi := range rendered.Services[si].Secrets {
			mount := &rendered.Services[si].Secrets[mi]
			rest, found := strings.CutPrefix(mount.Ref, "sops/")
			if !found {
				continue
			}
			relative, _, _ := strings.Cut(rest, "#")
			ciphertext, err := read(ctx, relative)
			if err != nil {
				return fmt.Errorf("HD0254: secret %q of service %q reads %q at this revision: %w",
					mount.Name, rendered.Services[si].Name, relative, err)
			}
			digest := sha256.Sum256(ciphertext)
			mount.ContentHint = "sha256:" + hex.EncodeToString(digest[:6])
		}
	}
	return nil
}

// Refresh fetches the repository, resolves the ref, renders the spec, and
// stores the revision if it is new. It mutates nothing on the target.
func (e *Engine) Refresh(ctx context.Context, appID string) (store.Revision, error) {
	context, err := e.resolve(appID)
	if err != nil {
		return store.Revision{}, err
	}
	return e.refresh(ctx, context, "")
}

func (e *Engine) refresh(ctx context.Context, resolved resolved, override string) (store.Revision, error) {
	revisions := store.In[store.Revision](e.Store, store.Revisions)

	// A rollback names a revision that is already rendered and stored. Going
	// back to git for it would let a force-push change what a rollback means.
	if override != "" {
		stored, err := revisions.Find(store.Equals("app_id", resolved.app.ID))
		if err != nil {
			return store.Revision{}, err
		}
		for _, revision := range stored {
			if revision.Commit == override || revision.ID == override {
				return revision, nil
			}
		}
		return store.Revision{}, fmt.Errorf(
			"HD0253: revision %q was never rendered for this application; a rollback can only re-apply a stored revision",
			override)
	}

	mirror := git.Repo{
		Dir: path.Join(e.CacheDir, resolved.repository.ID),
		URL: resolved.repository.URL,
	}
	if err := git.Open(ctx, mirror); err != nil {
		return store.Revision{}, err
	}

	ref := resolved.app.Ref
	if ref == "" {
		ref = resolved.repository.DefaultRef
	}
	commit, err := git.Resolve(ctx, mirror, ref)
	if err != nil {
		return store.Revision{}, err
	}

	if resolved.repository.RequireSignature {
		// An optional per-repository gate, checked before anything is
		// rendered so an unsigned commit never reaches a plan.
		if err := git.VerifySignature(ctx, mirror, commit.SHA); err != nil {
			return store.Revision{}, err
		}
		commit.Signed = true
	}

	// A commit already rendered for this application is reused rather than
	// re-rendered: the spec is immutable, so re-rendering could only agree or
	// reveal a bug, and reuse keeps the revision id stable.
	existing, err := revisions.Find(store.Equals("commit", commit.SHA))
	if err != nil {
		return store.Revision{}, err
	}
	for _, revision := range existing {
		if revision.AppID == resolved.app.ID {
			return revision, nil
		}
	}

	files := make([]render.File, 0, len(resolved.app.ComposeFiles()))
	for _, name := range resolved.app.ComposeFiles() {
		body, err := git.ReadFile(ctx, mirror, commit.SHA, path.Join(resolved.app.Path, name))
		if err != nil {
			return store.Revision{}, err
		}
		files = append(files, render.File{Name: name, Data: body})
	}

	rendered, err := render.Render(render.Input{
		App:       resolved.app.Name,
		Revision:  commit.SHA,
		Files:     files,
		Variables: resolved.app.Variables,
	})
	if err != nil {
		return store.Revision{}, err
	}
	if err := StampSecretHints(ctx, &rendered, func(hintCtx context.Context, relative string) ([]byte, error) {
		return git.ReadFile(hintCtx, mirror, commit.SHA, path.Join(resolved.app.Path, relative))
	}); err != nil {
		return store.Revision{}, err
	}
	hash, err := spec.Hash(rendered)
	if err != nil {
		return store.Revision{}, err
	}

	revision := store.Revision{
		AppID:      resolved.app.ID,
		Commit:     commit.SHA,
		Ref:        ref,
		SpecHash:   hash,
		Spec:       rendered,
		Author:     commit.Author,
		Message:    commit.Message,
		Signed:     commit.Signed,
		RenderedAt: e.now(),
	}
	id, err := revisions.Put(revision)
	if err != nil {
		return store.Revision{}, err
	}
	revision.ID = id
	return revision, nil
}

// Status reads live state and reports the diff without changing anything. It
// is what the app detail view and drift detection both call.
func (e *Engine) Status(ctx context.Context, appID string) (diff.Summary, error) {
	resolved, err := e.resolve(appID)
	if err != nil {
		return diff.Summary{}, err
	}
	revision, err := e.refresh(ctx, resolved, "")
	if err != nil {
		return diff.Summary{}, err
	}

	live, liveErr := resolved.adapter.Observe(ctx, resolved.target.Ref(), resolved.app.AppRef())

	var plan provider.Plan
	if liveErr == nil {
		plan, err = resolved.adapter.Plan(ctx, resolved.target.Ref(), revision.Spec)
		if err != nil {
			// A plan-time rejection is a real answer about this revision, not
			// a failed read: the application is out of sync and cannot be
			// synced until the compose file changes.
			summary := diff.Report(resolved.app.AppRef(), resolved.target.ID, revision.Commit,
				provider.Plan{Operations: []provider.Operation{{Kind: provider.OpUpdate}}},
				live, false, nil)
			summary.Services = append(summary.Services, diff.ServiceDiff{
				Service: "*", Message: err.Error(),
			})
			return summary, nil //nolint:nilerr // a plan-time rejection is the diff's answer, not a failed read
		}
	}

	// The field-level diff compares the desired revision against the one the
	// runtime says is deployed, both read from the immutable revision store.
	var deployed spec.DeploySpec
	if live.Revision != "" && live.Revision != revision.Commit {
		if previous, err := e.revisionByCommit(resolved.app.ID, live.Revision); err == nil {
			deployed = previous.Spec
		}
	} else if live.Revision == revision.Commit {
		deployed = revision.Spec
	}

	summary := diff.Report(resolved.app.AppRef(), resolved.target.ID, revision.Commit,
		plan, live, liveErr != nil, diff.Specs(revision.Spec, deployed))

	if liveErr == nil {
		e.cacheLiveState(resolved.app.ID, summary, live)
	}
	return summary, nil
}

func (e *Engine) revisionByCommit(appID, commit string) (store.Revision, error) {
	revisions, err := store.In[store.Revision](e.Store, store.Revisions).Find(store.Equals("commit", commit))
	if err != nil {
		return store.Revision{}, err
	}
	for _, revision := range revisions {
		if revision.AppID == appID {
			return revision, nil
		}
	}
	return store.Revision{}, fmt.Errorf("HD0254: no stored revision %s for application %s", commit, appID)
}

// cacheLiveState writes the projection. A failure here is logged by the
// caller and never fails the read: hd-livestate is a cache, and returning an
// error because a cache write failed would be strictly worse than the cache
// being stale.
func (e *Engine) cacheLiveState(appID string, summary diff.Summary, live provider.LiveState) {
	_, _ = store.In[store.LiveStateDoc](e.Store, store.LiveState).Put(store.LiveStateDoc{
		AppID: appID, Summary: summary, State: live, ReadAt: e.now(),
	})
}

// Sync plans and, unless this is a dry run, applies.
//
// Everything it learns is written to one hd-operations document, patched as
// the state machine advances. A crash at any point leaves that document in a
// known phase rather than leaving four collections disagreeing.
func (e *Engine) Sync(ctx context.Context, request Request) (store.Operation, error) {
	// A group application fans out: one child sync per member target, each
	// with its own operation document, bounded and haltable.
	if request.TargetOverride == "" {
		if app, err := store.In[store.Application](e.Store, store.Applications).Get(request.AppID); err == nil &&
			app.GroupID != "" {
			return e.fanOut(ctx, request, app)
		}
	}

	resolved, err := e.resolveFor(request.AppID, request.TargetOverride)
	if err != nil {
		return store.Operation{}, err
	}
	if resolved.app.Suspended && !request.DryRun {
		return store.Operation{}, fmt.Errorf("HD0255: application %s is suspended", resolved.app.Name)
	}

	operations := store.In[store.Operation](e.Store, store.Operations)
	operation := store.Operation{
		AppID: resolved.app.ID, Project: resolved.app.Project, App: resolved.app.Name,
		TargetID: resolved.target.ID,
		Phase:    PhaseOf(request), DryRun: request.DryRun, Rollback: request.Revision != "",
		PrincipalID:   request.PrincipalID,
		PolicyVersion: request.PolicyVersion,
		ReasonCode:    request.ReasonCode,
		StartedAt:     e.now(),
	}
	id, err := operations.Put(operation)
	if err != nil {
		return store.Operation{}, err
	}
	operation.ID = id

	// fail records the failure on the operation document and returns it, so
	// every exit from this function leaves one consistent record.
	fail := func(cause error) (store.Operation, error) {
		operation.Phase = store.PhaseFailed
		operation.Message = cause.Error()
		operation.FinishedAt = e.now()
		e.patchOperation(id, map[string]any{
			"phase": string(store.PhaseFailed), "message": operation.Message,
			"finished_at": operation.FinishedAt,
		})
		return operation, cause
	}

	revision, err := e.refresh(ctx, resolved, request.Revision)
	if err != nil {
		return fail(err)
	}
	operation.Revision = revision.Commit
	operation.RevisionID = revision.ID
	operation.SpecHash = revision.SpecHash

	// What is deployed now, recorded before anything changes so a rollback
	// target is always in the record.
	if live, err := resolved.adapter.Observe(ctx, resolved.target.Ref(), resolved.app.AppRef()); err == nil {
		operation.PreviousRevision = live.Revision
	}

	e.patchOperation(id, map[string]any{
		"phase": string(store.PhasePlanning), "revision": operation.Revision,
		"revision_id": operation.RevisionID, "spec_hash": operation.SpecHash,
		"previous_revision": operation.PreviousRevision,
	})

	want := revision.Spec
	if len(request.Services) > 0 {
		// A selective sync narrows the spec before planning, so the plan an
		// operator sees is exactly what will run.
		if want, err = selectServices(want, request.Services); err != nil {
			return fail(err)
		}
	}

	plan, err := resolved.adapter.Plan(ctx, resolved.target.Ref(), want)
	if err != nil {
		// An offline agent is normal operation for an outbound-only fleet,
		// not a failure: the sync parks and runs on reconnect. A dry run
		// still fails honestly — there is nothing to defer, and "I could not
		// look" must never read as a plan.
		if resolved.remote && !request.DryRun && errors.Is(err, dispatch.ErrNoAgent) {
			return e.park(operation, resolved)
		}
		return fail(err)
	}
	operation.Operations = plan.Operations

	if request.DryRun {
		operation.Phase = store.PhaseSucceeded
		operation.FinishedAt = e.now()
		operation.Message = "dry run; nothing was applied"
		e.patchOperation(id, map[string]any{
			"phase": string(store.PhaseSucceeded), "operations": plan.Operations,
			"message": operation.Message, "finished_at": operation.FinishedAt,
		})
		return operation, nil
	}

	prune := resolved.app.SyncPolicy.Prune
	if request.Prune != nil {
		prune = *request.Prune
	}

	// sops references decrypt ciphertext from the applying revision's own
	// commit — app-relative, read from the bare mirror, never a checkout.
	// The commit SHA pins the content, so a rollback decrypts exactly the
	// ciphertext its revision was rendered from and a force-push cannot
	// change what a stored revision means. Both the direct adapters and the
	// agent dispatcher resolve on this context, control-plane side.
	sopsMirror := git.Repo{
		Dir: path.Join(e.CacheDir, resolved.repository.ID),
		URL: resolved.repository.URL,
	}
	sopsCommit := revision.Commit
	sopsBase := resolved.app.Path
	ctx = secrets.WithSource(ctx, secrets.Source{
		Read: func(readCtx context.Context, relative string) ([]byte, error) {
			return git.ReadFile(readCtx, sopsMirror, sopsCommit, path.Join(sopsBase, relative))
		},
	})

	var applyCtx context.Context
	if resolved.remote {
		applyCtx = dispatch.WithApply(ctx, dispatch.ApplyOptions{Spec: want, Prune: prune})
	} else {
		applyContext, ok := e.ApplyContext[resolved.target.Provider]
		if !ok {
			return fail(fmt.Errorf("HD0256: no apply context registered for provider %q", resolved.target.Provider))
		}
		applyCtx = applyContext(ctx, ApplyParams{
			Spec: want, Prune: prune, Registries: e.registryResolver(resolved),
		})
	}

	e.patchOperation(id, map[string]any{
		"phase": string(store.PhaseApplying), "operations": plan.Operations,
	})

	result, err := resolved.adapter.Apply(applyCtx, resolved.target.Ref(), plan)
	if err != nil {
		return fail(err)
	}

	operation.Applied = result.Applied
	operation.Failures = result.Failures
	operation.FinishedAt = e.now()
	operation.Phase = store.PhaseSucceeded
	if len(result.Failures) > 0 {
		operation.Phase = store.PhaseFailed
		operation.Message = fmt.Sprintf("%d service(s) failed", len(result.Failures))
	}
	e.patchOperation(id, map[string]any{
		"phase": string(operation.Phase), "applied": result.Applied,
		"failures": result.Failures, "message": operation.Message,
		"finished_at": operation.FinishedAt,
	})

	e.Notify(operation)
	if operation.Phase == store.PhaseFailed {
		return operation, fmt.Errorf("HD0257: sync of %s completed with failures: %v",
			resolved.app.Name, result.Failures)
	}
	return operation, nil
}

// PhaseOf is the phase a request starts in.
func PhaseOf(request Request) store.OperationPhase {
	if request.DryRun {
		return store.PhasePlanning
	}
	return store.PhasePending
}

// History returns this application's operations, newest first.
func (e *Engine) History(appID string, limit int) ([]store.Operation, error) {
	operations, err := store.In[store.Operation](e.Store, store.Operations).Find(store.Equals("app_id", appID))
	if err != nil {
		return nil, err
	}
	// Find returns ascending TTID order, which is chronological; the UI wants
	// the reverse.
	sort.Slice(operations, func(i, j int) bool {
		return operations[i].StartedAt.After(operations[j].StartedAt)
	})
	if limit > 0 && len(operations) > limit {
		operations = operations[:limit]
	}
	return operations, nil
}

// Revisions returns this application's stored revisions, newest first. They
// are the rollback targets.
// AdapterFor answers which provider an application's reads must go through,
// and the target they address. A target behind an agent resolves to the
// dispatching provider, exactly as every write path already does — the
// observability routes used to pick the local adapter by provider name and
// dial a Docker socket the control plane does not have.
func (e *Engine) AdapterFor(appID string) (provider.Provider, store.Target, error) {
	resolved, err := e.resolve(appID)
	if err != nil {
		return nil, store.Target{}, err
	}
	return resolved.adapter, resolved.target, nil
}

func (e *Engine) Revisions(appID string, limit int) ([]store.Revision, error) {
	revisions, err := store.In[store.Revision](e.Store, store.Revisions).Find(store.Equals("app_id", appID))
	if err != nil {
		return nil, err
	}
	sort.Slice(revisions, func(i, j int) bool {
		return revisions[i].RenderedAt.After(revisions[j].RenderedAt)
	})
	if limit > 0 && len(revisions) > limit {
		revisions = revisions[:limit]
	}
	return revisions, nil
}

// selectServices narrows a spec to a chosen subset, keeping every dependency
// of a chosen service. Deploying a service without what it depends on is a
// selective sync that produces a broken application.
func selectServices(want spec.DeploySpec, chosen []string) (spec.DeploySpec, error) {
	declared := map[string]spec.Service{}
	for _, service := range want.Services {
		declared[service.Name] = service
	}

	keep := map[string]bool{}
	var include func(name string) error
	include = func(name string) error {
		if keep[name] {
			return nil
		}
		service, ok := declared[name]
		if !ok {
			return fmt.Errorf("HD0258: %q is not a service of this application", name)
		}
		keep[name] = true
		for _, dependency := range service.DependsOn {
			if err := include(dependency); err != nil {
				return err
			}
		}
		return nil
	}
	// Render already rejected dependency cycles, so this recursion terminates.
	for _, name := range chosen {
		if err := include(name); err != nil {
			return spec.DeploySpec{}, err
		}
	}

	narrowed := want
	narrowed.Services = nil
	for _, service := range want.Services {
		if keep[service.Name] {
			narrowed.Services = append(narrowed.Services, service)
		}
	}
	if len(narrowed.Services) == 0 {
		return spec.DeploySpec{}, errors.New("HD0259: a selective sync must choose at least one service")
	}
	narrowed.Normalize()
	return narrowed, nil
}

// registryResolver builds the pull-credential lookup for one application.
//
// Registries are looked up per project, narrowed to the target when one is
// named, and the longest matching server wins so a specific host beats a
// project-wide Docker Hub entry. The password is resolved from its reference
// here, in process, at apply time — it exists for the duration of one pull
// and is never returned, stored, or logged.
func (e *Engine) registryResolver(resolved resolved) provider.RegistryResolver {
	if e.Secrets == nil {
		// With no secret resolver a private registry cannot be used. Returning
		// nil means "every image is public", which is correct and lets a
		// deployment that uses none work; a private image then fails at the
		// pull with the registry's own message.
		return nil
	}
	return func(ctx context.Context, image string) (*provider.RegistryCredential, error) {
		registries, err := store.In[store.Registry](e.Store, store.Registries).
			Find(store.Equals("project", resolved.app.Project))
		if err != nil {
			return nil, err
		}

		var best *store.Registry
		for i := range registries {
			candidate := registries[i]
			if candidate.TargetID != "" && candidate.TargetID != resolved.target.ID {
				continue
			}
			if !candidate.Matches(image) {
				continue
			}
			// A target-scoped entry is more specific than a project-wide one.
			if best == nil || (best.TargetID == "" && candidate.TargetID != "") {
				best = &candidate
			}
		}
		if best == nil {
			return nil, nil
		}
		password, err := e.Secrets(ctx, best.PasswordRef)
		if err != nil {
			return nil, fmt.Errorf("HD0260: resolve credential %q for registry %s: %w",
				best.PasswordRef, best.Server, err)
		}
		return &provider.RegistryCredential{
			Server: best.Server, Username: best.Username, Password: password,
		}, nil
	}
}
