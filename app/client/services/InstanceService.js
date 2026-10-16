import { api, session, tone, shortRevision } from '/shared/scripts/api.js'
import '/shared/scripts/hd-timeseries.js'
import { paginate, pageFromEvent } from '/shared/scripts/pager.js'

@Service
export default class InstanceService {
  /**
   * @private
   * @type {Record<string, string>}
   */
  static _TONE_TEXT = {
  success: 'text-[var(--w-success,#1b7f3b)]',
  warning: 'text-[var(--w-warning,#b26b00)]',
  error: 'text-[var(--w-error,#b3261e)]',
  }
  /**
   * @private
   * @type {string}
   */
  static _VALUE_BASE = 'hd-tile-value text-[1.05rem] font-[650]'

  /**
   * @private
   * @type {import('/models/PanelDef.js').PanelDef[]}
   */
  static _PANEL_DEFINITIONS = [
  { id: 'hd-cpu', title: 'CPU', group: 'cpu' },
  { id: 'hd-mem', title: 'Memory', group: 'memory' },
  { id: 'hd-memp', title: 'Memory % of limit', group: 'memory' },
  { id: 'hd-net', title: 'Network I/O', group: 'network' },
  { id: 'hd-block', title: 'Block I/O', group: 'block' },
  { id: 'hd-pids', title: 'PIDs', group: 'pids' },
  { id: 'hd-throttle', title: 'CPU throttling', group: 'throttling' },
  { id: 'hd-neterr', title: 'Network errors', group: 'net_errors' },
  ]

  /**
   * @private
   * @type {string[]}
   */
  static _DOCKER_STATS_GROUPS = ['cpu', 'memory', 'network', 'block', 'pids']

  /**
   * @private
   * @type {import('/models/PanelDef.js').PanelDef[]}
   */
  static _DEFAULT_PANELS = InstanceService._PANEL_DEFINITIONS.filter((panel) => InstanceService._DOCKER_STATS_GROUPS.includes(panel.group))

  /**
   * @private
   * @type {number}
   */
  static _RUNS_PAGE_SIZE = 10

  /**
   * @private
   * @type {{value: string, label: string}[]}
   */
  static _WINDOWS = [
  { value: '15m', label: '15m' },
  { value: '1h', label: '1h' },
  { value: '6h', label: '6h' },
  { value: '24h', label: '24h' },
  ]

  /**
   * @private
   * @type {string}
   */
  static _WINDOW_BASE = 'px-2.5 py-1 text-[0.8rem] bg-transparent border rounded-md transition-colors duration-150 motion-reduce:transition-none'
  /**
   * @private
   * @type {string}
   */
  static _WINDOW_IDLE = `${InstanceService._WINDOW_BASE} border-[var(--w-border,#ddd)] text-[var(--w-text-muted,#666)] hover:text-[var(--w-text,#111)]`
  /**
   * @private
   * @type {string}
   */
  static _WINDOW_ACTIVE = `${InstanceService._WINDOW_BASE} border-[var(--w-primary,#c8860d)] text-[var(--w-primary,#c8860d)] font-[600]`

  /**
   * @private
   * @type {{id: string, label: string, scheduledOnly?: boolean}[]}
   */
  static _TAB_DEFINITIONS = [
  { id: 'metrics', label: 'Metrics' },
  { id: 'logs', label: 'Logs' },
  { id: 'runs', label: 'Runs', scheduledOnly: true },
  { id: 'inspect', label: 'Inspect' },
  { id: 'events', label: 'Events' },
  ]

  /**
   * @private
   * @type {string}
   */
  static _TAB_BASE = 'px-4 py-2 text-[0.9rem] bg-transparent border-x-0 border-t-0 rounded-none border-b-2 -mb-px whitespace-nowrap transition-colors duration-150 motion-reduce:transition-none'
  /**
   * @private
   * @type {string}
   */
  static _TAB_IDLE = `${InstanceService._TAB_BASE} border-b-transparent text-[var(--w-text-muted,#666)] hover:text-[var(--w-text,#111)]`
  /**
   * @private
   * @type {string}
   */
  static _TAB_ACTIVE = `${InstanceService._TAB_BASE} border-b-[var(--w-primary,#c8860d)] text-[var(--w-text,#111)] font-[600]`

  /**
   * @private
   * @type {string}
   */
  static _SERVICE_QUERY = 'service'
  /**
   * @private
   * @type {string}
   */
  static _WINDOW_QUERY = 'window'
  /**
   * @private
   * @type {string}
   */
  static _DEFAULT_WINDOW = '1h'
  /**
   * @private
   * @type {string}
   */
  static _INSTANCE_LABEL = 'instance'
  /**
   * @private
   * @type {string}
   */
  static _HOME_PATH = '/'
  /**
   * @private
   * @type {string}
   */
  static _EMPTY_VALUE = '—'
  /**
   * @private
   * @type {string}
   */
  static _UNKNOWN_STATE = 'Unknown'
  /**
   * @private
   * @type {string}
   */
  static _COPY_ID_LABEL = 'Copy id'
  /**
   * @private
   * @type {string}
   */
  static _COPY_LOGS_LABEL = 'Copy'
  /**
   * @private
   * @type {string}
   */
  static _COPIED_LABEL = 'Copied'
  /**
   * @private
   * @type {string}
   */
  static _RUN_LABEL = 'Run now'
  /**
   * @private
   * @type {string}
   */
  static _STARTING_LABEL = 'Starting…'
  /**
   * @private
   * @type {string}
   */
  static _STARTED_LABEL = 'Started'
  /**
   * @private
   * @type {string}
   */
  static _METRICS_TAB = 'metrics'
  /**
   * @private
   * @type {string}
   */
  static _LOGS_TAB = 'logs'
  /**
   * @private
   * @type {string}
   */
  static _RUNS_TAB = 'runs'
  /**
   * @private
   * @type {string}
   */
  static _INSPECT_TAB = 'inspect'
  /**
   * @private
   * @type {string}
   */
  static _EVENTS_TAB = 'events'
  /**
   * @private
   * @type {string}
   */
  static _TRUE_VALUE = 'true'
  /**
   * @private
   * @type {string}
   */
  static _FALSE_VALUE = 'false'
  /**
   * @private
   * @type {string}
   */
  static _LOGS_ELEMENT = 'hd-logs'
  /**
   * @private
   * @type {string}
   */
  static _PROCESSOR_ELEMENT = 'hd-cpu'
  /**
   * @private
   * @type {string}
   */
  static _WIRED_VALUE = '1'
  /**
   * @private
   * @type {string}
   */
  static _NO_MATCHING_LINES = '(no matching lines)'
  /**
   * @private
   * @type {string}
   */
  static _LINE_BREAK = '\n'
  /**
   * @private
   * @type {'percent'}
   */
  static _PERCENT_UNIT = 'percent'
  /**
   * @private
   * @type {'bytes'}
   */
  static _BYTES_UNIT = 'bytes'
  /**
   * @private
   * @type {string}
   */
  static _HEALTH_LABEL = 'Health'
  /**
   * @private
   * @type {string}
   */
  static _STATE_LABEL = 'State'
  /**
   * @private
   * @type {string}
   */
  static _RESTARTS_LABEL = 'Restarts'
  /**
   * @private
   * @type {string}
   */
  static _REVISION_LABEL = 'Revision'
  /**
   * @private
   * @type {string}
   */
  static _IMAGE_LABEL = 'Image'
  /**
   * @private
   * @type {number}
   */
  static _COPY_RESET_DELAY = 1500
  /**
   * @private
   * @type {number}
   */
  static _LOG_POLL_DELAY = 2500
  /**
   * @private
   * @type {number}
   */
  static _METRICS_POLL_DELAY = 15000
  /**
   * @private
   * @type {number}
   */
  static _EVENT_LIMIT = 50
  /**
   * @private
   * @type {number}
   */
  static _STAMP_LENGTH = 19
  /**
   * @private
   * @type {number}
   */
  static _MILLISECONDS_PER_SECOND = 1000
  /**
   * @private
   * @type {number}
   */
  static _SECONDS_PER_MINUTE = 60
  /**
   * @private
   * @type {number}
   */
  static _PERCENT_MAXIMUM = 100
  /**
   * @private
   * @type {number}
   */
  static _LOG_TAIL_LIMIT = 300
  /**
   * @private
   * @type {number}
   */
  static _FOLLOW_MARGIN = 8
  /**
   * @private
   * @type {number}
   */
  static _SHORT_ID_LENGTH = 12
  /**
   * @private
   * @type {{name: string, seconds: number}[]}
   */
  static _AGE_UNITS = [{ name: 'day', seconds: 86400 }, { name: 'hour', seconds: 3600 }, { name: 'minute', seconds: 60 }]

