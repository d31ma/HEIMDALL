// TEMPORARY — demo only. One-click demo sign-in: GET this route and land on
// the app list with a session cookie. Exists so a driver that cannot type
// can walk the UI; it is enabled only when bff.json carries auditLogin, and
// deleted after the audit. The module convention applies even here: this
// file is the HTTP shape, service.js is the flow (delegating to the session
// route's own sign-in), repo.js is the demo-credential read.

import { pathToFileURL } from 'node:url'
import { resolve } from 'node:path'

/**
 * Imports a sibling module, falling back to a path relative to the working
 * directory: a Yon handler has no module base URL in every runtime, so the
 * static form fails there and the dynamic one is what works.
 *
 * @param {string} specifier
 * @param {string} fromRoot
 * @returns {Promise<any>}
 */
async function load(specifier, fromRoot) {
  try {
    return await import(new URL(specifier, import.meta.url).href)
  } catch {
    return await import(pathToFileURL(resolve(fromRoot)).href)
  }
}

const serviceModule = load('./service.js', 'server/routes/audit-login/service.js')

export class Handler {
  /**
   * Signs the demo operator in and redirects. Demo-only, and deliberately
   * separate from the session route so deleting this directory removes the
   * whole affordance.
   *
   * @returns {Promise<{status: number, headers: Record<string, string>, body: string}>}
   */
  static async GET() {
    const service = await serviceModule
    const { refusal, cookie } = await service.demoSignIn()
    if (refusal) {
      return {
        status: refusal.status,
        headers: { 'content-type': 'text/plain' },
        body: refusal.body,
      }
    }
    return {
      status: 302,
      headers: { location: '/', 'set-cookie': cookie },
      body: '',
    }
  }
}
