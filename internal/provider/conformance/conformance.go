// Package conformance is the one test suite every adapter must pass.
//
// It exists so an adapter cannot silently diverge. Each assertion below is a
// promise the reconciler, the diff view, and the UI already rely on; an
// adapter that fails one is not "different", it is broken in a way that will
// surface as a wedged sync at 3am instead of a red build.
//
// Extend this suite *before* adding an adapter, never after.
package conformance

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

// Harness is what an adapter supplies to be tested. Everything
// provider-specific lives here so the assertions stay provider-neutral.
type Harness struct {
	// Provider under test.
	Provider provider.Provider
	// Target the adapter should deploy to. Created and torn down by the
	// adapter's own test, not by this suite.
	Target provider.Target
	// Supported returns a spec this adapter can actually accept, at the given
	// revision. It must declare at least two services so ordering and
	// per-service planning are exercised.
	Supported func(revision string) spec.DeploySpec
	// Unsupported returns a spec using at least one feature this adapter
	// rejects. Return the zero spec if the adapter genuinely supports
	// everything, and the rejection assertions are skipped.
	Unsupported func() spec.DeploySpec
	// ApplyContext decorates a context with whatever the adapter's Apply
	// needs beyond the plan — the spec, prune opt-in, and so on.
	ApplyContext func(ctx context.Context, want spec.DeploySpec) context.Context
	// Reset removes everything a previous run left behind.
	Reset               func(t *testing.T)
	FunctionSupported   func(revision string) spec.DeploySpec
	InstancesMayBeEmpty bool
	ChangedImage        func(string) string
}

// Run executes the suite.
func Run(t *testing.T, harness Harness) {
	t.Helper()

	t.Run("Capabilities", func(t *testing.T) { testCapabilities(t, harness) })
	t.Run("PlanIsPureAndIdempotent", func(t *testing.T) { testPlanIsPureAndIdempotent(t, harness) })
	t.Run("ApplyConverges", func(t *testing.T) { testApplyConverges(t, harness) })
	t.Run("ObserveReportsTheDeployingRevision", func(t *testing.T) { testObserveRevision(t, harness) })
	t.Run("PlanRejectsUnsupportedFeatures", func(t *testing.T) { testRejection(t, harness) })
	t.Run("ApplyRefusesAMismatchedSpec", func(t *testing.T) { testSpecMismatch(t, harness) })
	t.Run("DriftIsDetected", func(t *testing.T) { testDriftIsDetected(t, harness) })
	t.Run("WavesAreOrdered", func(t *testing.T) { testWavesAreOrdered(t, harness) })
	t.Run("InstancesCarryTheRevision", func(t *testing.T) { testInstancesCarryRevision(t, harness) })
	t.Run("ObservingAnAbsentAppIsEmptyNotError", func(t *testing.T) { testObserveAbsent(t, harness) })
	t.Run("RemovedServiceIsPlannedAsDelete", func(t *testing.T) { testRemovalPlansDelete(t, harness) })
	t.Run("RolloutStrategyConverges", func(t *testing.T) { testRolloutConverges(t, harness) })
	t.Run("StartFirstRolloutSurvivesAndReportsHealthy", func(t *testing.T) { testStartFirstRollout(t, harness) })
	t.Run("ScheduledWorkloads", func(t *testing.T) { testScheduled(t, harness) })
	t.Run("FunctionWorkloads", func(t *testing.T) { testFunctionWorkloads(t, harness) })
}

