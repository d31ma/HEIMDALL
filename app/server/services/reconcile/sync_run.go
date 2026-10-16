package reconcile

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/d31ma/heimdall/app/server/helpers/git"
	"github.com/d31ma/heimdall/app/server/helpers/secrets"
	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/repositories/store"
	"github.com/d31ma/heimdall/app/server/services/dispatch"
	"github.com/d31ma/heimdall/app/server/services/provider"
)

type syncRun struct {
	engine       *Engine
	request      Request
	context      context.Context
	resolved     resolved
	operation    store.Operation
	position     store.ActivityCursor
	indexError   error
	revision     store.Revision
	want         spec.DeploySpec
	plan         provider.Plan
	prune        bool
	applyContext context.Context
	applied      provider.Result
	failure      string
	result       error
	done         bool
}

func (r *syncRun) Run(ctx context.Context) (store.Operation, error) {
	r.context = ctx
	r.perform(r.ReadGroup)
	r.perform(r.Resolve)
	r.perform(r.CheckSuspension)
	r.perform(r.CreateOperation)
	r.perform(r.AppendActivity)
	r.perform(r.Refresh)
	r.perform(r.ReadPreviousRevision)
	r.perform(r.RecordPlanning)
	r.perform(r.SelectServices)
	r.perform(r.Plan)
	r.perform(r.RecordPlan)
	r.perform(r.CompleteDryRun)
	r.perform(r.ConfigureApply)
	r.perform(r.RecordApplying)
	r.perform(r.Apply)
	r.perform(r.CheckConvergence)
	r.perform(r.HandleFailureAction)
	r.perform(r.Finish)
	return r.operation, r.result
}

func (r *syncRun) perform(stage func()) {
	if !r.done {
		stage()
	}
}

func (r *syncRun) ReadGroup() {
	r.readGroup()
}

func (r *syncRun) readGroup() {
	if r.request.TargetOverride != "" {
		return
	}
	app, err := store.In[store.Application](r.engine.Store, store.Applications).Get(r.request.AppID)
	r.FanOut(app, err)
}

func (r *syncRun) FanOut(app store.Application, err error) {
	r.fanOut(app, err)
}

func (r *syncRun) fanOut(app store.Application, err error) {
	if err == nil && app.GroupID != "" {
		r.operation, r.result = r.engine.fanOut(r.context, r.request, app)
		r.done = true
	}
}

func (r *syncRun) Resolve() {
	resolved, err := r.engine.resolveFor(r.request.AppID, r.request.TargetOverride)
	r.resolved = resolved
	r.stopOnError(err)
}

func (r *syncRun) stopOnError(err error) {
	if err != nil {
		r.result = err
		r.done = true
	}
}

func (r *syncRun) CheckSuspension() {
	r.checkSuspension()
}

func (r *syncRun) checkSuspension() {
	if r.resolved.app.Suspended && !r.request.DryRun {
		r.result = fmt.Errorf("HD0255: application %s is suspended", r.resolved.app.Name)
		r.done = true
	}
}

func (r *syncRun) CreateOperation() {
	r.operation = store.Operation{
		AppID: r.resolved.app.ID, Project: r.resolved.app.Project, App: r.resolved.app.Name,
		TargetID: r.resolved.target.ID,
		Phase:    PhaseOf(r.request), DryRun: r.request.DryRun, Rollback: r.request.Revision != "",
		PrincipalID: r.request.PrincipalID, PolicyVersion: r.request.PolicyVersion,
		ReasonCode: r.request.ReasonCode, StartedAt: r.engine.now(),
	}
	id, err := store.In[store.Operation](r.engine.Store, store.Operations).Put(r.operation)
	r.finishCreation(id, err)
}

func (r *syncRun) finishCreation(id string, err error) {
	if err != nil {
		r.operation = store.Operation{}
		r.result = err
		r.done = true
		return
	}
	r.operation.ID = id
}

func (r *syncRun) AppendActivity() {
	r.position, r.indexError = r.engine.Store.AppendActivity(store.EntryOf(r.operation), r.operation.AppID)
	r.reportAppendError()
}

func (r *syncRun) reportAppendError() {
	if r.indexError != nil {
		r.engine.logger().Error("activity index write failed; rebuild it to repair", "operation", r.operation.ID, "error", r.indexError)
	}
}

func (r *syncRun) PatchActivity() {
	r.patchActivity()
}

