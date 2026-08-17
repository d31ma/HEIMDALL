# Disaster recovery runbook

The DR unit is the **deployment directory** — both FYLO roots, SESAME's key
material, HEIMDALL's TLS keys and OIDC client, and the sealed secrets — per
ADR 0004. A FYLO snapshot alone restores a control plane that cannot
authenticate anyone.

## Objectives

- **RPO**: the age of the newest cold backup. State changes on every sync, so
  back up on the cadence your history matters at; nightly is typical. What a
  lost interval costs: operation history and audit entries since the backup.
  Git remains the source of truth for desired state, so *deployments are not
  lost* — the first sync after restore converges every application.
- **RTO**: the drill below runs in under a minute plus your restore-media
  time. Measured by `scripts/dr-drill.sh`.

## Taking a backup

```bash
# 1. Stop the control plane. A copy under a live writer is corrupt.
systemctl stop heimdall     # or however serve is supervised

# 2. Archive the deployment.
heimdall backup --deployment /var/lib/heimdall --output /backup/heimdall-$(date +%F).tar.gz

# 3. Start it again.
systemctl start heimdall
```

The archive holds key material and sealed secrets. Store it with the same
care as the deployment itself.

Agents ride out the window: an outbound-only agent that cannot reach the
control plane keeps its workloads running and reconnects when serve returns;
parked syncs drain on reconnect.

## Restoring

```bash
heimdall restore --deployment /var/lib/heimdall --input /backup/heimdall-2026-08-16.tar.gz
heimdall doctor --deployment /var/lib/heimdall
heimdall serve --deployment /var/lib/heimdall --addr 0.0.0.0:8443
```

Restore refuses a non-empty target directory and rejects traversal in the
archive. Paths inside the deployment are relative, so restoring to a
different directory than the backup came from is supported and drilled.

After restore:
- Pre-disaster **sessions still verify** (the drill's exit assertion).
- Pre-disaster **agent certificates still verify** — agents trust the CA in
  the archive.
- Sessions and tokens issued *after* the backup was taken are gone; users log
  in again.

## The drill

```bash
scripts/dr-drill.sh
```

Run it whenever the backup format or deployment layout changes, and quarterly
regardless. It fails loudly if a pre-disaster session does not verify against
the restored control plane.

## Active/passive failover

Run a second `heimdall serve --standby` against the same deployment directory
on shared block storage. FYLO's exclusive root lock is the leader election:
the standby retries the open every five seconds and takes over when the
active process dies and the lock frees. No consensus protocol and no split
brain — the storage engine's own exclusivity is the arbiter. Front both with
a load balancer probing `/healthz`.
