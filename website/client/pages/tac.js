// The landing page companion: the theme attribute, the tab's identity, and
// the favicon — Tac owns the head, so the page sets them.
export default class LandingPage {
  static __tachyonOnMount = ['mount']

  mount() {
    const html = document.documentElement
    if (!html.hasAttribute('w-theme')) html.setAttribute('w-theme', 'auto')
    if (!html.lang) html.lang = 'en'
    if (!document.title) document.title = 'HEIMDALL — GitOps for Docker Compose'
    if (!document.querySelector('link[rel="icon"]')) {
      const icon = document.createElement('link')
      icon.rel = 'icon'
      icon.type = 'image/svg+xml'
      icon.href = '/shared/assets/heimdall-mark.svg'
      document.head.appendChild(icon)
    }
  }
}
