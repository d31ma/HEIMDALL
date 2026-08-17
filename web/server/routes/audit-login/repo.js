// TEMPORARY — demo only. The audit-login route's data access: the demo
// credentials from bff.json. It is enabled only when bff.json carries
// auditLogin, and this whole directory is deleted after the audit.

import { readFileSync } from 'node:fs'

// credentials returns the demo identity, or null when the route is
// disabled — which is the production state.
export function credentials() {
  try {
    const parsed = JSON.parse(readFileSync('server/data/bff.json', 'utf8'))
    if (!parsed.auditLogin) return null
    return {
      identifier: String(parsed.auditLogin.identifier || ''),
      password: String(parsed.auditLogin.password || ''),
    }
  } catch {
    return null
  }
}
