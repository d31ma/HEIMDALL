# HEIMDALL web tier

A [Tachyon](https://tachyon.del.ma) project: Tac pages in the browser, and two
Yon routes that stand between them and the control-plane API.

## What it is, and what it deliberately is not

The web tier holds **no business logic and makes no authorization decision**.
Every decision is SESAME's, made once at the control plane's own boundary. This
tier attaches a session and forwards; adding an opinion here would be a second
authority over the same question.

There are exactly two server routes:

| Route | Job |
|---|---|
| `POST/DELETE /session` | Exchange credentials for an httpOnly cookie, and revoke it |
| `POST /gateway` | Attach the session to one control-plane call and return the answer untouched |

**Why the session lives in an httpOnly cookie.** A control-plane session can
deploy software. In `localStorage` it is readable by any script that gets
injected; in an httpOnly cookie script cannot read it at all. The page is told
who it is and when the session expires, and nothing it could replay.

**Why one gateway rather than a route per endpoint.** A Yon handler cannot
import a local module — the handler protocol is deliberately isolated — so
per-endpoint routes would mean copying the cookie parsing, the header
construction, and the path allowlist into every file. A security check
duplicated seven times is one that will diverge. One door is one place to get
right, and the allowlist is what keeps it from becoming an open relay: only
`GET`, `POST`, `PATCH`, `DELETE`, only paths under `/api/v1/`, never the
session routes, and no traversal or scheme.

**Why the call is described in a POST body.** Yon handlers do not receive query
strings, so `?limit=50` and `?instance=...` cannot survive a URL-shaped proxy.
Putting the downstream path in the body is what makes them work at all.

## Running it

```bash
scripts/vendor-duvay.sh                      # from the repository root
cp web/server/data/bff.example.json web/server/data/bff.json
$EDITOR web/server/data/bff.json             # point apiUrl and caFile at your control plane
cd web && ty serve --port 8081
```

`bff.json` rather than environment variables because `ty serve` gives handlers
a deny-by-default environment and offers no way to widen it.

| Field | Meaning |
|---|---|
| `apiUrl` | The control plane, e.g. `https://127.0.0.1:8443` |
| `caFile` | Its CA, usually `<deployment>/keys/agent-ca.crt` |
| `secureCookie` | Keep `true` in production; `false` only for a plaintext local loop |
| `timeoutMs` | Bound on one control-plane call |

The control plane serves its own certificate, and Node's `fetch` offers no way
to trust an extra CA, so the routes use `node:https`, which does. There is
deliberately no option to skip verification — that is the setting an operator
reaches for at 3am and never removes.

## Pages

| Page | Shows |
|---|---|
| `/` | Sign in, then every project and its applications |
| `/apps/{project}/{app}` | Sync status, health, the desired-versus-live diff, dry run, sync, rollback, and history |

The diff distinguishes the two sides by more than colour: the live column is
struck through, which survives both colour blindness and a printed page. A
secret shows as `${secret:...}` on both sides and is marked `ref` — there is no
value to redact, because render never produced one.

## Tests

```bash
cd web && npm test
```

`node:test` — the stdlib runner, no install step. The suite covers the
gateway's allowlist: every refusal asserts not just the status code but that
nothing reached the control plane. Tests live in `tests/` rather than beside
the route because Tachyon treats every file under `server/routes/` as a
handler and refuses ones it cannot execute.

End to end, from the repository root:

```bash
scripts/e2e-web.sh
```

Stands up a real control plane and `ty serve`, seeds one application, and
drives the pages with Playwright — the only place the whole stack meets. The
`@playwright/test` dev dependency is the web tier's only dependency.

## Styling

DuVay supplies the tokens, components, and themes; `client/shared/styles/app.css`
is layout only, and every colour is a DuVay custom property so light, dark, and
high-contrast all work from one set of rules.

DuVay is vendored rather than linked from a CDN: a control plane is routinely
air-gapped, and Tachyon's default `content-security-policy` is `default-src
'self'`, which would block a CDN anyway. Only the CSS is taken — this UI uses
DuVay's classes rather than its `<w-*>` elements, so the 754 KiB component
bundle would be weight with no use.
