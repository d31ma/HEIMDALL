// The gateway is the one place the browser can reach the control plane, so
// the allowlist is the only thing standing between a page and an open relay.
// These run on node:test — the stdlib runner, no dependency — against a real
// upstream server, because a stubbed fetch would not prove the request that
// actually goes out.
//
// Run with: node --test  (from web/)

import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, writeFileSync } from 'node:fs'
import http from 'node:http'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { after, before, describe, test } from 'node:test'

// Imported from outside server/routes because Tachyon treats every file
// under routes/ as a handler and refuses ones it cannot execute (TY2003).
import { Handler } from '../server/routes/gateway/yon.js'

const COOKIE = 'hd_session=ses_1.secret'

let upstream
let received = []
const originalCwd = process.cwd()

function request(body, { cookie = COOKIE } = {}) {
  return {
    protocol_version: 1,
    kind: 'request',
    method: 'POST',
    route: '/gateway',
    headers: cookie ? { cookie: [cookie] } : {},
    parameters: {},
    body: { encoding: 'utf8', data: JSON.stringify(body) },
  }
}

before(async () => {
  upstream = http.createServer((incoming, response) => {
    const chunks = []
    incoming.on('data', (chunk) => chunks.push(chunk))
    incoming.on('end', () => {
      received.push({
        method: incoming.method,
        url: incoming.url,
        authorization: incoming.headers.authorization,
        body: Buffer.concat(chunks).toString('utf8'),
      })
      response.writeHead(200, { 'content-type': 'application/json' })
      response.end(JSON.stringify({ ok: true, saw: incoming.url }))
    })
  })
  await new Promise((resolve) => upstream.listen(0, '127.0.0.1', resolve))

  // The handler reads server/data/bff.json relative to the working
  // directory, so the test runs from a directory it owns and restores cwd
  // afterwards — the test runner resolves later file paths against it.
  const root = mkdtempSync(join(tmpdir(), 'hd-gateway-'))
  mkdirSync(join(root, 'server', 'data'), { recursive: true })
  writeFileSync(
    join(root, 'server', 'data', 'bff.json'),
    JSON.stringify({ apiUrl: `http://127.0.0.1:${upstream.address().port}`, timeoutMs: 5000 }),
  )
  process.chdir(root)
})

after(() => {
  process.chdir(originalCwd)
  upstream.close()
})

describe('the gateway forwards', () => {
  test('a permitted call, with the session attached as a bearer token', async () => {
    received = []
    const response = await Handler.POST(request({ method: 'GET', path: '/api/v1/version' }))

    assert.equal(response.status, 200)
    assert.equal(received.length, 1)
    assert.equal(received[0].url, '/api/v1/version')
    // The cookie became a header. The page never held the secret and the
    // control plane never sees a cookie.
    assert.equal(received[0].authorization, 'Bearer ses_1.secret')
  })

  test('a query string, which is the reason the path travels in the body', async () => {
    received = []
    await Handler.POST(request({ method: 'GET', path: '/api/v1/projects/a/apps/b/logs?instance=web&tail=50' }))
    assert.equal(received[0].url, '/api/v1/projects/a/apps/b/logs?instance=web&tail=50')
  })

  test('the control plane’s status and body unchanged', async () => {
    const response = await Handler.POST(request({ method: 'GET', path: '/api/v1/version' }))
    assert.equal(response.body, JSON.stringify({ ok: true, saw: '/api/v1/version' }))
  })
})

describe('the gateway refuses', () => {
  // Each of these would let a page choose where this server connects to, or
  // reach past the surface it is allowed to.
  const forbidden = [
    ['no session at all', { method: 'GET', path: '/api/v1/version' }, { cookie: null }, 401],
    ['a path outside /api/v1/', { method: 'GET', path: '/healthz' }, {}, 400],
    ['an absolute URL', { method: 'GET', path: 'http://evil.example/api/v1/x' }, {}, 400],
    ['a protocol-relative URL', { method: 'GET', path: '//evil.example/api/v1/x' }, {}, 400],
    ['a traversal segment', { method: 'GET', path: '/api/v1/../../etc/passwd' }, {}, 400],
    ['a backslash', { method: 'GET', path: '/api/v1/x\\..\\y' }, {}, 400],
    ['the login route, which would hand the secret back to the page',
      { method: 'POST', path: '/api/v1/auth/login' }, {}, 400],
    ['the logout route', { method: 'POST', path: '/api/v1/auth/logout' }, {}, 400],
    ['the device token route, which returns credentials',
      { method: 'POST', path: '/api/v1/auth/device/token' }, {}, 400],
    ['the device start route', { method: 'POST', path: '/api/v1/auth/device/start' }, {}, 400],
    ['the device refresh route', { method: 'POST', path: '/api/v1/auth/device/refresh' }, {}, 400],
    ['a method that is not in the allowlist', { method: 'TRACE', path: '/api/v1/version' }, {}, 400],
    ['a missing path', { method: 'GET' }, {}, 400],
  ]

  for (const [name, call, options, want] of forbidden) {
    test(name, async () => {
      received = []
      const response = await Handler.POST(request(call, options))
      assert.equal(response.status, want, `${name} answered ${response.status}`)
      // The point is not the status code — it is that nothing went out.
      assert.equal(received.length, 0, `${name} reached the control plane`)
    })
  }

  test('a body that is not JSON', async () => {
    const response = await Handler.POST({
      headers: { cookie: [COOKIE] },
      body: { encoding: 'utf8', data: 'not json' },
    })
    assert.equal(response.status, 400)
  })

  test('a body over the size bound', async () => {
    received = []
    const response = await Handler.POST(
      request({ method: 'POST', path: '/api/v1/repos', body: { blob: 'x'.repeat(1 << 21) } }),
    )
    assert.equal(response.status, 413)
    assert.equal(received.length, 0)
  })
})

describe('the gateway reports', () => {
  test('an unreachable control plane as 502 rather than a crash', async () => {
    const root = mkdtempSync(join(tmpdir(), 'hd-gateway-down-'))
    mkdirSync(join(root, 'server', 'data'), { recursive: true })
    // Port 1 has nothing on it.
    writeFileSync(
      join(root, 'server', 'data', 'bff.json'),
      JSON.stringify({ apiUrl: 'http://127.0.0.1:1', timeoutMs: 2000 }),
    )
    const previous = process.cwd()
    process.chdir(root)

    try {
      const response = await Handler.POST(request({ method: 'GET', path: '/api/v1/version' }))
      assert.equal(response.status, 502)
      assert.match(response.body, /unreachable/)
    } finally {
      process.chdir(previous)
    }
  })

  test('a missing configuration as 503, not as a silent success', async () => {
    const previous = process.cwd()
    process.chdir(mkdtempSync(join(tmpdir(), 'hd-gateway-unconfigured-')))
    try {
      const response = await Handler.POST(request({ method: 'GET', path: '/api/v1/version' }))
      assert.equal(response.status, 503)
    } finally {
      process.chdir(previous)
    }
  })
})
