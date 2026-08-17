// The gateway's flow: everything between "a POST arrived" and "bytes went
// upstream". This is transport policy, not business logic — every
// authorization decision is still SESAME's, made once at the control plane's
// own boundary; this tier has no opinion about who may do what, and adding
// one would be a second authority over the same question (ADR 0009).

import { pathToFileURL } from 'node:url'
import { resolve } from 'node:path'

async function load(specifier, fromRoot) {
  try {
    return await import(new URL(specifier, import.meta.url).href)
  } catch {
    return await import(pathToFileURL(resolve(fromRoot)).href)
  }
}

const repoModule = load('./repo.js', 'server/routes/gateway/repo.js')

// Only these methods, and only paths under this prefix. Anything else is
// refused before a socket is opened, so this cannot be turned into a
// server-side request forgery tool against the control plane's host network.
const ALLOWED_METHODS = new Set(['GET', 'POST', 'PATCH', 'DELETE'])
const ALLOWED_PREFIX = '/api/v1/'

// The login and logout routes are handled by the session route, which owns
// the cookie — reaching them through the gateway would return the session
// secret to the page. The device token routes are forbidden for the same
// reason: start, token, and refresh mint or return credentials, and only the
// CLI has any business with them. lookup, approve, and deny return no
// credential and are exactly what the approval page needs.
const FORBIDDEN_PATHS = [
  '/api/v1/auth/login',
  '/api/v1/auth/logout',
  '/api/v1/auth/device/start',
  '/api/v1/auth/device/token',
  '/api/v1/auth/device/refresh',
]

const MAX_BODY_BYTES = 1 << 20

export function sessionBearer(cookieHeader) {
  const match = /(?:^|;\s*)hd_session=([^;]+)/.exec(cookieHeader || '')
  return match ? decodeURIComponent(match[1]) : null
}

// safePath refuses anything that is not a plain absolute path under the
// allowlisted prefix. A traversal segment, a scheme, or a host would let the
// browser choose where this server connects to.
export function safePath(candidate) {
  if (typeof candidate !== 'string' || candidate.length === 0 || candidate.length > 2048) {
    return null
  }
  if (!candidate.startsWith(ALLOWED_PREFIX)) return null
  if (candidate.includes('..') || candidate.includes('\\')) return null
  // Reject anything that would parse as absolute — "//host/path" is a URL to
  // another host, not a path on this one.
  if (candidate.startsWith('//')) return null

  const [path] = candidate.split('?')
  if (FORBIDDEN_PATHS.includes(path)) return null
  return candidate
}

// relay vets one browser call and forwards it. The answer is either
// { refusal } — a ready HTTP shape — or { response } from the control plane.
export async function relay(settings, bearer, call) {
  const method = String(call.method || 'GET').toUpperCase()
  if (!ALLOWED_METHODS.has(method)) {
    return { refusal: { status: 400, code: 'HD0603', message: `method ${method} is not allowed` } }
  }
  const path = safePath(call.path)
  if (!path) {
    return {
      refusal: {
        status: 400,
        code: 'HD0604',
        message: 'path must be a plain path under /api/v1/ and may not name the session routes',
      },
    }
  }

  // A read that hangs a minute freezes the page; only actions that pull
  // images deserve the long leash. The page says which kind this is, and
  // the configured timeout stays the ceiling.
  const requested = Number(call.timeout_ms)
  const timeoutMs = requested > 0 ? Math.min(requested, settings.timeoutMs) : settings.timeoutMs

  const headers = { authorization: `Bearer ${bearer}` }
  let payload
  if (call.body !== undefined && call.body !== null && method !== 'GET') {
    payload = JSON.stringify(call.body)
    if (payload.length > MAX_BODY_BYTES) {
      return { refusal: { status: 413, code: 'HD0605', message: 'the request body is too large' } }
    }
    headers['content-type'] = 'application/json'
    headers['content-length'] = Buffer.byteLength(payload)
  }

  const repo = await repoModule
  try {
    const response = await repo.forward({ ...settings, timeoutMs }, method, path, headers, payload)
    return { response }
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
}
