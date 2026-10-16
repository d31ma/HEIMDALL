/** @typedef {import('/models/ChartData.js').ChartData} ChartData */
/** @typedef {import('/models/ChartPoint.js').ChartPoint} ChartPoint */

/**
 * @file `<hd-timeseries>` — HEIMDALL's inline chart.
 *
 * HEIMDALL-owned because DuVay's w-chart is a placeholder shell with no axes.
 * It renders inline SVG from DuVay custom properties, so every theme works
 * without a second rule set. No dependency, no canvas, no animation frames:
 * an incident chart's job is to be legible and cheap, not to move.
 *
 * Set data with {@link HdTimeseries#series}. Markers are deploys: vertical
 * lines with the short revision, which is the product's headline claim drawn
 * on the chart — "what changed and when".
 */









/** @type {number} The viewBox width; the SVG scales to its panel. */
const WIDTH = 640
/** @type {number} */
const HEIGHT = 160
/** @type {{top: number, right: number, bottom: number, left: number}} */
const PAD = { top: 10, right: 10, bottom: 22, left: 46 }

/**
 * @type {number} Slack: the plot is inset from the axis ends so the first and
 * last samples are not drawn flush against the panel's edges. A line that
 * touches its frame reads as clipped — as though the series continues past
 * what you can see — and the marker labels at the edges had nowhere to sit.
 */
const INSET_X = 12

/**
 * @type {number} Vertical slack, as a fraction above the highest sample.
 * Enough that a peak has air over it rather than grazing the top gridline,
 * which is the visual difference between "busy" and "at the limit".
 */
const HEADROOM = 1.18

/**
 * Reads a DuVay custom property, falling back when a theme does not set it.
 *
 * @param {string} name
 * @param {string} fallback
 * @returns {string}
 */
function token(name, fallback) {
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  return value || fallback
}

/**
 * @param {number} domainMin
 * @param {number} domainMax
 * @param {number} rangeMin
 * @param {number} rangeMax
 * @returns {(value: number) => number}
 */
function scale(domainMin, domainMax, rangeMin, rangeMax) {
  const span = domainMax - domainMin || 1
  return (v) => rangeMin + ((v - domainMin) / span) * (rangeMax - rangeMin)
}

/**
 * Picks a display unit so the y axis reads "256 MiB" rather than "268435456".
 *
 * @param {number} value
 * @param {string} [unit]
 * @returns {string}
 */
function formatValue(value, unit) {
  if (unit === 'bytes') {
    const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
    let index = 0
    while (value >= 1024 && index < units.length - 1) {
      value /= 1024
      index += 1
    }
    return `${value < 10 ? value.toFixed(1) : Math.round(value)} ${units[index]}`
  }
  if (unit === 'percent') return `${value < 10 ? value.toFixed(1) : Math.round(value)}%`
  return String(Math.round(value))
}

/**
 * @param {Date} date
 * @param {number} spanMs Width of the window, which decides whether the label
 *   needs a date as well as a time.
 * @returns {string}
 */
function formatTime(date, spanMs) {
  const hours = String(date.getHours()).padStart(2, '0')
  const minutes = String(date.getMinutes()).padStart(2, '0')
  if (spanMs > 12 * 3600_000) {
    return `${String(date.getDate()).padStart(2, '0')}/${String(date.getMonth() + 1).padStart(2, '0')} ${hours}:${minutes}`
  }
  return `${hours}:${minutes}`
}

/**
 * Draws a smooth path through the points using monotone cubic interpolation
 * (Fritsch–Carlson).
 *
 * The interpolation is monotone on purpose. An ordinary spline overshoots
 * between samples — it invents a peak higher than anything measured, or dips
 * a value below zero — and on a metric chart that is a lie with a nice
 * shape: a CPU line that never exceeded 60% would show a 70% spike nobody
 * can find in the data. Monotone cubic curves stay inside the interval its
 * neighbouring samples define, so the line is smooth and still only ever
 * claims what was measured.
 *
 * @param {[number, number][]} points Screen coordinates, x ascending.
 * @returns {string} An SVG path.
 */