func testFunctionWorkloads(t *testing.T, harness Harness) {
	if harness.Provider.Capabilities().Of(provider.FeatureFunction) != provider.Rejected {
		testNativeFunctionWorkload(t, harness)
		return
	}
	harness.Reset(t)
	want := harness.Supported("rev-function")
	want.Services = want.Services[:1]
	service := &want.Services[0]
	service.Ports, service.Healthcheck, service.DependsOn, service.Volumes = nil, nil, nil, nil
	service.Schedule, service.Update = nil, nil
	service.Restart, service.Entrypoint, service.Command = "", nil, nil
	service.Replicas = 1
	service.Function = &spec.Function{RuntimeContract: "lambda-runtime-api", Invocation: "json", TimeoutMS: 30000, Architecture: "amd64"}
	want.Normalize()
	_, err := harness.Provider.Plan(context.Background(), harness.Target, want)
	var rejection *provider.RejectionError
	if !errors.As(err, &rejection) {
		t.Fatalf("function workload requires a capability rejection, got %v", err)
	}
	if !strings.Contains(rejection.Error(), service.Name) || !strings.Contains(rejection.Error(), string(provider.FeatureFunction)) {
		t.Fatalf("rejection must identify the function service: %v", rejection)
	}
}

func testNativeFunctionWorkload(t *testing.T, harness Harness) {
	if harness.FunctionSupported == nil {
		t.Fatal("a function-aware adapter must supply its actual native runtime contract")
	}
	harness.Reset(t)
	want := harness.FunctionSupported("rev-native-function")
	if len(want.Services) == 0 || want.Services[0].Function == nil {
		t.Fatal("native function conformance requires an explicit function declaration")
	}
	ctx := context.Background()
	plan, err := harness.Provider.Plan(ctx, harness.Target, want)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.Provider.Apply(harness.ApplyContext(ctx, want), harness.Target, plan); err != nil {
		t.Fatal(err)
	}
	unchanged, err := harness.Provider.Plan(ctx, harness.Target, want)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Changes() {
		t.Fatalf("native function did not converge: %+v", unchanged)
	}
	live, err := harness.Provider.Observe(ctx, harness.Target, plan.App)
	if err != nil {
		t.Fatal(err)
	}
	if live.Revision != want.Revision || live.Rollup() != provider.Healthy {
		t.Fatalf("native function readiness/revision = %+v", live)
	}
}