  /** @type {{render: () => Promise<void>}} */
  @Needs
  taskScheduler;

  /** @type {AbortSignal} */
  @Needs
  lifecycle;

  /**
   * @private
   * @type {MutationObserver|null}
   */
  _observer = null;

  /**
   * @private
   * @type {{window: string}|null}
   */
  _metricsRead = null;

  /**
   * @private
   * @type {string}
   */
  static _PROCESS_GROUP = 'pids'
  /**
   * @private
   * @type {string}
   */
  static _MEMORY_LABEL = 'Memory'
  /**
   * @private
   * @type {string}
   */
  static _JUST_NOW_LABEL = 'just now'
  /**
   * @private
   * @type {string}
   */
  static _UNSCHEDULED_LABEL = 'unscheduled'

  /** @type {{value: string, label: string, className: string, current: string}[]} */
  windows;
  /** @type {{id: string, label: string, className: string, current: string}[]} */
  tabs;
  /** @type {boolean} */
  onMetrics;
  /** @type {boolean} */
  onLogs;
  /** @type {boolean} */
  onRuns;
  /** @type {boolean} */
  onInspect;
  /** @type {boolean} */
  onEvents;

  constructor() {
    const [, , project, app, , instanceID] = window.location.pathname.split(InstanceService._HOME_PATH).map(decodeURIComponent)
    /** @type {string} */
    this.project = project
    /** @type {string} */
    this.app = app
    /** @type {string} */
    this.instanceID = instanceID
    /** @type {string} */
    this.shortID = instanceID.slice(0, InstanceService._SHORT_ID_LENGTH)
    /** @type {string} */
    this.appHref = `/apps/${encodeURIComponent(project)}/${encodeURIComponent(app)}`
    /** @type {string} */
    this.service = this._queryValue(InstanceService._SERVICE_QUERY)
    /** @type {string} */
    this.serviceName = this._serviceName()
    /** @type {boolean} */
    this.busy = true
    /** @type {boolean} */
    this.loading = true
    /** @type {string} */
    this.pageError = ''
    /** @type {boolean} */
    this.gone = false
    /** @type {string} */
    this.image = InstanceService._EMPTY_VALUE
    /** @type {string} */
    this.statusText = InstanceService._UNKNOWN_STATE
    /** @type {string} */
    this.statusClass = InstanceService._VALUE_BASE
    /** @type {string} */
    this.copyLabel = InstanceService._COPY_ID_LABEL
    /** @type {string} */
    this.copyLogsLabel = InstanceService._COPY_LOGS_LABEL
    /** @type {{label: string, value: string, valueClass: string}[]} */
    this.tiles = []
    /** @type {import('/models/PanelDef.js').PanelDef[]} */
    this.panels = InstanceService._DEFAULT_PANELS
    /** @type {ReturnType<typeof setInterval>[]} */
    this.timers = []
    /** @type {import('/models/ChartMarker.js').ChartMarker[]} */
    this.markers = []
    /** @type {import('/models/Series.js').Series|null} */
    this.series = null
    /** @type {string} */
    this.logsText = ''
    /** @type {string} */
    this.logFilter = ''
    /** @type {import('/models/RunRow.js').RunRow[]} */
    this.executions = []
    /** @type {import('/models/RunRow.js').RunRow[]} */
    this.runs = []
    /** @type {number[]} */
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
    /** @type {import('/models/Instance.js').Instance|null} */
    this.instance = null

            /** @type {boolean} */
    this.scheduled = false
    /** @type {string} */
    this.schedule = ''
    /** @type {string} */
    this.nextRun = InstanceService._EMPTY_VALUE
    /** @type {boolean} */
    this.running = false
    /** @type {string} */
    this.runLabel = InstanceService._RUN_LABEL

    /** @type {string} */
    /** @type {string} */
    this.window = this._initialWindow()
    this.window = this._checkedWindow(this.window)
    this._applyWindows()

    /** @type {string} */
    this.tab = this._initialTab()
    this.tab = this._checkedTab(this.tab)
    this._applyTab()
  }

  /**
   * @param {Event} event
   * @returns {Promise<void>}
   */
  async selectWindow(event) {
    await this._chooseWindow(event)
  }

  /**
   * @param {Event} event
   * @returns {Promise<void>}
   */
  async selectTab(event) {
    await this._chooseTab(event)
  }

  /**
   * @returns {Promise<void>}
   */
  @OnStart
  async mount() {
    document.title = `${this.shortID} · ${this.app} — HEIMDALL`
    this._observer = new MutationObserver(() => this._wireActiveIslands())
    this._observer.observe(document.body, { childList: true, subtree: true })
    window.addEventListener('pagehide', (event) => this._pageHide(event), { signal: this.lifecycle })
    void this._start()
  }

  /**
   * @returns {void}
   */
  @OnStop
  stop() {
    this._stopPolling()
    this._observer?.disconnect()
    this._observer = null
    this._metricsRead = null
  }

  /**
   * @param {PageTransitionEvent} event
   * @returns {void}
   */
  _pageHide(event) {
    if (!event.persisted) this._stopPolling()
  }

  /**
   * @param {Event} event
   * @returns {Promise<void>}
   */
  async goToRunsPage(event) {
    await this._chooseRunsPage(pageFromEvent(event))
  }