function smoothPath(points) {
  if (points.length === 0) return ''
  if (points.length < 3) {
    return points.map(([px, py], i) => `${i === 0 ? 'M' : 'L'}${px.toFixed(1)},${py.toFixed(1)}`).join(' ')
  }

  // Secant slopes between neighbours.
  const secants = []
  for (let i = 0; i < points.length - 1; i++) {
    const run = points[i + 1][0] - points[i][0]
    secants.push(run === 0 ? 0 : (points[i + 1][1] - points[i][1]) / run)
  }

  // Tangents: the average of the neighbouring secants, except where the data
  // turns — a local peak or trough gets a flat tangent, which is what keeps
  // the curve from sailing past it.
  const tangents = [secants[0]]
  for (let i = 1; i < secants.length; i++) {
    tangents.push(secants[i - 1] * secants[i] <= 0 ? 0 : (secants[i - 1] + secants[i]) / 2)
  }
  tangents.push(secants[secants.length - 1])

  // Fritsch–Carlson: pull tangents back inside the circle of monotonicity so
  // no segment can overshoot its own endpoints.
  for (let i = 0; i < secants.length; i++) {
    if (secants[i] === 0) {
      tangents[i] = 0
      tangents[i + 1] = 0
      continue
    }
    const alpha = tangents[i] / secants[i]
    const beta = tangents[i + 1] / secants[i]
    const distance = Math.hypot(alpha, beta)
    if (distance > 3) {
      tangents[i] = (3 / distance) * alpha * secants[i]
      tangents[i + 1] = (3 / distance) * beta * secants[i]
    }
  }

  let path = `M${points[0][0].toFixed(1)},${points[0][1].toFixed(1)}`
  for (let i = 0; i < points.length - 1; i++) {
    const [x0, y0] = points[i]
    const [x1, y1] = points[i + 1]
    const run = (x1 - x0) / 3
    path += ` C${(x0 + run).toFixed(1)},${(y0 + tangents[i] * run).toFixed(1)}`
      + ` ${(x1 - run).toFixed(1)},${(y1 - tangents[i + 1] * run).toFixed(1)}`
      + ` ${x1.toFixed(1)},${y1.toFixed(1)}`
  }
  return path
}

/**
 * Turns one point list into sorted, finite samples.
 *
 * @param {ChartPoint[]} [points]
 * @returns {{at: number, value: number}[]}
 */
function clean(points) {
  return (points || [])
    .map((p) => ({ at: new Date(p.at).getTime(), value: Number(p.value) }))
    .filter((p) => Number.isFinite(p.at) && Number.isFinite(p.value))
    .sort((a, b) => a.at - b.at)
}

class HdTimeseries extends HTMLElement {
  /**
   * Sets the chart's data and draws it.
   *
   * @param {ChartData} input
   * @returns {void}
   */
  series(input) {
    /** @type {ChartData} */
    this._data = input
    this.render()
  }

