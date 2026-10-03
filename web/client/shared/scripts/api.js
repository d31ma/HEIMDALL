/**
 * @file The browser's view of the control plane.
 *
 * Every call goes through the gateway, which attaches the session from an
 * httpOnly cookie. Nothing here holds a credential, and there is nothing in
 * this module for an injected script to steal.
 *
 * The response typedefs below are deliberately partial: they name the fields
 * the UI reads, not every field the API returns. A typedef that claims to
 * mirror the whole contract goes stale the first time the contract grows,
 * and `api/openapi.json` is the generated source of truth for that.
 */

/**
 * @typedef {object} AppRef
 * @property {string} project
 * @property {string} app
 */

/**
 * @typedef {object} InstanceRef
 * @property {string} service
 * @property {string} instance
 */

/**
 * @typedef {object} Instance
 * @property {InstanceRef} ref
 * @property {string} [image]
 * @property {string} [status]
 * @property {string} [health]
 * @property {string} [revision]
 * @property {string} [node]
 * @property {number} [restarts]
 * @property {string} [started_at]
 * @property {string} [finished_at]
 * @property {number} [exit_code]
 */

/**
 * @typedef {object} ActivityEntry
 * @property {string} operation_id
 * @property {string} at
 * @property {string} phase
 * @property {string} [kind]
 * @property {string} [revision]
 * @property {string} [principal_id]
 * @property {string} [reason_code]
 * @property {string} [message]
 */

/**
 * @typedef {object} HistoryAnswer
 * @property {ActivityEntry[]} [activity] The paged index — the Activity table's source.
 * @property {Operation[]} [operations] The same page as whole operation documents.
 * @property {RevisionSummary[]} [revisions]
 * @property {string} [next_cursor] Empty on the last page.
 * @property {string} [revisions_cursor]
 * @property {boolean} [truncated]
 */

/**
 * @typedef {object} RevisionSummary
 * @property {string} [id]
 * @property {string} [commit]
 * @property {string} [spec_hash]
 * @property {string} [author]
 * @property {string} [message]
 * @property {boolean} [signed]
 * @property {string} [rendered_at]
 */

/**
 * @typedef {object} ServiceDiff
 * @property {string} service
 * @property {string} [health]
 * @property {string} [message]
 * @property {string} [schedule]
 * @property {string} [next_run_at]
 * @property {string} [last_run_at]
 * @property {string} [last_run_status]
 */

/**
 * @typedef {object} StatusAnswer
 * @property {string} [sync_status]
 * @property {string} [health]
 * @property {string} [desired_revision]
 * @property {string} [live_revision]
 * @property {string} [target]
 * @property {ServiceDiff[]} [services]
 */

/**
 * @typedef {object} RegistryBinding
 * @property {string} [url]
 * @property {string} [ref]
 * @property {string} [path]
 * @property {boolean} [prune]
 * @property {boolean} [require_signature]
 * @property {string} [bound_at]
 * @property {string} [bound_by]
 */

/**
 * @typedef {object} Project
 * @property {string} name
 * @property {string} [id]
 */

/**
 * @typedef {object} Application
 * @property {string} name
 * @property {string} [id]
 * @property {string} [project]
 * @property {string} [path]
 * @property {string} [ref]
 * @property {boolean} [suspended]
 * @property {string} [repo_id]
 * @property {string} [target_id]
 */

/**
 * @typedef {object} Operation
 * @property {string} [id]
 * @property {string} [phase]
 * @property {string} [started_at]
 * @property {string} [finished_at]
 * @property {string} [revision]
 * @property {string} [message]
 * @property {string} [principal_id]
 * @property {string} [reason_code]
 * @property {boolean} [dry_run]
 * @property {boolean} [rollback]
 * @property {{kind?: string, service?: string, reason?: string}[]} [operations]
 * @property {{kind?: string, service?: string}[]} [applied]
 * @property {Record<string, string>} [failures]
 */

/**
 * @typedef {object} RegistryAnswer
 * @property {boolean} [bound]
 * @property {RegistryBinding} [binding]
 * @property {Operation[]} [syncs] Recent registry_sync operations.
 */

/**
 * @typedef {object} RuntimeEvent
 * @property {string} at
 * @property {string} [source]
 * @property {string} [type]
 * @property {string} [service]
 * @property {string} [message]
 */

