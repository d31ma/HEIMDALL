// The gateway's data access. This tier's store is the control plane's API —
// FYLO is exclusively locked by `heimdall serve`, so "repo" here means the
// upstream call, bounded and CA-verified, and nothing else.

import { pathToFileURL } from 'node:url'
import { resolve } from 'node:path'

// Yon evaluates a handler without its own file URL as a base, so a static
// relative import cannot resolve. Under plain Node (tests) import.meta.url
// is a real file URL and module-relative works; under Yon the fallback
// resolves from the working directory, which the runtime sets to the web
// root — the same contract config() already leans on.
/**
 * Imports a sibling module, falling back to a path relative to the working
 * directory. Yon handlers have no module base URL in every runtime, so the
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

const clientModule = load('../../controlplane/client.js', 'server/controlplane/client.js')

// A forwarded response may carry a whole log tail; a session answer never
// legitimately exceeds a fraction of this.
const MAX_RESPONSE_BYTES = 32 << 20

/**
 * The web tier's own configuration — where the control plane is and which CA
 * signs it.
 *
 * @returns {Promise<import('../../controlplane/client.js').ControlPlaneSettings>} Normalised from bff.json, whose key is
 *   `apiUrl`; everything downstream reads `apiURL`.
 * @throws {Error} When bff.json is missing or unreadable, which is a
 *   deployment fault rather than a request fault.
 */
export async function settings() {
  const client = await clientModule
  return client.config()
}

// forward relays one already-vetted request to the control plane.
/**
 * The one place this tier opens a socket to the control plane.
 *
 * @param {import('../../controlplane/client.js').ControlPlaneSettings} callSettings
 * @param {string} method
 * @param {string} path
 * @param {Record<string, string>} headers
 * @param {string|undefined} body
 * @returns {Promise<{status: number, headers: Record<string, string>, body: string}>}
 */
export async function forward(callSettings, method, path, headers, body) {
  const client = await clientModule
  return client.call(callSettings, method, path, headers, body, MAX_RESPONSE_BYTES)
}
