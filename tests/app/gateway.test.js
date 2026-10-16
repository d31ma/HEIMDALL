import assert from 'node:assert/strict'
import { spawn, spawnSync } from 'node:child_process'
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import http from 'node:http'
import https from 'node:https'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { after, before, describe, test } from 'node:test'

const COOKIE = 'hd_session=ses_1.secret'
const ROOT = mkdtempSync(join(tmpdir(), 'hd-gateway-runtime-'))
const COMPILER = process.env.HD_TEST_COMPILER || 'ty'
const WEB = fileURLToPath(new URL('../../app/', import.meta.url))
const RESPONSE_LIMIT = 8 << 20
let upstream, runtime, gatewayURL, upstreamURL
let received = []
let runtimeLog = ''
let reply = (_request, response) => response.end(JSON.stringify({ ok: true, saw: _request.url }))

/** @param {{apiUrl: string, timeoutMs?: number, caFile?: string, secureCookie?: boolean, padding?: string}} [settings] */
async function configure(settings = { apiUrl: upstreamURL, timeoutMs: 2000 }) {
  writeFileSync(join(ROOT, 'server/data/bff.json'), JSON.stringify(settings))
  if (runtime) await restartRuntime()
}
async function request(body, { cookie = COOKIE, raw = false } = {}) {
  const response = await fetch(gatewayURL + '/gateway', {
    method: 'POST', headers: { 'content-type': 'application/json', ...(cookie ? { cookie } : {}) },
    body: raw ? body : JSON.stringify(body), signal: AbortSignal.timeout(10000),
  })
  return { status: response.status, body: await response.text(), headers: response.headers }
}


async function stopRuntime() {
  const owned = runtime
  if (!owned) return
  runtime = null
  if (owned.exitCode !== null || owned.signalCode !== null) return
  let timer
  const stopped = new Promise(resolve => owned.once('exit', resolve))
  try {
    process.kill(-owned.pid, 'SIGTERM')
    await Promise.race([stopped, new Promise(resolve => { timer = setTimeout(resolve, 5000) })])
    if (owned.exitCode === null && owned.signalCode === null) process.kill(-owned.pid, 'SIGKILL')
    await stopped
  } finally { clearTimeout(timer) }
}
async function restartRuntime() {
  await stopRuntime()
  await startRuntime()
}
async function startRuntime() {
  if (!gatewayURL) {
  const reservation = http.createServer()
  await new Promise(resolve => reservation.listen(0, '127.0.0.1', () => resolve(undefined)))
  const port = /** @type {import('node:net').AddressInfo} */ (reservation.address()).port
  await new Promise(resolve => reservation.close(resolve))
  gatewayURL = `http://127.0.0.1:${port}`
  }
  runtime = spawn(COMPILER, ['serve', ROOT, '--host', '127.0.0.1', '--port', String(new URL(gatewayURL).port), '--out-dir', 'dist', '--no-bundle', '--no-watch', '--no-self-restart'], { cwd: ROOT, detached: true, stdio: ['ignore', 'pipe', 'pipe'] })
  runtime.stdout.on('data', chunk => { runtimeLog += chunk })
  runtime.stderr.on('data', chunk => { runtimeLog += chunk })
  const deadline = Date.now() + 30000
  while (Date.now() < deadline) {
    assert.equal(runtime.exitCode, null, runtimeLog)
    try { if ((await fetch(gatewayURL, { signal: AbortSignal.timeout(1000) })).ok) return } catch {}
    await new Promise(resolve => setTimeout(resolve, 100))
  }
  assert.fail('owned Tachyon runtime did not become ready: ' + runtimeLog)
}