func (r *syncRun) patchActivity() {
	if r.indexError != nil {
		return
	}
	err := r.engine.Store.PatchActivity(r.operation.AppID, r.position, store.EntryOf(r.operation))
	r.ReportPatchError(err)
}

func (r *syncRun) ReportPatchError(err error) {
	r.reportPatchError(err)
}

func (r *syncRun) reportPatchError(err error) {
	if err != nil {
		r.engine.logger().Error("activity index patch failed; rebuild it to repair", "operation", r.operation.ID, "error", err)
	}
}

func (r *syncRun) Refresh() {
	revision, err := r.engine.refresh(r.context, r.resolved, r.request.Revision)
	r.revision = revision
	r.failOnError(err)
}

func (r *syncRun) failOnError(err error) {
	if err != nil {
		r.Fail(err)
	}
}

func (r *syncRun) Fail(cause error) {
	r.operation.Phase = store.PhaseFailed
	r.operation.Message = cause.Error()
	r.operation.FinishedAt = r.engine.now()
	r.engine.patchOperation(r.operation.ID, map[string]any{
		"phase": string(store.PhaseFailed), "message": r.operation.Message, "finished_at": r.operation.FinishedAt,
	})
	r.PatchActivity()
	r.result = cause
	r.done = true
}

func (r *syncRun) ReadPreviousRevision() {
	r.operation.Revision = r.revision.Commit
	r.operation.RevisionID = r.revision.ID
	r.operation.SpecHash = r.revision.SpecHash
	r.readPreviousRevision()
}

func (r *syncRun) readPreviousRevision() {
	if live, err := r.resolved.adapter.Observe(r.context, r.resolved.target.Ref(), r.resolved.app.AppRef()); err == nil {
		r.operation.PreviousRevision = live.Revision
	}
}

func (r *syncRun) RecordPlanning() {
	r.engine.patchOperation(r.operation.ID, map[string]any{
		"phase": string(store.PhasePlanning), "revision": r.operation.Revision,
		"revision_id": r.operation.RevisionID, "spec_hash": r.operation.SpecHash,
		"previous_revision": r.operation.PreviousRevision,
	})
}

func (r *syncRun) SelectServices() {
	r.want = r.revision.Spec
	r.selectServices()
}

func (r *syncRun) selectServices() {
	if len(r.request.Services) == 0 {
		return
	}
	want, err := selectServices(r.want, r.request.Services)
	r.want = want
	r.CheckSelection(err)
}

func (r *syncRun) CheckSelection(err error) {
	r.failOnError(err)
}

func (r *syncRun) Plan() {
	plan, err := r.resolved.adapter.Plan(r.context, r.resolved.target.Ref(), r.want)
	r.plan = plan
	r.checkPlanError(err)
}

func (r *syncRun) checkPlanError(err error) {
	if err != nil {
		r.PlanFailed(err)
	}
}

func (r *syncRun) PlanFailed(err error) {
	r.parkOfflineAgent(err)
	r.failUnparked(err)
}

func (r *syncRun) parkOfflineAgent(err error) {
	if r.resolved.remote && !r.request.DryRun && errors.Is(err, dispatch.ErrNoAgent) {
		r.operation, r.result = r.engine.park(r.operation, r.resolved)
		r.done = true
	}
}

func (r *syncRun) failUnparked(err error) {
	if !r.done {
		r.Fail(err)
	}
}

func (r *syncRun) RecordPlan() {
	selection := selectivePlan{plan: r.plan, want: r.want, selective: len(r.request.Services) > 0}
	r.plan = selection.Restrict()
	r.operation.Operations = r.plan.Operations
}

func (r *syncRun) CompleteDryRun() {
	r.completeDryRun()
}

func (r *syncRun) completeDryRun() {
	if !r.request.DryRun {
		return
	}
	r.operation.Phase = store.PhaseSucceeded
	r.operation.FinishedAt = r.engine.now()
	r.operation.Message = "dry run; nothing was applied"
	r.engine.patchOperation(r.operation.ID, map[string]any{
		"phase": string(store.PhaseSucceeded), "operations": r.plan.Operations,
		"message": r.operation.Message, "finished_at": r.operation.FinishedAt,
	})
	r.PatchActivity()
	r.done = true
}

func (r *syncRun) ConfigureApply() {
	r.prune = r.resolved.app.SyncPolicy.Prune
	r.overridePrune()
	r.context = secrets.WithSource(r.context, secrets.Source{Read: r.ReadSecretSource})
	applyContext, err := r.engine.applyContextFor(r.context, r.resolved, r.want, r.prune)
	r.applyContext = applyContext
	r.failOnError(err)
}

