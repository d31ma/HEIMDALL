# Changelog

All notable changes to HEIMDALL are recorded here. Versions are CalVer,
`YY.WW.DD`, derived in UTC.

## [Unreleased]

### Added

- An ECS target can name a `capacity_provider` — `FARGATE_SPOT` buys
  interruptible capacity at roughly a third of the price, which is the
  right trade for a staging or batch target and the wrong one for
  production. It is opt-in per target and mutually exclusive with the
  launch type, because ECS refuses a service that carries both.

### Changed

- The website documented a product two releases old and made three claims
  that were no longer true: Cloud Run replicas as full support (it is
  partial), a metrics-and-logs page for "every instance" (Cloud Run and ACA
  refuse both by design and name the platform store instead), and a version
  footer stuck at 26.33.1. Fixed, and the gaps filled: fleet fan-out to
  target groups, ECS load-balancer attachment and the `config` keys every
  cloud target needs, the registry manifest's full field set (`ref`,
  `overlays`, `variables`, `suspended`), a worked one-repository/several-
  environments example, the SCIM and audit-export surface, and the agent's
  own environment variables.

- Three one-line `errors.As` wrappers and a duplicated per-endpoint engine
  cache are gone: callers use `errors.As` directly, and the Docker and
  Swarm adapters share one `engineCache` — they speak to the same Engine,
  so they pool the same way. Net 24 fewer lines, no behaviour change; the
  connection-reuse regression tests cover it.

## [26.34.02] — 2026-08-18

The first release cut after the product deployed itself: HEIMDALL now runs
its own website on ECS, hosts its own control plane, and reconciles all
three of its environments from a root repository. Every fix below was found
by that live use — a real Docker Engine, a real load balancer, a real agent —
and none of them by the fakes, which is the honest argument for having done
it.

### Upgrading

Containers an agent deployed before this release carry a project label
derived from the target's region, and the corrected identity does not match
them: the first sync after upgrading plans them as absent and recreates
them under the right labels. Expect one brief replacement per
agent-deployed service, and let self-heal do it rather than removing
containers by hand.

### Added

- The ECS adapter registers a service into a load-balancer target group when
  the target's config carries `target_group_arn` — the seam between
  HEIMDALL and the infrastructure that owns the ALB, TLS, and DNS. With two
  ported services the adapter refuses to guess (HD0363) until
  `load_balanced_service` names the fronted one. Attachment happens at
  service creation; a service that predates the target group needs
  recreating to pick it up.

### Changed

- The Docker Engine API pin moves from 1.43 to 1.44 (Docker Engine 25,
  January 2024). Modern engines now enforce a minimum client API version
  and refused the old pin outright — found by the first live agent
  enrollment, not by any fake.

### Fixed

- The instance, metrics, log and event routes picked the local adapter by
  provider name, so an agent-managed target answered "docker engine
  unreachable at http://docker" from a control plane that has no such
  socket — while the same application's status, which resolves through the
  reconciler, read fine. Reads now resolve exactly as writes do.

- Agent-dispatched applications read back correctly: Remote.Plan stamped
  the target's *region* into the workload identity (a fossil from before
  targets had a project field), so the agent labelled containers with
  project "ca-central-1" while every observe filtered on the real project —
  the app served traffic and planned clean no-ops, yet its status read
  Missing with no live revision, forever. One identity now flows both
  directions, regression-tested through the real Remote → dispatcher →
  agent → fake-engine loop.

- The Docker and ECS adapters built a fresh HTTP transport per call, and a
  dropped transport never returns its keep-alive sockets — one leaked
  connection per reconcile or scrape poll, forever. A long-running demo
  control plane accumulated ~16k connections against its engine and
  exhausted the host's ephemeral ports, taking down all outbound
  networking. Docker and Swarm now cache one engine client per endpoint;
  ECS shares one SDK HTTP client while still resolving credentials per
  call. Regression tests count accepted connections across thirty polls.
  Cloud Run and ACA were audited: their SDKs pool through shared
  platform transports and do not leak.

- The registry diff never compared an application's overlays or variables,
  and its patch never wrote them — an overlay change in the manifest
  silently never propagated, and a removed overlay file left the document
  rendering a path that no longer exists (HD0240). Found by the staging
  environment's first overlay removal; regression-tested.

- The instance dashboard read nothing on a cloud target: the page called
  the logs route without the service name, and cloud adapters rebuild the
  provider-side stream name from it, so ECS asked CloudWatch for a stream
  with an empty service segment and got a not-found. The panel set also
  assumed every provider answers all ten Docker metric groups, leaving a
  CloudWatch-backed target showing eight panels "Collecting…" forever; the
  panels now derive from the series the provider actually returned, gated
  on sample count.

- `website/Dockerfile` had never met a real Docker daemon, which hid three
  defects the first build surfaced: the ty installer reads `TAC_INSTALL_DIR`
  (not `INSTALL_DIR`) so the binary never landed in `/opt/ty`; it downloads
  from `releases/latest` unless `TAC_BASE_URL` pins it, so the version pin
  was an illusion; and `ty-linux-x64` needs glibc ≥ 2.39, so the base image
  moved from bookworm to trixie. The first live deploy added a fourth: ty
  binds loopback unless told otherwise, so the container passed its own
  healthcheck while failing every ALB probe — the CMD now binds 0.0.0.0
  with `--allow-non-loopback`.

## [26.34.01-1] — 2026-08-17

Re-cut of 26.34.01: that tag built and checksummed all five platforms but
could not publish — GitHub artifact attestation is unavailable to private
repositories, and the workflow treated it as load-bearing. Attestation is
now conditional on the repository being public, with release notes that say
which path was taken. No product change beyond the workflow fix.