before(async () => {
  upstream = http.createServer((incoming, response) => {
    const chunks = []
    incoming.on('data', chunk => chunks.push(chunk))
    incoming.on('end', () => {
      received.push({ method: incoming.method, url: incoming.url, authorization: incoming.headers.authorization, body: Buffer.concat(chunks).toString('utf8') })
      response.setHeader('content-type', 'application/json')
      reply(incoming, response)
    })
  })
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', () => resolve(undefined)))
  upstreamURL = `http://127.0.0.1:${/** @type {import('node:net').AddressInfo} */ (upstream.address()).port}`
  cpSync(join(WEB, 'server'), join(ROOT, 'server'), { recursive: true, filter: source => source !== join(WEB, 'server/data/bff.json') })
  mkdirSync(join(ROOT, 'client/pages'), { recursive: true })
  mkdirSync(join(ROOT, 'client/components/gateway-fixture'), { recursive: true })
  writeFileSync(join(ROOT, 'client/pages/tac.html'), '<gateway-fixture />')
  writeFileSync(join(ROOT, 'client/components/gateway-fixture/tac.html'), '<p>Gateway protocol fixture</p>')
  await configure()
  const bundle = spawnSync(COMPILER, ['bundle', ROOT, '--out-dir', 'dist', '--no-incremental'], { cwd: ROOT, encoding: 'utf8', timeout: 120000 })
  assert.equal(bundle.status, 0, bundle.stdout + bundle.stderr)
  await startRuntime()
})
after(async () => {
  await stopRuntime()
  upstream?.closeAllConnections()
  if (upstream) await new Promise(resolve => upstream.close(resolve))
  rmSync(ROOT, { recursive: true, force: true })
})

describe('the actual Yon gateway forwards', () => {
  test('a permitted call with its session attached as bearer', async () => {
    received = []
    const response = await request({ method: 'GET', path: '/api/v1/version' })
    assert.equal(response.status, 200)
    assert.equal(received.length, 1)
    assert.equal(received[0].authorization, 'Bearer ses_1.secret')
    assert.equal(response.body, JSON.stringify({ ok: true, saw: '/api/v1/version' }))
  })
  test('plain-text upstream responses preserve bytes and their content type', async () => {
    reply = (_incoming, response) => { response.setHeader('content-type', 'text/plain; charset=utf-8'); response.end('plain 界 log\n') }
    try {
      const response = await request({ method: 'GET', path: '/api/v1/projects/a/apps/b/logs' })
      assert.equal(response.status, 200, response.body.slice(0, 700) + runtimeLog.slice(-3000))
      assert.equal(response.headers.get('content-type'), 'text/plain; charset=utf-8')
      assert.equal(response.body, 'plain 界 log\n')
    } finally { reply = (_incoming, response) => response.end(JSON.stringify({ ok: true, saw: _incoming.url })) }
  })
  test('encoded query filters remain valid', async () => {
    received = []
    const path = '/api/v1/projects/a/apps/b/logs?instance=web&filter=a%20b&tail=50'
    assert.equal((await request({ method: 'GET', path })).status, 200)
    assert.equal(received[0].url, path)
  })
})

describe('the actual Yon gateway refuses before upstream', () => {
  /** @type {[string, {method: string, path?: string}, {cookie?: string | null, raw?: boolean}, number][]} */
  const forbidden = [
    ['no session', { method: 'GET', path: '/api/v1/version' }, { cookie: null }, 401],
    ['outside API', { method: 'GET', path: '/healthz' }, {}, 400],
    ['absolute URL', { method: 'GET', path: 'http://evil.example/api/v1/x' }, {}, 400],
    ['protocol relative URL', { method: 'GET', path: '//evil.example/api/v1/x' }, {}, 400],
    ['traversal', { method: 'GET', path: '/api/v1/../../etc/passwd' }, {}, 400],
    ['backslash', { method: 'GET', path: '/api/v1/x\\..\\y' }, {}, 400],
    ['login', { method: 'POST', path: '/api/v1/auth/login' }, {}, 400],
    ['logout', { method: 'POST', path: '/api/v1/auth/logout' }, {}, 400],
    ['device token', { method: 'POST', path: '/api/v1/auth/device/token' }, {}, 400],
    ['device start', { method: 'POST', path: '/api/v1/auth/device/start' }, {}, 400],
    ['device refresh', { method: 'POST', path: '/api/v1/auth/device/refresh' }, {}, 400],
    ['encoded login', { method: 'POST', path: '/api/v1/auth/%6cogin' }, {}, 400],
    ['encoded device token', { method: 'POST', path: '/api/v1/auth/device/%74oken' }, {}, 400],
    ['encoded traversal', { method: 'GET', path: '/api/v1/%2e%2e/version' }, {}, 400],
    ['double slash', { method: 'POST', path: '/api/v1/auth//login' }, {}, 400],
    ['fragment alias', { method: 'POST', path: '/api/v1/auth/login#x' }, {}, 400],
    ['single-dot alias', { method: 'POST', path: '/api/v1/./auth/login' }, {}, 400],
    ['newline alias', { method: 'POST', path: '/api/v1/auth/lo\ngin' }, {}, 400],
    ['disallowed method', { method: 'TRACE', path: '/api/v1/version' }, {}, 400],
    ['missing path', { method: 'GET' }, {}, 400],
  ]
  for (const [name, input, options, status] of forbidden) {
    test(name, async () => {
      received = []
      assert.equal((await request(input, options)).status, status)
      assert.equal(received.length, 0)
    })
  }
  test('invalid JSON', async () => {
    received = []
    assert.equal((await request('not json', { raw: true })).status, 400)
    assert.equal(received.length, 0)
  })
  test('an oversized UTF8 envelope', async () => {
    received = []
    assert.equal((await request({ method: 'POST', path: '/api/v1/repos', body: { blob: '界'.repeat(400000) } })).status, 413)
    assert.equal(received.length, 0)
  })
})

