import { PaginationUtils } from './PaginationUtils.js'

export class DocumentNavigationUtils {
  /** @returns {void} */
  static mark() {
    const links = DocumentNavigationUtils.#links()
    const index = DocumentNavigationUtils.#index(links)
    DocumentNavigationUtils.#clearCurrent(links)
    DocumentNavigationUtils.#mark(links[index])
  }

  /** @param {HTMLElement|null} article @returns {void} */
  static paginate(article) {
    const links = DocumentNavigationUtils.#links()
    const index = DocumentNavigationUtils.#index(links)
    const navigation = DocumentNavigationUtils.#navigation(links, index)
    DocumentNavigationUtils.#append(article, navigation, links[index])
  }

  /** @returns {HTMLAnchorElement[]} */
  static #links() {
    return /** @type {HTMLAnchorElement[]} */ ([...document.querySelectorAll('nav[aria-label="documentation"] a')])
  }

  /** @param {HTMLAnchorElement[]} links @returns {number} */
  static #index(links) {
    return links.findIndex((link) => link.getAttribute('href') === location.pathname.replace(/\/$/, ''))
  }

  /** @returns {string} */
  static #currentAttribute() { return 'aria-current' }

  /** @returns {string} */
  static #currentClass() { return 'font-bold' }

  /** @param {HTMLAnchorElement[]} links @returns {void} */
  static #clearCurrent(links) {
    for (const link of links) {
      link.removeAttribute(DocumentNavigationUtils.#currentAttribute())
      link.classList.remove(DocumentNavigationUtils.#currentClass())
    }
  }

  /** @param {HTMLAnchorElement|undefined} link @returns {void} */
  static #mark(link) {
    if (!link) return
    link.setAttribute(DocumentNavigationUtils.#currentAttribute(), 'page')
    link.classList.add(DocumentNavigationUtils.#currentClass())
  }

  /** @param {HTMLAnchorElement[]} links @param {number} index @returns {HTMLElement} */
  static #navigation(links, index) {
    const navigation = document.createElement('nav')
    navigation.setAttribute('aria-label', 'pagination')
    navigation.className = 'flex justify-between gap-4 mt-12 pt-5 border-t border-[var(--w-border,#e3e3e3)] text-[0.93rem]'
    navigation.append(PaginationUtils.step(links[index - 1], 'previous'), PaginationUtils.step(links[index + 1], 'next'))
    return navigation
  }

  /** @param {HTMLElement|null} article @param {HTMLElement} navigation @param {HTMLAnchorElement|undefined} current @returns {void} */
  static #append(article, navigation, current) {
    if (!article || article.dataset.paginated || !current) return
    article.dataset.paginated = '1'
    article.appendChild(navigation)
  }
}