/**
 * @typedef {object} AppDetail
 * @property {Application} [application]
 * @property {StatusAnswer} [status]
 * @property {{name: string, image?: string, depends_on?: string[], replicas?: number}[]} [topology]
 */

/**
 * @typedef {object} Sample
 * @property {string} at
 * @property {number} value
 */

/**
 * @typedef {object} Series
 * @property {Sample[]} [cpu_percent]
 * @property {Sample[]} [memory_bytes]
 * @property {number} [memory_limit]
 * @property {Sample[]} [net_rx_bytes]
 * @property {Sample[]} [net_tx_bytes]
 * @property {Sample[]} [block_read_bytes]
 * @property {Sample[]} [block_write_bytes]
 * @property {Sample[]} [pids]
 * @property {Sample[]} [cpu_throttled]
 * @property {Sample[]} [net_errors]
 */

// DuVay themes switch on a `w-theme` attribute on <html>. Tac builds the
// document in the browser, so there is no server-rendered <html> tag to put it
// on — the page has to set it, and every page imports this module.
//
// "auto" follows the operating system. An operator watching a deploy at 3am
// has usually already told their machine which they want.
if (typeof document !== 'undefined' && !document.documentElement.hasAttribute('w-theme')) {
  document.documentElement.setAttribute('w-theme', 'auto')
}

// The document language, for the same reason as the theme: Tac builds the
// document in the browser, so there is no server-rendered <html> to carry it.
if (typeof document !== 'undefined' && !document.documentElement.lang) {
  document.documentElement.lang = 'en'
}

// The favicon rides the same path: Tac owns the <head>, so the mascot link
// is added here, once, for every page that imports this module.
if (typeof document !== 'undefined' && !document.querySelector('link[rel="icon"]')) {
  const icon = document.createElement('link')
  icon.rel = 'icon'
  icon.type = 'image/svg+xml'
  icon.href = '/shared/assets/heimdall-mark.svg'
  document.head.appendChild(icon)
}

/** Thrown for any non-2xx answer, carrying what the control plane said. */
export class ApiError extends Error {
  /**
   * @param {number} status HTTP status the gateway relayed.
   * @param {{message?: string, code?: string, reason_code?: string}} [payload]
   */
  constructor(status, payload) {
    super(payload?.message || payload?.code || `request failed with ${status}`)
    this.name = 'ApiError'
    this.status = status
    this.code = payload?.code || ''
    // SESAME's own reason for a denial. It distinguishes "you have no grant"
    // from "your session assurance is too low", and guessing between them
    // would be inventing authorization logic in a browser.
    this.reasonCode = payload?.reason_code || ''
    this.payload = payload || {}
  }
}

/**
 * Reads the UI repeats while nothing has changed — switching to Logs and
 * back re-asks for the same metrics, and every page reload re-asks for the
 * same history. The cache is keyed by the exact call and expires quickly:
 * an operator watching a deploy must never be shown a stale answer for
 * longer than the poll they are already waiting through.
 *
 * Only reads are cached, and only ones whose staleness is measured in
 * seconds. Logs are never cached — a tail that repeats itself is worse than
 * no tail — and neither is anything that mutates.
 *
 * Tachyon's own caching does not reach this. Its service worker caches static
 * assets, and it sets `Cache-Control: no-store` only if a handler has not
 * (so a Yon route may opt into HTTP caching) — but every control-plane read
 * tunnels through one `POST /gateway`, which no HTTP cache may store. Turning
 * reads into cacheable GETs would put per-principal answers in a shared disk
 * cache for a few seconds saved; this map is the smaller answer.
 *
 * @type {Map<string, {at: number, payload: any}>}
 */
const reads = new Map()

/** @type {number} How long a cached read stays usable. */
const READ_TTL_MS = 4000

/** @type {number} Bounds the cache; the working set is one open page. */
const READ_MAX = 64

/**
 * Drops every cached read. Called after anything that changes state, because
 * the whole point of a sync is that the next read differs.
 *
 * @returns {void}
 */
export function invalidateReads() {
  reads.clear()
}