  /**
   * @param {Event} event
   * @param {number} delta
   * @returns {Promise<void>}
   */
  async stepRuns(event, delta) {
    this.runsPage += delta
    this._paginateRuns()
    await this._renderDisplay()
  }

  /**
   * @param {Event} event
   * @returns {Promise<void>}
   */
  async filterLogs(event) {
    this.logFilter = /** @type {HTMLInputElement} */ (event.target).value
    this._writeLogs(document.getElementById(InstanceService._LOGS_ELEMENT), this._displayedLogs())
  }

  /**
   * @returns {Promise<void>}
   */
  async copyID() {
    await navigator.clipboard?.writeText(this.instanceID)
    this.copyLabel = InstanceService._COPIED_LABEL
    setTimeout(() => this._resetCopyLabel(), InstanceService._COPY_RESET_DELAY)
  }

  /**
   * @returns {Promise<void>}
   */
  async copyLogs() {
    await navigator.clipboard?.writeText(this._visibleLogs())
    this.copyLogsLabel = InstanceService._COPIED_LABEL
    setTimeout(() => this._resetLogsLabel(), InstanceService._COPY_RESET_DELAY)
  }

  /**
   * @returns {void}
   */
  jumpToLatest() {
    this._scrollLogs(document.getElementById(InstanceService._LOGS_ELEMENT))
  }

  /**
   * @returns {Promise<void>}
   */
  async runNow() {
    this.running = true
    this.runLabel = InstanceService._STARTING_LABEL
    await this._renderDisplay()
    await this._requestRun()
    this.running = false
    await this._renderDisplay()
    setTimeout(() => this._resetRunLabel(), InstanceService._LOG_POLL_DELAY)
  }

  /**
   * @returns {Promise<void>}
   */
  async signOut() {
    await session.logout()
    window.location.href = InstanceService._HOME_PATH
  }

  /**
   * @param {string} name
   * @returns {string}
   */
  _queryValue(name) {
    return new URLSearchParams(window.location.search).get(name) || ''
  }

  /**
   * @returns {string}
   */
  _serviceName() {
    return this.service || InstanceService._INSTANCE_LABEL
  }

  /**
   * @returns {string}
   */
  _initialWindow() {
    return this._queryValue(InstanceService._WINDOW_QUERY)
  }

  /**
   * @param {string|undefined} value
   * @returns {string}
   */
  _checkedWindow(value) {
    return InstanceService._WINDOWS.some((entry) => entry.value === value) ? value : InstanceService._DEFAULT_WINDOW
  }

  /**
   * @returns {string}
   */
  _initialTab() {
    return window.location.hash.slice(1)
  }

  /**
   * @param {string|undefined} value
   * @returns {string}
   */
  _checkedTab(value) {
    return InstanceService._TAB_DEFINITIONS.some((entry) => entry.id === value) ? value : InstanceService._METRICS_TAB
  }

  /**
   * @param {string|undefined} value
   * @returns {string}
   */
  _windowClass(value) {
    return value === this.window ? InstanceService._WINDOW_ACTIVE : InstanceService._WINDOW_IDLE
  }

  /**
   * @param {string|undefined} value
   * @returns {string}
   */
  _windowCurrent(value) {
    return value === this.window ? InstanceService._TRUE_VALUE : InstanceService._FALSE_VALUE
  }

  /**
   * @param {{value: string, label: string}} entry
   * @returns {{value: string, label: string, className: string, current: string}}
   */
  _windowEntry(entry) {
    return { value: entry.value, label: entry.label, className: this._windowClass(entry.value), current: this._windowCurrent(entry.value) }
  }

  /**
   * @returns {void}
   */
  _applyWindows() {
    this.windows = InstanceService._WINDOWS.map((entry) => this._windowEntry(entry))
  }

  /**
   * @param {Event} event
   * @returns {string|undefined}
   */
  _windowFromEvent(event) {
    return /** @type {HTMLElement} */ (/** @type {Element} */ (event.target)?.closest?.('[data-window]'))?.dataset?.window
  }

  /**
   * @param {Event} event
   * @returns {Promise<void>}
   */
  async _chooseWindow(event) {
    const chosen = this._windowFromEvent(event)
    await this._changeWindow(chosen)
  }

  /**
   * @param {string|undefined} chosen
   * @returns {Promise<void>}
   */
  async _changeWindow(chosen) {
    if (!chosen || chosen === this.window) return
    this.window = chosen
    this._applyWindows()
    this._writeWindowUrl(chosen)
    await this._renderDisplay()
    await this._paintMetrics()
  }

  /**
   * @param {string|undefined} chosen
   * @returns {void}
   */
  _writeWindowUrl(chosen) {
    const url = new URL(window.location.href)
    url.searchParams.set(InstanceService._WINDOW_QUERY, chosen)
    window.history.replaceState(null, '', url)
  }

  /**
   * @param {{id: string, label: string, scheduledOnly?: boolean}} entry
   * @returns {boolean}
   */
  _availableTab(entry) {
    return !entry.scheduledOnly || this.scheduled
  }

  /**
   * @param {string|undefined} value
   * @returns {string}
   */
  _tabClass(value) {
    return value === this.tab ? InstanceService._TAB_ACTIVE : InstanceService._TAB_IDLE
  }

  /**
   * @param {string|undefined} value
   * @returns {string}
   */
  _tabCurrent(value) {
    return value === this.tab ? 'page' : InstanceService._FALSE_VALUE
  }

  /**
   * @param {{id: string, label: string, scheduledOnly?: boolean}} entry
   * @returns {{id: string, label: string, className: string, current: string}}
   */
  _tabEntry(entry) {
    return { id: entry.id, label: entry.label, className: this._tabClass(entry.id), current: this._tabCurrent(entry.id) }
  }

  /**
   * @returns {void}
   */
  _applyTab() {
    this.tabs = InstanceService._TAB_DEFINITIONS.filter((entry) => this._availableTab(entry)).map((entry) => this._tabEntry(entry))
    this.onMetrics = this.tab === InstanceService._METRICS_TAB
    this.onLogs = this.tab === InstanceService._LOGS_TAB
    this.onRuns = this.tab === InstanceService._RUNS_TAB
    this.onInspect = this.tab === InstanceService._INSPECT_TAB
    this.onEvents = this.tab === InstanceService._EVENTS_TAB
  }

  /**
   * @param {Event} event
   * @returns {string|undefined}
   */
  _tabFromEvent(event) {
    return /** @type {HTMLElement} */ (event.currentTarget)?.dataset?.tab || /** @type {HTMLElement} */ (/** @type {Element} */ (event.target)?.closest?.('[data-tab]'))?.dataset?.tab
  }

  /**
   * @param {Event} event
   * @returns {Promise<void>}
   */
  async _chooseTab(event) {
    await this._changeTab(this._tabFromEvent(event))
  }

