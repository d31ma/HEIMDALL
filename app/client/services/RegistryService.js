/** @typedef {import('/models/Operation.js').Operation} Operation */
/** @typedef {import('/models/RegistryAnswer.js').RegistryAnswer} RegistryAnswer */
/** @typedef {import('/models/RegistryBinding.js').RegistryBinding} RegistryBinding */
/** @typedef {import('/models/RegistryBindingDisplay.js').RegistryBindingDisplay} RegistryBindingDisplay */
/** @typedef {import('/models/RegistrySyncRow.js').RegistrySyncRow} RegistrySyncRow */

import { api, session, tone } from '/shared/scripts/api.js'

@Service
export default class RegistryService {
  /** @private @returns {string} */
  static get _emptyText() {
    return ''
  }
  /** @private @returns {string} */
  static get _homePath() {
    return '/'
  }
  /** @private @returns {string} */
  static get _boundView() {
    return 'bound'
  }
  /** @private @returns {string} */
  static get _unboundView() {
    return 'unbound'
  }
  /** @private @returns {number} */
  static get _timestampWidth() {
    return 19
  }
  /** @private @returns {number} */
  static get _commitWidth() {
    return 12
  }
  /** @private @returns {string} */
  static get _unknownChanges() {
    return '—'
  }

  /** @type {{render: () => Promise<void>}} */
  @Needs
  taskScheduler;

  constructor() {
    this.busy = true
    this.view = 'loading'
    this.bound = false
    /** @type {RegistryBindingDisplay|null} */
    this.binding = null
    /** @type {RegistrySyncRow[]} */
    this.syncs = []
    this.noSyncs = true
    this.working = false
    this.unbindArmed = false
    this.pageError = RegistryService._emptyText
  }

  /** @returns {Promise<void>} */
  @OnStart
  async mount() {
    document.title = 'Registry — HEIMDALL'
    void this.#start()
  }

  /** @param {SubmitEvent} event @returns {Promise<void>} */
  async bind(event) {
    event.preventDefault()
    const binding = this.#formBinding()
    await this.#runAction(() => this.#bindAndSync(binding))
  }

  /** @returns {Promise<void>} */
  async syncNow() {
    await this.#runAction(() => api.registrySync())
  }

  /** @returns {Promise<void>} */
  async unbindArm() {
    this.unbindArmed = true
    await this.#renderDisplay()
  }

  /** @returns {Promise<void>} */
  async unbindCancel() {
    this.unbindArmed = false
    await this.#renderDisplay()
  }

  /** @returns {Promise<void>} */
  async unbindConfirm() {
    this.unbindArmed = false
    await this.#unbind()
    await this.#refresh()
  }

  /** @returns {Promise<void>} */
  async signOut() {
    await session.logout()
    window.location.href = RegistryService._homePath
  }

