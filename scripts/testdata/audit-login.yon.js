// TEMPORARY, audit-only: signs in with credentials from bff.json and sets the
// session cookie. Exists so a driver that cannot type can walk the UI; it is
// enabled only when bff.json carries auditLogin, and deleted after the audit.
import { readFileSync } from 'node:fs'
import https from 'node:https'

export class Handler {
  static async GET() {
    const parsed = JSON.parse(readFileSync('server/data/bff.json', 'utf8'))
    if (!parsed.auditLogin) {
      return { status: 404, headers: { 'content-type': 'text/plain' }, body: 'not enabled' }
    }
    const target = new URL(parsed.apiUrl + '/api/v1/auth/login')
    const body = JSON.stringify({
      identifier: parsed.auditLogin.identifier, password: parsed.auditLogin.password,
    })
    const answer = await new Promise((resolve, reject) => {
      const request = https.request({
        method: 'POST', hostname: target.hostname, port: target.port, path: target.pathname,
        ca: readFileSync(parsed.caFile),
        headers: { 'content-type': 'application/json', 'content-length': Buffer.byteLength(body) },
      }, (response) => {
        const chunks = []
        response.on('data', (chunk) => chunks.push(chunk))
        response.on('end', () => resolve({ status: response.statusCode, body: Buffer.concat(chunks).toString() }))
      })
      request.on('error', reject)
      request.write(body)
      request.end()
    })
    if (answer.status !== 200) {
      return { status: 502, headers: { 'content-type': 'text/plain' }, body: 'login failed' }
    }
    const issued = JSON.parse(answer.body)
    return {
      status: 302,
      headers: {
        location: '/',
        'set-cookie': `hd_session=${issued.session_id}.${issued.session_secret}; Path=/; HttpOnly; SameSite=Strict; Max-Age=3600`,
      },
      body: '',
    }
  }
}