  /**
   * @param {string|undefined} id
   * @returns {Promise<void>}
   */
  async _changeTab(id) {
    if (!id) return
    this.tab = id
    this._applyTab()
    window.history.replaceState(null, '', `#${id}`)
    await this._renderDisplay()
    await this._paintLogs()
    await this._paintMetrics()
  }

  /**
   * @returns {Promise<void>}
   */
  async _renderDisplay() {
    await this._renderActiveDisplay()
    this._wireActiveIslands()
  }

  /**
   * @returns {Promise<void>}
   */
  async _renderActiveDisplay() {
    if (this.lifecycle.aborted) return
    await this.taskScheduler.render()
  }

  /**
   * @returns {void}
   */
  _wireActiveIslands() {
    if (this.lifecycle.aborted) return
    this._wireIslands()
  }

  /**
   * @returns {void}
   */
  _stopPolling() {
    this.timers.forEach(clearInterval)
    this.timers = []
  }

  /**
   * @returns {Promise<void>}
   */
  async _start() {
    await this._startSession(await session.active())
  }

  /**
   * @param {boolean} active
   * @returns {Promise<void>}
   */
  async _startSession(active) {
    if (!active) {
      window.location.href = InstanceService._HOME_PATH
      return
    }
    await this._activeDisplay()
  }

  /**
   * @returns {Promise<void>}
   */
  async _activeDisplay() {
    await this._loadDisplay()
    await Promise.all([this._paintMetrics(), this._paintLogs()])
    this._startPolling()
  }

  /**
   * @returns {Promise<void>}
   */
  async _loadDisplay() {
    try {
      await this._readDisplay()
    } catch (error) {
      this.pageError = error.message
    } finally {
      this.loading = false
      this.busy = false
      await this._renderDisplay()
    }
  }

  /**
   * @returns {Promise<void>}
   */
  async _readDisplay() {
    const [instancesAnswer, history, status, eventsAnswer] = await Promise.all([
      api.instances(this.project, this.app).catch(() => ({ instances: [] })),
      api.history(this.project, this.app).catch(() => ({ operations: [] })),
      api.app(this.project, this.app).catch(() => ({})),
      api.events(this.project, this.app).catch(() => ({ events: [] })),
    ])
    const instances = this._answeredInstances(instancesAnswer)
    const instance = this._findInstance(instances)
    this._readIdentity(instance)
    this.markers = this._deploymentMarkers(history)
    this._readSchedule(status)
    this._readRuns(instances)
    this.events = this._eventRows(eventsAnswer)
    this._readInstanceDisplay(instance)
    this._applyTab()
  }

  /**
   * @param {{instances?: import('/models/Instance.js').Instance[]}} answer
   * @returns {import('/models/Instance.js').Instance[]}
   */
  _answeredInstances(answer) {
    return answer.instances || []
  }

  /**
   * @param {import('/models/Instance.js').Instance[]} instances
   * @returns {import('/models/Instance.js').Instance|null}
   */
  _findInstance(instances) {
    return instances.find((candidate) => candidate.ref.instance === this.instanceID) || null
  }

  /**
   * @param {import('/models/Instance.js').Instance|null} instance
   * @returns {void}
   */
  _readIdentity(instance) {
    this._readInstanceService(instance)
    this.serviceName = this._serviceName()
    this.gone = !instance
    this.instance = instance
    this.image = this._image(instance)
  }

  /**
   * @param {import('/models/Instance.js').Instance|null} instance
   * @returns {void}
   */
  _readInstanceService(instance) {
    if (instance) this.service = instance.ref.service
  }

  /**
   * @param {import('/models/Instance.js').Instance|null} instance
   * @returns {string}
   */
  _image(instance) {
    return instance?.image || InstanceService._EMPTY_VALUE
  }

  /**
   * @param {import('/models/HistoryAnswer.js').HistoryAnswer} history
   * @returns {import('/models/ChartMarker.js').ChartMarker[]}
   */
  _deploymentMarkers(history) {
    return this._operations(history).filter((operation) => this._completedOperation(operation)).map((operation) => ({ at: operation.finished_at, label: shortRevision(operation.revision) }))
  }

  /**
   * @param {import('/models/Operation.js').Operation} operation
   * @returns {boolean}
   */
  _completedOperation(operation) {
    return !operation.dry_run && Boolean(operation.finished_at)
  }

  /**
   * @param {import('/models/HistoryAnswer.js').HistoryAnswer} history
   * @returns {import('/models/Operation.js').Operation[]}
   */
  _operations(history) {
    return history.operations || []
  }

  /**
   * @param {{events?: (import('/models/RuntimeEvent.js').RuntimeEvent & {reason?: string})[]}} answer
   * @returns {{at: string, message: string}[]}
   */
  _eventRows(answer) {
    return this._events(answer).slice(0, InstanceService._EVENT_LIMIT).map((entry) => ({ at: this._stamp(entry.at), message: this._eventMessage(entry) }))
  }

  /**
   * @param {{events?: (import('/models/RuntimeEvent.js').RuntimeEvent & {reason?: string})[]}} answer
   * @returns {(import('/models/RuntimeEvent.js').RuntimeEvent & {reason?: string})[]}
   */
  _events(answer) {
    return answer.events || []
  }

  /**
   * @param {import('/models/RuntimeEvent.js').RuntimeEvent & {reason?: string}} entry
   * @returns {string}
   */
  _eventMessage(entry) {
    return this._eventText(entry, this._eventReason(entry))
  }

  /**
   * @param {import('/models/RuntimeEvent.js').RuntimeEvent & {reason?: string}} entry
   * @returns {string}
   */
  _eventReason(entry) {
    return entry.reason || ''
  }

  /**
   * @param {import('/models/RuntimeEvent.js').RuntimeEvent & {reason?: string}} entry
   * @param {string} fallback
   * @returns {string}
   */
  _eventText(entry, fallback) {
    return entry.message || fallback
  }

  /**
   * @param {import('/models/Instance.js').Instance|null} instance
   * @returns {string}
   */
  _health(instance) {
    return instance?.health || InstanceService._UNKNOWN_STATE
  }

  /**
   * @param {import('/models/Instance.js').Instance|null} instance
   * @returns {string}
   */
  _state(instance) {
    return instance?.status || 'gone'
  }

  /**
   * @param {string} health
   * @returns {string}
   */
  _toneColor(health) {
    return InstanceService._TONE_TEXT[tone(health)] || ''
  }

  /**
   * @param {import('/models/Instance.js').Instance|null} instance
   * @param {string} state
   * @returns {string}
   */
  _statusText(instance, state) {
    return instance?.started_at ? `${state.charAt(0).toUpperCase()}${state.slice(1)} (${this._ago(instance.started_at)})` : state
  }

  /**
   * @param {import('/models/Instance.js').Instance|null} instance
   * @returns {void}
   */
  _readInstanceDisplay(instance) {
    const health = this._health(instance)
    const state = this._state(instance)
    this.statusText = this._statusText(instance, state)
    this.statusClass = `${InstanceService._VALUE_BASE} ${this._toneColor(health)}`.trim()
    this.tiles = this._tiles(instance, health, state)
    this.inspectRows = this._inspectRows(instance, health, state)
  }

