// The shared documentation page companion. Every docs page exports a
// subclass; the runtime reads the inherited __tachyonOnMount and this class
// does what every docs page needs: the DuVay theme attribute, the document
// title and favicon (Tac owns the head, so the page sets them), entity
// decoding for code, current-page marking, and previous/next pagination
// derived from the sidebar's own order.
export class DocsPage {
  static __tachyonOnMount = ['mount']

  mount() {
    const html = document.documentElement
    if (!html.hasAttribute('w-theme')) html.setAttribute('w-theme', 'auto')
    if (!html.lang) html.lang = 'en'
    if (!document.querySelector('link[rel="icon"]')) {
      const icon = document.createElement('link')
      icon.rel = 'icon'
      icon.type = 'image/svg+xml'
      icon.href = '/shared/assets/heimdall-mark.svg'
      document.head.appendChild(icon)
    }

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
      const article = document.querySelector('article.docs-article')
      if (!article) return false

      document.querySelectorAll('.site-code, .docs-article code').forEach((block) => {
        if (block.dataset.decoded) return
        block.dataset.decoded = '1'
        block.textContent = decodeEntities(block.textContent)
      })

      const heading = article.querySelector('h1')
      if (heading && !document.title) {
        document.title = heading.textContent.trim() + ' — HEIMDALL Docs'
      }

      const links = [...document.querySelectorAll('nav[aria-label="documentation"] a')]
      const here = location.pathname.replace(/\/$/, '') || '/docs'
      const index = links.findIndex((link) => link.getAttribute('href') === here)
      if (index >= 0) {
        links[index].setAttribute('aria-current', 'page')
        links[index].classList.add('font-bold')
      }

      // Previous/next, from the sidebar order — one source of truth.
      if (index >= 0 && !article.dataset.paginated) {
        article.dataset.paginated = '1'
        const nav = document.createElement('nav')
        nav.setAttribute('aria-label', 'pagination')
        nav.className = 'flex justify-between gap-4 mt-12 pt-5 border-t border-[var(--w-border,#e3e3e3)] text-[0.93rem]'
        const previous = links[index - 1]
        const next = links[index + 1]
        nav.innerHTML =
          (previous ? `<a class="site-link no-underline hover:underline" href="${previous.getAttribute('href')}">← ${previous.textContent}</a>` : '<span></span>') +
          (next ? `<a class="site-link no-underline hover:underline text-right" href="${next.getAttribute('href')}">${next.textContent} →</a>` : '<span></span>')
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
