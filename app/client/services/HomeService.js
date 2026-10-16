/** @typedef {import('/models/Application.js').Application} Application */
/** @typedef {import('/models/HomeApplicationRow.js').HomeApplicationRow} HomeApplicationRow */
/** @typedef {import('/models/HomeProjectRow.js').HomeProjectRow} HomeProjectRow */
/** @typedef {import('/models/Project.js').Project} Project */

import { api, session } from '/shared/scripts/api.js'

@Service
export default class HomeService {
  /** @private @returns {string} */
  static get _emptyText() {
    return ''
  }
  /** @private @returns {string} */
  static get _loadingView() {
    return 'loading'
  }
  /** @private @returns {string} */
  static get _loginView() {
    return 'login'
  }
  /** @private @returns {string} */
  static get _listView() {
    return 'list'
  }
  /** @private @returns {string} */
  static get _emptyView() {
    return 'empty'
  }

  /** @type {{render: () => Promise<void>}} */
  @Needs
  taskScheduler;

  constructor() {
    this.view = HomeService._loadingView
    this.busy = true
    this.signedIn = false
    this.signingIn = false
    this.identifier = HomeService._emptyText
    this.loginError = HomeService._emptyText
    this.pageError = HomeService._emptyText
    /** @type {HomeProjectRow[]} */
    this.projects = []
    this.term = HomeService._emptyText
    /** @type {HomeProjectRow[]} */
    this.visibleProjects = []
  }

  /** @returns {Promise<void>} */
  @OnStart
  async mount() {
    document.title = 'HEIMDALL'
    void this.#refresh()
  }

  /** @param {SubmitEvent} event @returns {Promise<void>} */
  async signIn(event) {
    event.preventDefault()
    this.identifier = this.#inputValue('hd-user').trim()
    const password = this.#inputValue('hd-password')
    const verificationCode = this.#inputValue('hd-totp').trim()
    this.signingIn = true
    this.loginError = HomeService._emptyText
    await this.#renderDisplay()
    await this.#login(password, verificationCode)
  }

  /** @returns {Promise<void>} */
  async signOut() {
    await session.logout()
    this.projects = []
    await this.#refresh()
  }

  /** @param {Event} event @returns {Promise<void>} */
  async filter(event) {
    this.term = this.#eventValue(event)
    this.#applyFilter()
    await this.#renderDisplay()
  }

  /** @returns {Promise<void>} */
  async #renderDisplay() {
    await this.taskScheduler.render()
  }

  /** @returns {Promise<void>} */
  async #refresh() {
    this.busy = true
    this.pageError = HomeService._emptyText
    await session.active()
      .then((active) => this.#showSession(active))
      .catch((error) => { this.pageError = error.message })
      .finally(() => this.#finishRefresh())
  }

  /** @param {boolean} active @returns {Promise<void>} */
  async #showSession(active) {
    if (!active) {
      this.signedIn = false
      this.view = HomeService._loginView
      return
    }
    await this.#showProjects()
  }

  /** @returns {Promise<void>} */
  async #showProjects() {
    await this.#loadProjects()
    this.signedIn = true
    this.#applyFilter()
    this.view = this.#projectView(this.projects.length)
  }

  /** @param {number} count @returns {string} */
  #projectView(count) {
    return count ? HomeService._listView : HomeService._emptyView
  }

  /** @returns {Promise<void>} */
  async #finishRefresh() {
    this.busy = false
    await this.#renderDisplay()
  }

  /** @returns {Promise<void>} */
  async #loadProjects() {
    const { projects } = await api.projects()
    this.projects = await Promise.all(projects.map((project) => this.#projectRow(project)))
  }

  /** @param {Project} project @returns {Promise<HomeProjectRow>} */
  async #projectRow(project) {
    const { applications } = await api.apps(project.name)
    return {
      name: project.name,
      empty: applications.length === 0,
      applications: applications.map((application) => this.#applicationRow(project.name, application)),
    }
  }

  /** @param {string} project @param {Application} application @returns {HomeApplicationRow} */
  #applicationRow(project, application) {
    return {
      name: application.name,
      path: this.#displayPath(application.path),
      ref: this.#displayRef(application.ref),
      suspended: Boolean(application.suspended),
      href: `/apps/${encodeURIComponent(project)}/${encodeURIComponent(application.name)}`,
    }
  }

  /** @param {string} path @returns {string} */
  #displayPath(path) {
    return path || '—'
  }

  /** @param {string} ref @returns {string} */
  #displayRef(ref) {
    return ref || 'default'
  }

  /** @param {string} password @param {string} verificationCode @returns {Promise<void>} */
  async #login(password, verificationCode) {
    try {
      await session.login(this.identifier, password, verificationCode)
      await this.#refresh()
    } catch (error) {
      this.loginError = error.message
      await this.#renderDisplay()
    } finally {
      this.signingIn = false
    }
  }

  /** @param {string} id @returns {string} */
  #inputValue(id) {
    return /** @type {HTMLInputElement} */ (document.getElementById(id))?.value || HomeService._emptyText
  }

  /** @param {Event} event @returns {string} */
  #eventValue(event) {
    return /** @type {HTMLInputElement} */ (event?.target)?.value || HomeService._emptyText
  }

  /** @returns {void} */
  #applyFilter() {
    const term = this.term.trim().toLowerCase()
    this.visibleProjects = this.projects
      .map((project) => this.#filteredProject(project, term))
      .filter((project) => this.#projectVisible(project, term))
  }

  /** @param {HomeProjectRow} project @param {string} term @returns {HomeProjectRow} */
  #filteredProject(project, term) {
    const applications = this.#filteredApplications(project.applications, term)
    return { ...project, applications, empty: this.#emptyProject(project.empty, term) }
  }

  /** @param {HomeApplicationRow[]} applications @param {string} term @returns {HomeApplicationRow[]} */
  #filteredApplications(applications, term) {
    if (term === HomeService._emptyText) return applications
    return applications.filter((application) => this.#applicationMatches(application, term))
  }

  /** @param {HomeApplicationRow} application @param {string} term @returns {boolean} */
  #applicationMatches(application, term) {
    return `${application.name} ${application.path} ${application.ref}`.toLowerCase().includes(term)
  }

  /** @param {boolean} empty @param {string} term @returns {boolean} */
  #emptyProject(empty, term) {
    return empty && term === HomeService._emptyText
  }

  /** @param {HomeProjectRow} project @param {string} term @returns {boolean} */
  #projectVisible(project, term) {
    return term === HomeService._emptyText || project.applications.length > 0
  }
}