  /**
   * @param {import('/models/Instance.js').Instance|null} instance
   * @returns {string}
   */
  _restartCount(instance) {
    return String(instance?.restarts ?? InstanceService._EMPTY_VALUE)
  }

  /**
   * @param {import('/models/Instance.js').Instance|null} instance
   * @param {string} health
   * @param {string} state
   * @returns {{label: string, value: string, valueClass: string}[]}
   */
  _tiles(instance, health, state) {
    return [
      this._tile(InstanceService._HEALTH_LABEL, health, health),
      this._tile(InstanceService._STATE_LABEL, state, ''),
      this._tile(InstanceService._RESTARTS_LABEL, this._restartCount(instance), ''),
      this._tile(InstanceService._STARTED_LABEL, this._stamp(instance?.started_at), ''),
      this._tile(InstanceService._REVISION_LABEL, shortRevision(instance?.revision), ''),
      this._tile(InstanceService._IMAGE_LABEL, this.image, ''),
    ]
  }

  /**
   * @param {import('/models/Instance.js').Instance|null} instance
   * @returns {string}
   */
  _revision(instance) {
    return instance?.revision || InstanceService._EMPTY_VALUE
  }

  /**
   * @param {import('/models/Instance.js').Instance|null} instance
   * @returns {string}
   */
  _node(instance) {
    return instance?.node || InstanceService._EMPTY_VALUE
  }

  /**
   * @param {import('/models/Instance.js').Instance|null} instance
   * @returns {string}
   */
  _exitCode(instance) {
    return instance?.exit_code === undefined ? InstanceService._EMPTY_VALUE : String(instance.exit_code)
  }

  /**
   * @returns {string}
   */
  _scheduleValue() {
    return this.schedule || InstanceService._EMPTY_VALUE
  }

  /**
   * @param {import('/models/Instance.js').Instance|null} instance
   * @param {string} health
   * @param {string} state
   * @returns {{label: string, value: string}[]}
   */
  _inspectRows(instance, health, state) {
    return [
      { label: 'Instance', value: this.instanceID },
      { label: 'Service', value: this.serviceName },
      { label: 'Application', value: `${this.project} / ${this.app}` },
      { label: InstanceService._IMAGE_LABEL, value: this.image },
      { label: InstanceService._REVISION_LABEL, value: this._revision(instance) },
      { label: 'Node', value: this._node(instance) },
      { label: InstanceService._HEALTH_LABEL, value: health },
      { label: InstanceService._STATE_LABEL, value: state },
      { label: InstanceService._RESTARTS_LABEL, value: this._restartCount(instance) },
      { label: InstanceService._STARTED_LABEL, value: this._stamp(instance?.started_at) },
      { label: 'Finished', value: this._stamp(instance?.finished_at) },
      { label: 'Exit code', value: this._exitCode(instance) },
      { label: 'Schedule', value: this._scheduleValue() },
    ]
  }

  /**
   * @param {string} label
   * @param {string|undefined} value
   * @param {string} health
   * @returns {{label: string, value: string, valueClass: string}}
   */
  _tile(label, value, health) {
    return { label, value, valueClass: this._tileClass(this._toneColor(health)) }
  }

  /**
   * @param {string} color
   * @returns {string}
   */
  _tileClass(color) {
    return color ? `${InstanceService._VALUE_BASE} ${color}` : InstanceService._VALUE_BASE
  }

  /**
   * @param {string|undefined} value
   * @returns {string}
   */
  _stamp(value) {
    return value ? String(value).replace('T', ' ').slice(0, InstanceService._STAMP_LENGTH) : InstanceService._EMPTY_VALUE
  }

  /**
   * @param {string|undefined} value
   * @returns {string}
   */
  _ago(value) {
    const seconds = Math.max(0, Math.floor((Date.now() - new Date(value).getTime()) / InstanceService._MILLISECONDS_PER_SECOND))
    return this._relativeAge(seconds, this._ageUnit(seconds))
  }

  /**
   * @param {number} seconds
   * @returns {{name: string, seconds: number}|undefined}
   */
  _ageUnit(seconds) {
    return InstanceService._AGE_UNITS.find((entry) => seconds >= entry.seconds)
  }

  /**
   * @param {number} seconds
   * @param {{name: string, seconds: number}|undefined} unit
   * @returns {string}
   */
  _relativeAge(seconds, unit) {
    const count = Math.floor(seconds / this._ageSize(unit))
    return this._ageText(unit, count, this._pluralSuffix(count))
  }

  /**
   * @param {{name: string, seconds: number}|undefined} unit
   * @returns {number}
   */
  _ageSize(unit) {
    return unit?.seconds || 1
  }

  /**
   * @param {{name: string, seconds: number}|undefined} unit
   * @param {number} count
   * @param {string} suffix
   * @returns {string}
   */
  _ageText(unit, count, suffix) {
    if (!unit) return InstanceService._JUST_NOW_LABEL
    return `${count} ${unit.name}${suffix} ago`
  }

  /**
   * @param {number} count
   * @returns {string}
   */
  _pluralSuffix(count) {
    return count === 1 ? '' : 's'
  }

  /**
   * @param {import('/models/AppDetail.js').AppDetail} detail
   * @returns {import('/models/ServiceDiff.js').ServiceDiff[]}
   */
  _scheduleEntries(detail) {
    return detail?.status?.services || []
  }

  /**
   * @param {import('/models/ServiceDiff.js').ServiceDiff[]} services
   * @returns {import('/models/ServiceDiff.js').ServiceDiff|undefined}
   */
  _scheduleEntry(services) {
    return services.find((entry) => entry.service === this.service)
  }

  /**
   * @param {import('/models/ServiceDiff.js').ServiceDiff|undefined} entry
   * @returns {string}
   */
  _scheduleText(entry) {
    return entry?.schedule || ''
  }

  /**
   * @param {import('/models/ServiceDiff.js').ServiceDiff|undefined} entry
   * @returns {string}
   */
  _nextRun(entry) {
    return this._nextRunText(entry, this._stamp(entry?.next_run_at))
  }

  /**
   * @param {import('/models/ServiceDiff.js').ServiceDiff|undefined} entry
   * @param {string} timestamp
   * @returns {string}
   */
  _nextRunText(entry, timestamp) {
    return entry?.next_run_at ? timestamp : InstanceService._UNSCHEDULED_LABEL
  }

  /**
   * @param {import('/models/AppDetail.js').AppDetail} detail
   * @returns {void}
   */
  _readSchedule(detail) {
    const entry = this._scheduleEntry(this._scheduleEntries(detail))
    this.scheduled = Boolean(entry?.schedule)
    this.schedule = this._scheduleText(entry)
    this.nextRun = this._nextRun(entry)
  }

