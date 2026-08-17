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

export async function settings() {
  const client = await clientModule
  return client.config()
}

// forward relays one already-vetted request to the control plane.
export async function forward(callSettings, method, path, headers, body) {
  const client = await clientModule
  return client.call(callSettings, method, path, headers, body, MAX_RESPONSE_BYTES)
}