  /** @returns {void} */
  render() {
    const data = this._data || {}
    // One shape internally: a single-series call is a one-line chart, which
    // keeps every existing caller working unchanged.
    const lines = (data.lines || [{ label: data.label, points: data.points }])
      .map((entry) => ({ label: entry.label || '', points: clean(entry.points) }))
      .filter((entry) => entry.points.length > 0)
    const drawn = lines.filter((entry) => entry.points.length >= 2)

    if (drawn.length === 0) {
      const collecting = lines.length > 0
      this.innerHTML = `<div class="hd-chart-empty h-24 flex items-center justify-center text-[var(--w-text-muted,#666)] text-[0.85rem] border border-dashed border-[var(--w-border,#ddd)] rounded-md mb-[0.8rem]">${collecting ? 'Collecting…' : 'No data in this window'}</div>`
      return
    }

    // --w-secondary rather than --w-info: DuVay defines the first and flips
    // it with the theme, and the second is unset here — a hardcoded fallback
    // would have drawn a dark teal line on a dark surface.
    const palette = [token('--w-primary', '#3b5bdb'), token('--w-secondary', '#5f7d8c')]
    const line = palette[0]
    const grid = token('--w-border', '#ddd')
    const text = token('--w-text-muted', '#666')
    const marker = token('--w-warning', '#b26b00')

    const all = drawn.flatMap((entry) => entry.points)
    const t0 = Math.min(...all.map((p) => p.at))
    const t1 = Math.max(...all.map((p) => p.at))
    let vMax = Math.max(...all.map((p) => p.value))
    if (data.max) vMax = Math.max(vMax, data.max)
    if (vMax <= 0) vMax = 1

    const x = scale(t0, t1, PAD.left + INSET_X, WIDTH - PAD.right - INSET_X)
    const y = scale(0, vMax * HEADROOM, HEIGHT - PAD.bottom, PAD.top)

    const pathOf = (points) => smoothPath(points.map((p) => [x(p.at), y(p.value)]))
    // The filled area is a one-series affordance: two translucent fills
    // overlapping read as a third colour that means nothing.
    const paths = drawn.map((entry, index) => {
      const stroke = palette[index % palette.length]
      const path = pathOf(entry.points)
      const area = drawn.length === 1
        ? `<path d="${path} L${x(entry.points[entry.points.length - 1].at).toFixed(1)},${y(0).toFixed(1)} L${x(entry.points[0].at).toFixed(1)},${y(0).toFixed(1)} Z" fill="${stroke}" opacity="0.08"/>`
        : ''
      return `${area}<path d="${path}" fill="none" stroke="${stroke}" stroke-width="1.5"
        stroke-linecap="round" stroke-linejoin="round"/>`
    }).join('')

    // Three horizontal gridlines with labels.
    const gridlines = [0.25, 0.5, 0.75, 1].map((f) => {
      const value = vMax * f
      const yy = y(value).toFixed(1)
      return `<line x1="${PAD.left}" y1="${yy}" x2="${WIDTH - PAD.right}" y2="${yy}"
                stroke="${grid}" stroke-width="0.5" stroke-dasharray="3 4"/>
              <text x="${PAD.left - 6}" y="${yy}" text-anchor="end" dominant-baseline="middle"
                fill="${text}" font-size="10">${formatValue(value, data.unit)}</text>`
    }).join('')

    // Time labels at the edges and middle.
    const span = t1 - t0
    const times = [t0, t0 + span / 2, t1].map((t, i) => {
      const anchor = i === 0 ? 'start' : i === 2 ? 'end' : 'middle'
      return `<text x="${x(t).toFixed(1)}" y="${HEIGHT - 6}" text-anchor="${anchor}"
                fill="${text}" font-size="10">${formatTime(new Date(t), span)}</text>`
    }).join('')

    // Deploy markers: a vertical line and the short revision that landed.
    const markers = (data.markers || [])
      .map((m) => ({ at: new Date(m.at).getTime(), label: m.label || '' }))
      .filter((m) => Number.isFinite(m.at) && m.at >= t0 && m.at <= t1)
      .map((m) => {
        const xx = x(m.at).toFixed(1)
        return `<line x1="${xx}" y1="${PAD.top}" x2="${xx}" y2="${HEIGHT - PAD.bottom}"
                  stroke="${marker}" stroke-width="1" stroke-dasharray="2 3"/>
                <text x="${xx}" y="${PAD.top + 8}" text-anchor="middle"
                  fill="${marker}" font-size="9">${m.label.slice(0, 7)}</text>`
      }).join('')

    // A legend only where it carries information: with one series the panel
    // title already says what the line is.
    const legend = drawn.length < 2 ? '' : `
      <div class="hd-chart-legend flex items-center gap-4 text-[0.75rem] text-[var(--w-text-muted,#666)] mb-1">
        ${drawn.map((entry, index) => `
          <span class="inline-flex items-center gap-1.5">
            <span aria-hidden="true" class="inline-block w-3 h-[2px]" style="background:${palette[index % palette.length]}"></span>
            ${entry.label}
          </span>`).join('')}
      </div>`
    const latest = drawn
      .map((entry) => `${entry.label ? entry.label + ' ' : ''}${formatValue(entry.points[entry.points.length - 1].value, data.unit)}`)
      .join(', ')

    this.innerHTML = `
      <figure class="hd-chart mt-0 mx-0 mb-[0.8rem]">
        ${data.caption === false ? '' : `<figcaption class="hd-chart-title text-[0.8rem] text-[var(--w-text-muted,#666)] mb-1">${data.label || ''}</figcaption>`}
        ${legend}
        <svg class="w-full h-[9.5rem] block" viewBox="0 0 ${WIDTH} ${HEIGHT}" role="img"
             aria-label="${data.label || 'time series'}, latest ${latest}"
             preserveAspectRatio="none">
          ${gridlines}
          ${paths}
          ${markers}
          ${times}
        </svg>
      </figure>`
  }
}

if (!customElements.get('hd-timeseries')) {
  customElements.define('hd-timeseries', HdTimeseries)
}



// See hd-apptree.js: side-effect import, so it declares itself a module.
export {}