  /**
   * @param {import('/models/Instance.js').Instance[]} instances
   * @returns {import('/models/Instance.js').Instance[]}
   */
  _finishedRuns(instances) {
    return instances.filter((candidate) => this._finishedService(candidate)).sort((left, right) => String(right.started_at).localeCompare(String(left.started_at)))
  }

  /**
   * @param {import('/models/Instance.js').Instance} candidate
   * @returns {boolean}
   */
  _finishedService(candidate) {
    return candidate.ref.service === this.service && Boolean(candidate.finished_at)
  }

  /**
   * @param {import('/models/Instance.js').Instance} run
   * @returns {number}
   */
  _durationSeconds(run) {
    return Math.max(0, Math.round((new Date(run.finished_at).getTime() - new Date(run.started_at).getTime()) / InstanceService._MILLISECONDS_PER_SECOND))
  }

  /**
   * @param {number} seconds
   * @returns {string}
   */
  _durationText(seconds) {
    return seconds >= InstanceService._SECONDS_PER_MINUTE ? `${Math.floor(seconds / InstanceService._SECONDS_PER_MINUTE)}m ${seconds % InstanceService._SECONDS_PER_MINUTE}s` : `${seconds}s`
  }

  /**
   * @param {import('/models/Instance.js').Instance} run
   * @returns {boolean}
   */
  _failedRun(run) {
    return run.exit_code !== undefined && run.exit_code !== 0
  }

  /**
   * @param {import('/models/Instance.js').Instance} run
   * @param {boolean} failed
   * @returns {string}
   */
  _runOutcome(run, failed) {
    return failed ? `exit ${run.exit_code}` : 'succeeded'
  }

  /**
   * @param {boolean} failed
   * @returns {string}
   */
  _outcomeClass(failed) {
    return failed ? InstanceService._TONE_TEXT.error : InstanceService._TONE_TEXT.success
  }

  /**
   * @param {import('/models/Instance.js').Instance} run
   * @returns {import('/models/RunRow.js').RunRow}
   */
  _runRow(run) {
    const seconds = this._durationSeconds(run)
    const failed = this._failedRun(run)
    return { started: this._stamp(run.started_at), duration: this._durationText(seconds), seconds, outcome: this._runOutcome(run, failed), outcomeClass: this._outcomeClass(failed), revision: shortRevision(run.revision) }
  }

  /**
   * @param {import('/models/Instance.js').Instance[]} instances
   * @returns {void}
   */
  _readRuns(instances) {
    this.executions = this._finishedRuns(instances).map((run) => this._runRow(run))
    this.durations = this.executions.map((run) => run.seconds).reverse()
    this.runsPage = 1
    this._paginateRuns()
  }

  /**
   * @returns {void}
   */
  _paginateRuns() {
    const paged = paginate(this.executions, this.runsPage, InstanceService._RUNS_PAGE_SIZE, { noun: InstanceService._RUNS_TAB })
    this.runsPage = paged.page
    this.runs = paged.rows
    this.showRunsPager = paged.show
    this.onFirstRun = paged.onFirst
    this.onLastRun = paged.onLast
    this.runsRange = paged.range
    this.runsPages = paged.pages
  }

  /**
   * @param {number|null} page
   * @returns {Promise<void>}
   */
  async _chooseRunsPage(page) {
    if (!page) return
    this.runsPage = page
    this._paginateRuns()
    await this._renderDisplay()
  }

  /**
   * @returns {void}
   */
  _startPolling() {
    if (this.lifecycle.aborted) return
    this._stopPolling()
    this.timers.push(setInterval(() => this._paintMetrics(), InstanceService._METRICS_POLL_DELAY), setInterval(() => this._paintLogs(), InstanceService._LOG_POLL_DELAY))
  }

  /**
   * @returns {void}
   */
  _wireIslands() {
    this.panels.forEach((definition) => this._wireChart(definition.id))
    this._wireLogs(document.getElementById(InstanceService._LOGS_ELEMENT))
  }

  /**
   * @param {string|undefined} id
   * @returns {void}
   */
  _wireChart(id) {
    const element = document.getElementById(id)
    this._wireChartElement(element, id)
  }

  /**
   * @param {HTMLElement|null} element
   * @param {string|undefined} id
   * @returns {void}
   */
  _wireChartElement(element, id) {
    if (!element || element.dataset.wired || this.lifecycle.aborted) return
    element.dataset.wired = InstanceService._WIRED_VALUE
    this._feedChart(id)
  }

  /**
   * @param {HTMLElement|null} element
   * @returns {void}
   */
  _wireLogs(element) {
    if (!element || element.dataset.wired || this.lifecycle.aborted) return
    element.dataset.wired = InstanceService._WIRED_VALUE
    this._initializeLogElement(element)
  }

  /**
   * @param {HTMLElement} element
   * @returns {void}
   */
  _initializeLogElement(element) {
    element.textContent = this._initialLogs()
    element.scrollTop = element.scrollHeight
  }

  /**
   * @returns {string}
   */
  _initialLogs() {
    return this._visibleLogs() || 'Loading…'
  }

  /**
   * @param {string|undefined} id
   * @returns {void}
   */
  _feedChart(id) {
    const element = /** @type {import('/models/HdTimeseriesElement.js').HdTimeseriesElement} */ (document.getElementById(id))
    this._drawChart(element, this._chartData(id, this.series))
  }

  /**
   * @param {import('/models/HdTimeseriesElement.js').HdTimeseriesElement|null} element
   * @param {import('/models/ChartData.js').ChartData|null} data
   * @returns {void}
   */
  _drawChart(element, data) {
    if (!element || !data || this.lifecycle.aborted) return
    element.series(data)
  }

  /**
   * @param {string|undefined} id
   * @param {import('/models/Series.js').Series|null} series
   * @returns {import('/models/ChartData.js').ChartData|null}
   */
  _chartData(id, series) {
    const data = this._selectedChart(id, this._chartSeries(series))
    return this._availableChart(series, data)
  }

  /**
   * @param {import('/models/Series.js').Series|null} series
   * @returns {import('/models/Series.js').Series}
   */
  _chartSeries(series) {
    return series || {}
  }

  /**
   * @param {import('/models/Series.js').Series|null} series
   * @param {import('/models/ChartData.js').ChartData|null} data
   * @returns {import('/models/ChartData.js').ChartData|null}
   */
  _availableChart(series, data) {
    return series ? data : null
  }

  /**
   * @param {string|undefined} id
   * @param {import('/models/Series.js').Series} series
   * @returns {import('/models/ChartData.js').ChartData|null}
   */
  _selectedChart(id, series) {
    switch (id) {
      case InstanceService._PROCESSOR_ELEMENT: return this._singleChart('CPU', InstanceService._PERCENT_UNIT, series.cpu_percent, 0)
      case 'hd-mem': return this._memoryChart(series)
      case 'hd-net': return this._pairChart('Network I/O — bytes/min', [{ label: 'in', points: series.net_rx_bytes }, { label: 'out', points: series.net_tx_bytes }])
      case 'hd-block': return this._pairChart('Block I/O — bytes/min', [{ label: 'read', points: series.block_read_bytes }, { label: 'write', points: series.block_write_bytes }])
      case 'hd-pids': return this._singleChart('PIDs', '', series.pids, 0)
      case 'hd-throttle': return this._singleChart('CPU throttling — periods/min', '', series.cpu_throttled, 0)
      case 'hd-neterr': return this._singleChart('Network errors — packets/min', '', series.net_errors, 0)
      case 'hd-memp': return this._memoryPercentChart(series)
    }
    return null
  }