describe('actual transport failures and byte bounds', () => {
  test('missing configuration refuses as 503', async () => {
    rmSync(join(ROOT, 'server/data/bff.json'))
    await restartRuntime()
    try { assert.equal((await request({ method: 'GET', path: '/api/v1/version' })).status, 503) }
    finally { await configure() }
  })
  test('unreachable upstream reports 502', async () => {
    await configure({ apiUrl: 'http://127.0.0.1:1', timeoutMs: 500 })
    try { assert.equal((await request({ method: 'GET', path: '/api/v1/version' })).status, 502) }
    finally { await configure() }
  })
  test('oversized response refuses complete success and calls once', async () => {
    received = []
    reply = (_incoming, response) => response.end('x'.repeat(RESPONSE_LIMIT + 1))
    try {
      assert.equal((await request({ method: 'GET', path: '/api/v1/version' })).status, 502)
      assert.equal(received.length, 1)
    } finally { reply = (_incoming, response) => response.end(JSON.stringify({ ok: true, saw: _incoming.url })) }
  })
  test('response exactly at the UTF8 byte ceiling stays complete', async () => {
    const payload = '"界' + 'x'.repeat(RESPONSE_LIMIT - 5) + '"'
    assert.equal(Buffer.byteLength(payload), RESPONSE_LIMIT)
    reply = (_incoming, response) => response.end(payload)
    try {
      const response = await request({ method: 'GET', path: '/api/v1/version' })
      assert.equal(response.status, 200, response.body.slice(0, 700) + runtimeLog.slice(-3000))
      assert.equal(Buffer.byteLength(response.body), RESPONSE_LIMIT)
      assert.equal(response.body, payload)
    } finally { reply = (_incoming, response) => response.end(JSON.stringify({ ok: true, saw: _incoming.url })) }
  })
  test('interrupted response refuses rather than hanging', async () => {
    received = []
    reply = (_incoming, response) => {
      response.writeHead(200, { 'content-length': 1024 })
      response.write('incomplete')
      setImmediate(() => response.destroy())
    }
    try {
      assert.equal((await request({ method: 'GET', path: '/api/v1/version' })).status, 502)
      assert.equal(received.length, 1)
    } finally { reply = (_incoming, response) => response.end(JSON.stringify({ ok: true, saw: _incoming.url })) }
  })
})

test('session response keeps credentials in a secure httpOnly cookie', async () => {
  reply = (_incoming, response) => response.end(JSON.stringify({ principal_id: 'fixture', assurance: 'password', expires_at: new Date(Date.now() + 3600000).toISOString(), session_id: 'ses_fixture', session_secret: 'fixture-secret' }))
  try {
    const response = await fetch(gatewayURL + '/session', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ identifier: 'fixture', password: 'fixture' }) })
    assert.equal(response.status, 200)
    const body = await response.json()
    assert.equal(Object.hasOwn(body, 'session_id'), false)
    assert.equal(Object.hasOwn(body, 'session_secret'), false)
    const cookie = response.headers.get('set-cookie')
    assert.match(cookie, /HttpOnly/)
    assert.match(cookie, /SameSite=Strict/)
    assert.match(cookie, /; Secure/)
  } finally { reply = (_incoming, response) => response.end(JSON.stringify({ ok: true, saw: _incoming.url })) }
})


