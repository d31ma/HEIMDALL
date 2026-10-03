// TEMPORARY — demo only. The audit-login flow: read the demo credentials
// and run the same sign-in the session route runs. Reusing session's
// service is the point — one login flow, one cookie shape, one place both
// can be right.

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

const sessionServiceModule = load('../session/service.js', 'server/routes/session/service.js')
const sessionRepoModule = load('../session/repo.js', 'server/routes/session/repo.js')
const repoModule = load('./repo.js', 'server/routes/audit-login/repo.js')

// demoSignIn answers { cookie } on success or { refusal } with a status.
/**
 * Signs the demo operator in through the ordinary session path, so the demo
 * cannot take a shortcut the product does not have.
 *
 * @returns {Promise<{
 *   cookie?: string,
 *   refusal?: {status: number, body: string},
 * }>}
 */
export async function demoSignIn() {
  const repo = await repoModule
  const demo = repo.credentials()
  if (!demo) {
    return { refusal: { status: 404, body: 'not enabled' } }
  }

  const sessionRepo = await sessionRepoModule
  const settings = await sessionRepo.settings()
  if (!settings) {
    return { refusal: { status: 503, body: 'the web tier is not configured' } }
  }

  const sessionService = await sessionServiceModule
  const outcome = await sessionService.signIn(settings, demo)
  if (outcome.refusal) {
    return { refusal: { status: 502, body: 'login failed' } }
  }
  return { cookie: outcome.cookie }
}