// testScheduled is the scheduled-workload contract. An adapter that rejects
// scheduled services must say so in its capabilities and refuse at plan time;
// one that accepts them must register the schedule with the runtime's own
// scheduler, re-register on a cron change, and — the 3am footgun — delete the
// registration when the service goes, because an orphaned rule fires forever
// against a stale image.
func testScheduled(t *testing.T, harness Harness) {
	harness.Reset(t)
	ctx := context.Background()

	want := harness.Supported("rev-sched")
	want.Services = want.Services[:1]
	job := &want.Services[0]
	job.Ports, job.Healthcheck, job.DependsOn, job.Restart = nil, nil, nil, ""
	job.Replicas = 1
	job.Schedule = &spec.Schedule{
		Cron: "0 3 * * *", Timezone: "UTC", Concurrency: "forbid", TimeoutMS: 1800000,
	}
	want.Normalize()

	plan, err := harness.Provider.Plan(ctx, harness.Target, want)
	if support := harness.Provider.Capabilities().Of(provider.FeatureScheduled); support == provider.Rejected {
		var rejection *provider.RejectionError
		if !asRejection(err, &rejection) {
			t.Fatalf("the adapter rejects scheduled workloads but planning one returned %v", err)
		}
		if !strings.Contains(rejection.Error(), job.Name) {
			t.Fatalf("the rejection does not name the offending service: %v", rejection)
		}
		return
	}
	if err != nil {
		t.Fatalf("plan a scheduled service: %v", err)
	}
	result, err := harness.Provider.Apply(harness.ApplyContext(ctx, want), harness.Target, plan)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(result.Failures) > 0 {
		t.Fatalf("scheduling %s failed: %v", job.Name, result.Failures)
	}

	// The registration is what live state reads for a job — not a running
	// container, which is the whole point: between runs there is nothing to
	// see, and a job with no registration is drift.
	live, err := harness.Provider.Observe(ctx, harness.Target, plan.App)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	state, ok := live.Services[job.Name]
	if !ok {
		t.Fatalf("a scheduled service is invisible to Observe; drift would never be detected")
	}
	if state.Schedule != job.Schedule.Cron {
		t.Fatalf("the runtime holds schedule %q, want %q", state.Schedule, job.Schedule.Cron)
	}

	// Editing the cron is an update, because the schedule is desired state.
	rescheduled := want
	rescheduled.Services = []spec.Service{*job}
	rescheduled.Services[0].Schedule = &spec.Schedule{
		Cron: "30 4 * * *", Timezone: "UTC", Concurrency: "forbid", TimeoutMS: 1800000,
	}
	rescheduled.Revision = "rev-sched2"
	rescheduled.Normalize()

	replan, err := harness.Provider.Plan(ctx, harness.Target, rescheduled)
	if err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	if !replan.Changes() {
		t.Fatal("changing the cron expression planned no change; a schedule edit would never deploy")
	}
	if _, err := harness.Provider.Apply(harness.ApplyContext(ctx, rescheduled), harness.Target, replan); err != nil {
		t.Fatalf("apply the new schedule: %v", err)
	}
	if live, err = harness.Provider.Observe(ctx, harness.Target, plan.App); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if live.Services[job.Name].Schedule != "30 4 * * *" {
		t.Fatalf("after rescheduling the runtime holds %q", live.Services[job.Name].Schedule)
	}

	// Deleting the service must take the registration with it.
	empty := rescheduled
	empty.Services = nil
	empty.Revision = "rev-sched3"
	empty.Normalize()

	prune, err := harness.Provider.Plan(ctx, harness.Target, empty)
	if err != nil {
		t.Fatalf("plan the removal: %v", err)
	}
	if _, err := harness.Provider.Apply(harness.ApplyContext(ctx, empty), harness.Target, prune); err != nil {
		t.Fatalf("apply the removal: %v", err)
	}
	if live, err = harness.Provider.Observe(ctx, harness.Target, plan.App); err != nil {
		t.Fatalf("observe after removal: %v", err)
	}
	if _, survived := live.Services[job.Name]; survived {
		t.Fatal("the schedule registration outlived its service; it will fire at 03:00 forever")
	}
}

// rollout returns the harness spec with a declared rollout strategy and no
// published ports. Ports are dropped because a runtime without a routing mesh
// cannot run two containers on one host port, and that rejection is asserted
// in the adapter's own tests rather than here.
func rollout(harness Harness, revision, order string) spec.DeploySpec {
	want := harness.Supported(revision)
	for i := range want.Services {
		want.Services[i].Ports = nil
		want.Services[i].Update = &spec.UpdateConfig{
			Order: order, FailureAction: "rollback", MonitorMS: 200,
		}
	}
	want.Normalize()
	return want
}

