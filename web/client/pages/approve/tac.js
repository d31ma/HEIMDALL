import { api, session } from '/shared/scripts/api.js'

document.title = 'Approve a device — HEIMDALL'

/**
 * @file The device-approval page.
 *
 * The moment of this page: a person attaches their identity to a terminal
 * they are (hopefully) looking at. The code was read off that terminal's
 * screen; nothing here can mint one, and approving proves this browser's
 * session to SESAME rather than naming anyone.
 *
 * All three states live in tac.html. The companion holds which one is
 * showing and the values in it — no markup is built here, so what an
 * operator sees is what the template says.
 */
export default class ApprovePage {
  static __tachyonOnMount = ['mount']

  constructor() {
    /** @type {'form'|'confirm'|'done'} */
    this.view = 'form'
    /** @type {boolean} True until the session check and first render settle. */
    this.loading = true
    /** @type {boolean} Disables the buttons while a call is in flight. */
    this.busy = true
    /** @type {string} The device code, as typed. */
    this.code = ''
    /** @type {string} Who is asking — the client the control plane named. */
    this.clientName = 'heimdall-cli'
    /** @type {string} What happened, once it has. */
    this.outcome = ''
    /** @type {string} The last refusal, shown in the alert region. */
    this.pageError = ''
    this.applyView()
  }

  /**
   * Derives the template's branches. Tac renders fields, not expressions, so
   * "which view" becomes three booleans here.
   *
   * @returns {void}
   */
  applyView() {
    /** @type {boolean} */
    this.onForm = this.view === 'form'
    /** @type {boolean} */
    this.onConfirm = this.view === 'confirm'
    /** @type {boolean} */
    this.onDone = this.view === 'done'
  }

  /** @returns {Promise<void>} */
  async rerender() {
    await globalThis.__tc_rerender?.()
  }

  /**
   * The runtime awaits onMount before it renders, so this must not block on
   * elements the render creates.
   *
   * @returns {Promise<void>}
   */
  async mount() {
    void this.start()
  }

  /**
   * Refuses to show anything until the browser proves it has a session:
   * approval is that session vouching for a terminal.
   *
   * @returns {Promise<void>}
   */
  async start() {
    if (!(await session.active())) {
      window.location.href = '/'
      return
    }
    this.loading = false
    this.busy = false
    await this.rerender()
  }

  /**
   * @param {Event} event
   * @returns {Promise<void>}
   */
  async typeCode(event) {
    this.code = /** @type {HTMLInputElement} */ (event.target)?.value || ''
  }

  /**
   * Looks the code up and moves to the confirmation, which names the client
   * so the operator approves something specific rather than a code.
   *
   * @param {SubmitEvent} event
   * @returns {Promise<void>}
   */
  async lookUp(event) {
    event?.preventDefault?.()
    // The field is read from the DOM as well as from the binding: a
    // re-render rebuilds the input, and a value typed before the last render
    // would otherwise be lost.
    const typed = /** @type {HTMLInputElement} */ (document.getElementById('hd-code'))?.value
    this.code = (typed || this.code).trim()
    if (!this.code) return

    this.busy = true
    this.pageError = ''
    await this.rerender()
    try {
      const pending = await api.deviceLookup(this.code)
      this.clientName = pending.client_name || pending.client_id || 'heimdall-cli'
      this.view = 'confirm'
    } catch (error) {
      this.pageError = this.describe(error)
    } finally {
      this.busy = false
      this.applyView()
      await this.rerender()
    }
  }

  /** @returns {Promise<void>} */
  async approve() {
    await this.settle(() => api.deviceApprove(this.code),
      'The device is signed in. You can close this page.')
  }

  /** @returns {Promise<void>} */
  async deny() {
    await this.settle(() => api.deviceDeny(this.code),
      'Denied. The terminal has been told to stop waiting.')
  }

  /**
   * Runs the decision and shows its outcome. Both answers end the page: there
   * is nothing further to do here either way.
   *
   * @param {() => Promise<unknown>} decide
   * @param {string} outcome
   * @returns {Promise<void>}
   */
  async settle(decide, outcome) {
    this.busy = true
    this.pageError = ''
    await this.rerender()
    try {
      await decide()
      this.outcome = outcome
      this.view = 'done'
    } catch (error) {
      this.pageError = this.describe(error)
    } finally {
      this.busy = false
      this.applyView()
      await this.rerender()
    }
  }

  /**
   * Prefers SESAME's own reason for a denial: it distinguishes "no grant"
   * from "assurance too low", and guessing between them would be inventing
   * authorization logic in a browser.
   *
   * @param {Error & {reasonCode?: string}} error
   * @returns {string}
   */
  describe(error) {
    return error?.reasonCode ? `${error.message} (${error.reasonCode})` : error?.message || String(error)
  }
}