func (r *syncRun) overridePrune() {
	if r.request.Prune != nil {
		r.prune = *r.request.Prune
	}
}

func (r *syncRun) ReadSecretSource(ctx context.Context, relative string) ([]byte, error) {
	mirror := git.Repo{Dir: path.Join(r.engine.CacheDir, r.resolved.repository.ID), URL: r.resolved.repository.URL}
	return git.ReadFile(ctx, mirror, r.revision.Commit, path.Join(r.resolved.app.Path, relative))
}

func (r *syncRun) RecordApplying() {
	r.engine.patchOperation(r.operation.ID, map[string]any{"phase": string(store.PhaseApplying), "operations": r.plan.Operations})
}

func (r *syncRun) Apply() {
	result, err := r.resolved.adapter.Apply(r.applyContext, r.resolved.target.Ref(), r.plan)
	r.applied = result
	r.failOnError(err)
}

func (r *syncRun) CheckConvergence() {
	r.operation.Applied = r.applied.Applied
	r.operation.Failures = r.applied.Failures
	r.operation.FinishedAt = r.engine.now()
	r.operation.Phase = store.PhaseSucceeded
	r.recordApplyFailures()
	r.checkConvergence()
}

func (r *syncRun) recordApplyFailures() {
	if len(r.applied.Failures) > 0 {
		r.failure = fmt.Sprintf("%d service(s) failed", len(r.applied.Failures))
	}
}

func (r *syncRun) checkConvergence() {
	if len(r.applied.Failures) == 0 {
		err := r.engine.converge(r.context, r.resolved, r.want, r.plan)
		r.RecordConvergenceError(err)
	}
}

func (r *syncRun) RecordConvergenceError(err error) {
	r.recordConvergenceError(err)
}

func (r *syncRun) recordConvergenceError(err error) {
	if err != nil {
		r.failure = err.Error()
	}
}

func (r *syncRun) HandleFailureAction() {
	r.handleFailure()
}

func (r *syncRun) handleFailure() {
	if r.failure != "" {
		r.ResolveFailure()
	}
}

func (r *syncRun) ResolveFailure() {
	r.resolveFailure()
}

func (r *syncRun) resolveFailure() {
	switch failureActionFor(r.want, r.plan) {
	case "continue":
		r.engine.logger().Warn("rollout did not converge; failure_action is continue", "app", r.resolved.app.Name, "target", r.resolved.target.ID, "reason", r.failure)
		r.failure = ""
	case "rollback":
		err := r.engine.rollback(r.context, r.resolved, r.operation.PreviousRevision, r.prune)
		r.RecordRollback(err)
	}
}

func (r *syncRun) RecordRollback(err error) {
	r.recordRollbackFailure(err)
	r.recordRollbackSuccess(err)
}

func (r *syncRun) recordRollbackFailure(err error) {
	if err != nil {
		r.failure += "; the rollback also failed: " + err.Error()
	}
}

func (r *syncRun) recordRollbackSuccess(err error) {
	if err == nil {
		r.failure += "; rolled back to " + r.operation.PreviousRevision
	}
}

func (r *syncRun) Finish() {
	r.markFailed()
	r.engine.patchOperation(r.operation.ID, map[string]any{
		"phase": string(r.operation.Phase), "applied": r.applied.Applied,
		"failures": r.applied.Failures, "message": r.operation.Message,
		"finished_at": r.operation.FinishedAt,
	})
	r.PatchActivity()
	r.engine.Notify(r.operation)
	r.finishFailure()
}

func (r *syncRun) markFailed() {
	if r.failure != "" {
		r.operation.Phase = store.PhaseFailed
		r.operation.Message = r.failure
	}
}

func (r *syncRun) finishFailure() {
	if r.operation.Phase == store.PhaseFailed {
		r.FinishError()
	}
}

func (r *syncRun) FinishError() {
	r.finishApplyFailure()
	r.finishConvergenceFailure()
}

func (r *syncRun) finishApplyFailure() {
	if len(r.applied.Failures) > 0 {
		r.result = fmt.Errorf("HD0257: sync of %s completed with failures: %v", r.resolved.app.Name, r.applied.Failures)
	}
}

func (r *syncRun) finishConvergenceFailure() {
	if len(r.applied.Failures) == 0 {
		r.result = fmt.Errorf("HD0257: sync of %s did not converge: %s", r.resolved.app.Name, r.operation.Message)
	}
}
