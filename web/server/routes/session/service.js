// The session flow: credentials go to the control plane, and what comes
// back becomes an httpOnly cookie the page can never read.
//
// No authentication logic lives here. The control plane forwards the
// credentials to SESAME and returns its verdict; this file only decides
// what shape the cookie takes — which is the whole reason a session route
// exists rather than the browser calling the control plane directly: a
// token in localStorage is readable by any script that gets injected, and a
// control-plane session is a credential that can deploy software.

import { pathToFileURL } from 'node:url'
import { resolve } from 'node:path'

async function load(specifier, fromRoot) {
  try {
    return await import(new URL(specifier, import.meta.url).href)
  } catch {
    return await import(pathToFileURL(resolve(fromRoot)).href)
  }
}

const repoModule = load('./repo.js', 'server/routes/session/repo.js')

export const COOKIE = 'hd_session'

export function sessionBearer(cookieHeader) {
  const match = /(?:^|;\s*)hd_session=([^;]+)/.exec(cookieHeader || '')
  return match ? decodeURIComponent(match[1]) : null
}

// setCookie builds the session cookie. Every flag here is load-bearing:
// HttpOnly keeps script away from it, SameSite=Strict means a cross-site
// request cannot carry it, Secure keeps it off plaintext connections, and
// the max age matches the session lifetime the control plane issues.
export function setCookie(value, seconds, secure) {
  const flags = ['Path=/', 'HttpOnly', 'SameSite=Strict', `Max-Age=${seconds}`]
  if (secure) flags.push('Secure')
  return `${COOKIE}=${value}; ${flags.join('; ')}`
}

export function clearCookie() {
  return `${COOKIE}=; Path=/; HttpOnly; SameSite=Strict; Max-Age=0`
}

// signIn runs the login flow. It answers either { refusal } (a ready HTTP
// shape) or { body, cookie } for the handler to emit.
export async function signIn(settings, credentials) {
  if (!credentials.identifier || !credentials.password) {
    return { refusal: { status: 400, code: 'HD0601', message: 'identifier and password are required' } }
  }

  const repo = await repoModule
  let response, body
  try {
    response = await repo.login(settings, credentials)
    body = JSON.parse(response.body || '{}')
  } catch (error) {
    return {
      refusal: {
        status: 502,
        code: 'HD0602',
        message: `the control plane is unreachable at ${settings.apiURL}`,
        detail: String(error),
      },
    }
  }

  if (response.status !== 200) {
    // Pass the control plane's refusal through unchanged. It is deliberately
    // uniform for every failure mode, and softening or elaborating it here
    // would give back what SESAME was careful not to disclose.
    return { refusal: { status: response.status, ...body } }
  }

  // The secret goes into the cookie and stops here. The page is told who it
  // is and when the session ends, and nothing it could replay.
  const value = `${body.session_id}.${body.session_secret}`
  const expiresAt = Date.parse(body.expires_at)
  const seconds = Number.isFinite(expiresAt)
    ? Math.max(60, Math.floor((expiresAt - Date.now()) / 1000))
    : 3600

  return {
    body: {
      principal_id: body.principal_id,
      expires_at: body.expires_at,
      assurance: body.assurance,
    },
    cookie: setCookie(value, seconds, settings.secureCookie),
  }
}

// signOut ends the session at the control plane. The caller clears the
// cookie regardless of the outcome: a revoke that failed leaves a session
// the operator can revoke elsewhere; a cookie that survives leaves a
// browser that still looks logged in.
export async function signOut(settings, bearer) {
  if (!settings || !bearer) return
  const repo = await repoModule
  try {
    await repo.logout(settings, bearer)
  } catch {
    // Deliberate: see above.
  }
}
