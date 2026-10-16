export class ThemePreferenceUtils {
  /** @returns {string|null} */
  static read() { return ThemePreferenceUtils.#stored() }

  /** @param {string} theme @returns {void} */
  static write(theme) { ThemePreferenceUtils.#save(theme) }

  /** @param {string|null} preference @returns {string} */
  static effective(preference) { return ThemePreferenceUtils.#chosen(preference, ThemePreferenceUtils.#system()) }

  /** @returns {string} */
  static #dark() { return 'dark' }

  /** @returns {string} */
  static #light() { return 'light' }

  /** @returns {string} */
  static #key() { return 'hd-site-theme' }

  /** @returns {string|null} */
  static #stored() {
    try { return localStorage.getItem(ThemePreferenceUtils.#key()) }
    catch { return null }
  }

  /** @param {string} theme @returns {void} */
  static #save(theme) {
    try { localStorage.setItem(ThemePreferenceUtils.#key(), theme) }
    catch { return }
  }

  /** @param {string|null} preference @param {string} system @returns {string} */
  static #chosen(preference, system) {
    if (preference === ThemePreferenceUtils.#light() || preference === ThemePreferenceUtils.#dark()) return preference
    return system
  }

  /** @returns {string} */
  static #system() {
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? ThemePreferenceUtils.#dark() : ThemePreferenceUtils.#light()
  }
}
