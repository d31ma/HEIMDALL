import { api, session, tone, shortRevision } from '/shared/scripts/api.js'
import '/shared/scripts/hd-timeseries.js'

document.title = `${decodeURIComponent(window.location.pathname.split('/')[5] || '').slice(0, 12)} · ${decodeURIComponent(window.location.pathname.split('/')[3] || '')} — HEIMDALL`

// Tone colours land directly on the value element; every colour is still a
// DuVay token, the template's :class binding only carries it there.
const TONE_TEXT = {
  success: 'text-[var(--w-success,#1b7f3b)]',
  warning: 'text-[var(--w-warning,#b26b00)]',
  error: 'text-[var(--w-error,#b3261e)]',
}
const VALUE_BASE = 'hd-tile-value text-[1.05rem] font-[650]'

// Every chart panel, in display order, with the heimdall.metrics group it
// belongs to. The memory panels share a group: the percent chart is derived
// from the same series the bytes chart draws.
const PANEL_DEFS = [
  { id: 'hd-cpu', title: 'CPU', group: 'cpu' },
  { id: 'hd-mem', title: 'Memory', group: 'memory' },
  { id: 'hd-rx', title: 'Network in', group: 'network' },
  { id: 'hd-tx', title: 'Network out', group: 'network' },
  { id: 'hd-br', title: 'Block read', group: 'block' },
  { id: 'hd-bw', title: 'Block write', group: 'block' },
  { id: 'hd-memp', title: 'Memory % of limit', group: 'memory' },
  { id: 'hd-pids', title: 'PIDs', group: 'pids' },
  { id: 'hd-throttle', title: 'CPU throttling', group: 'throttling' },
  { id: 'hd-neterr', title: 'Network errors', group: 'net_errors' },
]

function tile(label, value, toneName) {
  const color = toneName ? TONE_TEXT[tone(toneName)] || '' : ''
  return { label, value, valueClass: color ? `${VALUE_BASE} ${color}` : VALUE_BASE }
}

// The page companion, Tac's declarative shape: tiles and panels are template
// loops in tac.html; the charts and the log tail are imperative islands this
// class feeds, re-fed by a MutationObserver because the runtime recreates
// them on every render. One page per running instance, Grafana-shaped;
// everything polls, nothing navigates away from an incident.
export default class InstancePage {
  static __tachyonOnMount = ['mount']

  constructor() {
    const [, , project, app, , instanceID] = window.location.pathname.split('/').map(decodeURIComponent)
    this.project = project
    this.app = app
    this.instanceID = instanceID
    this.shortID = instanceID.slice(0, 12)
    this.appHref = `/apps/${encodeURIComponent(project)}/${encodeURIComponent(app)}`
    this.service = new URLSearchParams(window.location.search).get('service') || ''
    this.serviceName = this.service || 'instance'
    this.busy = true
    this.loading = true
    this.pageError = ''
    this.gone = false
    this.tiles = []
    this.panels = PANEL_DEFS
    this.timers = []
    this.markers = []
    this.series = null
    this.logsText = ''
  }

  async rerender() {
    await globalThis.__tc_rerender?.()
    this.wireIslands()
  }

  stopPolling() {
    this.timers.forEach(clearInterval)
    this.timers = []
  }

  async mount() {
    // The runtime awaits onMount before the first render, so this kicks the
    // data flow off without blocking. The observer re-feeds the chart and
    // log islands whenever a render recreates them — including the render
    // the runtime runs after every handled event.
    new MutationObserver(() => this.wireIslands())
      .observe(document.body, { childList: true, subtree: true })
    window.addEventListener('pagehide', () => this.stopPolling())
    void this.start()
  }

  async start() {
    if (!(await session.active())) {
      window.location.href = '/'
      return
    }
    try {
      const [instancesAnswer, history] = await Promise.all([
        api.instances(this.project, this.app).catch(() => ({ instances: [] })),
        api.history(this.project, this.app).catch(() => ({ operations: [] })),
      ])
      const instance = (instancesAnswer.instances || [])
        .find((candidate) => candidate.ref.instance === this.instanceID) || null
      if (instance) this.service = instance.ref.service
      this.serviceName = this.service || 'instance'
      this.gone = !instance

      this.markers = (history.operations || [])
        .filter((operation) => !operation.dry_run && operation.finished_at)
        .map((operation) => ({ at: operation.finished_at, label: shortRevision(operation.revision) }))

      this.tiles = [
        tile('Health', instance?.health || 'Unknown', instance?.health || 'Unknown'),
        tile('State', instance?.status || 'gone'),
        tile('Restarts', String(instance?.restarts ?? '—')),
        tile('Started', instance?.started_at ? instance.started_at.replace('T', ' ').slice(0, 19) : '—'),
        tile('Revision', shortRevision(instance?.revision)),
        tile('Image', instance?.image || '—'),
      ]
      this.loading = false
    } catch (error) {
      this.pageError = error.message
      this.loading = false
    } finally {
      this.busy = false
      await this.rerender()
    }

    await Promise.all([this.paintMetrics(), this.paintLogs()])
    this.stopPolling()
    this.timers.push(
      setInterval(() => this.paintMetrics(), 15000),
      setInterval(() => this.paintLogs(), 2500),
    )
  }