describe('real Go Yon transport parity', () => {
  test('redirects are returned without replaying an invocation POST', async () => {
    let redirected = 0
    const destination = http.createServer((_incoming, response) => { redirected++; response.end('{}') })
    await new Promise(resolve => destination.listen(0, '127.0.0.1', () => resolve(undefined)))
    received = []
    reply = (_incoming, response) => {
      response.writeHead(307, { location: `http://127.0.0.1:${/** @type {import('node:net').AddressInfo} */ (destination.address()).port}/replayed` })
      response.end('redirect')
    }
    try {
      const response = await request({ method: 'POST', path: '/api/v1/projects/a/apps/b/services/handler/invoke', body: { payload: {} } })
      assert.equal(response.status, 307)
      assert.equal(response.body, 'redirect')
      assert.equal(received.length, 1)
      assert.equal(redirected, 0)
    } finally {
      reply = (_incoming, response) => response.end(JSON.stringify({ ok: true, saw: _incoming.url }))
      await new Promise(resolve => destination.close(resolve))
    }
  })
  test('a slow body hits the total deadline and closes its upstream connection', async () => {
    let closed = false
    await configure({ apiUrl: upstreamURL, timeoutMs: 120 })
    reply = (_incoming, response) => {
      response.writeHead(200)
      const timer = setInterval(() => response.write(' '), 20)
      response.on('close', () => { closed = true; clearInterval(timer) })
    }
    const started = Date.now()
    try {
      assert.equal((await request({ method: 'GET', path: '/api/v1/version' })).status, 502)
      assert.ok(Date.now() - started < 2000)
      await new Promise(resolve => setTimeout(resolve, 50))
      assert.equal(closed, true)
    } finally {
      await configure()
      reply = (_incoming, response) => response.end(JSON.stringify({ ok: true, saw: _incoming.url }))
    }
  })
  test('an invocation deadline stays ambiguous and is never replayed', async () => {
    await configure({ apiUrl: upstreamURL, timeoutMs: 100 })
    received = []
    reply = (_incoming, _response) => {}
    try {
      const response = await request({ method: 'POST', path: '/api/v1/projects/a/apps/b/services/handler/invoke', body: { payload: {} } })
      assert.equal(response.status, 504)
      assert.equal(JSON.parse(response.body).code, 'HD0408')
      assert.match(response.body, /execution may continue/)
      assert.match(response.body, /do not retry automatically/)
      await new Promise(resolve => setTimeout(resolve, 150))
      assert.equal(received.length, 1)
    } finally {
      await configure()
      reply = (_incoming, response) => response.end(JSON.stringify({ ok: true, saw: _incoming.url }))
    }
  })
  test('encoded output is bounded below the native protocol frame ceiling', async () => {
    reply = (_incoming, response) => response.end('<'.repeat(3 << 20))
    try {
      const response = await request({ method: 'GET', path: '/api/v1/version' })
      assert.equal(response.status, 502)
      assert.match(response.body, /encoded response limit/)
    } finally { reply = (_incoming, response) => response.end(JSON.stringify({ ok: true, saw: _incoming.url })) }
  })
  test('invalid UTF8 never silently becomes replacement characters', async () => {
    reply = (_incoming, response) => response.end(Buffer.from([0xff, 0xfe]))
    try { assert.equal((await request({ method: 'GET', path: '/api/v1/version' })).status, 502) }
    finally { reply = (_incoming, response) => response.end(JSON.stringify({ ok: true, saw: _incoming.url })) }
  })
  test('custom CA succeeds while wrong trust and wrong hostname are refused', async () => {
    const certificates = []
    for (const [name, alternative] of [['trusted', 'IP:127.0.0.1'], ['wrong', 'DNS:fixture.invalid']]) {
      const key = join(ROOT, name + '.key'), certificate = join(ROOT, name + '.crt')
      const generated = spawnSync('openssl', ['req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes', '-keyout', key, '-out', certificate, '-days', '1', '-subj', '/CN=fixture', '-addext', 'subjectAltName=' + alternative], { encoding: 'utf8', timeout: 10000 })
      assert.equal(generated.status, 0, generated.stderr)
      certificates.push({ key, certificate })
    }
    let attempts = 0
    const endpoint = https.createServer({ key: readFileSync(certificates[0].key), cert: readFileSync(certificates[0].certificate) }, (_incoming, response) => { attempts++; response.end('{"trusted":true}') })
    const mismatch = https.createServer({ key: readFileSync(certificates[1].key), cert: readFileSync(certificates[1].certificate) }, (_incoming, response) => { attempts++; response.end('{}') })
    await new Promise(resolve => endpoint.listen(0, '127.0.0.1', () => resolve(undefined)))
    await new Promise(resolve => mismatch.listen(0, '127.0.0.1', () => resolve(undefined)))
    try {
      await configure({ apiUrl: `https://127.0.0.1:${/** @type {import('node:net').AddressInfo} */ (endpoint.address()).port}`, caFile: certificates[0].certificate, timeoutMs: 2000 })
      assert.equal((await request({ method: 'GET', path: '/api/v1/version' })).status, 200)
      assert.equal(attempts, 1)
      await configure({ apiUrl: `https://127.0.0.1:${/** @type {import('node:net').AddressInfo} */ (endpoint.address()).port}`, caFile: certificates[1].certificate, timeoutMs: 2000 })
      assert.equal((await request({ method: 'GET', path: '/api/v1/version' })).status, 502)
      await configure({ apiUrl: `https://127.0.0.1:${/** @type {import('node:net').AddressInfo} */ (mismatch.address()).port}`, caFile: certificates[1].certificate, timeoutMs: 2000 })
      assert.equal((await request({ method: 'GET', path: '/api/v1/version' })).status, 502)
      assert.equal(attempts, 1)
    } finally {
      await configure()
      endpoint.closeAllConnections(); mismatch.closeAllConnections()
      await Promise.all([new Promise(resolve => endpoint.close(resolve)), new Promise(resolve => mismatch.close(resolve))])
    }
  })
})


