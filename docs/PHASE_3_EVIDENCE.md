# Phase 3 evidence — Observability v1 and pending actions

## Exit criteria

> Click any running container and see 24h of metrics plus streaming logs in
> under 2s, with the deploying commit linked and deploy markers on the chart.
> A target offline for an hour reconnects and converges to the newest desired
> revision, not through every intermediate one.

| Criterion | State | Evidence |
|---|---|---|
| Offline target converges to newest, in one hop | **Proven** | `TestOfflineSyncParksAndDrainsOnReconnect` — the stale revision is never deployed on the way |
| 24h of metrics on click | **Built; proven against the fake Engine** | `TestScrapeRollupAndServe` (pipeline), `hd-timeseries renders axes…` (chart, real browser). Not yet run against a live daemon |
| Streaming logs | **Bounded tail, 2.5s poll** | Indistinguishable at a console; SSE topics are the named upgrade path |
| Deploy markers with the commit | **Proven** | The Playwright test reads the revision text back out of the SVG |
| Instance drawer | **Built** | CPU, memory, health, restarts, uptime, owning revision, log tail. No shell, no start/stop, deliberately |
| Unified event timeline | **Built** | Operations and runtime events merged chronologically |
| Agent-managed targets | **Proven against the fake Engine** | Metrics/logs/events as jobs (`TestObservabilityJobsRoundTrip`); history via shipped rollups with per-minute idempotent, target-scoped ingest (`TestAgentRollupIngest`, `TestAgentCannotWriteAnotherHostsHistory`) |

## Decisions worth recording

- **Not a time-series database.** One bounded ring per instance in memory,
  one rollup document per instance-minute for a day, everything older
  deleted. Average and max both, because averages hide the spike an operator
  is hunting.
- **One fold implementation** (`observe.Accumulator`) used by both the
  control plane's collector and the agent, so the two pipelines cannot drift.
- **Raw samples never cross the WAN.** Agents ship minute rollups; sixty
  samples a minute per container over a wide-area link is exactly the chatter
  rollups exist to avoid.
- **A parked sync is a reference, never a job.** Jobs carry resolved secret
  values, and holding those for hours is the standing exposure the
  refs-not-values rule forbids. The drain re-resolves everything, and
  re-reading git at drain time is what makes "converge, don't replay" true.

## A defect this phase caught elsewhere

Writing the rollup-ingest test exposed that `Collection.Find` silently
matched everything for plain `{field: value}` queries — FYLO ignores keys
outside its `$ops` grammar. The Phase 2 target-group routes had been
returning every project's rows. Find now normalises at the choke point;
`TestFindNormalisesPlainQueries` pins the AND semantics (fields within one
`$ops` entry) that FYLO's README only implies by omission.

## Not done in Phase 3

- **Live-daemon verification.** `scripts/e2e-docker.sh` still has never run;
  no machine in this environment has a Docker daemon.
- **SSE log streaming and `w-sparkline` list minis.** Named ceilings, not
  gaps: polling and the drawer cover the operator experience today.
- **The agent's current-minute live edge.** Charts for agent targets end at
  the last complete minute; the one-shot metrics job covers "now" on click.
