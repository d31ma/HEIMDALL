import { ThemePreferenceUtils } from '../shared/utils/ThemePreferenceUtils.js'

@Service
export default class ThemeService {
  /** @type {string} */
  #next = ThemeService._dark
  /** @type {AbortSignal} */
  lifecycle
  /** @type {{render: () => void}} */
  taskScheduler

  /** @returns {void} */
  @OnStart
  initialize() {
    this.#apply(ThemePreferenceUtils.read())
    this.#nameDocument()
    this.#addIcon()
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => this.#refresh(), { signal: this.lifecycle })
  }

  /** @returns {void} */
  #refresh() {
    this.#apply(ThemePreferenceUtils.read())
    this.taskScheduler.render()
  }

  /** @returns {void} */
  toggle() {
    ThemePreferenceUtils.write(this.#next)
    this.#apply(this.#next)
  }

  /** @returns {string} */
  get label() { return `Switch to ${this.#next} theme` }

  /** @returns {boolean} */
  get showDark() { return this.#next === ThemeService._dark }

  /** @returns {boolean} */
  get showLight() { return this.#next === ThemeService._light }

  /** @param {string|null} preference @returns {void} */
  #apply(preference) {
    document.documentElement.setAttribute('w-theme', this.#attribute(preference))
    this.#next = this.#opposite(ThemePreferenceUtils.effective(preference))
  }

  /** @param {string|null} preference @returns {string} */
  #attribute(preference) { return preference || 'auto' }

  /** @param {string} theme @returns {string} */
  #opposite(theme) { return theme === ThemeService._dark ? ThemeService._light : ThemeService._dark }

  /** @returns {void} */
  #nameDocument() {
    document.documentElement.lang = 'en'
    document.title = this.#landingTitle()
  }

  /** @private @returns {string} */
  static get _dark() { return 'dark' }

  /** @private @returns {string} */
  static get _light() { return 'light' }

  /** @returns {string} */
  #landingTitle() { return location.pathname === '/' ? 'HEIMDALL — GitOps for Docker Compose' : '' }

  /** @returns {void} */
  #addIcon() {
    const icon = document.createElement('link')
    icon.rel = 'icon'
    icon.type = 'image/svg+xml'
    icon.href = '/shared/assets/heimdall-mark.svg'
    document.head.appendChild(icon)
  }
}
