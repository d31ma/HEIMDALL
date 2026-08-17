# Phase 1 evidence — Vertical slice: Docker Engine

What the phase claimed, what is actually true, and how to check it. Anything
not proven is listed as not proven.

## Exit criteria

> A compose repo pulling a private image deploys to a real host; drift visible
> within 60s; a one-service change syncs cleanly; every mutating call lands in
> `hd-audit` with a principal and `policy_version`.

| Criterion | State | Evidence |
|---|---|---|
| Compose repo deploys | **Proven against a fake Engine** | `TestVerticalSlice` — real git, real FYLO, real render/diff/adapter code, fake only at the Engine's HTTP boundary |
| …against a **live** Engine | **Not run** | `scripts/e2e-docker.sh` is written and syntax-checked; no Docker daemon was available on this machine |
| Private image deploys | **Proven** | `TestPrivateRegistryCredentialsReachThePullAndNothingElse` — the credential reaches `X-Registry-Auth` and no persisted document |
| Drift visible | **Proven** | `TestDriftIsVisible`, `TestOutOfBandRemovalIsDrift`. Latency is not measured: Phase 1 reads on request, so "within 60s" is a Phase 2 polling claim |
| One-service change syncs cleanly | **Proven** | `TestOneServiceChangeSyncsCleanly` — asserts the untouched container is not recreated |
| Every mutating call attributable | **Proven** | `TestEveryDecisionIsAudited`, and `TestVerticalSlice` asserts `principal_id` and `policy_version` on the operation document |
| Tachyon UI | **Proven by hand, not by test** | `web/` holds the app list, app detail, diff view, sync, dry run, rollback, and history. Driven in a real browser against a real control plane: sign in, list, open an application, run a dry run, and see the failure and the audit row it produced |
| `heimdall login` over **device authorization** | **Proven** | The full RFC 8628 flow ran live: `heimdall login --device` printed a code, the code was approved through the web tier's gateway, the CLI received tokens, called the API with them, and refreshed with rotation when the token expired |
| Agent, outbound mTLS | **Proven** | `internal/agent` enrols, polls, applies, and reports. `TestEnrollAndDeploy` runs the real agent against a real TLS control plane; the enrollment, mTLS, poll, and report round trip was also driven end to end with the built binaries |

## How to reproduce

```bash
scripts/install-deps.sh
go test ./... -count=1        # 176 tests
scripts/ci-gates.sh
scripts/e2e-docker.sh         # needs a running Docker daemon
```

The reconcile, store, git, and authorization tests drive real compiled `git`,
`fylo`, `sesame`, and `chex` binaries and **skip** when one is absent. A green
run with skips proves less than it looks like.

## What was built

| Package | Responsibility |
|---|---|
| `internal/render` | Compose → `DeploySpec`: overlays with Compose's own merge rules, single-pass interpolation, `${secret:...}` rewritten to references |
| `internal/git` | Bare mirror, ref resolution, file reads at a commit, optional signature verification |
| `internal/provider` | The adapter interface, `Capabilities`, plan-time `Validate`, resource-tier snapping |
| `internal/provider/docker` | Docker Engine adapter over the Engine API, stdlib only |
| `internal/provider/conformance` | The suite every adapter must pass |
| `internal/provider/docker/dockertest` | A fake Engine, so the adapter is covered without a daemon |
| `internal/diff` | Field-level spec diff, sync status, health rollup |
| `internal/reconcile` | Refresh, status, sync, dry run, rollback, selective sync |
| `internal/enroll` | Fingerprint-pinned enrollment tokens, the agent CA, and the control plane's own TLS certificate |
| `internal/dispatch` | The rendezvous for outbound-only agents, and `Remote`, which makes an agent-managed target a `provider.Provider` |
| `internal/agent` | The host process: enrol, long-poll, apply, report |
| `cmd/heimdall` | `login`, `app`, `diff`, `sync`, `enroll`, `agent` added to the Phase 0 verbs |

## Decisions worth recording

**The Engine API is spoken directly, with stdlib `net/http`.** The Docker SDK
would pull a large dependency tree to wrap eight endpoints. Two Engine
behaviours are easy to get wrong and are covered by test: a failed pull is
reported as an error object *inside* a 200 stream, and log output is
multiplexed behind 8-byte frame headers.

