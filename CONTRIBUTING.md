# Contributing

## Before you start

Read [PLAN.md](PLAN.md) for the architecture and roadmap, [CONTEXT.md](CONTEXT.md)
for the domain language, and [AGENTS.md](AGENTS.md) for the rules that are
non-negotiable. A change that violates one of those rules is a defect in the
change, not a follow-up task.

## Setup

```bash
scripts/install-deps.sh     # fylo, sesame, chex into ~/.local/bin
export HD_BUILD_DIR=/tmp/heimdall-build
mkdir -p "$HD_BUILD_DIR"
go -C app/server build -o "$HD_BUILD_DIR/heimdall" ./commands/heimdall
scripts/native-go.sh test
scripts/ci-gates.sh
```

Tests drive real compiled binaries rather than fakes. An authorization test
against a stub would return "allow" and prove nothing. Tests skip when a binary
is absent, so check for skips before believing a green run.

The public website is a separate Tachyon project under `website/`.

Build and test only in an isolated execution checkout with external caches,
output and runtime state. These current command paths are pending qualification;
historical passing receipts belong to their recorded source layouts.

## What CI enforces

`gofmt`, `go vet`, `golangci-lint`, `go test`, `govulncheck`, a dependency
licence allowlist, and `scripts/ci-gates.sh`. The gates check for absence: no
password hashing, no role-name comparison, no token minting, no literal
credentials in fixtures, no cloud SDK outside `app/server/services/provider/`, no
shell-invoked subprocesses and no generic `utils`/`helpers`/`common` Go packages.
The requested `app/server/helpers/` and `app/server/utils/` role directories contain
named domain packages. The native commands and these domain packages share the
merged Go 1.27 module with 23 BFF fragments. BFF fragments must import no native
authoritative domain package and retain public-HTTP forwarding; explicit CI
checks replace the former Go `internal/` visibility guard. Operational application logic belongs under `app/`; the public site lives in
sibling `website/`. The merged module, native fixes and framework intake need
fresh qualification. Authored tests live under sibling `tests/`: native Go tests
in `tests/server/`, operational UI/HTTP tests in `tests/app/`. Use the explicit
`scripts/native-go.sh` scope and its Go overlay or exact isolated staging closure;
a direct package test without mapped test sources could falsely run no cases.
Overlay test discovery and whitebox package access remain unqualified.
Do not traverse package-less Yon fragments with an unfiltered `./...`.

## Conventions

- Diagnostic codes are `HD` plus four digits. Errors are typed and actionable;
  never parse human-readable error text.
- One environment namespace: `HD_`.
- Vertical slices, not horizontal layers. Bound input, recursion, queues,
  concurrency, retries, and external reads.
- Generated output identifies its source and is never hand-edited. Regenerate
  the API contract with `heimdall contract --write api/openapi.json`.
- ADRs only for hard-to-reverse decisions with real tradeoffs.
- Comments explain why, not what. If a shortcut has a known ceiling, say what
  the ceiling is and what replaces it.

## Two things that will bite you

**Never put a FYLO root inside a sync filesystem** — Dropbox, iCloud, OneDrive,
or a network mount. FYLO's locking forbids it and the failure is silent
corruption found much later. `store.Open` refuses the obvious cases; point
`HD_FYLO_ROOT` at `/tmp` or `~/.heimdall` in development.

**Changing `spec.DeploySpec` changes every stored revision hash.**
`TestHashIsPinned` will fail. If the change is intentional, bump
`spec.SchemaVersion` and update the pinned constant in the same commit.

## Vendored code

`app/server/repositories/store/fylo` and `app/server/helpers/schema/chex` are copied verbatim from
upstream Rust projects that do not publish Go modules. Do not edit them here —
change them upstream and re-run `scripts/vendor-clients.sh`. SESAME is a real Go
module and is a `go get`.