describe('session transport fails closed on malformed successful responses', () => {
  /** @type {[string, string | Buffer][]} */
  const malformed = [
    ['empty object', '{}'],
    ['null', 'null'],
    ['missing session secret', JSON.stringify({ session_id: 'fixture', principal_id: 'fixture', expires_at: new Date(Date.now() + 3600000).toISOString() })],
    ['invalid expiration', JSON.stringify({ session_id: 'fixture', session_secret: 'fixture', principal_id: 'fixture', expires_at: 'never' })],
    ['invalid UTF8', Buffer.from([0xff])],
    ['invalid cookie characters', JSON.stringify({ session_id: 'fixture\r\n', session_secret: 'fixture', principal_id: 'fixture', expires_at: new Date(Date.now() + 3600000).toISOString() })],
  ]
  for (const [name, body] of malformed) {
    test(name, async () => {
      reply = (_incoming, response) => response.end(body)
      try {
        const response = await fetch(gatewayURL + '/session', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ identifier: 'fixture', password: 'fixture' }) })
        assert.equal(response.status, 502)
        assert.equal(response.headers.has('set-cookie'), false)
        assert.equal((await response.json()).code, 'HD0602')
      } finally { reply = (_incoming, response) => response.end(JSON.stringify({ ok: true, saw: _incoming.url })) }
    })
  }
})

describe('bounded private configuration reads', () => {
  test('oversized configuration is refused before upstream', async () => {
    received = []
    await configure({ apiUrl: upstreamURL, padding: 'x'.repeat(65536) })
    try {
      assert.equal((await request({ method: 'GET', path: '/api/v1/version' })).status, 503)
      assert.equal(received.length, 0)
    } finally { await configure() }
  })
  test('oversized CA is refused before opening upstream TLS', async () => {
    received = []
    const certificate = join(ROOT, 'oversized-ca.crt')
    writeFileSync(certificate, 'x'.repeat(65537))
    await configure({ apiUrl: upstreamURL, caFile: certificate, timeoutMs: 2000 })
    try {
      assert.equal((await request({ method: 'GET', path: '/api/v1/version' })).status, 502)
      assert.equal(received.length, 0)
    } finally { await configure() }
  })
})
