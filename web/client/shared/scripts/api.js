// The browser's view of the control plane.
//
// Every call goes through the gateway, which attaches the session from an
// httpOnly cookie. Nothing here holds a credential, and there is nothing in
// this module for an injected script to steal.

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

async function gateway(method, path, body, { raw = false, slow = false } = {}) {
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
  return payload
}

export const api = {
  projects: () => gateway('GET', '/api/v1/projects'),
  apps: (project) => gateway('GET', `/api/v1/projects/${encodeURIComponent(project)}/apps`),
  app: (project, app) =>
    gateway('GET', `/api/v1/projects/${encodeURIComponent(project)}/apps/${encodeURIComponent(app)}`),
  history: (project, app) =>
    gateway('GET', `/api/v1/projects/${encodeURIComponent(project)}/apps/${encodeURIComponent(app)}/history`),
  sync: (project, app, options = {}) =>
    gateway('POST', `/api/v1/projects/${encodeURIComponent(project)}/apps/${encodeURIComponent(app)}/sync`, options,
      { slow: true }),
  rollback: (project, app, revision) =>
    gateway('POST', `/api/v1/projects/${encodeURIComponent(project)}/apps/${encodeURIComponent(app)}/rollback`, {
      revision,
    }, { slow: true }),
  instances: (project, app) =>
    gateway('GET', `/api/v1/projects/${encodeURIComponent(project)}/apps/${encodeURIComponent(app)}/instances`),
  metrics: (project, app, instance, service) =>
    gateway('GET', `/api/v1/projects/${encodeURIComponent(project)}/apps/${encodeURIComponent(app)}/metrics?instance=${encodeURIComponent(instance)}&service=${encodeURIComponent(service)}`),
  logs: (project, app, instance, service, tail = 200) =>
    // service travels with the read: cloud adapters (ECS) reconstruct the
    // provider-side stream name from it, and an empty service reads a
    // stream that does not exist.
    gateway('GET', `/api/v1/projects/${encodeURIComponent(project)}/apps/${encodeURIComponent(app)}/logs?instance=${encodeURIComponent(instance)}&service=${encodeURIComponent(service)}&tail=${tail}`,
      undefined, { raw: true }),
  events: (project, app) =>
    gateway('GET', `/api/v1/projects/${encodeURIComponent(project)}/apps/${encodeURIComponent(app)}/events`),
  targets: () => gateway('GET', '/api/v1/targets'),
  registry: () => gateway('GET', '/api/v1/registry'),
  registryBind: (binding) => gateway('POST', '/api/v1/registry/bind', binding),
  registryUnbind: () => gateway('DELETE', '/api/v1/registry/bind'),
  registrySync: () => gateway('POST', '/api/v1/registry/sync', undefined, { slow: true }),
  deviceLookup: (userCode) => gateway('POST', '/api/v1/auth/device/lookup', { user_code: userCode }),
  deviceApprove: (userCode) => gateway('POST', '/api/v1/auth/device/approve', { user_code: userCode }),
  deviceDeny: (userCode) => gateway('POST', '/api/v1/auth/device/deny', { user_code: userCode }),
  version: () => gateway('GET', '/api/v1/version'),
}

export const session = {
  async login(identifier, password, totp) {
    const response = await fetch('/session', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({ identifier, password, totp }),
    })
    const payload = await response.json().catch(() => ({}))
    if (!response.ok) throw new ApiError(response.status, payload)
    return payload
  },

  async logout() {
    await fetch('/session', { method: 'DELETE', credentials: 'same-origin' })
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
   */
  async active() {
    try {
      await api.projects()
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

/** Escapes text for insertion into HTML. */
export function escape(value) {
  return String(value ?? '').replace(
    /[&<>"']/g,
    (character) =>
      ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character],
  )
}

/**
 * Maps a health or sync status onto a DuVay colour suffix. The class grammar
 * is single-dash (`w-chip-success`), not the BEM double-dash this UI
 * originally guessed — a guess that silently no-opped, so every status chip
 * rendered neutral and the health colour coding never existed on screen.
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

/** Shortens a commit for display without losing its identity. */
export function shortRevision(revision) {
  return revision ? String(revision).slice(0, 12) : '—'
}

/** Renders an error into a page region, preferring the actionable part. */
export function showError(element, error) {
  const detail =
    error instanceof ApiError && error.reasonCode
      ? `${error.message} (${error.reasonCode})`
      : error.message
  element.innerHTML = `<div class="w-alert w-alert--color-error" role="alert">${escape(detail)}</div>`
}
