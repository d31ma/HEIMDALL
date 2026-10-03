import { applyChrome } from '/shared/scripts/theme.js'

// The landing page companion: the shared chrome — theme, language, favicon,
// and the header's theme toggle — plus this page's title.
export default class LandingPage {
  static __tachyonOnMount = ['mount']

  mount() {
    applyChrome({ title: 'HEIMDALL — GitOps for Docker Compose' })
  }
}