/**
 * Sends one control-plane request through the gateway.
 *
 * @param {'GET'|'POST'|'PATCH'|'DELETE'} method
 * @param {string} path Control-plane path, e.g. `/api/v1/projects`.
 * @param {unknown} [body]
 * @param {{raw?: boolean, slow?: boolean, cache?: boolean}} [options] raw
 *   returns the body as text (logs are not JSON); slow gives the long leash
 *   an image pull needs; cache reuses a recent identical read.
 * @returns {Promise<any>} The decoded payload, or the raw text when raw.
 * @throws {ApiError} On any non-2xx answer.
 */
async function gateway(method, path, body, { raw = false, slow = false, cache = false } = {}) {
  const key = `${method} ${path}`
  if (cache) {
    const hit = reads.get(key)
    if (hit && Date.now() - hit.at < READ_TTL_MS) return hit.payload
  } else if (method !== 'GET') {
    // A mutation invalidates everything: a sync changes history, live state,
    // instances, and metrics at once, and tracking which is a bookkeeping
    // job that goes wrong quietly.
    invalidateReads()
  }

  // Reads time out fast so a wedged control plane shows an error in seconds,
  // not a frozen page; syncs and rollbacks keep the long leash an image pull
  // needs.
  const response = await fetch('/gateway', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    // Same-origin only: the cookie is SameSite=Strict and there is no other
    // origin this page talks to.
    credentials: 'same-origin',
    body: JSON.stringify({ method, path, body, timeout_ms: slow ? 120000 : 8000 }),
  })

  const text = await response.text()
  // Logs are plain text; parsing them as JSON would truncate a tail to an
  // error message.
  if (raw && response.ok) return text

  let payload = {}
  try {
    payload = text ? JSON.parse(text) : {}
  } catch {
    payload = { message: text.slice(0, 400) }
  }

  if (!response.ok) throw new ApiError(response.status, payload)
  if (cache) {
    if (reads.size >= READ_MAX) reads.clear()
    reads.set(key, { at: Date.now(), payload })
  }
  return payload
}

/**
 * The control-plane calls the UI makes. Every one of them is a route in
 * `api/openapi.json`; nothing here invents a path.
 */
