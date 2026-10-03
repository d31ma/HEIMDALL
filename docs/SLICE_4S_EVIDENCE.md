# Slice 4S evidence — scheduled and batch workloads

The slice designed in [PLAN.md](../PLAN.md#scheduled-and-batch-workloads). A nightly
backup now has somewhere to live: `x-heimdall-schedule` on a compose service,
registered with the runtime's own scheduler.

## What is proven

| Claim | Where |
|---|---|
| `x-heimdall-schedule` parses, defaults (UTC, `forbid`), and validates cron, timezone, concurrency, and durations | `internal/render` — `TestScheduleRendersWithDefaults` |
| A scheduled service rejects `ports`, `deploy.replicas`, `restart`, `healthcheck`, `depends_on`, `update_config`, and `catchup` by name (HD0221) | `TestScheduledServiceRejectsContradictions`, thirteen cases |
| The schedule is part of the content hash, so a cron edit plans as an update | `spec.Service.Schedule` in canonical JSON; conformance asserts the re-plan |
| Cron evaluation is correct in a named timezone, and an impossible schedule never fires | `internal/cron` — `TestNextFiringTimes` (including Europe/Berlin), `TestImpossibleScheduleNeverFires`, `TestBadExpressionsAreRefused` |
| Every adapter answers for scheduled workloads, and a rejecting one refuses at plan time naming the service | conformance `ScheduledWorkloads`, on all five adapters |
| ECS registers an EventBridge schedule, re-registers on a cron change, and reads it back through `Observe` | conformance `ScheduledWorkloads` against the fake AWS, plus the five-field-to-EventBridge translation in `internal/provider/ecs/schedule.go` |
| Deleting a scheduled service deletes the registration *first* | conformance `ScheduledWorkloads` (the last assertion), and `TestUnregisterLeavesNothingBehind` |
| The agent's clock fires a due schedule, runs the container with the deploying revision's labels, and records the outcome | `TestDueScheduleRuns` |
| A run that exits non-zero is recorded as failed rather than silently green | `TestFailedRunIsRecordedAsFailed` |
| Registrations survive an agent restart | `TestRegistrationsSurviveAnAgentRestart` |
| A Docker target with no agent refuses a scheduled service at plan time (HD0340) | `scheduleSupport.rejectScheduledWithoutAClock`; every non-agent plan path exercises it |
| An execution is an instance that stopped: exit code and finish time ride on `Instance` | `Instances` on the Docker adapter now lists every container, newest run first |

## What is proven, second pass (the rest of the slice)

| Claim | Where |
|---|---|
| Cloud Run maps a job to a Cloud Run job plus a Cloud Scheduler trigger, and deletes the trigger before the job | conformance `ScheduledWorkloads` against the fake Cloud Run + Cloud Scheduler |
| Container Apps maps a job to an ACA job with a Schedule trigger, and refuses a non-UTC timezone at plan time (HD0383) rather than silently moving the hour | conformance `ScheduledWorkloads` against the SDK's generated fake |
| `heimdall run` starts one execution without touching desired state, through `app:run` — a separate SESAME action from `app:sync` | `TestScheduledServiceDeploysAsAScheduleAndRunsOnDemand`; the action is in the operator and admin role bundles |
| Running a long-running service is refused (HD0279): run is for jobs | `TestRunningAnOrdinaryServiceIsRefused` |
| A Docker target's manual run reaches the agent's clock over the existing mTLS rendezvous, as a new job kind | `dispatch.KindRun`, handled in `agent.ExecuteJob` |
| A scheduled service deploys as a registration and no container | same end-to-end test: the fake Engine holds zero containers after the sync |
| The instance page carries runs, next-run time, and a Run now button — and no exec, files, or lifecycle controls | `web/tests/ui.spec.js`, the tabbed-instance-page test |

## What is not implemented, deliberately

- **`deadline` on ECS.** Compose's deadline is honoured by the agent clock and
  by Cloud Scheduler's attempt deadline; the EventBridge mapping registers a
  flexible time window of `OFF`, which never starts a run late but does not
  express "skip if more than N late" either.
- **Execution history off Docker.** `Instance.ExitCode` and `FinishedAt` come
  from exited run containers, which only the Docker adapters keep. ECS, Cloud
  Run, and ACA report the registration and the last run's outcome, not a list
  of executions; reading their execution APIs is the obvious next slice.

## Notable decisions

- **The contradictions are render-time, not capability-time.** A job with
  published ports is incoherent on every runtime, so it is refused once in
  render. `Capabilities()` still answers "can this runtime run scheduled work
  at all", which is a different question and is asked per adapter.
- **Identity rides in the EventBridge schedule's description.** EventBridge
  tags schedule *groups*, not schedules, so the description carries the JSON
  identity that tags carry on every other resource. A schedule sharing the name
  prefix whose description is not HEIMDALL's is left alone.
- **Six-field cron is refused, not translated.** AWS and Quartz put seconds
  first; POSIX does not. Guessing which one an expression means is how a
  nightly job runs every minute, so `internal/cron` accepts five fields only
  and the ECS adapter translates explicitly, including the day-of-week
  numbering shift and the `?` rule.
- **Executions are exited containers, not a second store.** The agent keeps the
  last ten run containers per service and prunes the rest, so the Engine's own
  record *is* the execution history and there is nothing to keep in sync.