  // wireIslands feeds freshly rendered islands from the caches. Guarded per
  // element (the flag is set before drawing) so the mutations the drawing
  // itself makes do not loop the observer.
  wireIslands() {
    for (const definition of this.panels) {
      const element = document.getElementById(definition.id)
      if (element && !element.dataset.wired) {
        element.dataset.wired = '1'
        this.feedChart(definition.id)
      }
    }
    const logs = document.getElementById('hd-logs')
    if (logs && !logs.dataset.wired) {
      logs.dataset.wired = '1'
      if (this.logsText) logs.textContent = this.logsText
    }
  }

  feedChart(id) {
    const element = document.getElementById(id)
    const series = this.series
    if (!element || !series) return
    const feed = (label, unit, points, max) =>
      element.series({ label, caption: false, unit, points: points || [], max: max || 0, markers: this.markers })
    // The panel already carries the title; the label still reaches the
    // SVG's aria description.
    switch (id) {
      case 'hd-cpu': return feed('CPU', 'percent', series.cpu_percent)
      case 'hd-mem': return feed('Memory', 'bytes', series.memory_bytes, series.memory_limit)
      case 'hd-rx': return feed('Network in — bytes/min', 'bytes', series.net_rx_bytes)
      case 'hd-tx': return feed('Network out — bytes/min', 'bytes', series.net_tx_bytes)
      case 'hd-br': return feed('Block read — bytes/min', 'bytes', series.block_read_bytes)
      case 'hd-bw': return feed('Block write — bytes/min', 'bytes', series.block_write_bytes)
      case 'hd-pids': return feed('PIDs', '', series.pids)
      case 'hd-throttle': return feed('CPU throttling — periods/min', '', series.cpu_throttled)
      case 'hd-neterr': return feed('Network errors — packets/min', '', series.net_errors)
      case 'hd-memp': {
        // Memory % is derived, not shipped: same bytes series against the
        // limit, scaled to 100 so OOM proximity reads at a glance.
        const limit = Number(series.memory_limit) || 0
        return feed('Memory of limit', 'percent',
          limit > 0 ? (series.memory_bytes || []).map((p) => ({ at: p.at, value: (p.value / limit) * 100 })) : [],
          limit > 0 ? 100 : 0)
      }
    }
  }

  async paintMetrics() {
    try {
      const answer = await api.metrics(this.project, this.app, this.instanceID, this.service)
      this.series = answer.series || {}
      // The service's heimdall.metrics selection decides which panels exist
      // at all — unchosen panels are absent, not empty. A change in the
      // selection re-renders; otherwise the existing charts update in
      // place, no re-render per poll.
      const selection = answer.metrics
      const chosen = new Set(selection && selection.length ? selection : PANEL_DEFS.map((d) => d.group))
      const panels = PANEL_DEFS.filter((definition) => chosen.has(definition.group))
      if (panels.length !== this.panels.length) {
        this.panels = panels
        await this.rerender()
      } else {
        for (const definition of this.panels) this.feedChart(definition.id)
      }
    } catch (error) {
      const element = document.getElementById('hd-cpu')
      if (element) element.series({ label: 'CPU — ' + error.message, points: [] })
    }
  }

  async paintLogs() {
    try {
      const text = await api.logs(this.project, this.app, this.instanceID, 300)
      this.logsText = text || '(no output)'
      const element = document.getElementById('hd-logs')
      if (!element) return
      const follow = element.scrollTop + element.clientHeight >= element.scrollHeight - 8
      element.textContent = this.logsText
      if (follow) element.scrollTop = element.scrollHeight
    } catch (error) {
      const element = document.getElementById('hd-logs')
      if (element) element.textContent = 'logs unavailable: ' + error.message
    }
  }

  async signOut() {
    await session.logout()
    window.location.href = '/'
  }
}
