# Phase 7 evidence — GA

## Exit deliverables

| Deliverable | State | Where |
|---|---|---|
| Quickstart | **Done** | [docs/QUICKSTART.md](QUICKSTART.md) — nothing to deployed app, including agents, fleets, and auto-sync |
| Migration from raw `docker compose` | **Done** | [docs/migration/FROM_DOCKER_COMPOSE.md](migration/FROM_DOCKER_COMPOSE.md) |
| Migration from Portainer | **Done** | [docs/migration/FROM_PORTAINER.md](migration/FROM_PORTAINER.md) — concept map, steps, and the deliberate absences named |
| Support tiers | **Done** | [SUPPORT.md](../SUPPORT.md) |
| Versioned public API contract | **Done** | `api/openapi.json` and `api/capabilities.md`, both generated from code by `heimdall contract`; a test fails the build when the committed copy drifts |
| CalVer release | **Done** | `VERSION` = 26.33.1; the release workflow (tag `v26.33.1`) builds five platforms with checksums and GitHub-native provenance, and refuses a tag that disagrees with `VERSION` |
| Docs site | **Partial, deliberately** | The documentation is complete as markdown in-repo. A rendered site (Tachyon + DuVay, like the sibling projects') is presentation over the same files and is deferred to the first release that has an audience beyond the repository |

## Release checklist (what `git tag v26.33.1` triggers)

1. CI green: fmt, vet, tests with the 60% coverage floor, standing gates,
   golangci-lint, govulncheck, licence scan, web-tier checks, Playwright e2e.
2. The release workflow re-runs vet + tests + gates, cross-builds
   linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64,
   writes `checksums.txt`, attests provenance, creates the release.

## The honest global caveats, carried from earlier phases

- `scripts/e2e-docker.sh` has never run against a live daemon, and no cloud
  account has been touched: every runtime is proven against fakes that speak
  the real wire protocols through the real clients.
- CI itself has never executed on a hosted runner; every gate and suite runs
  locally.
- OIDC/SAML login flows and passkeys are engine-complete and host-deferred
  (Phase 5 evidence).