**A per-service content hash is stamped as a label at apply time.** A plan then
compares one label instead of inspecting a container field by field, which is
what makes planning cheap enough to do on every status read.

**Interpolation is single pass.** A variable's value is data, never a further
instruction. That removes expansion-depth attacks entirely and makes a render a
pure function of (files, variables) — the host environment is never consulted,
so a render cannot depend on the machine that ran it.

**A `${secret:...}` reference must be the whole value.** Interpolating one into
a larger string would require resolving it at render time, which is exactly
what must never happen. Refusing is the mechanism, not a limitation.

**A rollback re-applies a stored revision and never re-reads git**, so a
force-push cannot change what rolling back means. A revision that was never
rendered for this application is refused.

**Selective sync carries dependencies.** Deploying a service without what it
depends on produces a broken application, so choosing `api` also brings `db`.

## Three more bugs implementation caught

The first two came from the fake Engine; the third came from running the real
binaries, which is why both kinds of test earn their place.

**A TLS certificate valid for five years is rejected by Apple's verifier and
by Chrome**, with the message "certificate is not standards compliant" — which
names neither the lifetime nor the 398-day rule that causes it. Agents never
saw it because they pin a fingerprint and skip chain validation; the CLI hit
it on the first real connection. Server certificates are now 397 days, and a
test pins the rule. Renewal is safe: an agent trusts the CA, not the leaf.

**`heimdall serve` holds FYLO's exclusive root lock**, so while it is running
nothing else on the host can open the store — including the `sesame` CLI,
which shares SESAME's root. That made it impossible to create the first user
or mint an enrollment token on a running system. Two fixes: `heimdall init
--admin NAME` bootstraps an administrator while init still holds the engine,
and `heimdall enroll` became an authorized API route rather than a local
command. The second is the better design anyway — minting a credential for a
host is a mutating action that should be authorized and audited.

**TTIDs are uppercase and SESAME's resource alphabet has none.** Every stored
document is keyed by a TTID, so `target:4VXTUING0MY` was rejected as a
malformed resource the moment a route addressed a target by id. `auth.Resource`
now lowercases identifiers, which is lossless rather than lossy: TTID
specifies identifiers as case-insensitive and canonically uppercase, so two
cannot differ by case alone.

## Two bugs the fake Engine caught

Both would have been silent in production and are now regression-tested.

1. **A stopped container reported Healthy.** The Engine retains the last health
   probe result after a container exits, so `healthOf` trusted a stale
   "healthy" on a dead service. Lifecycle is now checked before the probe.
2. **A deleted service was invisible to the health rollup.** `Observe` can only
   describe what exists, so a container removed out of band simply vanished
   from live state and the application still rolled up Healthy. The plan is now
   the evidence: a create operation for a service means nothing is running it,
   and the service is reported Missing.

## Three more FYLO behaviours found by running it

Phase 0 found three; these are new, and all three are handled at the store
boundary so nothing above it knows.

1. **`getLatest` returns an id-keyed envelope, not the bare document.** Decoding
   it directly produced a silently zero-valued struct — every field empty, no
   error. Now unwrapped in `Collection.Get`.
2. **FYLO refuses to index an array of objects inside a document**
   (`EARRAYOFOBJECTS`), and suggests a separate collection. A `DeploySpec`'s
   services and an operation's steps are neither independently addressable nor
   independently queryable, so a second collection would buy nothing and cost a
   join. The store encodes those arrays to a marked string on write and decodes
   on read; scalar top-level fields stay untouched, so `app_id`, `commit`, and
   `phase` remain queryable.
3. **Collection names reject underscores** — found in Phase 0, and the reason
   `hd-*` uses a hyphen.

## Plan updates applied in this phase

The plan gained a Portainer competitive analysis, which moved two items into
Phase 1. Both are implemented.

**Registry credentials (`hd-registries`).** A registry document holds a server,
a username, and a `password_ref` — never a value. At apply time the reference
resolves in process and becomes an `X-Registry-Auth` header on the pull. A
target-scoped entry beats a project-wide one, so a per-host credential is not
shadowed by a default. Creating a registry is authorized with `secret:bind`,
which is exactly what it is; the action vocabulary is unchanged and
`secret:bind` finally has a route.