### Fixed — regressions the release audit caught in the Tac migration

The web e2e suite (`scripts/e2e-web.sh`) caught three regressions the page-class
migration introduced; all eight UI tests pass again.

- Sign-in sent empty credentials: `signIn` re-rendered before reading the
  form, and a re-render rebuilds the inputs empty. The fields are now read
  first, and the username is data-bound (`:value="identifier"`) so it
  survives the re-render; the password is deliberately not kept. The
  migration also dropped `id="hd-login-error"` from the alert region — the
  uniform refusal rendered, but outside the page's stated contract.
- The application page lost its `Instances` heading, its visible
  "Nothing is running" empty state (it existed only inside the hidden Table
  tab), the `#hd-action` region around the action-result card, and the
  `hd-timeseries` import. A failed dry run or sync now reports in the action
  card ("Dry run — failed" plus the control plane's HD-coded message)
  instead of vanishing into the page banner.
- `scripts/e2e-web.sh` refuses to start when its ports are occupied and
  takes `HD_E2E_CP_PORT`/`HD_E2E_WEB_PORT` overrides — a stale listener on
  18443 made the health check pass against the wrong control plane and every
  later step fail on a CA mismatch.

### Security

- Toolchain pinned to go1.26.6 (`toolchain` directive): go1.26.0 left 25
  stdlib vulnerabilities reachable from this module, including crypto/tls,
  crypto/x509, and net/http — the front door of an mTLS control plane.
  `govulncheck` now reports zero reachable vulnerabilities; the one
  remaining module-level advisory (GO-2026-5932, golang.org/x/crypto) has no
  fixed release and no call path from this code.

### Added

- Four more instance-dashboard panels from data the scrape already carried:
  PIDs (gauge, folded by per-minute max — a fork leak shows in the peak),
  CPU throttling (throttled periods/min), network errors (rx/tx errors and
  drops folded into one packets/min series), and memory as a percentage of
  the limit (derived in the page from the bytes series — nothing new
  shipped). The Docker adapter now parses `pids_stats`, `throttling_data`,
  and per-interface error counters; rollups carry the three new fields, and
  agent-shipped rollups inherit them through the shared Accumulator.
- `fake-engine` accepts a fixed listen address, so a demo target registered
  against it survives the fake being restarted.

### Changed — Tac page-class migration

- The app list went fully declarative as the proof of the pattern: all of
  its markup now lives in `tac.html` using the framework's control tags —
  `<if :when>` branches for the loading/login/empty/list states and the
  suspended chip, nested `<loop :for>` for projects and their rows,
  `{field}` interpolation (escaping now comes from the template engine, not
  a hand-rolled helper), `:attr` bindings, and `on:event="method()"`
  handlers resolving against the page class. The class holds only data and
  behaviour — hrefs are precomputed fields because the bounded expression
  language deliberately has no calls. Two runtime facts the migration
  surfaced: an `on:event` argument arrives as the event object itself
  (read `$event.target.value` in the method, not the binding), and the
  runtime re-renders after every handled event — so search filtering is
  data (`term` + `visibleProjects` fields) rather than DOM hiding, with
  `:value="term"` keeping the input across re-renders.
- The application page followed: status chips, the view toolbar, the
  instance table, the Activity table, the action-result card, and the
  two-step rollback are all `<if>`/`<else>`/`<loop>` template markup bound
  to precomputed fields (`:class` carries chip classes as data). The
  resource tree stays the one imperative island — an SVG-drawing web
  component the class re-feeds after every render, since a re-render
  recreates the element. Third runtime fact for the record: event handlers
  are invoked as `(event, ...declaredArguments)` — the DOM event is always
  prepended to whatever the binding names. Fourth: the runtime re-renders
  once more after every handled event, after the handler's own rerender —
  so the tree island is re-fed by a MutationObserver that catches every
  fresh element rather than by the handler that just ran.
- The instance dashboard completed the set: the stat tiles and the ten
  chart panels are template loops over fields (`heimdall.metrics` selection
  now decides which panel definitions exist rather than hiding rendered
  ones), the breadcrumb interpolates, and the charts and log tail are
  observer-fed islands updated in place on each poll — a metrics poll never
  re-renders the page unless the panel set itself changed. All four pages
  now hold zero HTML in their scripts.

- The four page companions now follow Tac's released page-class contract: a
  default-exported class the runtime constructs, with lifecycle methods
  named in `static __tachyonOnMount` (the hand-authored form of the
  `@onMount` decorator). Page state (`project`, `app`, DOM refs, poll
  timers) lives on the instance; stateful flows are methods; pure template
  helpers stay module functions. One trap the migration surfaced, worth
  recording: the runtime awaits `onMount` *before* it renders the document,
  so a mount that awaits `whenReady('hd-main')` deadlocks the page — mount
  schedules the flow without awaiting it, and `whenReady` resolves once the
  renderer has run. `<hd-timeseries>` and `<hd-apptree>` stay plain web
  components (already classes; the spec explicitly permits unregistered
  hyphenated elements, and Tac components cannot be created from dynamic
  `innerHTML` anyway).

### Added — mascot, favicon, and a responsive sweep

- The application wears the mark's colours: `--w-primary` is now
  Heimdallr's amber (darker roast `#b45309` on light so text and fills keep
  their contrast, `#f5a623` glow on dark), reaching every reader of the
  token — buttons, links, the view toggle, focus rings, and the chart line
  itself. Chips and buttons trade DuVay's pills for slightly rounded
  corners (0.45rem).

