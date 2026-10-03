import { api, session, tone, shortRevision } from '/shared/scripts/api.js'
import '/shared/scripts/hd-timeseries.js'
import { paginate, pageFromEvent } from '/shared/scripts/pager.js'

/** @typedef {import('/shared/scripts/hd-timeseries.js').HdTimeseriesElement} HdTimeseriesElement */

/**
 * @typedef {object} RunRow
 * @property {string} started
 * @property {string} duration
 * @property {number} seconds
 * @property {string} outcome
 * @property {string} outcomeClass
 * @property {string} revision
 */

document.title = `${decodeURIComponent(window.location.pathname.split('/')[5] || '').slice(0, 12)} · ${decodeURIComponent(window.location.pathname.split('/')[3] || '')} — HEIMDALL`

// Tone colours land directly on the value element; every colour is still a
// DuVay token, the template's :class binding only carries it there.
/** @type {Record<string, string>} Tailwind classes carrying a DuVay tone. */
const TONE_TEXT = {
  success: 'text-[var(--w-success,#1b7f3b)]',
  warning: 'text-[var(--w-warning,#b26b00)]',
  error: 'text-[var(--w-error,#b3261e)]',
}
/** @type {string} */
const VALUE_BASE = 'hd-tile-value text-[1.05rem] font-[650]'

// Every chart panel, in display order, with the heimdall.metrics group it
// belongs to. The order is `docker stats`' column order — CPU %, memory
// against the limit, memory %, net I/O, block I/O, PIDs — so an operator
// reads the same things in the same sequence they already know. The memory
// panels share a group: the percent chart is derived from the same series
// the bytes chart draws.
//
// The last two are HEIMDALL's own and never default; they appear only when
// heimdall.metrics asks for them.
/**
 * @typedef {object} PanelDef
 * @property {string} id Element id the chart island renders into.
 * @property {string} title
 * @property {string} group The heimdall.metrics group it belongs to.
 */

/** @type {PanelDef[]} */
const PANEL_DEFS = [
  { id: 'hd-cpu', title: 'CPU', group: 'cpu' },
  { id: 'hd-mem', title: 'Memory', group: 'memory' },
  { id: 'hd-memp', title: 'Memory % of limit', group: 'memory' },
  { id: 'hd-net', title: 'Network I/O', group: 'network' },
  { id: 'hd-block', title: 'Block I/O', group: 'block' },
  { id: 'hd-pids', title: 'PIDs', group: 'pids' },
  { id: 'hd-throttle', title: 'CPU throttling', group: 'throttling' },
  { id: 'hd-neterr', title: 'Network errors', group: 'net_errors' },
]

// The default panel set is exactly what `docker stats` reports: CPU %,
// memory usage against the limit (and the percentage of it), network I/O,
// block I/O, and PIDs. An operator arriving from Docker Desktop should see
// the same numbers, in the same set, without configuring anything.
//
// Throttling and network errors are HEIMDALL additions and are deliberately
// *not* default: they answer questions you have to know to ask, and two
// panels nobody reads crowd out the six that matter. A service opts into
// them with heimdall.metrics.
/** @type {string[]} */
const DOCKER_STATS_GROUPS = ['cpu', 'memory', 'network', 'block', 'pids']

// The panels shown before the first metrics answer arrives — and the ones an
// errored read falls back to. Starting from every panel would flash the two
// non-default ones on every load.
/** @type {PanelDef[]} */
const DEFAULT_PANELS = PANEL_DEFS.filter((panel) => DOCKER_STATS_GROUPS.includes(panel.group))

// The tabs. Runs only exists for a scheduled service — a long-running
// container has no executions, and an always-empty tab is a dead end.
// Runs paging. Ten rows fits the panel; the list itself is bounded by what
// the runtime keeps — the agent prunes to ten run containers per service, so
// a page or two is the whole history until executions are persisted.
/** @type {number} */
const RUNS_PAGE_SIZE = 10

/**
 * The windows the Metrics tab offers. Bounded by what the collector keeps —
 * the default retention is 25 hours, so a longer choice would draw a flat
 * line and call it history.
 *
 * @type {{value: string, label: string}[]}
 */
const WINDOWS = [
  { value: '15m', label: '15m' },
  { value: '1h', label: '1h' },
  { value: '6h', label: '6h' },
  { value: '24h', label: '24h' },
]

