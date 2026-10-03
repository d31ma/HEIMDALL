# ADR 0003 — No time-series database

**Status:** accepted (Phase 0)

## Context

The product promise is: click a running instance and see its metrics, logs,
events, and the commit that put it there. The obvious reading of that is
"build metric storage", which brings ingest, compaction, retention,
cardinality control, and a query language — a subsystem larger than the rest
of the control plane.

For three of the four runtimes, that data already exists and is already paid
for.

## Decision

HEIMDALL does not ship a TSDB. In priority order:

1. **Provider-native.** ECS → CloudWatch, ACA → Azure Monitor, Cloud Run →
   Cloud Monitoring. Query on demand, cache 30s, render.
2. **Agent ring buffer.** Docker Engine has nothing, so the agent scrapes
   `/containers/{id}/stats` at 10s into a bounded in-memory ring (24h is a few
   MB per host) and pushes 1m/5m/1h rollups to `hd-rollups`.
3. **Prometheus passthrough.** If the operator already runs one, point a
   target at it and skip the agent rollups entirely.

## Consequences

- Long-window Docker charts read rollups; the live chart reads the ring. Raw
  samples older than the ring are gone, by design.
- Multi-month raw retention for Docker Engine targets is not offered. That is
  the one thing that would reopen this decision, and it needs a customer
  asking, not an engineer anticipating.
- Drift detection is event-driven first (EventBridge, Event Grid, audit-log
  sinks, the Engine event stream) with a jittered adaptive poll as backstop —
  30s after a sync, decaying to 5m when stable. Naive 30s polling of every app
  would hit provider quotas and bill for the privilege.
