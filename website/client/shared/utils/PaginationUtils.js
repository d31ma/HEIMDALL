export class PaginationUtils {
  /** @param {HTMLAnchorElement|undefined} link @param {string} direction @returns {HTMLElement} */
  static step(link, direction) { return PaginationUtils.#step(link, direction) }

  /** @param {HTMLAnchorElement|undefined} link @param {string} direction @returns {HTMLElement} */
  static #step(link, direction) {
    if (!link) return document.createElement('span')
    const anchor = document.createElement('a')
    anchor.className = PaginationUtils.#className(direction)
    anchor.href = PaginationUtils.#href(link)
    anchor.textContent = PaginationUtils.#label(link.textContent, direction)
    return anchor
  }

  /** @param {string} direction @returns {string} */
  static #className(direction) { return 'site-link site-nav-link ' + direction }

  /** @param {HTMLAnchorElement} link @returns {string} */
  static #href(link) { return link.href }

  /** @param {string} text @param {string} direction @returns {string} */
  static #label(text, direction) { return PaginationUtils.#labels(text)[direction] }

  /** @param {string} text @returns {{previous:string,next:string}} */
  static #labels(text) { return { previous: `← ${text}`, next: `${text} →` } }
}