/** @type {string} */
const WINDOW_BASE = 'px-2.5 py-1 text-[0.8rem] bg-transparent border rounded-md transition-colors duration-150 motion-reduce:transition-none'
/** @type {string} */
const WINDOW_IDLE = `${WINDOW_BASE} border-[var(--w-border,#ddd)] text-[var(--w-text-muted,#666)] hover:text-[var(--w-text,#111)]`
/** @type {string} */
const WINDOW_ACTIVE = `${WINDOW_BASE} border-[var(--w-primary,#c8860d)] text-[var(--w-primary,#c8860d)] font-[600]`

/** @type {{id: string, label: string, scheduledOnly?: boolean}[]} */
const TAB_DEFS = [
  { id: 'metrics', label: 'Metrics' },
  { id: 'logs', label: 'Logs' },
  { id: 'runs', label: 'Runs', scheduledOnly: true },
  { id: 'inspect', label: 'Inspect' },
  { id: 'events', label: 'Events' },
]

// bg-transparent and the zeroed side borders are load-bearing: DuVay gives
// every bare <button> a filled chip, which reads as five grey buttons rather
// than as a tab strip.
/** @type {string} */
const TAB_BASE = 'px-4 py-2 text-[0.9rem] bg-transparent border-x-0 border-t-0 rounded-none border-b-2 -mb-px whitespace-nowrap transition-colors duration-150 motion-reduce:transition-none'
/** @type {string} */
const TAB_IDLE = `${TAB_BASE} border-b-transparent text-[var(--w-text-muted,#666)] hover:text-[var(--w-text,#111)]`
/** @type {string} */
const TAB_ACTIVE = `${TAB_BASE} border-b-[var(--w-primary,#c8860d)] text-[var(--w-text,#111)] font-[600]`

function tile(label, value, toneName) {
  const color = toneName ? TONE_TEXT[tone(toneName)] || '' : ''
  return { label, value, valueClass: color ? `${VALUE_BASE} ${color}` : VALUE_BASE }
}

function stamp(value) {
  return value ? String(value).replace('T', ' ').slice(0, 19) : '—'
}

// ago renders "10 hours ago" the way a status line reads best: one unit,
// rounded down, no false precision.
function ago(value) {
  if (!value) return ''
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(value).getTime()) / 1000))
  /** @type {[string, number][]} */
  const units = [['day', 86400], ['hour', 3600], ['minute', 60]]
  for (const [name, size] of units) {
    const count = Math.floor(seconds / size)
    if (count >= 1) return `${count} ${name}${count === 1 ? '' : 's'} ago`
  }
  return 'just now'
}

