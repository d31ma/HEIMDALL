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
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"sort"
	"strconv"
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

	// ConvergeTimeout bounds the post-apply wait for the runtime to actually
	// hold the new state. Zero uses defaultConvergeTimeout. A sync that has
	// not converged by then is a failed sync, not a slow one.
	ConvergeTimeout time.Duration
	// Heartbeat is called while waiting for convergence. A rollout can outrun
	// the queue's visibility lease, and a redelivered half-finished rollout is
	// not a retry, it is an outage.
	// ponytail: nil today — nothing consumes a queue yet; the seam is here so
	// the consumer that lands next extends its own lease without touching this
	// loop.
	Heartbeat func()
}

// defaultConvergeTimeout bounds the wait when none is configured. Long enough
// for an ECS rolling deploy, short enough that a wedged rollout is reported
// while the operator is still watching.
const defaultConvergeTimeout = 5 * time.Minute

func (e *Engine) logger() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}

// patchOperation advances an operation's state machine and refuses to be
// silent about failure: the document is the record an operator reads, and a
// record that quietly stopped advancing is worse than a loud error.
func (e *Engine) patchOperation(id string, changes map[string]any) {
	if err := store.In[store.Operation](e.Store, store.Operations).Patch(id, changes); err != nil {
		e.logger().Error("operation patch lost; the stored record is now behind the truth",
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

	if err := (provider.TargetCheck{Adapter: adapter, Target: target.Ref()}).Definition(); err != nil {
		return out, err
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

	// The activity index is written here, not at the end: an operation that
	// is still applying belongs in the history an operator is watching. Its
	// entry is patched in place as the phase advances.
	position, indexErr := e.Store.AppendActivity(store.EntryOf(operation), operation.AppID)
	if indexErr != nil {
		// A projection is repairable by rebuilding, so a failed index write
		// is logged rather than failing the sync it describes.
		e.logger().Error("activity index write failed; rebuild it to repair",
			"operation", id, "error", indexErr)
	}
	// indexEntry keeps the index in step with the operation document. Both
	// are written from the same value, so a row cannot say one thing here
	// and another there.
	indexEntry := func() {
		if indexErr != nil {
			return
		}
		if err := e.Store.PatchActivity(operation.AppID, position, store.EntryOf(operation)); err != nil {
			e.logger().Error("activity index patch failed; rebuild it to repair",
				"operation", id, "error", err)
		}
	}

	// fail records the failure on the operation document and returns it, so
	// every exit from this function leaves one consistent record.
	fail := func(cause error) (store.Operation, error) {
		operation.Phase = store.PhaseFailed
		operation.Message = cause.Error()
		operation.FinishedAt = e.now()
		defer indexEntry()
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
		defer indexEntry()
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

	applyCtx, err := e.applyContextFor(ctx, resolved, want, prune)
	if err != nil {
		return fail(err)
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

	// "Applied" must mean the runtime holds this state, not that the calls
	// were submitted. A rollout that fails ninety seconds from now is a failed
	// sync, not drift discovered later by a confused operator.
	failed := ""
	if len(result.Failures) > 0 {
		failed = fmt.Sprintf("%d service(s) failed", len(result.Failures))
	} else if convergeErr := e.converge(ctx, resolved, want, plan); convergeErr != nil {
		failed = convergeErr.Error()
	}
	if failed != "" {
		switch failureActionFor(want, plan) {
		case "continue":
			// The spec says carry on. Loud in the log, green in the record —
			// that is what continue asked for.
			e.logger().Warn("rollout did not converge; failure_action is continue",
				"app", resolved.app.Name, "target", resolved.target.ID, "reason", failed)
			failed = ""
		case "rollback":
			if rollbackErr := e.rollback(ctx, resolved, operation.PreviousRevision, prune); rollbackErr != nil {
				failed += "; the rollback also failed: " + rollbackErr.Error()
			} else {
				failed += "; rolled back to " + operation.PreviousRevision
			}
		}
	}
	if failed != "" {
		operation.Phase = store.PhaseFailed
		operation.Message = failed
	}
	e.patchOperation(id, map[string]any{
		"phase": string(operation.Phase), "applied": result.Applied,
		"failures": result.Failures, "message": operation.Message,
		"finished_at": operation.FinishedAt,
	})
	indexEntry()

	e.Notify(operation)
	if operation.Phase == store.PhaseFailed {
		if len(result.Failures) > 0 {
			return operation, fmt.Errorf("HD0257: sync of %s completed with failures: %v",
				resolved.app.Name, result.Failures)
		}
		return operation, fmt.Errorf("HD0257: sync of %s did not converge: %s",
			resolved.app.Name, operation.Message)
	}
	return operation, nil
}

// applyContextFor builds the context one Apply needs: the spec the adapter
// will materialise secrets from, and whether it may prune.
func (e *Engine) applyContextFor(
	ctx context.Context, resolved resolved, want spec.DeploySpec, prune bool,
) (context.Context, error) {
	if resolved.remote {
		return dispatch.WithApply(ctx, dispatch.ApplyOptions{Spec: want, Prune: prune}), nil
	}
	applyContext, ok := e.ApplyContext[resolved.target.Provider]
	if !ok {
		return nil, fmt.Errorf("HD0256: no apply context registered for provider %q", resolved.target.Provider)
	}
	return applyContext(ctx, ApplyParams{
		Spec: want, Prune: prune, Registries: e.registryResolver(resolved),
	}), nil
}

// converge polls the runtime until every service this sync touched reports
// Healthy at the applied spec hash, held for the longest monitor window the
// spec declares. Adapters submit; this is where "submitted" becomes
// "converged".
func (e *Engine) converge(ctx context.Context, resolved resolved, want spec.DeploySpec, plan provider.Plan) error {
	touched := map[string]bool{}
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case provider.OpCreate, provider.OpUpdate, provider.OpRestart:
			touched[operation.Service] = true
		}
	}
	if len(touched) == 0 {
		return nil
	}

	// The hold comes from compose: monitor is how long a replacement must stay
	// healthy to count. Undeclared means one healthy reading is enough — the
	// wait still closes the "Applied means submitted" gap without inventing a
	// delay nobody asked for.
	hold := time.Duration(0)
	for _, service := range want.Services {
		if touched[service.Name] && service.Update != nil && service.Update.MonitorMS > 0 {
			if declared := time.Duration(service.Update.MonitorMS) * time.Millisecond; declared > hold {
				hold = declared
			}
		}
	}
	interval := hold / 5
	switch {
	case interval < 250*time.Millisecond:
		interval = 250 * time.Millisecond
	case interval > 5*time.Second:
		interval = 5 * time.Second
	}

	timeout := e.ConvergeTimeout
	if timeout <= 0 {
		timeout = defaultConvergeTimeout
	}
	deadline := time.Now().Add(timeout + hold)

	var healthySince time.Time
	lastReason := "the runtime never reported the new revision as healthy"
	for {
		if e.Heartbeat != nil {
			e.Heartbeat()
		}
		live, err := resolved.adapter.Observe(ctx, resolved.target.Ref(), resolved.app.AppRef())
		switch {
		case err != nil:
			// A read that failed is not a rollout that failed; the deadline
			// decides. Reporting a transient Observe error as a bad deploy
			// would roll back a healthy release.
			healthySince = time.Time{}
			lastReason = "the runtime could not be read: " + err.Error()
		default:
			if reason := unhealthy(live, touched, plan.SpecHash); reason != "" {
				healthySince = time.Time{}
				lastReason = reason
			} else {
				if healthySince.IsZero() {
					healthySince = time.Now()
				}
				if time.Since(healthySince) >= hold {
					return nil
				}
			}
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("HD0273: %s did not converge within %s: %s",
				resolved.app.Name, timeout, lastReason)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("HD0274: waiting for %s to converge: %w", resolved.app.Name, ctx.Err())
		case <-time.After(interval):
		}
	}
}

// unhealthy names the first service that is not yet holding the applied
// state, or "" when all of them are. A live spec hash that still reads as the
// old one means the runtime has not picked the deploy up yet.
func unhealthy(live provider.LiveState, touched map[string]bool, specHash string) string {
	if live.SpecHash != "" && specHash != "" && live.SpecHash != specHash {
		return "the runtime still reports the previous revision"
	}
	names := make([]string, 0, len(touched))
	for name := range touched {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		state, ok := live.Services[name]
		if !ok {
			return name + " is not running"
		}
		if state.Health != provider.Healthy {
			message := string(state.Health)
			if state.Message != "" {
				message += " (" + state.Message + ")"
			}
			return name + " is " + message
		}
	}
	return ""
}

// failureActionFor is the strictest action any touched service declared.
// Mixing strategies inside one application is legal compose; the safest of
// them decides, because a rollout is applied as a unit.
func failureActionFor(want spec.DeploySpec, plan provider.Plan) string {
	touched := map[string]bool{}
	for _, operation := range plan.Operations {
		touched[operation.Service] = true
	}
	action := ""
	for _, service := range want.Services {
		if !touched[service.Name] || service.Update == nil {
			continue
		}
		switch service.Update.FailureAction {
		case "rollback":
			return "rollback"
		case "pause":
			action = "pause"
		case "continue":
			if action == "" {
				action = "continue"
			}
		}
	}
	if action == "" {
		return "pause"
	}
	return action
}

// rollback re-applies the revision that was running before this sync. The
// immutable hd-revisions collection makes that a lookup rather than a
// re-render, so the rollback deploys exactly the bytes that were serving.
func (e *Engine) rollback(ctx context.Context, resolved resolved, previous string, prune bool) error {
	if previous == "" {
		return errors.New("HD0275: no previous revision was recorded, so there is nothing to roll back to")
	}
	revision, err := e.revisionByCommit(resolved.app.ID, previous)
	if err != nil {
		return err
	}
	plan, err := resolved.adapter.Plan(ctx, resolved.target.Ref(), revision.Spec)
	if err != nil {
		return err
	}
	applyCtx, err := e.applyContextFor(ctx, resolved, revision.Spec, prune)
	if err != nil {
		return err
	}
	result, err := resolved.adapter.Apply(applyCtx, resolved.target.Ref(), plan)
	if err != nil {
		return err
	}
	if len(result.Failures) > 0 {
		return fmt.Errorf("HD0276: rolling back to %s failed: %v", previous, result.Failures)
	}
	return nil
}

// RunService starts one out-of-band execution of a scheduled service. It
// changes no desired state — nothing is planned, nothing is applied, and the
// schedule is untouched — so it records no operation document; the
// authorization decision at the boundary is what hd-audit keeps.
func (e *Engine) RunService(ctx context.Context, appID, service string) error {
	resolved, err := e.resolve(appID)
	if err != nil {
		return err
	}
	if resolved.app.Suspended {
		return fmt.Errorf("HD0255: application %s is suspended", resolved.app.Name)
	}
	runner, ok := resolved.adapter.(provider.Runner)
	if !ok {
		return fmt.Errorf(
			"HD0277: the %s adapter cannot start a run on demand; scheduled workloads are its runtime's job",
			resolved.target.Provider)
	}

	revision, err := e.latestRevision(resolved.app.ID)
	if err != nil {
		return err
	}
	declared, found := revision.Spec.Service(service)
	if !found {
		return fmt.Errorf("HD0278: %s declares no service %q at revision %s",
			resolved.app.Name, service, revision.Commit)
	}
	if declared.Schedule == nil {
		return fmt.Errorf("HD0279: %s/%s is not a scheduled service; only jobs are run on demand",
			resolved.app.Name, service)
	}
	return runner.RunNow(ctx, resolved.target.Ref(), resolved.app.AppRef(), service)
}

// latestRevision is the most recent stored revision of one application — the
// desired state a run executes, which is the same bytes the last sync applied.
func (e *Engine) latestRevision(appID string) (store.Revision, error) {
	revisions, err := e.Revisions(appID, 1)
	if err != nil {
		return store.Revision{}, err
	}
	if len(revisions) == 0 {
		return store.Revision{}, fmt.Errorf("HD0278: application %s has no rendered revision yet", appID)
	}
	return revisions[0], nil
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
	page, err := e.HistoryPage(appID, limit, store.Cursor{})
	return page.Operations, err
}

// Page is one page of a history read, newest first, with the cursor that
// asks for the next one. Truncated says the scan hit its bound before the
// collection ended — a partial answer presented as a complete one is how an
// operator concludes something never happened.
type Page struct {
	Operations []store.Operation
	Revisions  []store.Revision
	NextCursor string
	Truncated  bool
}

// HistoryPage reads one page of an application's operations, newest first,
// older than `before` when it is set. The read is bounded in the store: it
// no longer loads every operation an application ever had in order to show
// ten of them.
// ActivityPage reads an application's history from the projection: a page
// is a chunk read or two, whatever the history's length. The cursor is the
// projection's own position, so it is exact — no row is shown twice or
// skipped when new operations land while an operator is paging.
func (e *Engine) ActivityPage(appID string, limit int, cursor string) (
	entries []store.ActivityEntry, next string, err error,
) {
	from, err := DecodeActivityCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	entries, position, more, err := e.Store.ActivityPage(appID, limit, from)
	if err != nil {
		return nil, "", err
	}
	if more {
		next = EncodeActivityCursor(position)
	}
	return entries, next, nil
}

// EncodeActivityCursor and DecodeActivityCursor carry a chunk position as an
// opaque token. It is durable — a restart does not invalidate it — because
// it names a place in the index rather than a live query.
func EncodeActivityCursor(cursor store.ActivityCursor) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(strconv.Itoa(cursor.Seq) + "." + strconv.Itoa(cursor.Index)))
}

func DecodeActivityCursor(cursor string) (store.ActivityCursor, error) {
	if cursor == "" {
		return store.ActivityCursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return store.ActivityCursor{}, fmt.Errorf("HD0281: cursor %q is not readable", cursor)
	}
	seqText, indexText, found := strings.Cut(string(raw), ".")
	if !found {
		return store.ActivityCursor{}, fmt.Errorf("HD0281: cursor %q does not carry a position", cursor)
	}
	seq, seqErr := strconv.Atoi(seqText)
	index, indexErr := strconv.Atoi(indexText)
	if seqErr != nil || indexErr != nil || seq < 0 || index < 0 {
		return store.ActivityCursor{}, fmt.Errorf("HD0281: cursor %q does not carry a position", cursor)
	}
	return store.ActivityCursor{Seq: seq, Index: index, Set: true}, nil
}

func (e *Engine) HistoryPage(appID string, limit int, before store.Cursor) (Page, error) {
	operations, truncated, err := store.In[store.Operation](e.Store, store.Operations).Newest(
		store.Equals("app_id", appID), limit, before,
		func(operation store.Operation) time.Time { return operation.StartedAt },
		func(operation store.Operation) string { return operation.ID },
	)
	if err != nil {
		return Page{}, err
	}
	page := Page{Operations: operations, Truncated: truncated}
	// A full page means there may be more; a short one is the end. The
	// cursor is the oldest row shown, so the next page continues from it
	// with no overlap and no gap when new work lands in between.
	if len(operations) > 0 && len(operations) == effectiveLimit(limit) {
		last := operations[len(operations)-1]
		page.NextCursor = EncodeCursor(store.Cursor{At: last.StartedAt, ID: last.ID})
	}
	return page, nil
}

// effectiveLimit mirrors the store's clamping so "was this page full?" is
// answered against the limit that was actually applied.
func effectiveLimit(limit int) int {
	if limit <= 0 || limit > store.MaxPage {
		return store.MaxPage
	}
	return limit
}

// EncodeCursor renders a keyset position for a client. It is a timestamp,
// not a store cursor: FYLO's own cursors belong to a live query in one
// process, and a page token that dies with a restart is a page token that
// fails during a deploy.
func EncodeCursor(cursor store.Cursor) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(cursor.At.UTC().Format(time.RFC3339Nano) + "|" + cursor.ID))
}