**Fingerprint-pinned enrollment (`internal/enroll`).** The token carries the
control plane's URL, the target id, an expiry, and the SHA-256 of the control
plane's TLS certificate, under an HMAC. The agent pins that fingerprint before
it will send the token, so a machine-in-the-middle on the first connection is
refused with nothing disclosed. `TestPinnedConnectionAcceptsTheRealServerAndRefusesAnImpostor`
proves it with two self-signed servers distinguished only by the pin. Every
verification failure returns an identical error, because the caller is
unauthenticated and distinguishable refusals tell an attacker which part of a
guess was right.

`hd-target-groups` is **not** created. It is a Phase 2 item and nothing writes
it yet; an unused collection is scaffolding.

## The agent

`heimdall agent` is built and works end to end. The whole path was exercised
with the real binaries: `heimdall init --admin` bootstraps an administrator
and generates the TLS certificate and agent CA; `heimdall serve` listens over
TLS and reports the fingerprint agents pin; `heimdall enroll --target`
produces a token through the authorized API; `heimdall agent enroll` pins that
fingerprint, exchanges the token for a client certificate, and stores it 0600;
and the issued certificate then polls `/api/v1/agent/work` successfully while
a request without one gets a 401.

Three things about the design are recorded in ADR 0008: the agent opens no
port, an agent is not a principal, and a remote target is a
`provider.Provider` so the reconciler has one code path rather than two.

Not done on the agent: the stats scrape and log streaming, which are Phase 3
and are refused with a message naming the phase rather than returning empty.

## The web tier

`web/` is a Tachyon project with two Yon routes and two Tac pages. It holds no
business logic and makes no authorization decision — see ADR 0009 for why the
session is an httpOnly cookie and why there is one gateway route rather than
seven.

Verified in a browser against a live control plane: the login form signs in and
the cookie comes back `HttpOnly; SameSite=Strict`; the application list renders
from `/api/v1/projects`; the detail page shows sync status, health, the desired
revision, and per-service diff cards; a dry run reaches the Docker adapter and
its real failure surfaces with the `HD0312` code intact; and the resulting
operation appears in the history table with its principal and timestamp.

Three defects were found and fixed while doing that, all invisible without a
browser:

1. **The page script ran before its own DOM existed.** Tac constructs the
   document in the browser from a render plan, so `getElementById` returned
   null and both pages died on load. `whenReady` observes until the elements
   appear and gives up loudly rather than hanging.
2. **Every DuVay token name was wrong.** The tokens are `--w-surface` and
   `--w-text`, not `--w-color-surface`. Because each `var()` had a light-theme
   fallback, nothing looked broken until the theme switched — the page then
   rendered dark cards on a light background with unreadable headings.
3. **Long control-plane errors overflowed their container**, cutting off the
   tail, which is usually the actionable part.

**Not verified:** a real narrow viewport. The browser pane would not apply its
mobile emulation, so the responsive rules and the 44px touch targets are
reasoned about and not observed. Tables now scroll inside their own box rather
than pushing the page sideways, which is the fix that matters there, but it
should be checked on a phone before anyone claims it.

## Not done in Phase 1
- **Visual regression across the DuVay themes.** `scripts/e2e-web.sh` now runs
  five Playwright tests against the real stack — login refusal, the list and
  detail pages, session lifecycle including server-side logout, a dry run, and
  a theming smoke check — and the gateway's allowlist has 17 `node:test`
  cases, each asserting a refused request never reached the control plane.
  What remains unproven is pixels: theme correctness is checked by attribute
  and token, not by screenshot baselines.
- **Secret-manager integrations.** `${secret:...}` resolves through a
  configured resolver, and `heimdall serve` currently configures one that
  refuses every reference with a message naming it — better than starting a
  container with the variable quietly missing. Phase 5 replaces that one
  function.
- **`scripts/e2e-docker.sh` has never run.** No Docker daemon was available.
- **The queue consumers.** `repo.poll`, `app.render`, `app.reconcile`,
  `app.observe`, and `metric.rollup` are named constants; Phase 1 syncs
  synchronously on request, which is what manual sync means. Auto-sync in
  Phase 2 adds a consumer in front of the same `Sync` call.
- **`Fold` still has no implementation**, so the crash-safety rebuild test is
  still outstanding.
- **CI has still never run**, and it does not yet build or check the web tier.