  /** @returns {Promise<void>} */
  async #renderDisplay() {
    await this.taskScheduler.render()
  }

  /** @returns {Promise<void>} */
  async #start() {
    const active = await session.active()
    await this.#showSession(active)
  }

  /** @param {boolean} active @returns {Promise<void>} */
  async #showSession(active) {
    if (!active) {
      window.location.href = RegistryService._homePath
      return
    }
    await this.#refresh()
  }

  /** @returns {Promise<void>} */
  async #refresh() {
    this.busy = true
    this.pageError = RegistryService._emptyText
    await api.registry()
      .then((answer) => this.#showRegistry(answer))
      .catch((error) => this.#readFailure(error))
      .finally(() => this.#finishRefresh())
  }

  /** @param {RegistryAnswer} answer @returns {void} */
  #showRegistry(answer) {
    this.bound = Boolean(answer.bound)
    this.view = this.#registryView(this.bound)
    this.#showBinding(answer)
  }

  /** @param {boolean} bound @returns {string} */
  #registryView(bound) {
    return bound ? RegistryService._boundView : RegistryService._unboundView
  }

  /** @param {RegistryAnswer} answer @returns {void} */
  #showBinding(answer) {
    if (!this.bound) return
    this.#readBinding(answer)
  }

  /** @param {RegistryAnswer} answer @returns {void} */
  #readBinding(answer) {
    const binding = this.#bindingValue(answer.binding)
    this.binding = this.#bindingDisplay(binding)
    const syncs = this.#syncValues(answer.syncs)
    this.syncs = syncs.map((operation) => this.#syncRow(operation))
    this.noSyncs = this.syncs.length === 0
  }

  /** @param {RegistryBinding} binding @returns {RegistryBinding} */
  #bindingValue(binding) {
    return binding || {}
  }

  /** @param {Operation[]} syncs @returns {Operation[]} */
  #syncValues(syncs) {
    return syncs || []
  }

  /** @param {RegistryBinding} binding @returns {RegistryBindingDisplay} */
  #bindingDisplay(binding) {
    const stamp = this.#timestamp(binding.bound_at)
    const owner = this.#bindingOwner(binding.bound_by)
    return {
      url: this.#textValue(binding.url),
      refLabel: this.#refLabel(binding.ref),
      pathLabel: this.#pathLabel(binding.path),
      pruneLabel: this.#pruneLabel(binding.prune),
      signedLabel: this.#signatureLabel(binding.require_signature),
      boundLabel: `${stamp}${owner}`,
    }
  }

  /** @param {string} value @returns {string} */
  #textValue(value) {
    return value || RegistryService._emptyText
  }

  /** @param {string} ref @returns {string} */
  #refLabel(ref) {
    return ref || 'default branch'
  }

  /** @param {string} path @returns {string} */
  #pathLabel(path) {
    return path || '(repository root)'
  }

  /** @param {boolean} prune @returns {string} */
  #pruneLabel(prune) {
    return prune ? 'deletions allowed' : 'deletions withheld'
  }

  /** @param {boolean} required @returns {string} */
  #signatureLabel(required) {
    return required ? 'signed commits required' : 'not required'
  }

  /** @param {string} name @returns {string} */
  #bindingOwner(name) {
    return name ? ` by ${name}` : RegistryService._emptyText
  }

  /** @param {string} stamp @returns {string} */
  #timestamp(stamp) {
    const value = this.#textValue(stamp)
    return value.replace('T', ' ').slice(0, RegistryService._timestampWidth)
  }

  /** @param {Operation} operation @returns {RegistrySyncRow} */
  #syncRow(operation) {
    const succeeded = operation.phase === 'succeeded'
    const failed = operation.phase === 'failed'
    const failureLabel = this.#failureLabel(failed)
    const phase = this.#successLabel(succeeded, failureLabel)
    const revision = this.#textValue(operation.revision)
    const changes = this.#changeValues(operation.operations)
    const summary = changes.map((change) => `${change.kind} ${change.service}`).join(', ')
    return {
      when: this.#timestamp(operation.started_at),
      phase: operation.phase,
      phaseChipClass: `w-chip w-chip-filled w-chip-${tone(phase)}`,
      commit: revision.slice(0, RegistryService._commitWidth),
      message: this.#textValue(operation.message),
      changes: this.#changesLabel(summary),
    }
  }

  /** @param {boolean} failed @returns {string} */
  #failureLabel(failed) {
    return failed ? 'Degraded' : 'Progressing'
  }

  /** @param {boolean} succeeded @param {string} fallback @returns {string} */
  #successLabel(succeeded, fallback) {
    return succeeded ? 'Healthy' : fallback
  }

  /** @param {{kind?: string, service?: string, reason?: string}[]} changes @returns {{kind?: string, service?: string, reason?: string}[]} */
  #changeValues(changes) {
    return changes || []
  }

  /** @param {string} summary @returns {string} */
  #changesLabel(summary) {
    return summary || RegistryService._unknownChanges
  }

  /** @param {Error} error @returns {void} */
  #readFailure(error) {
    this.pageError = error.message
    this.view = RegistryService._unboundView
  }

  /** @returns {Promise<void>} */
  async #finishRefresh() {
    this.busy = false
    await this.#renderDisplay()
  }

  /** @returns {RegistryBinding} */
  #formBinding() {
    return {
      url: this.#inputValue('hd-root-url'),
      ref: this.#inputValue('hd-root-ref'),
      path: this.#inputValue('hd-root-path'),
      prune: /** @type {HTMLInputElement} */ (document.getElementById('hd-root-prune'))?.checked,
      require_signature: /** @type {HTMLInputElement} */ (document.getElementById('hd-root-signed'))?.checked,
    }
  }

  /** @param {string} id @returns {string} */
  #inputValue(id) {
    return /** @type {HTMLInputElement} */ (document.getElementById(id))?.value.trim()
  }

  /** @param {RegistryBinding} binding @returns {Promise<void>} */
  async #bindAndSync(binding) {
    await api.registryBind(binding)
    await api.registrySync()
  }

  /** @param {() => Promise<unknown>} action @returns {Promise<void>} */
  async #runAction(action) {
    this.working = true
    await this.#renderDisplay()
    let failure = RegistryService._emptyText
    try {
      await action()
    } catch (error) {
      failure = error.message
    } finally {
      await this.#finishAction(failure)
    }
  }

  /** @param {string} failure @returns {Promise<void>} */
  async #finishAction(failure) {
    this.working = false
    await this.#refresh()
    await this.#showActionFailure(failure)
  }

  /** @param {string} failure @returns {Promise<void>} */
  async #showActionFailure(failure) {
    if (!failure) return
    this.pageError = failure
    await this.#renderDisplay()
  }

  /** @returns {Promise<void>} */
  async #unbind() {
    try {
      await api.registryUnbind()
    } catch (error) {
      this.pageError = error.message
    }
  }
}
