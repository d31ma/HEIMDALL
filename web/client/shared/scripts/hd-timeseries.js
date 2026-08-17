// <hd-timeseries> — HEIMDALL's inline chart.
//
// HEIMDALL-owned because DuVay's w-chart is a placeholder shell with no axes.
// It renders inline SVG from DuVay custom properties, so every theme works
// without a second rule set. No dependency, no canvas, no animation frames:
// an incident chart's job is to be legible and cheap, not to move.
//
// Set data with element.series({label, unit, points: [{at, value}], markers}).
// Markers are deploys: vertical lines with the short revision, which is the
// product's headline claim drawn on the chart — "what changed and when".

const WIDTH = 640
const HEIGHT = 160
const PAD = { top: 10, right: 10, bottom: 22, left: 46 }

function token(name, fallback) {
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  return value || fallback
}

function scale(domainMin, domainMax, rangeMin, rangeMax) {
  const span = domainMax - domainMin || 1
  return (v) => rangeMin + ((v - domainMin) / span) * (rangeMax - rangeMin)
}

// niceUnit picks a display unit so the y axis reads "256 MiB" rather than
// "268435456".
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

function formatTime(date, spanMs) {
  const hours = String(date.getHours()).padStart(2, '0')
  const minutes = String(date.getMinutes()).padStart(2, '0')
  if (spanMs > 12 * 3600_000) {
    return `${String(date.getDate()).padStart(2, '0')}/${String(date.getMonth() + 1).padStart(2, '0')} ${hours}:${minutes}`
  }
  return `${hours}:${minutes}`
}

class HdTimeseries extends HTMLElement {
  series(input) {
    this._data = input
    this.render()
  }

  render() {
    const data = this._data || {}
    const points = (data.points || [])
      .map((p) => ({ at: new Date(p.at).getTime(), value: Number(p.value) }))
      .filter((p) => Number.isFinite(p.at) && Number.isFinite(p.value))
      .sort((a, b) => a.at - b.at)

    if (points.length < 2) {
      this.innerHTML = `<div class="hd-chart-empty h-24 flex items-center justify-center text-[var(--w-text-muted,#666)] text-[0.85rem] border border-dashed border-[var(--w-border,#ddd)] rounded-md mb-[0.8rem]">${points.length === 1 ? 'Collecting…' : 'No data in this window'}</div>`
      return
    }

    const line = token('--w-primary', '#3b5bdb')
    const grid = token('--w-border', '#ddd')
    const text = token('--w-text-muted', '#666')
    const marker = token('--w-warning', '#b26b00')

    const t0 = points[0].at
    const t1 = points[points.length - 1].at
    let vMax = Math.max(...points.map((p) => p.value))
    if (data.max) vMax = Math.max(vMax, data.max)
    if (vMax <= 0) vMax = 1

    const x = scale(t0, t1, PAD.left, WIDTH - PAD.right)
    const y = scale(0, vMax * 1.08, HEIGHT - PAD.bottom, PAD.top)

    const path = points
      .map((p, i) => `${i === 0 ? 'M' : 'L'}${x(p.at).toFixed(1)},${y(p.value).toFixed(1)}`)
      .join(' ')
    const area = `${path} L${x(t1).toFixed(1)},${y(0).toFixed(1)} L${x(t0).toFixed(1)},${y(0).toFixed(1)} Z`

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

    this.innerHTML = `
      <figure class="hd-chart mt-0 mx-0 mb-[0.8rem]">
        ${data.caption === false ? '' : `<figcaption class="hd-chart-title text-[0.8rem] text-[var(--w-text-muted,#666)] mb-1">${data.label || ''}</figcaption>`}
        <svg class="w-full h-[9.5rem] block" viewBox="0 0 ${WIDTH} ${HEIGHT}" role="img"
             aria-label="${data.label || 'time series'}, latest ${formatValue(points[points.length - 1].value, data.unit)}"
             preserveAspectRatio="none">
          ${gridlines}
          <path d="${area}" fill="${line}" opacity="0.08"/>
          <path d="${path}" fill="none" stroke="${line}" stroke-width="1.5"/>
          ${markers}
          ${times}
        </svg>
      </figure>`
  }
}

if (!customElements.get('hd-timeseries')) {
  customElements.define('hd-timeseries', HdTimeseries)
}
