/**
 * @file Client-side paging for the tables the UI already holds in memory.
 *
 * It is deliberately not a fetch layer: it slices a list a page already has.
 * A table whose source grows without bound — operations, audit — is paged by
 * the *store*, through the activity index and its cursor. What this fixes is
 * a page that renders four hundred rows nobody scrolls to, or truncates to
 * forty and hides the rest.
 */

/** @type {symbol} Stands in for the pages a window skips. */
const ELLIPSIS = Symbol('ellipsis')

/**
 * The classic windowed pager: first, last, and a window around the current
 * page, with an ellipsis standing in for what is skipped. Short runs are
 * listed in full — an ellipsis hiding one page is a worse answer than the
 * page itself.
 *
 * @param {number} current
 * @param {number} count
 * @returns {(number|symbol)[]}
 */
export function pageNumbers(current, count) {
  if (count <= 7) return Array.from({ length: count }, (_, i) => i + 1)
  const pages = new Set([1, count, current, current - 1, current + 1])
  if (current <= 3) [2, 3, 4].forEach((page) => pages.add(page))
  if (current >= count - 2) [count - 3, count - 2, count - 1].forEach((page) => pages.add(page))

  const ordered = [...pages].filter((page) => page >= 1 && page <= count).sort((a, b) => a - b)
  const out = []
  for (const [index, page] of ordered.entries()) {
    if (index > 0 && page - ordered[index - 1] > 1) out.push(ELLIPSIS)
    out.push(page)
  }
  return out
}

/**
 * Returns everything a template branches on. Tac renders fields, not
 * expressions, so the DuVay classes and the aria-current strings are computed
 * here — ARIA wants the literal "page", not a boolean.
 *
 * @template T
 * @param {T[]} items
 * @param {number} page One-based; out-of-range clamps rather than empties.
 * @param {number} size
 * @param {{noun?: string}} [options] Names the things being counted, so the
 *   range line reads "of 24 runs" rather than "of 24".
 * @returns {{
 *   page: number, rows: T[], empty: boolean, show: boolean,
 *   onFirst: boolean, onLast: boolean, range: string,
 *   pages: {value: string, label: string, ellipsis: boolean, className: string,
 *           current: string, ariaLabel: string}[],
 * }}
 */
export function paginate(items, page, size, { noun = 'items' } = {}) {
  const total = items.length
  const count = Math.max(1, Math.ceil(total / size))
  const current = Math.min(Math.max(1, page), count)
  const start = (current - 1) * size
  const rows = items.slice(start, start + size)

  return {
    page: current,
    rows,
    empty: total === 0,
    show: count > 1,
    onFirst: current === 1,
    onLast: current === count,
    range: total === 0 ? '' : `Showing ${start + 1}–${start + rows.length} of ${total} ${noun}`,
    pages: pageNumbers(current, count).map((entry) => ({
      value: String(entry),
      label: entry === ELLIPSIS ? '…' : String(entry),
      ellipsis: entry === ELLIPSIS,
      className: entry === ELLIPSIS
        ? 'w-page-ellipsis'
        : entry === current
          ? 'w-page-item w-page-item--active-color-primary active'
          : 'w-page-item',
      current: entry === current ? 'page' : 'false',
      ariaLabel: entry === ELLIPSIS ? '' : `page ${String(entry)}`,
    })),
  }
}

/**
 * Reads the page a click landed on. The number rides on a data attribute
 * because a loop variable is not in scope in a Tac handler expression.
 *
 * @param {Event} event
 * @returns {number} The page clicked, or 0 when the click was not on one.
 */
export function pageFromEvent(event) {
  const value = /** @type {HTMLElement} */ (
    /** @type {Element} */ (event?.target)?.closest?.('[data-page]'))?.dataset?.page
  const page = Number(value)
  return Number.isFinite(page) && page >= 1 ? page : 0
}
