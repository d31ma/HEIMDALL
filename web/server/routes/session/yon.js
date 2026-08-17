// Session routes: log in, log out. The module convention: this file is the
// HTTP shape, service.js is the cookie flow, repo.js is the two
// control-plane auth calls. Nothing in any of the three makes an
// authentication decision — SESAME does, behind the control plane.

import { pathToFileURL } from 'node:url'
import { resolve } from 'node:path'

async function load(specifier, fromRoot) {
  try {
    return await import(new URL(specifier, import.meta.url).href)
  } catch {
    return await import(pathToFileURL(resolve(fromRoot)).href)
  }
}

const serviceModule = load('./service.js', 'server/routes/session/service.js')
const repoModule = load('./repo.js', 'server/routes/session/repo.js')

function header(request, name) {
  const value = request.headers?.[name]
  return Array.isArray(value) ? value[0] : value || ''
}

function json(status, body, extra = {}) {
  return {
    status,
    headers: { 'content-type': 'application/json; charset=utf-8', ...extra },
    body: JSON.stringify(body),
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

    let credentials
    try {
      credentials = JSON.parse(request.body?.data || '{}')
    } catch {
      return json(400, { code: 'HD0601', message: 'the request body is not valid JSON' })
    }

    const { refusal, body, cookie } = await service.signIn(settings, credentials)
    if (refusal) {
      const { status, ...rest } = refusal
      return json(status, rest)
    }
    return json(200, body, { 'set-cookie': cookie })
  }

  // DELETE ends the session at the control plane and clears the cookie.
  // Both matter: clearing only the cookie would leave a live session that
  // anyone holding the value could still use.
  static async DELETE(request) {
    const service = await serviceModule
    const repo = await repoModule

    const settings = await repo.settings()
    await service.signOut(settings, service.sessionBearer(header(request, 'cookie')))
    return json(200, { status: 'logged out' }, { 'set-cookie': service.clearCookie() })
  }
}