- HEIMDALL has a mark: Heimdallr's amber iris, the one that sees a hundred
  leagues by day or night — just the fibrous amber iris, dark limbal ring,
  pupil, in the faint glow the bridge-keeper's eyes give off; no lids,
  because this eye does not blink. What it reflects is what it monitors:
  the corneal glint is a tiny dashboard with a metrics sparkline, the live
  series glows green in the pupil where reflection is strongest, and log
  lines shimmer across the lower iris
  (`web/client/shared/assets/heimdall-mark.svg`). It sits
  in every page header and is the favicon; Tac owns the `<head>`, so the
  icon link is injected by the shared `api.js` module every page already
  imports. Fixed brand colours by design — a mark keeps its identity across
  themes.
- Responsive sweep at 375/768/1400: chart panel columns floor at
  `min(24rem,100%)` so a phone gets one full-width column instead of a
  sideways scroll; the rollback select yields to the viewport rather than
  pushing it (a stored revision's message can be long); search fields go
  full-width when the toolbar wraps; the resource tree keeps its natural
  size and pans inside its scroll box instead of shrinking to confetti.
  A Playwright sweep asserts zero horizontal page overflow on all three
  pages at all three widths.
- Cascade layers now express the styling contract explicitly: DuVay imports
  into a `duvay` layer below Tailwind's `utilities`, so a utility in the
  markup reliably overrides a DuVay base rule (unlayered DuVay used to beat
  every layered utility on conflict — which is exactly how the tree SVG got
  squeezed by DuVay's `max-width`). `app.css` stays unlayered and outranks
  both.

### Changed — UI fluidity (Tailwind)

- Tailwind CSS v4 joins the web tier as a utilities-only layer: `npm run
  css` compiles `tailwind.src.css` to a committed `tailwind.css` (~8KB) —
  static files, no bundler, no CDN; a control plane must not fetch its
  stylesheet from the internet. Preflight is deliberately excluded and no
  Tailwind colour is used anywhere: DuVay keeps the base styles, tokens, and
  themes; Tailwind supplies motion, elevation, and interaction.
- What moved: the sticky header gained backdrop blur over a translucent
  surface; pages and chart panels fade in; stat tiles and panels lift with a
  shadow on hover (disabled under `prefers-reduced-motion`); table rows and
  search fields transition instead of snapping; the log tail scrolls
  smoothly.
- Full conversion followed: every layout, spacing, and typography rule moved
  from `app.css` into Tailwind utilities in the markup. The `hd-*` class
  names remain as JS and test hooks. `app.css` shrank to what utilities
  cannot carry — the base document, token aliases (`hd-muted`, `hd-link`,
  the mono/diff colours), the scroll edge-fade (four stacked backgrounds),
  child-selector row hover, keyframes, and the global accessibility rules
  (44px touch targets, `prefers-reduced-motion`). Colours everywhere are
  still DuVay tokens, reaching the markup via arbitrary values like
  `text-[var(--w-success,#1b7f3b)]`.

### Added — metrics retention knob

- `HD_METRICS_RETENTION` extends how long minute rollups live and how far
  back the served series reaches, from the default 25h up to 336h (14 days).
  A typo or out-of-range value fails `serve` at startup with HD0395 — before
  the FYLO root lock is taken — rather than falling back silently: the
  consequence of a bad value would be a week of history pruned to a day,
  discovered during exactly the incident it was kept for. Log retention is
  deliberately unchanged: HEIMDALL persists no log lines; rotation belongs
  to the runtime's logging driver or the cloud's log store.

### Added — the website ships as its own workload

- `website/Dockerfile`: the site as a container — a pinned ty binary
  fetched in a build stage, the committed pages served with
  `ty serve --no-watch`, a non-root user, a health-probe curl and nothing
  else. The build fails loudly if DuVay was not vendored first, because
  shipping token fallbacks would quietly unstyle every component.
- `website/deploy/compose.yaml`: the website declared for HEIMDALL to
  deploy to Amazon ECS — two replicas, a healthcheck, CPU and memory
  limits — with the matching registry manifest entries in the header
  comment. A repo-integrity test renders that file and clears it through
  the ECS adapter's capability matrix, so an edit to either can never
  quietly break the product's own deployment.
- Found by inspection while wiring the image fetch: `install-deps.sh`
  built an impossible URL for *pinned* dependency versions
  (`releases/download/v…/download/install.sh` — asset names cannot nest),
  which CI's pinned SESAME and FYLO would have hit on the first real
  runner. Fixed for both the script and the Dockerfile.

### Changed — CI brought up to TACHYON's standard

- The CI workflow adopts the conventions TACHYON's pipeline proved:
  timeouts on every job, `actions/checkout` and `setup-node` pinned to the
  commit SHAs TACHYON's own CI verified rather than movable tags, and the
  new surfaces gated. New jobs and steps: sops and age install via the Go
  module proxy (checksum-pinned) so the SOPS end-to-end tests run instead
  of skipping silently — plus a step that *fails* if any binary-backed
  suite skipped, because a politely-skipping CI proves nothing; a website
  job serving `website/` with ty and running its committed audit suite; and
  a Tailwind freshness check (the committed sheets must match a rebuild).
- Housekeeping the workflow surfaced: `install-deps.sh` now fetches ty
  (TACHYON) too; `vendor-duvay.sh` feeds both `web/` and `website/` and
  both copies are gitignored with a CI check; `e2e-web.sh` runs only its
  own harness-compatible suite by default, while the demo-stack specs
  (views, fullpath) mark themselves CI-skipped as the developer tools they
  are, and the site suite takes `SITE_URL` from the environment. The
  release workflow now installs the real binaries before its gate — a
  release check whose tests skip proves nothing — and pins checkout.

### Added — the website

- The documentation grew from one page to ten, ArgoCD/Grafana-shaped: a
  persistent sidebar (build-time Tac component, shared with a header
  component across every page), current-page marking, and a page per
  feature area — Get started, Core concepts, The registry in git,
  Providers and capabilities (with the honest-matrix highlights as a
  table), Sync/drift/rollback, Secrets, Agents, Observability, Security
  model, and Reference (CLI, environment, diagnostic ranges, contract
  locations). Article typography is scoped element CSS rather than
  utilities-per-paragraph; wide tables scroll inside their own box on
  phones. One more renderer finding joined the record: the SPA leaves
  *all* character entities undecoded, not just braces — the shared docs
  companion now decodes the full set in code elements, and prose avoids
  entities outright. The committed sweep covers all ten pages at two
  widths: zero overflow, zero undecoded entities, sidebar marking, and no
  console errors.
- The docs audit that followed found six defects, all fixed: every page had
  an empty `document.title` (now derived from the article heading), no
  favicon (the companions inject the mark, as the product UI does), no
  skip-to-content link (now the first tab stop, visible on focus), no
  `lang` on the document, no previous/next pagination (now generated from
  the sidebar's own order — one source of truth), and table headers without
  scope. Passing already: single h1 per page, correct heading order, all
  five landmarks, and 13:1 inline-code contrast in both themes.

- `website/` is the product site: Tac pages only (no Yon routes), DuVay +
  Tailwind, the same styling contract as the app itself — DuVay in a
  cascade layer below the utilities, amber brand tokens, the eye as hero
  and favicon. A landing page (the four-mechanics loop, six feature cards,
  the five-provider strip, the security section with the declared-registry
  sample) and a five-step Get Started page. Static output; the compiled
  Tailwind sheet is committed and the build borrows `../web`'s Tailwind
  install so the repo carries one copy.
- Two Tac findings the site surfaced, recorded for the next author: the
  compiler reads a literal `{` in text as a template expression, and the
  SPA renderer leaves character entities undecoded (the bundle path
  decodes them — the Tachyon site's own dist proves it), so code samples
  are authored as `&#123;`/`&#125;` and decoded by a small page companion
  after render. A responsive sweep at 375/768/1440 asserts zero horizontal
  overflow on both pages.
- The UI/UX audit that followed found and fixed four defects: the code
  samples used a DuVay *surface* token that resolves light in the light
  theme under near-white text — a 1.05:1 contrast ratio — and are now a
  fixed dark terminal (13.9:1) in every theme; no script set the `w-theme`
  attribute, so dark-scheme visitors were pinned to light — the page
  companions now set `auto`; anchor targets scrolled under the sticky
  header — `scroll-margin-top` clears it; and site links had no visible
  keyboard focus — they outline in the brand amber now.

### Changed — app UI audit

- The same audit that hardened the website, run against the app: the
  document language is now set (`en`, beside the theme and favicon in the
  shared module); every page gains a skip-to-content link as the first tab
  stop; the view switcher's ARIA was actually broken — a boolean
  `:aria-selected` binding emits an *empty* attribute for true, which
  assistive tech reads as invalid, so the state is now the literal strings,
  with `aria-controls` on the tabs and `role="tabpanel"` on the panels;
  and keyboard-focused instance rows show a visible outline (DuVay's table
  reset had removed it). Checked and already passing: titles per page,
  favicon, landmarks, single h1s, dark theme end to end (including the
  chart SVGs, which read DuVay tokens), chip contrast in both themes, and
  zero horizontal overflow at phone width on every page including the
  registry. Accepted with rationale: tree nodes are mouse-only — the Table
  view is the keyboard-accessible twin of the same data.

### Changed — Yon routes follow the module convention

- The gateway and session routes each split into `yon.js` (the HTTP shape),
  `service.js` (the flow — allowlist, cookie policy, timeout clamping), and
  `repo.js` (the data access — which in this tier means the control-plane
  API call, since FYLO is exclusively locked by `heimdall serve` and the
  web tier has no database). The shared transport lives once in
  `server/controlplane/client.js` — one CA story, one timeout story, one
  bounded-read story — replacing the forty-line copy each route carried.
- The enabling discovery, corrected from this repo's own folklore: Tachyon
  *ignores* non-`yon.*` siblings in a route directory rather than rejecting
  them, and the "a Yon handler cannot import a local module" comment was
  half-true — a static relative import cannot resolve (the handler is
  evaluated without its own file URL as a base), but a dynamic import with
  an absolute file URL resolves fine. Siblings load module-relative first
  (plain Node: tests) with a working-directory fallback (the Yon runtime,
  whose cwd contract `bff.json` reads already rely on). `audit-login` follows the
  convention too, and its flow now delegates to the session route's own
  sign-in — one login flow, one cookie shape, one place both can be right
  (the hand-rolled cookie it previously minted lacked the Secure-flag
  handling the real login had).
- The record on Yon languages, corrected twice over: the built-in adapters
  are exactly two — `yon.js` (javascript.v1) and `yon.py` (python.v1);
  TypeScript is a Tac-side companion, not a Yon language. And no language
  is excluded: anything else — Go included — participates as a
  `direct.v1` handler via a `.tachyonrc` interpreter registration or an
  executable speaking the bounded length-prefixed JSON handler protocol.
  The earlier "yon.rs/yon.cpp unsupported" citation was an archived
  `ty migrate check` snapshot classifying a bare source file with no
  interpreter, not a language ban.
- Behaviour is unchanged by design: same HD06xx codes, bounds, cookie
  flags, and pass-through semantics; all 20 gateway tests and the browser
  flows pass unmodified.

### Added — the registry view

- `/registry` in the web UI is ADR 0010's interactive surface as a page:
  unbound, it offers the bind form (URL, ref, path, prune and signature
  toggles) and the app list's empty state now points fresh installations at
  it; bound, it shows the binding facts, a Sync now button, the recent
  sync history (phase, commit, what happened, each change), and a two-step
  unbind with the managed-documents caveat spelled out. The status route
  now carries the recent registry syncs, so the page's question — "is the
  registry converged, and what changed last" — is one answer from one
  route. Declarative Tac shape like every other page. Two page bugs the
  live walkthrough caught: reading form values *after* a re-render reads
  freshly rebuilt empty inputs, and a refresh that clears the error field
  was erasing failure messages before anyone saw them.
- The demo deployment is now bootstrapped entirely through this page: a
  fresh control plane, one bind of a root repository whose manifest
  declares the whole demo — project, repository, target, application —
  and the first sync creates all four and the workload deploys itself.

### Added — the registry loop (ADR 0010 implemented)

- ADR 0010 is now code. `POST /api/v1/registry/bind` (SESAME action
  `registry:bind`, owner-bundle only) names a root repository; its
  `registry.yaml` declares projects, repositories, targets, and
  applications by name, parsed fail-closed (an unmodelled field fails by
  name) and published as `schemas/registry.schema.json`. The registry
  engine diffs the manifest against the `hd-*` collections and closes the
  gap in dependency order — creates, in-place updates, visible adoption of
  interactively-registered documents, and prune-gated deletion — recording
  each pass as one operation document (`reason_code: registry_sync`).
- The one-authority rule is enforced at the boundary: PATCH/DELETE/suspend
  on a `managed_by: registry` application answers 409 `HD0272` naming the
  root repository where the truth lives. Agent enrollment on managed
  targets is untouched by syncs — enrollment is not declarable.
- The sync runs on every Auto tick (from the loop's own goroutine, like
  nudges), on `POST /api/v1/registry/sync`, and at bind time via the same
  route. Unbinding keeps the `managed_by` stamps deliberately: losing the
  binding must not silently hand a fleet back to interactive mutation.
- Deliberately deferred, recorded here: target *groups* in the manifest
  (fan-out registration), a webhook nudge for the root repository itself
  (the tick covers it within the sync interval), and note that existing
  deployments' seeded roles predate the `registry:*` actions — a new
  deployment's owner bundle carries them; an old one grants them
  explicitly.

### Added — ADR 0010: the application registry is declared in git

- The app-of-apps decision is recorded before any code:
  [`docs/adr/0010`](docs/adr/0010-the-application-registry-is-declared-in-git.md).
  One authorized `registry:bind` act names a root repository; a
  CHEX-validated manifest there declares projects, repositories, targets,
  groups, and applications by name, reconciled by the existing
  refresh/diff/operation machinery with prune-gated deletion. One authority
  per document (`managed_by: registry`, API mutations refused with the root
  repository named); principals, grants, secret values, and agent
  enrollment are never declarable. Implementation is deliberately not yet
  scheduled — the ADR fixes the expensive-to-reverse choices first.

### Added — Swarm file secrets with content-hash rotation

- Compose file secrets now deploy to Swarm, with the value source GitOps
  can honour: a top-level secret declares `x-heimdall-ref: <store>/<name>`
  and services mount it by name (short or `source`/`target` long form).
  Compose's own forms are refused by name at render time — `file:` would
  commit plaintext beside the compose file, `environment:` would make the
  deployed state depend on the machine that applied it, `external:` points
  at state nothing declared (HD0216).
- Rotation is where this beats the `docker stack deploy` lineage: Swarm
  secrets are immutable, so each distinct value gets its own
  content-hash-named object (`<project>-<app>-<name>-<hash8>`), services
  reference the name their value hashes to, and rotation becomes an
  ordinary service update — no rename dance. Stale versions are pruned
  after the waves settle; one a service still references survives because
  the Engine refuses, and the next sync retries.
- For in-repo (sops) references, refresh stamps a digest of the
  *ciphertext* into the spec (`content_hint`), which puts value rotation
  into the service's content hash — a re-encrypted secret plans as an
  update instead of a noop. Nothing secret enters the document. Other
  stores have no revision to pin, so their values rotate on the next real
  update, same as ArgoCD.
- The other adapters reject file secrets by name with the honest
  alternative in the caveat (the standalone Engine has no secrets API; the
  cloud adapters' native secret volumes are future work) — the generated
  capability matrix carries the row.

### Changed — webhook nudges are scoped and serialized

- The webhook receiver (`POST /api/v1/webhooks/{repo}` — HMAC over the raw
  body, payload never parsed, forge-agnostic) now nudges only the pushed
  repository's applications instead of sweeping the whole fleet, closing
  the ceiling its own comment recorded. The nudge also moved off the
  request's goroutine into the Auto loop itself, over a bounded channel: a
  push and the ticker can no longer run the same application's sync
  concurrently, ten pushes coalesce into one pass over the union of their
  repositories, and a full channel drops the nudge because the regular
  tick is the backstop — a storm is exactly when one more pass helps
  least.

### Added — SOPS secret references

- A new secret scheme, `${secret:sops/<path>#<key>}`, decrypts a
  SOPS-encrypted file committed in the application's own repository — the
  in-repo secrets model SwarmCD popularised, under HEIMDALL's stricter
  rules. The ciphertext is read from the bare git mirror at the applying
  revision (the commit SHA pins the content, so a rollback decrypts exactly
  the ciphertext its revision was rendered from, and a force-push cannot
  change what a stored revision means). Decryption drives the real `sops`
  binary — never through a shell — with the plaintext crossing only an
  in-process pipe; only ciphertext ever touches disk. `<path>#<key>`
  extracts one value from an encrypted YAML/JSON file; `<path>` alone
  returns the whole decrypted file (a PEM bundle, say). Key material is
  whatever SOPS understands from the environment (age, PGP, cloud KMS);
  `<deployment>/keys/age.key` is offered as `SOPS_AGE_KEY_FILE`
  automatically, which puts the age key inside the deployment directory —
  the backup unit — beside every other key. Resolution happens
  control-plane side for direct and agent targets alike; a reference
  outside an apply, a path escaping the app directory, and a key that
  could escape the extract expression are each refused (HD0162). The
  end-to-end proof commits real sops/age ciphertext and asserts the value
  reaches the fake Engine's container while the stored revision carries
  only ciphertext.

### Added — metric selection

- A service can now choose which metric groups its instance dashboards
  collect and show, with a compose label — GitOps like everything else:

      labels:
        heimdall.metrics: "cpu, memory, network"

  The vocabulary is `cpu`, `memory`, `network`, `block`, `pids`,
  `throttling`, `net_errors`; a typo is rejected at render time (HD0214)
  naming the allowed groups, never silently collected as nothing. The label
  is promoted onto the DeploySpec service (`metrics`, sorted, hashed) and
  dropped from labels, exactly as `heimdall.wave` is. The metrics API
  enforces the selection at the boundary and reports it, and the dashboard
  hides unchosen panels. Empty selection means everything: opting out of
  observability requires a choice, never a forgotten default.

### Changed — UI

- The application page's resource views replaced three sections. The
  ArgoCD-style dependency tree (and its Table twin) carries the health
  signal the "Desired versus live" cards duplicated, so those are gone; and
  Timeline + History merged into one Activity table — "what happened around
  14:07" is one question, so it is one table, with the rollback controls
  beneath it.
- Clicking a service instance now opens a Grafana-style dashboard page —
  stat tiles (health, state, restarts, started, revision, image), six titled
  chart panels (CPU, memory, network in/out, block read/write) with deploy
  markers, and the tailing log, each in its own card — replacing the inline
  drawer.
- Search bars: on the application list, and on the resource view (dims
  non-matching tree nodes, filters table rows).
- Rollup series now include block I/O; `getApp` returns the desired
  topology (name, image, depends_on, wave, replicas) for the tree.

### Fixed

- `serve` never wired the metrics collector into the API (`Observe` stayed
  nil — twice, because gofmt realignment defeated anchored patches), so
  charts always fell back to a single live sample and read "Collecting…"
  forever. Wired; a live instance dashboard now shows the full series.


## [26.33.1] — 2026-08-16

The first GA release: everything below this line, from Phase 0's foundations
through Phase 6's hardening. Five runtimes behind one conformance suite;
GitOps mechanics end to end; an outbound-only agent; observability with
deploy markers; SCIM-only enterprise access; drilled backup/restore and
failover. See docs/PHASE_*_EVIDENCE.md for what is proven and how.


### Added — Phase 5

- The SCIM 2.0 host and group→role mappings: an identity provider provisions
  users and groups through `/scim/v2/*` (bodies pass to SESAME verbatim, the
  IdP's token authenticated per call), and a mapped directory group becomes a
  SESAME grant scoped to one project's subtree. The exit criterion — operator
  on a project through SCIM alone — is proven against the real engine.
- Secret-manager resolution: `local/` (sealed AES-256-GCM files),
  `aws-sm/`, `azure-kv/`, `gcp-sm/`; unknown schemes refused with directions.
  `heimdall secret set` seals values from env or stdin, never argv.
- Outbound webhooks, HMAC-signed in the GitHub header shape, fired on
  operation completion, bounded and fire-and-forget.
- Correlated audit export: both ledgers as one tagged NDJSON stream.


### Added — Phase 4

- Four new runtimes behind the same conformance suite as Docker: **Swarm**
  (same package, two more Engine endpoint families), **ECS on Fargate**
  (official AWS SDK v2; CloudWatch metrics and logs; resource-tier snapping),
  **Cloud Run** (identity in annotations because the label alphabet cannot
  round-trip a content hash), and **Azure Container Apps** (ARM tags, SDK
  server fakes for testing). Every unsupported compose feature is rejected at
  plan time with the offending service named; `api/capabilities.md` is
  generated from the same answers.
- Fan-out deploy: an application may name a target group instead of a target.
  Each member gets its own operation document; rollout runs in bounded waves
  (`max_parallel`, default 4) and halts at a failure threshold (default 1),
  leaving unstarted hosts literally untouched.
- The conformance suite gained three cases first: instances carry the
  deploying revision, observing an absent app is empty not an error, and a
  removed service plans a visible delete.


### Added — Phase 3 (started)

- A unified timeline on the application page: HEIMDALL's operations and the
  runtime's own events merged chronologically, because "what happened around
  14:07" is usually answered by one of each, and two tables make the reader
  do the join in their head.
- Rollup ingest is idempotent per instance-minute, so an agent redelivering
  a batch after a lost acknowledgement cannot double a chart's points.
- Observability for agent-managed targets. Metrics, a bounded log tail, and
  events travel as jobs through the same rendezvous a sync does, so the
  instance drawer works for hosts the control plane cannot dial; a followed
  stream is refused with direction rather than holding the long poll hostage.
  For history, the agent runs the same `observe.Accumulator` the control
  plane does — one fold implementation, two pipelines that cannot drift —
  and ships completed minute rollups over mTLS to `POST /api/v1/agent/rollups`.
  The ingest resolves applications by label and drops any rollup naming an
  application that does not live on the certificate's target, so an agent
  cannot write history into another host's charts.

- Observability v1 for direct Docker targets. `internal/observe` is the
  scrape → ring buffer → minute-rollup pipeline: fifteen-second samples, an
  hour of raw points per instance in memory, a day of minute rollups in
  `hd-rollups` (average and max both — a chart drawn from averages alone
  hides exactly the spike an operator is hunting), counters stored as deltas
  with reset clamping, everything older than 25h deleted. Not a time-series
  database, deliberately.
- `<hd-timeseries>`: HEIMDALL's inline SVG chart. DuVay tokens for every
  colour, a labeled y axis with byte/percent units, a time axis, and deploy
  markers — the short revision drawn at the moment it landed, which is the
  product's headline claim on a chart. No dependency, no canvas.
- The instance drawer on the application page: click a running instance for
  CPU and memory charts with deploy markers, health, restarts, uptime, owning
  revision, and a tailing log view that only follows the bottom if the reader
  was already there. No shell button and no start/stop, deliberately — that
  is Portainer's product, and an anti-GitOps escape hatch besides.

- Pending actions for offline targets. Agents are outbound-only, so a
  disconnected host is normal operation: a sync against it parks — the
  operation document stays `pending`, durably — and drains when the agent
  next polls. A newer sync for the same app supersedes the older one (new
  phase: `superseded`), so a host away for an hour converges to the newest
  desired revision in one hop instead of replaying a backlog; the drain
  re-reads git, which is what makes that true. Bounded: 16 parked syncs per
  target, 24h TTL, and what is parked is a reference — never a job, because
  jobs carry resolved secret values. On restart the parking is rebuilt from
  the durable Pending operations (`Repark`); dry runs never park, because "I
  could not look" must never read as a plan.

### Added — Phase 2

- Device authorization (RFC 8628): `heimdall login --device` shows a short
  code, a person approves it in the web UI at `/approve` where they are
  already signed in, and the CLI receives tokens — no password ever touches a
  terminal. One confidential OIDC client, registered by init and stored 0600
  beside the TLS keys: the "device" in the OAuth sense is the control plane
  itself, so the secret never leaves the deployment, and same-client
  introspection is what lets the boundary verify access tokens. Approval
  proves the approver's live session to SESAME rather than naming a
  principal; refresh tokens rotate on every use; every approval is audited.
- The authorization boundary now accepts either bearer form — a session pair
  from password login or an access token from the device grant — both
  verified by SESAME (`VerifyBearer`), with introspection reporting live
  grant state so a revoked session kills its tokens.

- Release workflow (`.github/workflows/release.yml`): tag `v*` → binaries for
  five platforms, sha256 checksums, and GitHub-native build provenance
  verifiable with `gh attestation verify`. Plain `go build` in a loop rather
  than a release tool — every dependency in a release pipeline is a
  supply-chain surface. The tag must match `VERSION` or the release refuses.
- Coverage floor in CI: 60%, cross-package (`-coverpkg`), measured 63.5% when
  set. A ratchet — raise it when it rises, never lower it to pass.
- Gateway allowlist tests: 17 `node:test` cases in `web/tests/`, each
  asserting a refused request never left the process.
- Playwright end-to-end (`scripts/e2e-web.sh`): five tests driving the real
  stack — control plane, SESAME, ty serve, browser. Login refusal stays
  uniform, the pages render seeded data, sign-out revokes at the server (a
  reload after logout shows the form, not a cached list), a dry run always
  reports an outcome, and DuVay theming is wired.

- Auto-sync and self-heal (`internal/reconcile/auto.go`). One loop, not two:
  `Status` refreshes from git and reads live state, so a new commit and a
  container killed by hand arrive as the same OutOfSync observation. The two
  policy flags only decide which of them an application consented to. A
  suspended application is never touched, and an automated sync records its
  reason rather than attributing itself to a principal who was not there.
  `HD_SYNC_INTERVAL` tunes the tick, default one minute.
- Webhook receiver (`POST /api/v1/webhooks/{repo}`). It turns a push into a
  nudge and nothing more: the forge's payload is never parsed, because which
  branch moved is something `Status` finds out by fetching, and a receiver that
  believed the body would be trusting input an attacker shaped. That also makes
  it forge-agnostic — GitHub, GitLab, Gitea, and a curl from CI all work.
  Authenticated by an HMAC over the body against a `webhook_secret_ref`; an
  unknown repository and a bad signature are indistinguishable, so the endpoint
  cannot be used to enumerate ids.
- Target groups (`hd-target-groups`) as named tag selectors. Membership is
  derived on every read, never stored, so retagging a host moves it between
  groups with no edit to either group. Projects still scope authorization;
  tags only organise a fleet.

### Added — Phase 1 vertical slice (Docker Engine)

- `internal/render`: compose → `DeploySpec`. Overlays follow Compose's own
  merge rules, interpolation is single-pass and never consults the host
  environment, and `${secret:...}` is rewritten to a reference that is only
  resolved at apply time. An unmodelled directive fails by name and line.
- `internal/git`: bare mirror, ref resolution, file reads at a commit,
  optional signature verification. Paths and refs are validated before they
  reach argv, so a repository document cannot smuggle in a git flag.
- `internal/provider`: the adapter interface, `Capabilities`, plan-time
  `Validate` that reports every rejection at once, and resource-tier snapping
  that never snaps downward.
- `internal/provider/docker`: the Docker Engine adapter, over the Engine API
  with stdlib `net/http` and no Docker SDK.
- `internal/provider/conformance`: the suite every adapter must pass, written
  before the second adapter as planned.
- `internal/diff`: field-level spec diff with secret references shown as
  references, sync status, and health rollup.
- `internal/reconcile`: refresh, status, sync, dry run, rollback, selective
  sync — each recorded in one `hd-operations` document.
- `internal/enroll`: fingerprint-pinned enrollment tokens, the agent CA, and
  the control plane's own TLS certificate.
- `internal/dispatch`: the rendezvous for outbound-only agents, and `Remote`,
  which makes an agent-managed target a `provider.Provider` so the reconciler
  has one code path rather than two.
- `internal/agent` and `heimdall agent`: the host process. Enrols over a
  pinned connection, long-polls for work over mTLS, runs the same Docker
  adapter the control plane would have, and reports back. It opens no port.
- `heimdall enroll`, an authorized route that mints an enrollment token.
- `heimdall init --admin NAME`, which creates the first administrator while
  init still holds the SESAME engine.
- Registry credentials (`hd-registries`) for private image pulls, held as
  references and resolved into an `X-Registry-Auth` header at apply time.
- `heimdall login`, `app`, `diff`, and `sync`; local login with Argon2id and
  TOTP, entirely through SESAME.
- `scripts/e2e-docker.sh`, the live-Engine gate.
- `web/`: the Tachyon web tier. Two Tac pages — the application list, and an
  application's status, desired-versus-live diff, sync, dry run, rollback, and
  history — over two Yon routes that attach a session and forward. The session
  lives in an httpOnly cookie, so an injected script cannot read a credential
  that can deploy software. It holds no business logic and makes no
  authorization decision (ADR 0009).
- `GET /api/v1/projects`, so the UI can discover projects before listing an
  application. Projects are derived from applications rather than stored,
  because nothing else creates one.
- `scripts/vendor-duvay.sh`, which vendors DuVay's CSS. A control plane is
  routinely air-gapped and Tachyon's default CSP is `default-src 'self'`, so a
  CDN is neither available nor allowed.

### Fixed

- `Collection.Patch` silently lost any patch carrying a typed slice of
  structs: `encodeNested` only recognised `[]any`, so every sync's final
  phase patch hit FYLO's `EARRAYOFOBJECTS` and was swallowed — the API
  returned "succeeded" while the store kept "planning" forever. Patches now
  normalise through JSON first; a regression test pins the round-trip; and
  reconcile's operation patches log loudly instead of discarding errors. The
  end-to-end UI audit found it: weeks of unit tests never had, because they
  asserted on the returned operation, not the stored one.
- The web tier's read calls now time out in 8 seconds so a wedged (not dead)
  control plane surfaces as an error instead of a 60-second frozen page;
  syncs and rollbacks keep the long leash an image pull needs.
- UI: rollback requires an inline confirm step; Escape closes the instance
  drawer; scrollable tables show an edge fade on narrow screens; the mobile
  header keeps one row.

- A stopped container reported `Healthy`, because the Engine retains the last
  health probe after exit. Lifecycle is now checked before the probe.
- A service removed out of band was invisible to the health rollup, since
  `Observe` can only describe what exists. The plan is now the evidence.
- The control plane's TLS certificate was valid for five years, which Apple's
  verifier and Chrome reject as "not standards compliant". It is now 397 days,
  inside the 398-day limit, and renews itself before expiry.
- Resource strings could not name a stored document: TTIDs are uppercase and
  SESAME's pattern alphabet has none. `auth.Resource` now lowercases
  identifiers, which is lossless because TTIDs are case-insensitive by
  specification.

### Added — Phase 0 foundations

- `spec.DeploySpec`: the provider-neutral canonical model, with deterministic
  canonical JSON and SHA-256 content addressing. The fixture hash is pinned by
  test, so a struct edit that changes every stored revision fails the build.
- `internal/schema`: CHEX validation of compose input and `DeploySpec` output,
  plus the `testdata/compose/` corpus. CHEX rejects unmodelled properties by
  name, which is how an unsupported compose directive fails loudly instead of
  being dropped.
- `internal/store`: FYLO wrapper with `hd-*` collections, queue topic
  constants, idempotent bootstrap, and the projection-rebuild path. Refuses a
  root under a file-sync tree.
- `internal/auth`: supervised SESAME engine, the closed action vocabulary, the
  four seed role bundles, and a `Decide` that fails closed — distinguishing a
  denial from an engine that did not answer.
- `internal/api`: the authorization boundary. One decide call per route,
  before any handler, with SESAME's `reason_code` and `policy_version` in the
  audit record.
- `cmd/heimdall`: `init`, `doctor`, `serve`, `contract`, `version`, with the
  fail-closed startup order `fylo` → `sesame doctor` → listener.
- `api/openapi.json`, generated from the route table by `heimdall contract`
  and kept honest by test.
- `scripts/ci-gates.sh`: standing gates against auth logic in the codebase,
  leaked credentials in fixtures, cloud SDKs outside adapters, shell-invoked
  subprocesses, and dumping-ground packages.
- ADRs 0001–0005.

### Changed from the plan, during implementation


- Resource strings are colon-separated (`project:alpha:app:checkout`), not
  slash-separated. SESAME's `ValidatePattern` rejects `/`.
- Collections are `hd-*`, not `hd_*`. FYLO's `validate_collection_name` accepts
  lowercase alphanumerics and `-` only.
- HEIMDALL keeps its own FYLO root beside SESAME's rather than sharing one.
  FYLO takes an exclusive lock per root (`EROOTLOCKED`), so a shared root is
  not possible. The deployment directory remains the single backup unit.

### Store adaptations found by running FYLO

- `getLatest` returns an id-keyed envelope rather than the bare document;
  decoding it directly produced a silently zero-valued struct.
- FYLO refuses to index an array of objects inside a document
  (`EARRAYOFOBJECTS`). The store encodes those arrays to a marked string on
  write and decodes on read, so top-level scalars stay queryable and nothing
  above the store knows.
