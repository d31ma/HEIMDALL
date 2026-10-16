import { DocumentTextUtils } from '../shared/utils/DocumentTextUtils.js'
import { DocumentNavigationUtils } from '../shared/utils/DocumentNavigationUtils.js'

@Service
export default class DocumentsService {
  /** @type {boolean} */
  ready = true
  /** @type {MutationObserver|null} */
  #observer = null

  /** @returns {void} */
  @OnStart
  initialize() {
    this.#observer = new MutationObserver(() => this.#refresh())
    this.#observer.observe(document.documentElement, { childList: true, subtree: true })
    this.#refresh()
  }

  /** @returns {void} */
  #refresh() {
    const article = /** @type {HTMLElement|null} */ (document.querySelector('article.docs-article'))
    DocumentTextUtils.decodeBlocks()
    DocumentTextUtils.title(article)
    DocumentNavigationUtils.mark()
    DocumentNavigationUtils.paginate(article)
  }

  /** @returns {void} */
  @OnStop
  dispose() { this.#observer?.disconnect() }

}
