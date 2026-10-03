/**
 * @file The site's theme, in one place: two page companions need it, and the
 * header that carries the toggle is a component, which has no companion of
 * its own.
 *
 * Three states, two of which are one button press apart. The stored
 * preference wins; with none stored the site follows the operating system,
 * which is what a first visit should do. DuVay reads all of it from one
 * `w-theme` attribute on `<html>`.
 */

/** @type {string} Where the visitor's explicit choice is remembered. */
const KEY = 'hd-site-theme'

/**
 * The visitor's stored preference, or null when they have expressed none.
 *
 * @returns {string|null}
 */
function stored() {
  try {
    return localStorage.getItem(KEY)
  } catch {
    // Private browsing, or storage disabled. The theme still works for this
    // page; it just does not persist.
    return null
  }
}

/**
 * What the visitor is actually looking at, which is the question the button's
 * label has to answer.
 *
 * @returns {'light'|'dark'}
 */
function effective() {
  const preference = stored()
  if (preference === 'light' || preference === 'dark') return preference
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

/**
 * Keeps every toggle on the page telling the truth: the icon shows where a
 * press goes, and the label says it in words.
 *
 * @returns {void}
 */
function paint() {
  /** @type {'light'|'dark'} */
  const next = effective() === 'dark' ? 'light' : 'dark'
  for (const button of document.querySelectorAll('[data-theme-toggle]')) {
    // The icons are in the template; this only says which one applies.
    for (const icon of button.querySelectorAll('[data-theme-icon]')) {
      icon.toggleAttribute('hidden', icon.getAttribute('data-theme-icon') !== next)
    }
    button.setAttribute('aria-label', `Switch to ${next} theme`)
    button.setAttribute('title', `Switch to ${next} theme`)
  }
}

/**
 * Switches to the other theme and remembers the choice.
 *
 * @returns {void}
 */
export function toggleTheme() {
  const next = effective() === 'dark' ? 'light' : 'dark'
  try {
    localStorage.setItem(KEY, next)
  } catch {
    // Not persisting is survivable; not switching would not be.
  }
  document.documentElement.setAttribute('w-theme', next)
  paint()
}

/**
 * Everything Tac's head does not own for us: the theme attribute, the
 * language, the favicon, and the header's theme toggle. Both page companions
 * call it.
 *
 * @param {{title?: string}} [options] title is set only when the page has not
 *   set one already.
 * @returns {void}
 */
export function applyChrome({ title } = {}) {
  const html = document.documentElement
  html.setAttribute('w-theme', stored() || 'auto')
  if (!html.lang) html.lang = 'en'
  if (title && !document.title) document.title = title
  if (!document.querySelector('link[rel="icon"]')) {
    const icon = document.createElement('link')
    icon.rel = 'icon'
    icon.type = 'image/svg+xml'
    icon.href = '/shared/assets/heimdall-mark.svg'
    document.head.appendChild(icon)
  }

  // The toggle lives in a component, so the listener is delegated from the
  // document: it survives the header being re-rendered by a soft
  // navigation, and it is attached exactly once per page load.
  if (!document.body.dataset.themeWired) {
    document.body.dataset.themeWired = '1'
    document.addEventListener('click', (event) => {
      const target = /** @type {Element} */ (event.target)
      if (target.closest('[data-theme-toggle]')) toggleTheme()
    })
  }

  // The button is painted when it appears — the SPA renderer may not have
  // inserted the header yet on the first call.
  paint()
  if (!document.querySelector('[data-theme-toggle]')) {
    const observer = new MutationObserver(() => {
      if (document.querySelector('[data-theme-toggle]')) {
        paint()
        observer.disconnect()
      }
    })
    observer.observe(document.documentElement, { childList: true, subtree: true })
  }

  // Following the system means following it as it changes.
  window.matchMedia?.('(prefers-color-scheme: dark)').addEventListener?.('change', paint)
}