// testRolloutConverges: update_config is part of the content hash, so an
// adapter that ignores it — or that stamps a hash computed without it —
// would re-plan an update forever against a converged runtime.
func testRolloutConverges(t *testing.T, harness Harness) {
	harness.Reset(t)
	ctx := context.Background()
	want := rollout(harness, "rev-rollout", "stop-first")

	plan, err := harness.Provider.Plan(ctx, harness.Target, want)
	if harness.FunctionSupported != nil && harness.Provider.Capabilities().Of(provider.FeatureStopFirst) == provider.Rejected {
		var rejection *provider.RejectionError
		if !errors.As(err, &rejection) {
			t.Fatalf("unsupported function rollout must be rejected: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	result, err := harness.Provider.Apply(harness.ApplyContext(ctx, want), harness.Target, plan)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(result.Failures) > 0 {
		t.Fatalf("apply reported failures: %v", result.Failures)
	}

	converged, err := harness.Provider.Plan(ctx, harness.Target, want)
	if err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	if converged.Changes() {
		t.Fatalf("a rollout strategy re-plans forever; still wants: %+v", changedOperations(converged))
	}
}

// testStartFirstRollout: an adapter that claims start-first must actually
// complete one and leave the service healthy at the new revision. The stronger
// property — that the old unit never stops serving mid-rollout — is asserted
// per adapter, where the runtime can be made to fail on demand; sampling
// Observe from here would be a race dressed up as an assertion.
func testStartFirstRollout(t *testing.T, harness Harness) {
	harness.Reset(t)
	ctx := context.Background()

	first := rollout(harness, "rev-sf-1", "start-first")
	plan, err := harness.Provider.Plan(ctx, harness.Target, first)
	if harness.FunctionSupported != nil && harness.Provider.Capabilities().Of(provider.FeatureStopFirst) == provider.Rejected {
		var rejection *provider.RejectionError
		if !errors.As(err, &rejection) {
			t.Fatalf("unsupported function rollout must be rejected: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := harness.Provider.Apply(harness.ApplyContext(ctx, first), harness.Target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Twice, because one start-first update can leave scratch state behind
	// that only collides on the next one — a runtime that is fine on the
	// first rollout and wedged on the second is the worst of both.
	for _, revision := range []string{"rev-sf-2", "rev-sf-3"} {
		next := rollout(harness, revision, "start-first")
		for i := range next.Services {
			next.Services[i].Image = changedImage(harness, next.Services[i].Image, "-"+revision)
		}
		next.Normalize()

		update, err := harness.Provider.Plan(ctx, harness.Target, next)
		if err != nil {
			t.Fatalf("plan %s: %v", revision, err)
		}
		if !update.Changes() {
			t.Fatalf("a new image planned no change at %s", revision)
		}
		result, err := harness.Provider.Apply(harness.ApplyContext(ctx, next), harness.Target, update)
		if err != nil {
			t.Fatalf("start-first apply of %s: %v", revision, err)
		}
		if len(result.Failures) > 0 {
			t.Fatalf("start-first apply of %s reported failures: %v", revision, result.Failures)
		}

		WaitFor(t, 10*time.Second, "the start-first rollout to settle", func() bool {
			live, err := harness.Provider.Observe(ctx, harness.Target, update.App)
			return err == nil && live.Revision == revision && allHealthy(live)
		})
		converged, err := harness.Provider.Plan(ctx, harness.Target, next)
		if err != nil {
			t.Fatalf("re-plan %s: %v", revision, err)
		}
		if converged.Changes() {
			t.Fatalf("the %s rollout did not converge; still wants: %+v", revision, changedOperations(converged))
		}
	}
}

func allHealthy(live provider.LiveState) bool {
	for _, state := range live.Services {
		if state.Health != provider.Healthy {
			return false
		}
	}
	return len(live.Services) > 0
}

// testInstancesCarryRevision: linking a running unit to the commit that put
// it there is the product's headline claim, and every runtime has somewhere
// to keep a label or a tag. An adapter that cannot answer is incomplete.
func testInstancesCarryRevision(t *testing.T, harness Harness) {
	harness.Reset(t)
	want := harness.Supported("rev-instances")
	ctx := context.Background()

	plan, err := harness.Provider.Plan(ctx, harness.Target, want)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := harness.Provider.Apply(harness.ApplyContext(ctx, want), harness.Target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	instances, err := harness.Provider.Instances(ctx, harness.Target, plan.App)
	if err != nil {
		t.Fatalf("instances: %v", err)
	}
	if harness.InstancesMayBeEmpty && harness.Provider.Capabilities().Of(provider.FeatureFunction) == provider.Rejected {
		t.Fatal("an ordinary container harness cannot waive its instance inventory contract")
	}
	if len(instances) == 0 && !harness.InstancesMayBeEmpty {
		t.Fatal("nothing is running after an apply")
	}
	for _, instance := range instances {
		if instance.Revision != "rev-instances" {
			t.Fatalf("instance %s carries revision %q, want the deploying commit", instance.Ref.Instance, instance.Revision)
		}
	}
}

// testObserveAbsent: "nothing is deployed" is an ordinary answer, not an
// error. The reconciler asks this about every application on every status
// read, and an adapter that errors turns a fresh application into a red one.
func testObserveAbsent(t *testing.T, harness Harness) {
	harness.Reset(t)
	live, err := harness.Provider.Observe(context.Background(), harness.Target,
		provider.AppRef{Project: "conf", App: "never-deployed"})
	if err != nil {
		t.Fatalf("observing an absent app errored: %v", err)
	}
	if len(live.Services) != 0 {
		t.Fatalf("an absent app reports %d services", len(live.Services))
	}
}

// testRemovalPlansDelete: dropping a service from the spec plans a delete for
// what is running — visibly, so prune's opt-in is a decision about a named
// thing rather than a surprise.
func testRemovalPlansDelete(t *testing.T, harness Harness) {
	harness.Reset(t)
	want := harness.Supported("rev-removal")
	if len(want.Services) < 2 {
		t.Fatal("the harness must supply at least two services")
	}
	ctx := context.Background()

	plan, err := harness.Provider.Plan(ctx, harness.Target, want)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := harness.Provider.Apply(harness.ApplyContext(ctx, want), harness.Target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	narrowed := want
	narrowed.Services = narrowed.Services[:1]
	narrowed.Normalize()

	replan, err := harness.Provider.Plan(ctx, harness.Target, narrowed)
	if err != nil {
		t.Fatalf("replan: %v", err)
	}
	sawDelete := false
	for _, operation := range replan.Operations {
		if operation.Kind == provider.OpDelete {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Fatalf("removing a service planned no delete: %s", Describe(replan))
	}
}

// testCapabilities enforces that an adapter answers for every feature
// explicitly. Of() defaults to Rejected, which is the safe default at runtime,
// but a silent default here would let an adapter forget a feature and have
// that read as a deliberate rejection in the published matrix.
func testCapabilities(t *testing.T, harness Harness) {
	capabilities := harness.Provider.Capabilities()

	if capabilities.Provider == "" {
		t.Error("Capabilities().Provider is empty")
	}
	if capabilities.Provider != harness.Provider.Name() {
		t.Errorf("Capabilities().Provider = %q but Name() = %q; the generated matrix would be mislabelled",
			capabilities.Provider, harness.Provider.Name())
	}
	for _, feature := range provider.Features {
		support, answered := capabilities.Support[feature]
		if !answered {
			t.Errorf("no answer for %q; every feature must be answered explicitly, "+
				"or the published matrix reports a forgotten one as a deliberate rejection", feature)
			continue
		}
		switch support {
		case provider.Full, provider.Partial, provider.Rejected:
		default:
			t.Errorf("%q has support %q, which is not one of full, partial, rejected", feature, support)
		}
		if support != provider.Full && capabilities.Caveats[feature] == "" {
			t.Errorf("%q is %q with no caveat; an operator reading the matrix needs to know what to do instead",
				feature, support)
		}
	}
}

func testPlanIsPureAndIdempotent(t *testing.T, harness Harness) {
	harness.Reset(t)
	ctx := context.Background()
	want := harness.Supported("aaaaaaa")

	first, err := harness.Provider.Plan(ctx, harness.Target, want)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !first.Changes() {
		t.Fatal("planning against an empty target reported no changes")
	}
	if first.SpecHash == "" {
		t.Error("plan carries no spec hash, so apply cannot verify it was given the right spec")
	}

	// Planning must not have created anything: a second plan sees the same
	// world and must produce the same operations.
	second, err := harness.Provider.Plan(ctx, harness.Target, want)
	if err != nil {
		t.Fatalf("second plan: %v", err)
	}
	if len(first.Operations) != len(second.Operations) {
		t.Fatalf("plan is not pure: %d operations then %d; the first plan mutated the target",
			len(first.Operations), len(second.Operations))
	}
	for i := range first.Operations {
		if first.Operations[i] != second.Operations[i] {
			t.Fatalf("plan is not deterministic at %d: %+v then %+v",
				i, first.Operations[i], second.Operations[i])
		}
	}
}

func testApplyConverges(t *testing.T, harness Harness) {
	harness.Reset(t)
	ctx := context.Background()
	want := harness.Supported("bbbbbbb")

	plan, err := harness.Provider.Plan(ctx, harness.Target, want)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	result, err := harness.Provider.Apply(harness.ApplyContext(ctx, want), harness.Target, plan)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(result.Failures) > 0 {
		t.Fatalf("apply reported failures: %v", result.Failures)
	}

	// The property that makes a reconciler safe to re-run: after an apply, a
	// fresh plan has nothing left to do.
	converged, err := harness.Provider.Plan(ctx, harness.Target, want)
	if err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	if converged.Changes() {
		t.Fatalf("apply did not converge; a re-plan still wants: %+v", changedOperations(converged))
	}

	// And applying the same spec twice must be safe.
	if _, err := harness.Provider.Apply(harness.ApplyContext(ctx, want), harness.Target, converged); err != nil {
		t.Fatalf("re-apply of a converged plan failed: %v", err)
	}
}

func testObserveRevision(t *testing.T, harness Harness) {
	harness.Reset(t)
	ctx := context.Background()
	const revision = "ccccccc"
	want := harness.Supported(revision)

	plan, err := harness.Provider.Plan(ctx, harness.Target, want)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := harness.Provider.Apply(harness.ApplyContext(ctx, want), harness.Target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	app := plan.App
	live, err := harness.Provider.Observe(ctx, harness.Target, app)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if len(live.Services) != len(want.Services) {
		t.Fatalf("observed %d services, applied %d", len(live.Services), len(want.Services))
	}
	// The headline product claim: a running workload knows the commit that
	// put it there.
	if live.Revision != revision {
		t.Errorf("live state reports revision %q, want %q", live.Revision, revision)
	}
	if live.SpecHash != plan.SpecHash {
		t.Errorf("live state reports spec hash %q, want %q", live.SpecHash, plan.SpecHash)
	}
	if live.ReadAt.IsZero() {
		t.Error("live state has no read timestamp, so staleness cannot be judged")
	}

	instances, err := harness.Provider.Instances(ctx, harness.Target, app)
	if err != nil {
		t.Fatalf("instances: %v", err)
	}
	if len(instances) == 0 && !harness.InstancesMayBeEmpty {
		t.Fatal("no instances after a successful apply")
	}
	for _, instance := range instances {
		if instance.Revision != revision {
			t.Errorf("instance %s reports revision %q, want %q",
				instance.Ref.Service, instance.Revision, revision)
		}
		if instance.Ref.Instance == "" {
			t.Errorf("instance %s has no id, so metrics and logs cannot address it", instance.Ref.Service)
		}
	}
}

func testRejection(t *testing.T, harness Harness) {
	unsupported := harness.Unsupported()
	if len(unsupported.Services) == 0 {
		t.Skip("adapter reports no unsupported features")
	}
	harness.Reset(t)

	_, err := harness.Provider.Plan(context.Background(), harness.Target, unsupported)
	if err == nil {
		t.Fatal("planned a spec using an unsupported feature; it must be rejected at plan time, not half-applied")
	}

	var rejection *provider.RejectionError
	if !asRejection(err, &rejection) {
		t.Fatalf("rejection is not a *provider.RejectionError, so callers must parse text: %v", err)
	}
	if len(rejection.Rejections) == 0 {
		t.Fatal("rejection names nothing")
	}
	for _, item := range rejection.Rejections {
		if item.Service == "" {
			t.Error("a rejection does not name the offending service")
		}
		if item.Detail == "" {
			t.Errorf("the rejection of %q on %s has no detail, so an operator cannot act on it",
				item.Feature, item.Service)
		}
	}
}

func testSpecMismatch(t *testing.T, harness Harness) {
	harness.Reset(t)
	ctx := context.Background()
	planned := harness.Supported("ddddddd")
	other := harness.Supported("eeeeeee")

	plan, err := harness.Provider.Plan(ctx, harness.Target, planned)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	// Applying a plan against a different spec than it was previewed on is
	// the one way a reviewed diff and the deployed reality can diverge.
	if _, err := harness.Provider.Apply(harness.ApplyContext(ctx, other), harness.Target, plan); err == nil {
		t.Fatal("applied a plan against a spec it was not produced from")
	}
}

func testDriftIsDetected(t *testing.T, harness Harness) {
	harness.Reset(t)
	ctx := context.Background()

	first := harness.Supported("fffffff")
	plan, err := harness.Provider.Plan(ctx, harness.Target, first)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := harness.Provider.Apply(harness.ApplyContext(ctx, first), harness.Target, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Change the desired state. The adapter must notice without being told
	// what changed.
	second := harness.Supported("ggggggg")
	if len(second.Services) == 0 {
		t.Fatal("harness produced no services")
	}
	second.Services[0].Image = changedImage(harness, second.Services[0].Image, "-changed")
	second.Normalize()

	drifted, err := harness.Provider.Plan(ctx, harness.Target, second)
	if err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	if !drifted.Changes() {
		t.Fatal("a changed image produced no operations; drift would go undetected")
	}
	for _, operation := range drifted.Operations {
		if operation.Service == second.Services[0].Name && operation.Kind != provider.OpNoop {
			if operation.Reason == "" {
				t.Error("the operation has no reason, so the diff view cannot say what changed")
			}
			return
		}
	}
	t.Errorf("no operation targets the changed service %q", second.Services[0].Name)
}

func testWavesAreOrdered(t *testing.T, harness Harness) {
	harness.Reset(t)
	want := harness.Supported("hhhhhhh")
	if len(want.Services) < 2 {
		t.Skip("harness supplies fewer than two services")
	}
	want.Services[0].Wave = 2
	want.Services[1].Wave = 1
	want.Normalize()

	plan, err := harness.Provider.Plan(context.Background(), harness.Target, want)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	waves := plan.Waves()
	for i := 1; i < len(waves); i++ {
		if waves[i] <= waves[i-1] {
			t.Fatalf("Waves() is not ascending: %v", waves)
		}
	}
	last := -1 << 31
	for _, operation := range plan.Operations {
		if operation.Wave < last {
			t.Fatalf("operations are not wave-ordered: %s in wave %d follows wave %d",
				operation.Service, operation.Wave, last)
		}
		last = operation.Wave
	}
}

func changedOperations(plan provider.Plan) []provider.Operation {
	var changed []provider.Operation
	for _, operation := range plan.Operations {
		if operation.Kind != provider.OpNoop {
			changed = append(changed, operation)
		}
	}
	return changed
}

func changedImage(harness Harness, image, suffix string) string {
	if harness.ChangedImage != nil {
		return harness.ChangedImage(image)
	}
	return image + suffix
}

func asRejection(err error, target **provider.RejectionError) bool {
	return errors.As(err, target)
}

// WaitFor polls until condition holds or the deadline passes. Adapters whose
// runtimes converge asynchronously use it rather than sleeping a fixed
// interval, which is how a suite becomes flaky.
func WaitFor(t *testing.T, within time.Duration, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", within, what)
}

// Describe renders a plan for a failure message.
func Describe(plan provider.Plan) string {
	lines := make([]string, 0, len(plan.Operations))
	for _, operation := range plan.Operations {
		lines = append(lines, string(operation.Kind)+" "+operation.Service+" ("+operation.Reason+")")
	}
	return strings.Join(lines, "; ")
}
