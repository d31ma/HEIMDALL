# ADR 0009 — The web tier holds no authority

**Status:** accepted (Phase 1)

## Context

Tachyon's Yon handlers run JavaScript, TypeScript, or Python — not Go — which
is why the control plane is a separate binary. That leaves an open question
about the web tier: how much of the product lives there?

The tempting answer is "some of it". A BFF that caches an app list, decides
which buttons to show, or interprets a token is a normal pattern. It is also
how a system acquires a second authority over the same questions, which then
drifts from the first.

## Decision

**The web tier decides nothing.** It has two routes and neither has an opinion:

- `POST/DELETE /session` exchanges credentials for a cookie and revokes it. It
  contains no authentication logic; every step is a control-plane call, which
  is itself a SESAME call.
- `POST /gateway` attaches the session to one request and returns the answer
  with its status and body unchanged.

A 403 arrives at the browser carrying SESAME's `reason_code`; a 422 arrives
naming the offending compose service. Summarising either would throw away the
part an operator needs.

**The session is an httpOnly cookie, not a token in the page.** A
control-plane session can deploy software. In `localStorage` it is readable by
any injected script; in an httpOnly cookie script cannot read it at all. The
page learns who it is and when the session ends — nothing replayable.
`SameSite=Strict` means a cross-site request cannot carry it, and `Secure` keeps
it off plaintext connections.

**One gateway route, not one per endpoint.** A Yon handler cannot import a
local module, so per-endpoint routes would copy the cookie parsing, header
construction, and path allowlist into every file — and a security check
written seven times is one that will diverge. The allowlist is what keeps a
single door from being an open relay: four methods, paths under `/api/v1/`
only, the session routes excluded, and no traversal, scheme, or host.

**The page asks whether it is signed in rather than remembering.** The cookie
is httpOnly, so the page genuinely cannot know. A page that guessed would show
an empty application list where it should show a login form.

## Consequences

- Adding a control-plane endpoint needs no server-side change here. The gateway
  already forwards it; only the browser's `api.js` gains a line.
- The web tier cannot be the reason something is permitted. If a button appears
  that the operator may not use, pressing it returns a 403 with a reason code —
  which is a worse experience than hiding it, and a far better failure than a
  UI that decides for itself and gets it wrong.
- Two Yon files share about forty lines of shape. That duplication is the
  honest cost of handler isolation, and it is bounded because there are only
  ever going to be two.
- The call is described in a POST body because Yon handlers do not receive
  query strings. It reads like an RPC tunnel and it is one, deliberately: the
  alternative is that `?limit=` and `?instance=` cannot be expressed.