// The page companion, Tac's declarative shape: tiles, panels, tabs, and run
// rows are template loops in tac.html; the charts and the log console are
// imperative islands this class feeds, re-fed by a MutationObserver because
// the runtime recreates them on every render.
//
// The layout follows Docker Desktop's container detail — identity, status,
// tabs — and stops where that product's container management begins. There
// is no exec, no file browser, and no start/stop/delete: HEIMDALL shows what
// git declared and what the runtime has.
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
    /** @type {boolean} True while a read is in flight, for aria-busy. */
    this.busy = true
    /** @type {boolean} */
    this.loading = true
    /** @type {string} */
    this.pageError = ''
    /** @type {boolean} True when the instance is no longer running. */
    this.gone = false
    /** @type {string} */
    this.image = '—'
    /** @type {string} e.g. "Running (10 hours ago)". */
    this.statusText = 'Unknown'
    /** @type {string} */
    this.statusClass = VALUE_BASE
    /** @type {string} */
    this.copyLabel = 'Copy id'
    /** @type {string} */
    this.copyLogsLabel = 'Copy'
    /** @type {{label: string, value: string, valueClass: string}[]} */
    this.tiles = []
    /** @type {PanelDef[]} */
    this.panels = DEFAULT_PANELS
    /** @type {ReturnType<typeof setInterval>[]} */
    this.timers = []
    /** @type {import('/shared/scripts/hd-timeseries.js').ChartMarker[]} Deploys, drawn on every chart. */
    this.markers = []
    /** @type {import('/shared/scripts/api.js').Series|null} */
    this.series = null
    /** @type {string} The whole tail, before the filter box narrows it. */
    this.logsText = ''
    /** @type {string} */
    this.logFilter = ''
    /** @type {RunRow[]} Every execution, which the trend charts. */
    this.executions = []
    /** @type {RunRow[]} The page of executions the table shows. */
    this.runs = []
    /** @type {number[]} Run durations in seconds, oldest first. */
    this.durations = []
    /** @type {number} */
    this.runsPage = 1
    /** @type {object[]} */
    this.runsPages = []
    /** @type {string} */
    this.runsRange = ''
    /** @type {boolean} */
    this.onFirstRun = true
    /** @type {boolean} */
    this.onLastRun = true
    /** @type {boolean} */
    this.showRunsPager = false
    /** @type {{label: string, value: string}[]} */
    this.inspectRows = []
    /** @type {{at: string, message: string}[]} */
    this.events = []
    /** @type {import('/shared/scripts/api.js').Instance|null} */
    this.instance = null

    // Scheduling, filled from the application's live state: a job's status
    // line is its schedule, not a replica count.
    /** @type {boolean} Whether this service is a scheduled job. */
    this.scheduled = false
    /** @type {string} The cron expression the runtime holds. */
    this.schedule = ''
    /** @type {string} */
    this.nextRun = '—'
    /** @type {boolean} True while a manual run is starting. */
    this.running = false
    /** @type {string} */
    this.runLabel = 'Run now'

    /**
     * @type {string} The metrics window, from the URL so a shared link keeps
     * it — an incident is discussed by pasting a link at the graph someone
     * else is describing.
     */
    this.window = new URLSearchParams(globalThis.location.search).get('window') || '1h'
    if (!WINDOWS.some((entry) => entry.value === this.window)) this.window = '1h'
    this.applyWindows()

    this.tab = (window.location.hash || '#metrics').slice(1)
    if (!TAB_DEFS.some((entry) => entry.id === this.tab)) this.tab = 'metrics'
    this.applyTab()
  }

  /**
   * Derives the window chooser's items, the same way the tabs are derived:
   * Tac renders fields, not expressions.
   *
   * @returns {void}
   */
  applyWindows() {
    /** @type {{value: string, label: string, className: string, current: string}[]} */
    this.windows = WINDOWS.map((entry) => ({
      value: entry.value,
      label: entry.label,
      className: entry.value === this.window ? WINDOW_ACTIVE : WINDOW_IDLE,
      current: entry.value === this.window ? 'true' : 'false',
    }))
  }

  /**
   * Switches the metrics window and redraws from a fresh read.
   *
   * @param {Event} event
   * @returns {Promise<void>}
   */
  async selectWindow(event) {
    const chosen = /** @type {HTMLElement} */ (
      /** @type {Element} */ (event?.target)?.closest?.('[data-window]'))?.dataset?.window
    if (!chosen || chosen === this.window) return
    this.window = chosen
    this.applyWindows()

    // The choice rides in the URL, beside the service, so a reload and a
    // shared link both land on the same graph.
    const url = new URL(globalThis.location.href)
    url.searchParams.set('window', chosen)
    globalThis.history.replaceState(null, '', url)

    await this.rerender()
    await this.paintMetrics()
  }

  /**
   * Derives everything the template branches on for the tabs. Tac renders
   * fields, not expressions, so the active class and the aria-current string
   * are computed here — ARIA needs the literal "true"/"false", which a
   * boolean binding would emit as a bare attribute.
   *
   * @returns {void}
   */
  applyTab() {
    this.tabs = TAB_DEFS
      .filter((entry) => !entry.scheduledOnly || this.scheduled)
      .map((entry) => ({
        id: entry.id,
        label: entry.label,
        className: entry.id === this.tab ? TAB_ACTIVE : TAB_IDLE,
        current: entry.id === this.tab ? 'page' : 'false',
      }))
    this.onMetrics = this.tab === 'metrics'
    this.onLogs = this.tab === 'logs'
    this.onRuns = this.tab === 'runs'
    this.onInspect = this.tab === 'inspect'
    this.onEvents = this.tab === 'events'
  }

  // The tab id travels on the button rather than as a handler argument: a
  // loop variable is not in scope in the handler expression, and reading the
  // element the click landed on is one less thing to get subtly wrong.
  async selectTab(event) {
    const id = event.currentTarget?.dataset?.tab || event.target?.closest?.('[data-tab]')?.dataset?.tab
    if (!id) return
    this.tab = id
    this.applyTab()
    // The hash keeps a refresh — and a shared link — on the same tab.
    window.history.replaceState(null, '', `#${id}`)
    await this.rerender()
    if (this.onLogs) this.paintLogs()
    if (this.onMetrics) this.paintMetrics()
  }

  /**
   * @returns {Promise<void>}
   */
  async rerender() {
    await globalThis.__tc_rerender?.()
    this.wireIslands()
  }

  /**
   * @returns {void}
   */
  stopPolling() {
    this.timers.forEach(clearInterval)
    this.timers = []
  }

  /**
   * @returns {Promise<void>}
   */
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

  /**
   * Loads the instance, its schedule, its runs, and its events.
   *
   * @returns {Promise<void>}
   */
  async start() {
    if (!(await session.active())) {
      window.location.href = '/'
      return
    }
    try {
      const [instancesAnswer, history, status, eventsAnswer] = await Promise.all([
        api.instances(this.project, this.app).catch(() => ({ instances: [] })),
        api.history(this.project, this.app).catch(() => ({ operations: [] })),
        api.app(this.project, this.app).catch(() => ({})),
        api.events(this.project, this.app).catch(() => ({ events: [] })),
      ])
      const instances = instancesAnswer.instances || []
      const instance = instances.find((candidate) => candidate.ref.instance === this.instanceID) || null
      if (instance) this.service = instance.ref.service
      this.serviceName = this.service || 'instance'
      this.gone = !instance
      this.instance = instance
      this.image = instance?.image || '—'

      this.markers = (history.operations || [])
        .filter((operation) => !operation.dry_run && operation.finished_at)
        .map((operation) => ({ at: operation.finished_at, label: shortRevision(operation.revision) }))

      this.readSchedule(status)
      this.readRuns(instances)
      this.events = (eventsAnswer.events || []).slice(0, 50).map((entry) => ({
        at: stamp(entry.at), message: entry.message || entry.reason || '',
      }))

      const health = instance?.health || 'Unknown'
      const state = instance?.status || 'gone'
      this.statusText = instance?.started_at
        ? `${state.charAt(0).toUpperCase()}${state.slice(1)} (${ago(instance.started_at)})`
        : state
      this.statusClass = `${VALUE_BASE} ${TONE_TEXT[tone(health)] || ''}`.trim()

      this.tiles = [
        tile('Health', health, health),
        tile('State', state),
        tile('Restarts', String(instance?.restarts ?? '—')),
        tile('Started', stamp(instance?.started_at)),
        tile('Revision', shortRevision(instance?.revision)),
        tile('Image', this.image),
      ]
      this.inspectRows = [
        { label: 'Instance', value: this.instanceID },
        { label: 'Service', value: this.serviceName },
        { label: 'Application', value: `${this.project} / ${this.app}` },
        { label: 'Image', value: this.image },
        { label: 'Revision', value: instance?.revision || '—' },
        { label: 'Node', value: instance?.node || '—' },
        { label: 'Health', value: health },
        { label: 'State', value: state },
        { label: 'Restarts', value: String(instance?.restarts ?? '—') },
        { label: 'Started', value: stamp(instance?.started_at) },
        { label: 'Finished', value: stamp(instance?.finished_at) },
        { label: 'Exit code', value: instance?.exit_code === undefined ? '—' : String(instance.exit_code) },
        { label: 'Schedule', value: this.schedule || '—' },
      ]
      this.applyTab()
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

  // readSchedule pulls this service's scheduling out of the application's
  // live state. A job has no replica count to report; it has a cron
  // expression, a last outcome, and a next firing time.
  /**
   * Pulls this service's scheduling out of the application's live state.
   *
   * @param {import('/shared/scripts/api.js').AppDetail} [detail] The whole
   *   answer from api.app — the per-service diff lives under its `status`,
   *   not on the answer itself. Reading the wrong level here is why the
   *   schedule block and the Run now button never appeared for a scheduled
   *   service: `detail.services` is always undefined, so `scheduled` was
   *   always false. Found by turning on checkJs.
   * @returns {void}
   */
  readSchedule(detail) {
    const services = detail?.status?.services || []
    const entry = services.find((service) => service.service === this.service)
    this.scheduled = Boolean(entry?.schedule)
    this.schedule = entry?.schedule || ''
    this.nextRun = entry?.next_run_at ? stamp(entry.next_run_at) : 'unscheduled'
  }

  // readRuns turns finished instances of this service into an execution
  // history. An execution is an instance that stopped, so this is the same
  // list the drawer shows, filtered to the ones that ended.
  /**
   * Turns finished instances of this service into an execution history.
   *
   * @param {import('/shared/scripts/api.js').Instance[]} instances
   * @returns {void}
   */
  readRuns(instances) {
    const finished = instances
      .filter((candidate) => candidate.ref.service === this.service && candidate.finished_at)
      .sort((left, right) => String(right.started_at).localeCompare(String(left.started_at)))

    this.executions = finished.map((run) => {
      const seconds = Math.max(0,
        Math.round((new Date(run.finished_at).getTime() - new Date(run.started_at).getTime()) / 1000))
      const failed = run.exit_code !== undefined && run.exit_code !== 0
      return {
        started: stamp(run.started_at),
        duration: seconds >= 60 ? `${Math.floor(seconds / 60)}m ${seconds % 60}s` : `${seconds}s`,
        seconds,
        outcome: failed ? `exit ${run.exit_code}` : 'succeeded',
        outcomeClass: failed ? TONE_TEXT.error : TONE_TEXT.success,
        revision: shortRevision(run.revision),
      }
    })
    // The trend is the whole history, not the page: a sparkline showing ten
    // of forty runs would say something untrue about the trend.
    this.durations = this.executions.map((run) => run.seconds).reverse()
    this.runsPage = 1
    this.paginateRuns()
  }

  /**
   * @returns {void}
   */
  paginateRuns() {
    const paged = paginate(this.executions, this.runsPage, RUNS_PAGE_SIZE, { noun: 'runs' })
    this.runsPage = paged.page
    this.runs = paged.rows
    this.showRunsPager = paged.show
    this.onFirstRun = paged.onFirst
    this.onLastRun = paged.onLast
    this.runsRange = paged.range
    this.runsPages = paged.pages
  }

  /**
   * @param {Event} event
   * @returns {Promise<void>}
   */
  async goToRunsPage(event) {
    const page = pageFromEvent(event)
    if (!page) return
    this.runsPage = page
    this.paginateRuns()
    await this.rerender()
  }

  /**
   * @param {Event} event
   * @param {number} delta
   * @returns {Promise<void>}
   */
  async stepRuns(event, delta) {
    this.runsPage += delta
    this.paginateRuns()
    await this.rerender()
  }

  // wireIslands feeds freshly rendered islands from the caches. Guarded per
  // element (the flag is set before drawing) so the mutations the drawing
  // itself makes do not loop the observer.
  /**
   * @returns {void}
   */
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
      logs.textContent = this.visibleLogs() || 'Loading…'
      logs.scrollTop = logs.scrollHeight
    }
  }

  /**
   * @param {string} id Panel element id.
   * @returns {void}
   */
  feedChart(id) {
    const element = /** @type {HdTimeseriesElement} */ (document.getElementById(id))
    const series = this.series
    if (!element || !series) return
    const feed = (label, unit, points, max) =>
      element.series({ label, caption: false, unit, points: points || [], max: max || 0, markers: this.markers })
    // Two directions on one axis, the way `docker stats` prints NET I/O and
    // BLOCK I/O as a single column: in against out is the comparison an
    // operator is actually making.
    const feedPair = (label, unit, lines) =>
      element.series({
        label, caption: false, unit, markers: this.markers,
        lines: lines.map((entry) => ({ label: entry.label, points: entry.points || [] })),
      })
    // The panel already carries the title; the label still reaches the
    // SVG's aria description.
    switch (id) {
      case 'hd-cpu': return feed('CPU', 'percent', series.cpu_percent)
      case 'hd-mem': return feed('Memory', 'bytes', series.memory_bytes, series.memory_limit)
      case 'hd-net': return feedPair('Network I/O — bytes/min', 'bytes', [
        { label: 'in', points: series.net_rx_bytes },
        { label: 'out', points: series.net_tx_bytes },
      ])
      case 'hd-block': return feedPair('Block I/O — bytes/min', 'bytes', [
        { label: 'read', points: series.block_read_bytes },
        { label: 'write', points: series.block_write_bytes },
      ])
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

  // defaultGroups is the docker stats set, narrowed to what this target's
  // provider actually answered with. A Docker Engine reports all five on
  // every sample, zeros included, so all five show; a CloudWatch-backed
  // target answers CPU and memory only, and a panel for a series it can
  // never produce is not "Collecting…", it is a lie with a spinner.
  //
  // CPU and memory always show: every provider answers those, and an empty
  // CPU chart during the first minute is honest.
  /**
   * @returns {Set<string>} The docker stats groups this target actually answers with.
   */
  defaultGroups() {
    const series = this.series || {}
    const has = (field) => (series[field] || []).length > 0
    const answered = new Set(['cpu', 'memory'])
    if (has('net_rx_bytes') || has('net_tx_bytes')) answered.add('network')
    if (has('block_read_bytes') || has('block_write_bytes')) answered.add('block')
    if (has('pids')) answered.add('pids')
    return new Set(DOCKER_STATS_GROUPS.filter((group) => answered.has(group)))
  }

  /**
   * @returns {Promise<void>}
   */
  async paintMetrics() {
    if (!this.onMetrics) return
    try {
      const answer = await api.metrics(this.project, this.app, this.instanceID, this.service, this.window)
      this.series = answer.series || {}
      // The service's heimdall.metrics selection decides which panels exist
      // at all — unchosen panels are absent, not empty. A change in the
      // selection re-renders; otherwise the existing charts update in
      // place, no re-render per poll.
      // Without an explicit heimdall.metrics selection the panels are the
      // docker stats set; with one, they are exactly what was asked for,
      // including the groups that are never default.
      const selection = answer.metrics
      const chosen = new Set(selection && selection.length ? selection : this.defaultGroups())
      const panels = PANEL_DEFS.filter((definition) => chosen.has(definition.group))
      if (panels.length !== this.panels.length) {
        this.panels = panels
        await this.rerender()
      } else {
        for (const definition of this.panels) this.feedChart(definition.id)
      }
    } catch (error) {
      const element = /** @type {HdTimeseriesElement} */ (document.getElementById('hd-cpu'))
      if (element) element.series({ label: 'CPU — ' + error.message, points: [] })
    }
  }

  // visibleLogs applies the filter box. Filtering is client-side on the tail
  // already fetched: the server read is bounded, and narrowing it further
  // would hide lines the operator has in hand.
  /**
   * @returns {string} The tail, narrowed by the filter box.
   */
  visibleLogs() {
    if (!this.logFilter) return this.logsText
    const needle = this.logFilter.toLowerCase()
    return this.logsText
      .split('\n')
      .filter((line) => line.toLowerCase().includes(needle))
      .join('\n')
  }

  /**
   * @param {Event} event
   * @returns {Promise<void>}
   */
  async filterLogs(event) {
    this.logFilter = /** @type {HTMLInputElement} */ (event.target).value
    const element = document.getElementById('hd-logs')
    if (element) element.textContent = this.visibleLogs() || '(no matching lines)'
  }

  /**
   * @returns {Promise<void>}
   */
  async copyID() {
    await navigator.clipboard?.writeText(this.instanceID)
    this.copyLabel = 'Copied'
    setTimeout(() => { this.copyLabel = 'Copy id'; void this.rerender() }, 1500)
  }

  /**
   * @returns {Promise<void>}
   */
  async copyLogs() {
    await navigator.clipboard?.writeText(this.visibleLogs())
    this.copyLogsLabel = 'Copied'
    setTimeout(() => { this.copyLogsLabel = 'Copy'; void this.rerender() }, 1500)
  }

  /**
   * @returns {void}
   */
  jumpToLatest() {
    const element = document.getElementById('hd-logs')
    if (element) element.scrollTop = element.scrollHeight
  }

  // runNow starts one out-of-band execution. It asks SESAME a different
  // question from a deploy — app:run — so an operator who may re-run last
  // night's report cannot thereby sync.
  /**
   * Starts one out-of-band execution through the app:run action.
   *
   * @returns {Promise<void>}
   */
  async runNow() {
    this.running = true
    this.runLabel = 'Starting…'
    await this.rerender()
    try {
      await api.runService(this.project, this.app, this.service)
      this.runLabel = 'Started'
    } catch (error) {
      this.pageError = error.message
      this.runLabel = 'Run now'
    } finally {
      this.running = false
      await this.rerender()
      setTimeout(() => { this.runLabel = 'Run now'; void this.rerender() }, 2500)
    }
  }

  /**
   * @returns {Promise<void>}
   */
  async paintLogs() {
    if (!this.onLogs) return
    try {
      const text = await api.logs(this.project, this.app, this.instanceID, this.service, 300)
      this.logsText = text || '(no output)'
      const element = document.getElementById('hd-logs')
      if (!element) return
      const follow = element.scrollTop + element.clientHeight >= element.scrollHeight - 8
      element.textContent = this.visibleLogs() || '(no matching lines)'
      if (follow) element.scrollTop = element.scrollHeight
    } catch (error) {
      const element = document.getElementById('hd-logs')
      if (element) element.textContent = 'logs unavailable: ' + error.message
    }
  }

  /**
   * @returns {Promise<void>}
   */
  async signOut() {
    await session.logout()
    window.location.href = '/'
  }
}
