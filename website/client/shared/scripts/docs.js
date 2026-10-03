import { applyChrome } from '/shared/scripts/theme.js'

/**
 * @file The shared documentation page companion. Every docs page exports a
 * subclass; the runtime reads the inherited `__tachyonOnMount` and this class
 * does what every docs page needs: the shared chrome (theme, language,
 * favicon, the header's theme toggle), entity decoding for code, current-page
 * marking, and previous/next pagination derived from the sidebar's own order.
 */
export class DocsPage {
  static __tachyonOnMount = ['mount']

  /**
   * Runs once, before the first render. The article may not exist yet — the
   * SPA renderer builds it — so the work retries behind a MutationObserver.
   *
   * @returns {void}
   */
  mount() {
    applyChrome()

    // The SPA renderer inserts text without decoding character entities,
    // so every entity an author needs — braces for the compiler, angle
    // brackets for HTML — is decoded here. &amp; goes last or it would
    // manufacture new entities.
    const decodeEntities = (text) => text
      .replaceAll('&#123;', '{')
      .replaceAll('&#125;', '}')
      .replaceAll('&lt;', '<')
      .replaceAll('&gt;', '>')
      .replaceAll('&amp;', '&')

    const fix = () => {
      const article = /** @type {HTMLElement} */ (document.querySelector('article.docs-article'))
      if (!article) return false

      document.querySelectorAll('.site-code, .docs-article code').forEach((/** @type {HTMLElement} */ block) => {
        if (block.dataset.decoded) return
        block.dataset.decoded = '1'
        block.textContent = decodeEntities(block.textContent)
      })

      const heading = article.querySelector('h1')
      if (heading && !document.title) {
        document.title = heading.textContent.trim() + ' — HEIMDALL Docs'
      }

      const links = /** @type {HTMLAnchorElement[]} */ (
        [...document.querySelectorAll('nav[aria-label="documentation"] a')])
      const here = location.pathname.replace(/\/$/, '') || '/docs'
      const index = links.findIndex((link) => link.getAttribute('href') === here)
      if (index >= 0) {
        links[index].setAttribute('aria-current', 'page')
        links[index].classList.add('font-bold')
      }

      // Previous/next, from the sidebar order — one source of truth. Built
      // with DOM calls rather than a string of markup: the link text comes
      // from the sidebar, and `textContent` cannot be talked into being an
      // element the way an interpolated string can.
      if (index >= 0 && !article.dataset.paginated) {
        article.dataset.paginated = '1'
        const nav = document.createElement('nav')
        nav.setAttribute('aria-label', 'pagination')
        nav.className = 'flex justify-between gap-4 mt-12 pt-5 border-t border-[var(--w-border,#e3e3e3)] text-[0.93rem]'

        /**
         * @param {HTMLAnchorElement|undefined} link
         * @param {'previous'|'next'} direction
         * @returns {HTMLElement} The link, or an empty span holding the
         *   column so the other end stays where it belongs.
         */
        const step = (link, direction) => {
          if (!link) return document.createElement('span')
          const anchor = document.createElement('a')
          anchor.className = direction === 'next'
            ? 'site-link site-nav-link text-right'
            : 'site-link site-nav-link'
          anchor.href = link.getAttribute('href') || '#'
          anchor.textContent = direction === 'next'
            ? `${link.textContent} →`
            : `← ${link.textContent}`
          return anchor
        }

        nav.append(step(links[index - 1], 'previous'), step(links[index + 1], 'next'))
        article.appendChild(nav)
      }
      return true
    }

    if (fix()) return
    const observer = new MutationObserver(() => {
      if (fix()) observer.disconnect()
    })
    observer.observe(document.documentElement, { childList: true, subtree: true })
  }
}