export const api = {
  /**
   * @param {{fresh?: boolean}} [options] fresh skips the read cache, which
   *   the session probe needs: a cached 200 would answer "signed in" for
   *   seconds after signing out.
   * @returns {Promise<{projects?: Project[]}>}
   */
  projects: (options) =>
    gateway('GET', '/api/v1/projects', undefined, { cache: !options?.fresh }),
  /**
   * @param {string} project
   * @returns {Promise<{applications?: Application[]}>}
   */
  apps: (project) =>
    gateway('GET', `/api/v1/projects/${encodeURIComponent(project)}/apps`, undefined, { cache: true }),
  /**
   * One application: its document, its live status and per-service diff, and
   * the desired topology the tree view draws from.
   *
   * @param {string} project
   * @param {string} app
   * @returns {Promise<AppDetail>}
   */
  app: (project, app) =>
    gateway('GET', `/api/v1/projects/${encodeURIComponent(project)}/apps/${encodeURIComponent(app)}`,
      undefined, { cache: true }),
  /**
   * One page of an application's history, newest first.
   *
   * `before` is a keyset cursor from a previous answer's next_cursor, not an
   * offset: operations are written continuously, and an offset would
   * double-show and skip rows as new ones land between requests.
   *
   * @param {string} project
   * @param {string} app
   * @param {{limit?: number, before?: string}} [page]
   * @returns {Promise<HistoryAnswer>}
   */
  history: (project, app, { limit = 50, before = '' } = {}) =>
    gateway('GET', `/api/v1/projects/${encodeURIComponent(project)}/apps/${encodeURIComponent(app)}/history`
      + `?limit=${limit}${before ? `&before=${encodeURIComponent(before)}` : ''}`,
      undefined, { cache: true }),
  /**
   * @param {string} project
   * @param {string} app
   * @param {{dry_run?: boolean, services?: string[], prune?: boolean}} [options]
   * @returns {Promise<object>} The operation document.
   */
  sync: (project, app, options = {}) =>
    gateway('POST', `/api/v1/projects/${encodeURIComponent(project)}/apps/${encodeURIComponent(app)}/sync`, options,
      { slow: true }),
  /**
   * @param {string} project
   * @param {string} app
   * @param {string} revision Commit of a stored revision.
   * @returns {Promise<object>} The operation document.
   */
  rollback: (project, app, revision) =>
    gateway('POST', `/api/v1/projects/${encodeURIComponent(project)}/apps/${encodeURIComponent(app)}/rollback`, {
      revision,
    }, { slow: true }),
  /**
   * @param {string} project
   * @param {string} app
   * @returns {Promise<{instances?: Instance[]}>}
   */
  instances: (project, app) =>
    gateway('GET', `/api/v1/projects/${encodeURIComponent(project)}/apps/${encodeURIComponent(app)}/instances`,
      undefined, { cache: true }),
  /**
   * @param {string} project
   * @param {string} app
   * @param {string} instance
   * @param {string} service
   * @param {string} [window] A duration such as `15m` or `6h`. Absent asks
   *   for everything retention kept.
   * @returns {Promise<{series?: Series, metrics?: string[]}>} metrics is the
   *   service's heimdall.metrics selection; empty means the default set.
   */
  metrics: (project, app, instance, service, window = '') =>
    gateway('GET', `/api/v1/projects/${encodeURIComponent(project)}/apps/${encodeURIComponent(app)}/metrics`
      + `?instance=${encodeURIComponent(instance)}&service=${encodeURIComponent(service)}`
      + (window ? `&window=${encodeURIComponent(window)}` : ''), undefined, { cache: true }),
  /**
   * A bounded log tail, as text rather than JSON.
   *
   * @param {string} project
   * @param {string} app
   * @param {string} instance
   * @param {string} service
   * @param {number} [tail]
   * @returns {Promise<string>}
   */
  logs: (project, app, instance, service, tail = 200) =>
    // service travels with the read: cloud adapters (ECS) reconstruct the
    // provider-side stream name from it, and an empty service reads a
    // stream that does not exist.
    gateway('GET', `/api/v1/projects/${encodeURIComponent(project)}/apps/${encodeURIComponent(app)}/logs?instance=${encodeURIComponent(instance)}&service=${encodeURIComponent(service)}&tail=${tail}`,
      undefined, { raw: true }),
  /**
   * Starts one out-of-band execution of a scheduled service (`app:run`).
   *
   * @param {string} project
   * @param {string} app
   * @param {string} service
   * @returns {Promise<{status?: string}>}
   */
  runService: (project, app, service) =>
    gateway('POST', `/api/v1/projects/${encodeURIComponent(project)}/apps/${encodeURIComponent(app)}/services/${encodeURIComponent(service)}/run`,
      {}, { slow: true }),
  /**
   * @param {string} project
   * @param {string} app
   * @returns {Promise<{events?: object[]}>} The runtime's own recent events.
   */
  events: (project, app) =>
    gateway('GET', `/api/v1/projects/${encodeURIComponent(project)}/apps/${encodeURIComponent(app)}/events`,
      undefined, { cache: true }),
  /** @returns {Promise<{targets?: object[]}>} */
  targets: () => gateway('GET', '/api/v1/targets'),
  /** @returns {Promise<RegistryAnswer>} */
  registry: () => gateway('GET', '/api/v1/registry'),
  /**
   * Binds the root repository — ADR 0010's one interactive act.
   *
   * @param {{url?: string, ref?: string, path?: string, prune?: boolean, require_signature?: boolean}} binding
   * @returns {Promise<object>}
   */
  registryBind: (binding) => gateway('POST', '/api/v1/registry/bind', binding),
  /** @returns {Promise<object>} */
  registryUnbind: () => gateway('DELETE', '/api/v1/registry/bind'),
  /** @returns {Promise<object>} */
  registrySync: () => gateway('POST', '/api/v1/registry/sync', undefined, { slow: true }),
  /**
   * @param {string} userCode
   * @returns {Promise<{client_name?: string, client_id?: string, scope?: string}>}
   *   What is waiting for approval — the client, so an operator approves
   *   something specific rather than a code.
   */
  deviceLookup: (userCode) => gateway('POST', '/api/v1/auth/device/lookup', { user_code: userCode }),
  /**
   * @param {string} userCode
   * @returns {Promise<object>}
   */
  deviceApprove: (userCode) => gateway('POST', '/api/v1/auth/device/approve', { user_code: userCode }),
  /**
   * @param {string} userCode
   * @returns {Promise<object>}
   */
  deviceDeny: (userCode) => gateway('POST', '/api/v1/auth/device/deny', { user_code: userCode }),
  /** @returns {Promise<{version?: string}>} */
  version: () => gateway('GET', '/api/v1/version'),
}