  /**
   * @param {import('/models/ChartPoint.js').ChartPoint[]|undefined} points
   * @returns {import('/models/ChartPoint.js').ChartPoint[]}
   */
  _points(points) {
    return points || []
  }

  /**
   * @param {string} label
   * @param {''|'bytes'|'percent'} unit
   * @param {import('/models/ChartPoint.js').ChartPoint[]|undefined} points
   * @param {number} max
   * @returns {import('/models/ChartData.js').ChartData}
   */
  _singleChart(label, unit, points, max) {
    return { label, caption: false, unit, points: this._points(points), max, markers: this.markers }
  }

  /**
   * @param {import('/models/ChartLine.js').ChartLine} entry
   * @returns {import('/models/ChartLine.js').ChartLine}
   */
  _chartLine(entry) {
    return { label: entry.label, points: this._points(entry.points) }
  }

  /**
   * @param {string} label
   * @param {import('/models/ChartLine.js').ChartLine[]} lines
   * @returns {import('/models/ChartData.js').ChartData}
   */
  _pairChart(label, lines) {
    return { label, caption: false, unit: InstanceService._BYTES_UNIT, markers: this.markers, lines: lines.map((entry) => this._chartLine(entry)) }
  }

  /**
   * @param {import('/models/Series.js').Series} series
   * @returns {import('/models/ChartData.js').ChartData}
   */
  _memoryChart(series) {
    return this._singleChart(InstanceService._MEMORY_LABEL, InstanceService._BYTES_UNIT, series.memory_bytes, this._memoryLimit(series))
  }

  /**
   * @param {import('/models/Series.js').Series} series
   * @returns {number}
   */
  _memoryLimit(series) {
    return Number(series.memory_limit) || 0
  }

  /**
   * @param {import('/models/Series.js').Series} series
   * @param {number} limit
   * @returns {import('/models/ChartPoint.js').ChartPoint[]}
   */
  _memoryPercentPoints(series, limit) {
    return this._points(series.memory_bytes).map((point) => ({ at: point.at, value: (point.value / limit) * InstanceService._PERCENT_MAXIMUM }))
  }

  /**
   * @param {import('/models/Series.js').Series} series
   * @param {number} limit
   * @returns {import('/models/ChartPoint.js').ChartPoint[]}
   */
  _positiveMemoryPoints(series, limit) {
    return limit > 0 ? this._memoryPercentPoints(series, limit) : []
  }

  /**
   * @param {number} limit
   * @returns {number}
   */
  _percentMaximum(limit) {
    return limit > 0 ? InstanceService._PERCENT_MAXIMUM : 0
  }

  /**
   * @param {import('/models/Series.js').Series} series
   * @returns {import('/models/ChartData.js').ChartData}
   */
  _memoryPercentChart(series) {
    const limit = this._memoryLimit(series)
    return this._singleChart('Memory of limit', InstanceService._PERCENT_UNIT, this._positiveMemoryPoints(series, limit), this._percentMaximum(limit))
  }

  /**
   * @param {import('/models/Series.js').Series} series
   * @param {string} field
   * @returns {import('/models/ChartPoint.js').ChartPoint[]}
   */
  _seriesValue(series, field) {
    return series[field] || []
  }

  /**
   * @param {import('/models/Series.js').Series} series
   * @param {string} field
   * @returns {boolean}
   */
  _hasSamples(series, field) {
    return this._seriesValue(series, field).length > 0
  }

  /**
   * @param {import('/models/Series.js').Series} series
   * @returns {boolean}
   */
  _networkAnswered(series) {
    return this._hasSamples(series, 'net_rx_bytes') || this._hasSamples(series, 'net_tx_bytes')
  }

  /**
   * @param {import('/models/Series.js').Series} series
   * @returns {boolean}
   */
  _blockAnswered(series) {
    return this._hasSamples(series, 'block_read_bytes') || this._hasSamples(series, 'block_write_bytes')
  }

  /**
   * @param {Set<string>} answered
   * @param {string} group
   * @param {boolean} available
   * @returns {void}
   */
  _answerGroup(answered, group, available) {
    if (available) answered.add(group)
  }

  /**
   * @param {import('/models/Series.js').Series} series
   * @returns {Set<string>}
   */
  _defaultGroups(series) {
    const answered = new Set(['cpu', 'memory'])
    this._answerGroup(answered, 'network', this._networkAnswered(series))
    this._answerGroup(answered, 'block', this._blockAnswered(series))
    this._answerGroup(answered, InstanceService._PROCESS_GROUP, this._hasSamples(series, InstanceService._PROCESS_GROUP))
    return new Set(InstanceService._DOCKER_STATS_GROUPS.filter((group) => answered.has(group)))
  }

  /**
   * @returns {Promise<void>}
   */
  async _paintMetrics() {
    await Promise.all(this._activeReads(this.onMetrics).map(() => this._readMetrics()))
  }

  /**
   * @param {boolean} active
   * @returns {boolean[]}
   */
  _activeReads(active) {
    if (!active || this.lifecycle.aborted) return []
    return [true]
  }

  /**
   * @returns {Promise<void>}
   */
  async _readMetrics() {
    const request = { window: this.window }
    this._metricsRead = request
    const result = await this._requestMetrics(request)
    await this._acceptMetrics(request, result)
  }

  /**
   * @param {{window: string}} request
   * @returns {Promise<{answer: {series?: import('/models/Series.js').Series, metrics?: string[]}|null, error: Error|null}>}
   */
  async _requestMetrics(request) {
    try {
      const answer = await api.metrics(this.project, this.app, this.instanceID, this.service, request.window)
      return { answer, error: null }
    } catch (error) {
      return { answer: null, error }
    }
  }

  /**
   * @param {{window: string}} request
   * @param {{answer: {series?: import('/models/Series.js').Series, metrics?: string[]}|null, error: Error|null}} result
   * @returns {Promise<void>}
   */
  async _acceptMetrics(request, result) {
    if (request !== this._metricsRead || request.window !== this.window || this.lifecycle.aborted) return
    await this._displayMetricsResult(result)
  }

  /**
   * @param {{answer: {series?: import('/models/Series.js').Series, metrics?: string[]}|null, error: Error|null}} result
   * @returns {Promise<void>}
   */
  async _displayMetricsResult(result) {
    const actions = [this._metricsAnswerAction(result.answer), this._metricsErrorAction(result.error)]
    await this._performMetricsActions(actions)
  }

