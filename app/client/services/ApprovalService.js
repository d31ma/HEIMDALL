import { api, session } from '/shared/scripts/api.js'

@Service
export default class ApprovalService {
  /** @private @returns {string} */
  static get _emptyText() {
    return ''
  }
  /** @private @returns {string} */
  static get _formView() {
    return 'form'
  }
  /** @private @returns {string} */
  static get _confirmView() {
    return 'confirm'
  }
  /** @private @returns {string} */
  static get _doneView() {
    return 'done'
  }
  /** @private @returns {string} */
  static get _defaultClient() {
    return 'heimdall-cli'
  }

  /** @type {{render: () => Promise<void>}} */
  @Needs
  taskScheduler;

  constructor() {
    this.view = ApprovalService._formView
    this.loading = true
    this.busy = true
    this.code = ApprovalService._emptyText
    this.clientName = ApprovalService._defaultClient
    this.outcome = ApprovalService._emptyText
    this.pageError = ApprovalService._emptyText
    this.#applyView()
  }

  /** @returns {Promise<void>} */
  @OnStart
  async mount() {
    document.title = 'Approve a device — HEIMDALL'
    void this.#start()
  }

  /** @param {Event} event @returns {Promise<void>} */
  async typeCode(event) {
    this.code = this.#eventValue(event)
  }

  /** @param {SubmitEvent} event @returns {Promise<void>} */
  async lookUp(event) {
    event?.preventDefault?.()
    const typed = /** @type {HTMLInputElement} */ (document.getElementById('hd-code'))?.value
    this.code = this.#typedCode(typed)
    await this.#lookUpCode()
  }

  /** @returns {Promise<void>} */
  async approve() {
    await this.#settle(() => api.deviceApprove(this.code),
      'The device is signed in. You can close this page.')
  }

  /** @returns {Promise<void>} */
  async deny() {
    await this.#settle(() => api.deviceDeny(this.code),
      'Denied. The terminal has been told to stop waiting.')
  }

  /** @returns {void} */
  #applyView() {
    this.onForm = this.view === ApprovalService._formView
    this.onConfirm = this.view === ApprovalService._confirmView
    this.onDone = this.view === ApprovalService._doneView
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
      window.location.href = '/'
      return
    }
    this.loading = false
    this.busy = false
    await this.#renderDisplay()
  }

  /** @param {Event} event @returns {string} */
  #eventValue(event) {
    return /** @type {HTMLInputElement} */ (event.target)?.value || ApprovalService._emptyText
  }

  /** @param {string} typed @returns {string} */
  #typedCode(typed) {
    return (typed || this.code).trim()
  }

  /** @returns {Promise<void>} */
  async #lookUpCode() {
    if (!this.code) return
    await this.#readCode()
  }

  /** @returns {Promise<void>} */
  async #readCode() {
    this.busy = true
    this.pageError = ApprovalService._emptyText
    await this.#renderDisplay()
    await api.deviceLookup(this.code)
      .then((pending) => this.#showClient(pending))
      .catch((error) => this.#showError(error))
      .finally(() => this.#finishAction())
  }

  /** @param {{client_name?: string, client_id?: string}} pending @returns {void} */
  #showClient(pending) {
    const fallback = this.#clientDefault(pending.client_id)
    this.clientName = this.#clientIdentity(pending.client_name, fallback)
    this.view = ApprovalService._confirmView
  }

  /** @param {string} name @param {string} id @returns {string} */
  #clientIdentity(name, id) {
    return name || id
  }

  /** @param {string} id @returns {string} */
  #clientDefault(id) {
    return id || ApprovalService._defaultClient
  }

  /** @param {() => Promise<unknown>} decide @param {string} outcome @returns {Promise<void>} */
  async #settle(decide, outcome) {
    this.busy = true
    this.pageError = ApprovalService._emptyText
    await this.#renderDisplay()
    await decide()
      .then(() => this.#showOutcome(outcome))
      .catch((error) => this.#showError(error))
      .finally(() => this.#finishAction())
  }

  /** @param {string} outcome @returns {void} */
  #showOutcome(outcome) {
    this.outcome = outcome
    this.view = ApprovalService._doneView
  }

  /** @param {Error & {reasonCode?: string}} error @returns {void} */
  #showError(error) {
    const message = this.#errorMessage(error)
    this.pageError = this.#reasonMessage(message, error?.reasonCode)
  }

  /** @param {Error} error @returns {string} */
  #errorMessage(error) {
    return error?.message || String(error)
  }

  /** @param {string} message @param {string} reason @returns {string} */
  #reasonMessage(message, reason) {
    return reason ? `${message} (${reason})` : message
  }

  /** @returns {Promise<void>} */
  async #finishAction() {
    this.busy = false
    this.#applyView()
    await this.#renderDisplay()
  }
}