/** The session, which lives in an httpOnly cookie the page cannot read. */
export const session = {
  /**
   * @param {string} identifier
   * @param {string} password
   * @param {string} [totp] Authenticator code, when one is enrolled.
   * @returns {Promise<object>}
   * @throws {ApiError} On a refused sign-in — deliberately uniform, so a
   *   wrong username and a wrong password are indistinguishable.
   */
  async login(identifier, password, totp) {
    const response = await fetch('/session', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({ identifier, password, totp }),
    })
    const payload = await response.json().catch(() => ({}))
    if (!response.ok) throw new ApiError(response.status, payload)
    // Whoever was signed in before, this is someone else's session now.
    invalidateReads()
    return payload
  },

  /** @returns {Promise<void>} */
  async logout() {
    await fetch('/session', { method: 'DELETE', credentials: 'same-origin' })
    // The cache outlives the cookie otherwise, and the page it feeds looks
    // signed in until it expires.
    invalidateReads()
  },

  /**
   * Whether this browser holds a session, decided by asking rather than by
   * reading a flag. The cookie is httpOnly, so the page genuinely cannot know
   * on its own — and a page that guessed would show an empty app list instead
   * of a login form.
   *
   * The probe must be an *authorized* route: /version answers 200 to anyone,
   * so probing it read every stale or forged cookie as "signed in" and
   * stranded the user on a raw error. The UI audit caught it doing exactly
   * that.
   *
   * @returns {Promise<boolean>}
   */
  async active() {
    try {
      await api.projects({ fresh: true })
      return true
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) return false
      throw error
    }
  },
}

/**
 * Resolves once the named elements exist.
 *
 * Tac constructs the page's DOM in the browser from a compiler-produced render
 * plan, so a page script runs before its own markup is there. Waiting on
 * DOMContentLoaded alone is not enough — the plan may still be mounting — so
 * this observes until the elements appear, and gives up rather than hanging
 * forever if a page asks for something it never renders.
 *
 * @param {string[]} ids
 * @param {number} [timeoutMs]
 * @returns {Promise<HTMLElement[]>}
 */
export function whenReady(ids, timeoutMs = 10_000) {
  const found = () => {
    const elements = ids.map((id) => document.getElementById(id))
    return elements.every(Boolean) ? elements : null
  }

  return new Promise((resolve, reject) => {
    const immediate = found()
    if (immediate) {
      resolve(immediate)
      return
    }

    const observer = new MutationObserver(() => {
      const elements = found()
      if (!elements) return
      observer.disconnect()
      clearTimeout(timer)
      resolve(elements)
    })
    const timer = setTimeout(() => {
      observer.disconnect()
      reject(new Error(`the page never rendered: ${ids.join(', ')}`))
    }, timeoutMs)

    observer.observe(document.documentElement, { childList: true, subtree: true })
  })
}

/**
 * Maps a health or sync status onto a DuVay colour suffix. The class grammar
 * is single-dash (`w-chip-success`), not the BEM double-dash this UI
 * originally guessed — a guess that silently no-opped, so every status chip
 * rendered neutral and the health colour coding never existed on screen.
 *
 * @param {string} status
 * @returns {'success'|'primary'|'warning'|'error'|'secondary'}
 */
export function tone(status) {
  switch (status) {
    case 'Healthy':
    case 'Synced':
      return 'success'
    case 'Progressing':
      return 'primary'
    case 'Degraded':
    case 'OutOfSync':
      return 'warning'
    case 'Missing':
      return 'error'
    case 'Suspended':
      return 'secondary'
    default:
      // Unknown is deliberately not success-coloured: "we could not look"
      // must never render as "all is well".
      return 'secondary'
  }
}

/**
 * Shortens a commit for display without losing its identity.
 *
 * @param {string} [revision]
 * @returns {string} The em dash when there is no revision, so a column never
 *   reads as an empty cell that might mean zero.
 */
export function shortRevision(revision) {
  return revision ? String(revision).slice(0, 12) : '—'
}