  /**
   * @param {{series?: import('/models/Series.js').Series, metrics?: string[]}|null} answer
   * @returns {(() => Promise<void>)|null}
   */
  _metricsAnswerAction(answer) {
    return answer ? () => this._applyMetrics(answer) : null
  }

  /**
   * @param {Error|null} error
   * @returns {(() => void)|null}
   */
  _metricsErrorAction(error) {
    return error ? () => this._metricsError(error) : null
  }

  /**
   * @param {((() => Promise<void>)|(() => void)|null)[]} actions
   * @returns {Promise<void>}
   */
  async _performMetricsActions(actions) {
    try {
      await Promise.all(actions.filter(Boolean).map((action) => action()))
    } catch (error) {
      this._metricsError(error)
    }
  }

  /**
   * @param {{series?: import('/models/Series.js').Series, metrics?: string[]}} answer
   * @returns {import('/models/Series.js').Series}
   */
  _answerSeries(answer) {
    return answer.series || {}
  }

  /**
   * @param {string[]|undefined} selection
   * @param {Set<string>} defaultGroups
   * @returns {Set<string>}
   */
  _selectedGroups(selection, defaultGroups) {
    return this._hasSelection(selection) ? new Set(selection) : defaultGroups
  }

  /**
   * @param {string[]|undefined} selection
   * @returns {boolean}
   */
  _hasSelection(selection) {
    return Boolean(selection?.length)
  }

  /**
   * @param {{series?: import('/models/Series.js').Series, metrics?: string[]}} answer
   * @returns {Promise<void>}
   */
  async _applyMetrics(answer) {
    this.series = this._answerSeries(answer)
    const chosen = this._selectedGroups(answer.metrics, this._defaultGroups(this.series))
    const panels = InstanceService._PANEL_DEFINITIONS.filter((definition) => chosen.has(definition.group))
    await this._applyPanels(panels)
  }

  /**
   * @param {import('/models/PanelDef.js').PanelDef[]} panels
   * @returns {Promise<void>}
   */
  async _applyPanels(panels) {
    if (this._panelKeys(panels) !== this._panelKeys(this.panels)) {
      this.panels = panels
      await this._renderDisplay()
      return
    }
    this._feedPanels()
  }

  /**
   * @param {import('/models/PanelDef.js').PanelDef[]} panels
   * @returns {string}
   */
  _panelKeys(panels) {
    return panels.map((panel) => panel.id).join('|')
  }

  /**
   * @returns {void}
   */
  _feedPanels() {
    this.panels.forEach((definition) => this._feedChart(definition.id))
  }

  /**
   * @param {Error} error
   * @returns {void}
   */
  _metricsError(error) {
    const element = /** @type {import('/models/HdTimeseriesElement.js').HdTimeseriesElement} */ (document.getElementById(InstanceService._PROCESSOR_ELEMENT))
    this._drawChart(element, { label: 'CPU — ' + error.message, points: [] })
  }

  /**
   * @returns {string}
   */
  _visibleLogs() {
    return this._filteredLogs(this.logsText, this.logFilter)
  }

  /**
   * @param {string} text
   * @param {string} filter
   * @returns {string}
   */
  _filteredLogs(text, filter) {
    if (!filter) return text
    return this._matchingLines(text, filter)
  }

  /**
   * @param {string} text
   * @param {string} filter
   * @returns {string}
   */
  _matchingLines(text, filter) {
    const needle = filter.toLowerCase()
    return text.split(InstanceService._LINE_BREAK).filter((line) => line.toLowerCase().includes(needle)).join(InstanceService._LINE_BREAK)
  }

  /**
   * @returns {string}
   */
  _displayedLogs() {
    return this._visibleLogs() || InstanceService._NO_MATCHING_LINES
  }

  /**
   * @param {HTMLElement|null} element
   * @param {string} text
   * @returns {void}
   */
  _writeLogs(element, text) {
    if (element && !this.lifecycle.aborted) element.textContent = text
  }

  /**
   * @param {HTMLElement|null} element
   * @returns {void}
   */
  _scrollLogs(element) {
    if (element && !this.lifecycle.aborted) element.scrollTop = element.scrollHeight
  }

  /**
   * @returns {void}
   */
  _resetCopyLabel() {
    this.copyLabel = InstanceService._COPY_ID_LABEL
    void this._renderDisplay()
  }

  /**
   * @returns {void}
   */
  _resetLogsLabel() {
    this.copyLogsLabel = InstanceService._COPY_LOGS_LABEL
    void this._renderDisplay()
  }

  /**
   * @returns {void}
   */
  _resetRunLabel() {
    this.runLabel = InstanceService._RUN_LABEL
    void this._renderDisplay()
  }

  /**
   * @returns {Promise<void>}
   */
  async _requestRun() {
    try {
      await api.runService(this.project, this.app, this.service)
      this.runLabel = InstanceService._STARTED_LABEL
    } catch (error) {
      this.pageError = error.message
      this.runLabel = InstanceService._RUN_LABEL
    }
  }

  /**
   * @returns {Promise<void>}
   */
  async _paintLogs() {
    await Promise.all(this._activeReads(this.onLogs).map(() => this._readLogs()))
  }

  /**
   * @returns {Promise<void>}
   */
  async _readLogs() {
    try {
      await this._loadLogs()
    } catch (error) {
      this._logsError(error)
    }
  }

  /**
   * @returns {Promise<void>}
   */
  async _loadLogs() {
    const text = await api.logs(this.project, this.app, this.instanceID, this.service, InstanceService._LOG_TAIL_LIMIT)
    this.logsText = this._logOutput(text)
    this._replaceLogs(document.getElementById(InstanceService._LOGS_ELEMENT))
  }

  /**
   * @param {Error} error
   * @returns {void}
   */
  _logsError(error) {
    this._writeLogs(document.getElementById(InstanceService._LOGS_ELEMENT), 'logs unavailable: ' + error.message)
  }

  /**
   * @param {string} text
   * @returns {string}
   */
  _logOutput(text) {
    return text || '(no output)'
  }

  /**
   * @param {HTMLElement|null} element
   * @returns {void}
   */
  _replaceLogs(element) {
    const text = this._displayedLogs()
    this._logElements(element).forEach((entry) => this._replaceLogElement(entry, text))
  }

  /**
   * @param {HTMLElement} element
   * @param {string} text
   * @returns {void}
   */
  _replaceLogElement(element, text) {
    const follow = element.scrollTop + element.clientHeight >= element.scrollHeight - InstanceService._FOLLOW_MARGIN
    element.textContent = text
    this._followLogs(element, follow)
  }

  /**
   * @param {HTMLElement|null} element
   * @returns {HTMLElement[]}
   */
  _logElements(element) {
    if (!element || this.lifecycle.aborted) return []
    return [element]
  }

  /**
   * @param {HTMLElement|null} element
   * @param {boolean} follow
   * @returns {void}
   */
  _followLogs(element, follow) {
    if (follow && !this.lifecycle.aborted) element.scrollTop = element.scrollHeight
  }
}
