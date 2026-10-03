# Phase 6 evidence — Hardening and HA

## Exit criteria

> Documented RTO/RPO; a restore from cold backup produces a control plane
> that can verify a pre-existing session; sustained load with p99 sync
> latency inside SLO.

| Criterion | State | Evidence |
|---|---|---|
| Restore verifies a pre-existing session | **Proven live** | `scripts/dr-drill.sh`: init → serve → login → stop → backup → destroy → restore *to a different path* → serve → the pre-disaster bearer answers 200 |
| RTO/RPO documented | **Done** | [docs/operations/DR_RUNBOOK.md](operations/DR_RUNBOOK.md); the drill measures RTO, RPO is the backup cadence, and git remaining the source of desired state bounds what a lost interval costs |
| Load, p99 inside SLO | **Measured** | `TestLoadSmoke` (HD_LOAD=1): 500 applications across 4 targets synced through the real engine against fake Engines in 2m39s; p50=316ms, p95=440ms, **p99=473ms**, max=586ms — against a 5s SLO, on a laptop |
| Active/passive failover | **Proven live** | `scripts/ha-drill.sh`: the standby probes with `sesame doctor`, logs that it is waiting while the active holds the locks, and takes over within its 5s retry after the active dies. FYLO's exclusive root lock is the leader election — no consensus protocol, no split brain |
| Rate limiting | **Proven** | Token bucket per bearer/IP; `TestRateLimitBoundsARunawayClient` shows one client's flood limited, another client unaffected, and health probes never limited |
| Backup safety | **Proven** | Backup refuses while serve runs (the root lock is the probe); restore refuses a non-empty directory and rejects archive traversal; the CLI session is excluded from the archive |
| Threat model | **Done** | [docs/operations/THREAT_MODEL.md](operations/THREAT_MODEL.md) — boundaries, attacker stories with the mechanism (and usually the test) that answers each, residual risks named |

## Chaos coverage, mapped to existing tests

- **SESAME engine gone mid-request** → `TestStoppedEngineGets503AndNeverBypasses`: unavailability is 503, never an allow.
- **Provider API failure mid-apply** → the fake Engine's `FailPull` drives the fan-out halt test and the in-stream error path tests.
- **Git unreachable** → a sync fails its refresh with a typed error and one failed operation document (`TestFailedRenderLeavesOneFailedOperation` covers the shape).
- **Agent partition** → the pending-actions suite: park, supersede, drain, expire.
- **Control plane restart mid-work** → `TestReparkRebuildsAfterRestart` and the interrupted-operation closure in `Repark`.

## Defects the drills caught

1. **Absolute paths in `heimdall.json` broke restore-to-a-different-path.**
   Config now stores deployment-relative paths, with a rebase fallback for
   archives taken by older builds. The DR drill restores to a new directory
   because that is what disaster recovery is.
2. **The first standby implementation died instead of waiting**: HEIMDALL's
   own store open is lazy, so the standby sailed past it and hit SESAME's
   root lock at `sesame doctor` as a fatal error. The doctor probe *is* the
   right wait condition, and now it is the wait condition.

## Not done in Phase 6

- **An external security review package** beyond the threat model: assembling
  one is a GA-gate activity with the reviewer chosen.
- **Chaos under sustained load** (the failures above are exercised, but not
  while the load smoke runs).
