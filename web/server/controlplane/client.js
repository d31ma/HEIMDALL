// The control-plane client: the one piece of transport both repos share.
//
// This tier's only data source is the control plane's HTTP API — FYLO is
// exclusively locked by `heimdall serve` (ADR 0004), so there is no database
// here to read or write. One client means one CA story, one timeout story,
// and one bounded-read story, instead of a copy per route drifting apart.
//
// Node's fetch offers no way to trust an extra CA, and a control plane
// initialised by `heimdall init` serves its own certificate. `node:https`
// takes one directly, so the connection is verified rather than skipped —
// there is deliberately no "ignore certificate errors" option here, because
// that is the setting an operator reaches for at 3am and never removes.

import { readFileSync } from 'node:fs'
import http from 'node:http'
import https from 'node:https'

// config reads the control-plane address from a file, because `ty serve`
// gives handlers a deny-by-default environment and offers no way to widen
// it. The path is working-directory relative: that is the Yon contract.
export function config() {
  try {
    const parsed = JSON.parse(readFileSync('server/data/bff.json', 'utf8'))
    return {
      apiURL: String(parsed.apiUrl || '').replace(/\/+$/, ''),
      // The CA is read once, here, rather than per request.
      ca: parsed.caFile ? readFileSync(String(parsed.caFile)) : null,
      secureCookie: parsed.secureCookie !== false,
      timeoutMs: Number(parsed.timeoutMs) > 0 ? Number(parsed.timeoutMs) : 60_000,
    }
  } catch {
    return null
  }
}

// call performs one request against the control plane and returns
// { status, body }. maxResponseBytes bounds the read: the control plane is
// trusted, but a wedged one returning an endless body must be an error
// rather than an out-of-memory kill.
export function call(settings, method, path, headers, body, maxResponseBytes) {
  return new Promise((resolve, reject) => {
    const target = new URL(settings.apiURL + path)
    const secure = target.protocol === 'https:'
    const transport = secure ? https : http

    const options = {
      method,
      hostname: target.hostname,
      port: target.port || (secure ? 443 : 80),
      path: target.pathname + target.search,
      headers: { ...headers },
      timeout: settings.timeoutMs,
    }
    if (secure && settings.ca) {
      options.ca = settings.ca
    }

    const outgoing = transport.request(options, (response) => {
      const chunks = []
      let size = 0
      response.on('data', (chunk) => {
        size += chunk.length
        if (size <= maxResponseBytes) chunks.push(chunk)
      })
      response.on('end', () =>
        resolve({ status: response.statusCode, body: Buffer.concat(chunks).toString('utf8') }),
      )
    })

    outgoing.on('timeout', () => outgoing.destroy(new Error('the control plane did not respond in time')))
    outgoing.on('error', reject)
    if (body !== undefined && body !== null) outgoing.write(body)
    outgoing.end()
  })
}
