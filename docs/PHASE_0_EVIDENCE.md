# Phase 0 evidence — Foundations and identity substrate

What the phase claimed, what is actually true, and how to check it yourself.
Anything not proven is listed as not proven.

## Exit criteria

> `heimdall version` ships from CI; schemas validate the compose corpus; a route
> with no grant returns `403` carrying SESAME's `reason_code`; the same route
> with the engine stopped returns `503`. Both proven by test.

| Criterion | State | Evidence |
|---|---|---|
| `heimdall version` ships from CI | **Partial** | The binary builds and prints its stamped version; `.github/workflows/ci.yml` asserts `heimdall version` equals `VERSION`. Not observed on a runner — this repository is not yet a git repository and no CI run exists |
| Schemas validate the compose corpus | **Proven** | `TestComposeCorpusValidates`, `TestDeploySpecValidates` |
| Unsupported directive rejected by name | **Proven** | `TestUnsupportedDirectiveIsRejectedByName` |
| No grant → `403` with `reason_code` | **Proven** | `TestUngrantedPrincipalGets403WithReasonCode`, asserting SESAME's `deny_no_grant` |
| Engine stopped → `503`, never a bypass | **Proven** | `TestStoppedEngineGets503AndNeverBypasses`, which first proves the same principal is authorized, then stops the engine |
| Single-binary release with checksums and provenance | **Not done** | Deferred with the release workflow; no `release.yml` exists |

## How to reproduce

```bash
scripts/install-deps.sh
go test ./... -count=1
scripts/ci-gates.sh
go build -ldflags "-X main.version=$(cat VERSION)" -o heimdall ./cmd/heimdall
./heimdall init --deployment /tmp/hd-dev
./heimdall doctor --deployment /tmp/hd-dev
./heimdall serve --deployment /tmp/hd-dev --addr 127.0.0.1:18080
```

The authorization and store tests drive real compiled `sesame`, `fylo`, and
`chex` binaries against throwaway deployments. They **skip** when a binary is
absent, so a green run with skips proves less than it looks like — CI installs
all three and fails if any is missing.

Observed on the local run: 33 tests pass, ~2,750 lines of first-party Go.

## What was built

| Package | Responsibility |
|---|---|
| `internal/spec` | `DeploySpec`, canonical JSON, SHA-256 content addressing |
| `internal/schema` | CHEX validation of compose input and `DeploySpec` output |
| `internal/store` | FYLO collections, queue topics, bootstrap, projection rebuild |
| `internal/auth` | SESAME supervision, action vocabulary, role bundles, fail-closed `Decide` |
| `internal/api` | Authorization boundary, route table, generated contract |
| `cmd/heimdall` | `init`, `doctor`, `serve`, `contract`, `version` |

The `${secret:...}` fixtures in the corpus carry references only; no fixture
holds a value, and a gate checks for credential-shaped strings.

## Three plan corrections found by implementation

Each was found by running the real dependency rather than by reading about it.

**1. Resource strings are colon-separated, not slash-separated.**
The plan specified `project:alpha/app:checkout`. SESAME's `ValidatePattern`
accepts lowercase alphanumerics, `-`, `_`, and `.` per colon-delimited segment,
and rejects `/` outright, so that form would have been refused on the first
real decision. Resources are now `project:alpha:app:checkout`, and a grant on
`project:alpha:*` covers the project — SESAME's wildcard is legal only as a
trailing segment. Recorded in ADR 0002.

**2. Collections are `hd-*`, not `hd_*`.**
FYLO's `validate_collection_name` accepts lowercase alphanumerics and `-` only.
`hd_projects` fails with `ENATIVE_COLLECTION`. SESAME uses `sesame-*`, so the
two namespaces still cannot collide. Recorded in ADR 0004.

**3. SESAME and HEIMDALL cannot share one FYLO root.**
The plan stated they would, making the root a single backup unit. FYLO takes an
exclusive lock per root: a second engine fails the handshake with
`EROOTLOCKED: FYLO root already has a live exclusive owner`. Observed directly
when `heimdall serve` opened the store on SESAME's root and `sesame doctor`
then refused to start.

HEIMDALL now keeps its own root at `<deployment>/fylo-root`, beside SESAME's at
`<deployment>/sesame/fylo-root`. The **deployment directory** is the backup and
restore unit — both roots plus `sesame/keys/`. That is arguably better than the
plan intended, since the key material was always outside the FYLO root and a
root-only backup was never sufficient. Recorded in ADR 0004.

## Deliberate deviations

**CHEX validates one compose service at a time, not a whole compose file.**
CHEX records require string values, so `services: {name: {...}}` — an object
map — is inexpressible. The renderer validates each service against
`schemas/compose-service.schema.json`. `DeploySpec` sidesteps this by
representing services as a sorted array, which canonical hashing wanted anyway.

**CHEX's strictness is used as a feature.** It rejects any property a schema
does not name. That is precisely the fail-closed behaviour the lossy-translation
risk calls for: an unmodelled directive fails by name rather than being dropped.
Adding a compose feature therefore means adding a schema entry, deliberately.

**`hd-audit` is authoritative, not a projection.** The plan lists audit among
the folded views. An audit log that can be regenerated is one that can be
rewritten, so it is append-only and authoritative. Recorded in ADR 0005.

**Rebuild folds in ascending document-id order.** TTIDs are time-ordered, so
that is chronological. An unordered fold cannot produce the byte-for-byte
reproducible projection the crash-safety gate requires.

## Not done in Phase 0

- **Release workflow.** No `release.yml`, no checksums, no provenance
  attestation. `ci.yml` covers fmt, vet, lint, test, `govulncheck`, and a
  licence allowlist.
- **CI has never run.** The workflow and `scripts/install-deps.sh` are written
  against the siblings' release-install convention but are unverified; the
  install URLs in particular are untested.
- **Coverage floors.** SESAME ratchets per-package coverage. HEIMDALL has no
  equivalent yet.
- **Index bootstrap.** Collections are created; FYLO prefix indexes are not.
  They land with the queries that need them in Phase 1.
- **`Fold` has no implementation**, only its contract. An operation has no shape
  until Phase 1, and the crash-safety test lands with it.
- **`heimdall agent`, `git`, `render`, `diff`, `reconcile`, `provider`,
  `observe`, `secrets`** — all Phase 1 and later. The directories do not exist
  rather than existing empty.
- **`docs/THREAT_MODEL.md`** — Phase 6 deliverable.

## Phase 1 preconditions this phase establishes

- Every route added from here goes into the table in `internal/api/api.go` and
  is authorized by construction; a route registered outside it is a review
  finding, and `TestUnauthenticatedSurfaceIsExactlyThree` bounds the exceptions.
- Adding a compose feature is a four-part edit: `DeploySpec` field, CHEX schema
  entry, `Capabilities()` answer, and the pinned-hash bump if the canonical form
  changes.
- The action vocabulary is closed and seeded as four role bundles. A new verb is
  a deliberate edit to `internal/auth/actions.go`.
