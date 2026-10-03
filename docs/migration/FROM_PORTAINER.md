# Migrating from Portainer

Portainer manages containers; HEIMDALL manages the *agreement between git and
your containers*. The mental shift: stop editing the runtime, start editing
the repository.

## Concept map

| Portainer | HEIMDALL |
|---|---|
| Stack | Application (compose file at a path in a repo) |
| Endpoint | Target |
| Endpoint group + tags | Target group (tag selector; membership derived) |
| Edge agent | `heimdall agent` (outbound mTLS, no open port) |
| Edge stacks fan-out | Group applications: bounded waves, failure threshold |
| GitOps updates (polling) | Auto-sync, self-heal, webhook nudge |
| Teams/RBAC | SESAME projects, role bundles, SCIM group mappings |
| App templates | Not shipped (deliberately; see PLAN.md deferrals) |

## Steps

1. **Export each stack's compose file** from Portainer (Stacks → editor) and
   commit it to a repository per project.
2. **Recreate endpoints as targets.** Local socket endpoints become `docker`
   targets; Swarm endpoints become `swarm` targets pointing at a manager;
   Edge endpoints become agent-managed targets (`heimdall enroll` on each
   host — the enrollment token replaces Portainer's edge key, and pins the
   control plane's certificate).
3. **Tag your fleet** on the targets and create groups with selectors —
   membership is derived, so re-tagging a host moves it with no group edit.
4. **Create applications** pointing at the committed files; dry-run; sync.
   Portainer-started stacks are adopted the same way hand-started compose is:
   HEIMDALL replaces them with labeled equivalents on first sync.
5. **Map your teams**: if your IdP provisions via SCIM, one mapping per
   directory group (`POST /api/v1/projects/{p}/group-mappings`) replaces
   per-user administration entirely.

## What you will not find

A container console, start/stop buttons for arbitrary containers, or an
exec-into-container shell. These are deliberate absences, not gaps: they are
the escape hatches that make the runtime drift from git. Streaming logs,
metrics, restarts-by-sync, and rollback are all present.