// DecodeCursor reads one back. An unreadable cursor is an error rather than
// a silent first page: paging that quietly restarts hides rows.
func DecodeCursor(cursor string) (store.Cursor, error) {
	if cursor == "" {
		return store.Cursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return store.Cursor{}, fmt.Errorf("HD0281: cursor %q is not readable", cursor)
	}
	stamp, id, found := strings.Cut(string(raw), "|")
	if !found {
		return store.Cursor{}, fmt.Errorf("HD0281: cursor %q does not carry a position", cursor)
	}
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return store.Cursor{}, fmt.Errorf("HD0281: cursor %q does not carry a timestamp", cursor)
	}
	return store.Cursor{At: at, ID: id}, nil
}

// Revisions returns this application's stored revisions, newest first. They
// are the rollback targets.
func (e *Engine) Revisions(appID string, limit int) ([]store.Revision, error) {
	page, err := e.RevisionsPage(appID, limit, store.Cursor{})
	return page.Revisions, err
}

// RevisionsPage is Revisions with a keyset. It matters more here than for
// operations: a revision carries its whole rendered spec, so loading every
// one of them to show twenty was the largest read in the API.
func (e *Engine) RevisionsPage(appID string, limit int, before store.Cursor) (Page, error) {
	revisions, truncated, err := store.In[store.Revision](e.Store, store.Revisions).Newest(
		store.Equals("app_id", appID), limit, before,
		func(revision store.Revision) time.Time { return revision.RenderedAt },
		func(revision store.Revision) string { return revision.ID },
	)
	if err != nil {
		return Page{}, err
	}
	page := Page{Revisions: revisions, Truncated: truncated}
	if len(revisions) > 0 && len(revisions) == effectiveLimit(limit) {
		last := revisions[len(revisions)-1]
		page.NextCursor = EncodeCursor(store.Cursor{At: last.RenderedAt, ID: last.ID})
	}
	return page, nil
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

func (e *Engine) InvokeService(ctx context.Context, request ServiceInvocation) (provider.Invocation, error) {
	return (&functionInvocation{engine: e, request: request}).Invoke(ctx)
}

func (e *Engine) FunctionMetrics(ctx context.Context, request ServiceObservation) (provider.FunctionSeries, error) {
	return (&functionRead{engine: e, request: request}).Metrics(ctx)
}
func (e *Engine) FunctionLogs(ctx context.Context, request ServiceObservation) (io.ReadCloser, error) {
	return (&functionRead{engine: e, request: request}).Logs(ctx)
}
