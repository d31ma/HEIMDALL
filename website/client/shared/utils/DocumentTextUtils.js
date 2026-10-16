import { TextUtils } from './TextUtils.js'

export class DocumentTextUtils {
  /** @returns {void} */
  static decodeBlocks() { DocumentTextUtils.#decode() }

  /** @param {HTMLElement|null} article @returns {void} */
  static title(article) {
    const title = DocumentTextUtils.#title(article?.querySelector('h1'))
    DocumentTextUtils.#assignTitle(title)
  }

  /** @returns {void} */
  static #decode() {
    for (const block of document.querySelectorAll('.site-code:not([data-decoded]), .docs-article code:not([data-decoded])')) {
      block.setAttribute('data-decoded', '1')
      block.textContent = TextUtils.decode(block.textContent)
    }
  }

  /** @param {HTMLElement|null|undefined} heading @returns {string} */
  static #title(heading) {
    if (!heading) return document.title
    return heading.textContent.trim() + ' — HEIMDALL Docs'
  }

  /** @param {string} title @returns {void} */
  static #assignTitle(title) {
    if (document.title === title) return
    document.title = title
  }
}
