import { api, session, tone } from '/shared/scripts/api.js'

document.title = 'Registry — HEIMDALL'

function when(stamp) {
  return (stamp || '').replace('T', ' ').slice(0, 19)
}

// The page companion for ADR 0010's interactive surface. Declarative like
// the other pages: fields in, template out, methods on events.
export default class RegistryPage {
  static __tachyonOnMount = ['mount']

  constructor() {
    /** @type {boolean} */
    this.busy = true
    this.view = 'loading'
    this.bound = false
    this.binding = null
    this.syncs = []
    this.noSyncs = true
    this.working = false
    this.unbindArmed = false
    this.pageError = ''
  }

  /**
   * @returns {Promise<void>}
   */
  async rerender() {
    await globalThis.__tc_rerender?.()
  }

  /**
   * @returns {Promise<void>}
   */
  async mount() {
    // The runtime awaits onMount before the first render; kick off without
    // blocking on it.
    void this.start()
  }

  /**
   * @returns {Promise<void>}
   */
  async start() {
    if (!(await session.active())) {
      window.location.href = '/'
      return
    }
    await this.refresh()
  }

  /**
   * Reads the binding and its recent syncs.
   *
   * @returns {Promise<void>}
   */
  async refresh() {
    this.busy = true
    this.pageError = ''
    try {
      const answer = await api.registry()
      this.bound = !!answer.bound
      this.view = this.bound ? 'bound' : 'unbound'
      if (this.bound) {
        const binding = answer.binding || {}
        this.binding = {
          url: binding.url || '',
          refLabel: binding.ref || 'default branch',
          pathLabel: binding.path || '(repository root)',
          pruneLabel: binding.prune ? 'deletions allowed' : 'deletions withheld',
          signedLabel: binding.require_signature ? 'signed commits required' : 'not required',
          boundLabel: `${when(binding.bound_at)}${binding.bound_by ? ' by ' + binding.bound_by : ''}`,
        }
        this.syncs = (answer.syncs || []).map((operation) => {
          const phase = operation.phase === 'succeeded' ? 'Healthy'
            : operation.phase === 'failed' ? 'Degraded' : 'Progressing'
          return {
            when: when(operation.started_at),
            phase: operation.phase,
            phaseChipClass: `w-chip w-chip-filled w-chip-${tone(phase)}`,
            commit: (operation.revision || '').slice(0, 12),
            message: operation.message || '',
            changes: (operation.operations || [])
              .map((change) => `${change.kind} ${change.service}`).join(', ') || '—',
          }
        })
        this.noSyncs = this.syncs.length === 0
      }
    } catch (error) {
      // A 403 here is an operator without registry:read — say so rather
      // than rendering an empty page that looks broken.
      this.pageError = error.message
      this.view = 'unbound'
    } finally {
      this.busy = false
      await this.rerender()
    }
  }

  /**
   * Binds the root repository — ADR 0010's one interactive act.
   *
   * @param {SubmitEvent} event
   * @returns {Promise<void>}
   */
  async bind(event) {
    event.preventDefault()
    // Read the form before anything re-renders: a re-render rebuilds the
    // inputs from the template, which empties them.
    const binding = {
      url: /** @type {HTMLInputElement} */ (document.getElementById('hd-root-url'))?.value.trim(),
      ref: /** @type {HTMLInputElement} */ (document.getElementById('hd-root-ref'))?.value.trim(),
      path: /** @type {HTMLInputElement} */ (document.getElementById('hd-root-path'))?.value.trim(),
      prune: /** @type {HTMLInputElement} */ (document.getElementById('hd-root-prune'))?.checked,
      require_signature: /** @type {HTMLInputElement} */ (document.getElementById('hd-root-signed'))?.checked,
    }
    this.working = true
    await this.rerender()
    let failure = ''
    try {
      await api.registryBind(binding)
      // The first sync follows the bind immediately: a bound registry that
      // has not converged yet is just a page full of questions.
      await api.registrySync()
    } catch (error) {
      failure = error.message
    } finally {
      this.working = false
      await this.refresh()
      // refresh clears pageError by design; a failed action's message must
      // outlive it.
      if (failure) {
        this.pageError = failure
        await this.rerender()
      }
    }
  }

  /**
   * @returns {Promise<void>}
   */
  async syncNow() {
    this.working = true
    await this.rerender()
    let failure = ''
    try {
      await api.registrySync()
    } catch (error) {
      failure = error.message
    } finally {
      this.working = false
      await this.refresh()
      if (failure) {
        this.pageError = failure
        await this.rerender()
      }
    }
  }

  /**
   * @returns {Promise<void>}
   */
  async unbindArm() {
    this.unbindArmed = true
    await this.rerender()
  }

  /**
   * @returns {Promise<void>}
   */
  async unbindCancel() {
    this.unbindArmed = false
    await this.rerender()
  }

  /**
   * Unbinds. Deliberately two clicks: the registry is what declares the fleet.
   *
   * @returns {Promise<void>}
   */
  async unbindConfirm() {
    this.unbindArmed = false
    try {
      await api.registryUnbind()
    } catch (error) {
      this.pageError = error.message
    }
    await this.refresh()
  }

  /**
   * @returns {Promise<void>}
   */
  async signOut() {
    await session.logout()
    window.location.href = '/'
  }
}
