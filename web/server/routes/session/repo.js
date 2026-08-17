// The session route's data access: the two control-plane auth calls. The
// value that comes back is handed straight to service.js to become an
// httpOnly cookie; nothing here stores or logs it.

import { pathToFileURL } from 'node:url'
import { resolve } from 'node:path'

async function load(specifier, fromRoot) {
  try {
    return await import(new URL(specifier, import.meta.url).href)
  } catch {
    return await import(pathToFileURL(resolve(fromRoot)).href)
  }
}

const clientModule = load('../../controlplane/client.js', 'server/controlplane/client.js')

// A session answer is small; a megabyte is already ten times generous.
const MAX_RESPONSE_BYTES = 1 << 20

export async function settings() {
  const client = await clientModule
  return client.config()
}

export async function login(settings, credentials) {
  const client = await clientModule
  const payload = JSON.stringify({
    identifier: credentials.identifier,
    password: credentials.password,
    totp: credentials.totp || '',
  })
  return client.call(settings, 'POST', '/api/v1/auth/login', {
    'content-type': 'application/json',
    'content-length': Buffer.byteLength(payload),
  }, payload, MAX_RESPONSE_BYTES)
}

export async function logout(settings, bearer) {
  const client = await clientModule
  return client.call(settings, 'POST', '/api/v1/auth/logout', {
    authorization: `Bearer ${bearer}`,
  }, undefined, MAX_RESPONSE_BYTES)
}
