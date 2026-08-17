// The gateway: one door from the browser to the control-plane API.
//
// It is a proxy and nothing else. It does not interpret a response, cache
// one, or rewrite one — it attaches the session and forwards, and returns
// what it gets. The route follows the module convention: this file is the
// HTTP shape, service.js is the flow and the allowlist, repo.js is the
// upstream call. Yon's loader cannot resolve a static relative import (the
// handler is evaluated without its own file URL as a base), so the siblings
// load through dynamic imports with an explicit fallback — see load() in
// service.js.
//
// The request names its own downstream path in a POST body rather than in
// the URL because Yon handlers do not receive query strings; encoding the
// path in the body is what lets `?limit=50` and `?instance=...` work at all.

import { pathToFileURL } from 'node:url'
import { resolve } from 'node:path'

async function load(specifier, fromRoot) {
  try {
    return await import(new URL(specifier, import.meta.url).href)
  } catch {
    return await import(pathToFileURL(resolve(fromRoot)).href)
  }
}

const serviceModule = load('./service.js', 'server/routes/gateway/service.js')
const repoModule = load('./repo.js', 'server/routes/gateway/repo.js')

function header(request, name) {
  const value = request.headers?.[name]
  return Array.isArray(value) ? value[0] : value || ''
}

function json(status, body) {
  return {
    status,
    headers: { 'content-type': 'application/json; charset=utf-8' },
    body: typeof body === 'string' ? body : JSON.stringify(body),
  }
}

export class Handler {
  static async POST(request) {
    const service = await serviceModule
    const repo = await repoModule

    const settings = await repo.settings()
    if (!settings) {
      return json(503, {
        code: 'HD0600',
        message: 'the web tier is not configured; server/data/bff.json is missing or unreadable',
      })
    }

    const bearer = service.sessionBearer(header(request, 'cookie'))
    if (!bearer) {
      return json(401, { code: 'HD0401', message: 'not signed in' })
    }

    let call
    try {
      call = JSON.parse(request.body?.data || '{}')
    } catch {
      return json(400, { code: 'HD0601', message: 'the request body is not valid JSON' })
    }

    const { refusal, response } = await service.relay(settings, bearer, call)
    if (refusal) {
      const { status, ...body } = refusal
      return json(status, body)
    }

    // The status and the body are the control plane's. A 403 carries
    // SESAME's reason code and a 422 names the offending compose service;
    // summarising either here would throw away the part an operator needs.
    return {
      status: response.status,
      headers: { 'content-type': 'application/json; charset=utf-8' },
      body: response.body || '{}',
    }
  }
}
