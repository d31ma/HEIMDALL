# Migrating from raw `docker compose`

You already have the hard part: a compose file that works. HEIMDALL deploys
that same file from git instead of from a shell.

## What changes

| Before | After |
|---|---|
| `docker compose up -d` on the host | `git push`, then auto-sync (or the Sync button) |
| The running host is the truth | Git is the truth; the host is compared against it |
| "Who changed this?" is folklore | Every operation records principal, revision, and the policy that allowed it |
| `.env` files beside the compose file | `${secret:...}` references resolved at apply time |

## Steps

1. **Commit your compose file** (and overrides) to a repository, e.g. under
   `deploy/`.
2. **Move secret values out of the file.** Replace literals with references:

   ```yaml
   environment:
     DATABASE_URL: "${secret:aws-sm/eu-west-1/prod/db-url}"
   ```

   Stores: `local/` (sealed on the control plane), `aws-sm/`, `azure-kv/`,
   `gcp-sm/`. Seed local ones with `heimdall secret set`.
3. **Register** the repo, the target, and the application (quickstart §4).
4. **Dry-run**: `heimdall sync --dry-run` shows exactly what would change.
   The first sync adopts cleanly: HEIMDALL labels what it creates and never
   touches containers it does not own — your hand-started containers are
   invisible to it until the compose-managed ones replace them.
5. **Check the plan-time answers.** Anything your file uses that a target
   cannot honour is rejected with the service named — see
   [`api/capabilities.md`](../../api/capabilities.md). On plain Docker Engine,
   everything compose does locally keeps working.

## What you stop doing

SSH-ing to hosts to deploy. `docker compose up` in a tmux. Wondering whether
prod matches the file. Keeping `.env.prod` in a password manager paste.
