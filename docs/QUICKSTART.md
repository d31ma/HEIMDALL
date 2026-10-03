# Quickstart

From nothing to a deployed compose application in about ten minutes.

## Prerequisites

- `heimdall`, `sesame`, `fylo`, and `chex` on PATH (`scripts/install-deps.sh`
  builds the last three from source), plus `git`.
- A Docker Engine — local, or on hosts you will enrol agents on.
- A deployment directory on **block storage**. Never a Dropbox/OneDrive/NFS
  path: FYLO's locking forbids sync filesystems and HEIMDALL refuses them.

## 1. Initialize

```bash
export HD_ADMIN_PASSWORD='choose-a-long-passphrase'
export HD_PUBLIC_URL='https://heimdall.example:8443'   # where agents will connect
heimdall init --deployment ~/.heimdall --issuer https://heimdall.example --admin alice
```

One command creates the tenant, the four role bundles (viewer, operator,
admin, owner), the TLS certificate and agent CA, the device-grant client, and
your administrator. It is idempotent.

## 2. Serve

```bash
heimdall serve --deployment ~/.heimdall --addr 0.0.0.0:8443
```

## 3. Sign in

```bash
export HD_ADDR_URL='https://heimdall.example:8443'
export HD_CA_FILE=~/.heimdall/keys/agent-ca.crt
heimdall login --device        # approve the short code in the web UI
```

(Or `HD_PASSWORD=... heimdall login --user alice` where a password in the
environment is acceptable.)

## 4. Register a repository, a target, and an application

Your compose file lives in git; HEIMDALL deploys what git says.

```bash
curl -s --cacert $HD_CA_FILE -H "Authorization: Bearer $(heimdall token)" \
  -H 'Content-Type: application/json' -X POST $HD_ADDR_URL/api/v1/repos \
  -d '{"project":"shop","name":"site","url":"git@github.com:acme/site.git","default_ref":"main"}'

curl -s ... -X POST $HD_ADDR_URL/api/v1/targets \
  -d '{"project":"shop","name":"prod-1","provider":"docker","endpoint":"unix:///var/run/docker.sock"}'

curl -s ... -X POST $HD_ADDR_URL/api/v1/projects/shop/apps \
  -d '{"name":"site","repo_id":"<repo>","target_id":"<target>","path":"deploy"}'
```

Targets may also be `swarm`, `ecs`, `cloudrun`, or `aca` — see
[`api/capabilities.md`](../api/capabilities.md) for what each runtime honours
and what it rejects at plan time.

## 5. Sync

```bash
heimdall sync --project shop --app site --dry-run   # the plan, applied nowhere
heimdall sync --project shop --app site
```

Or press **Sync** in the web UI, which shows the same plan, the live diff,
per-instance metrics and logs, and the deploy marker on the chart.

## 6. Let it run itself

```bash
curl -s ... -X PATCH $HD_ADDR_URL/api/v1/projects/shop/apps/site \
  -d '{"sync_policy":{"automated":true,"self_heal":true}}'
```

A new commit deploys within a minute (`HD_SYNC_INTERVAL`); a container killed
out-of-band is restored. Add a forge webhook pointing at
`POST /api/v1/webhooks/{repo}` to make pushes deploy immediately.

## Hosts the control plane cannot reach

```bash
heimdall enroll --target <target-id>            # prints a short-lived token
# on the Docker host:
heimdall agent enroll --token '<token>'
heimdall agent run
```

The agent connects outbound over mTLS and opens no port.

## A fleet

```bash
curl -s ... -X POST $HD_ADDR_URL/api/v1/projects/shop/target-groups \
  -d '{"name":"stores","selector":{"fleet":"store"}}'
# applications with "group_id" deploy to every matching target, in bounded
# waves, halting at the failure threshold.
```
