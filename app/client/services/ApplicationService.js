/** @typedef {import('/models/ActivityEntry.js').ActivityEntry} ActivityEntry */
/** @typedef {import('/models/ActivityRow.js').ActivityRow} ActivityRow */
/** @typedef {import('/models/AppDetail.js').AppDetail} AppDetail */
/** @typedef {import('/models/ApplicationInstanceRow.js').ApplicationInstanceRow} ApplicationInstanceRow */
/** @typedef {import('/models/ApplicationRuntimeActivityRow.js').ApplicationRuntimeActivityRow} ApplicationRuntimeActivityRow */
/** @typedef {import('/models/ApplicationStatusDisplay.js').ApplicationStatusDisplay} ApplicationStatusDisplay */
/** @typedef {import('/models/ApplicationTopologyService.js').ApplicationTopologyService} ApplicationTopologyService */
/** @typedef {import('/models/FunctionDisplayRow.js').FunctionDisplayRow} FunctionDisplayRow */
/** @typedef {import('/models/FunctionInvocationState.js').FunctionInvocationState} FunctionInvocationState */
/** @typedef {import('/models/HdAppTreeElement.js').HdAppTreeElement} HdAppTreeElement */
/** @typedef {import('/models/HistoryAnswer.js').HistoryAnswer} HistoryAnswer */
/** @typedef {import('/models/Instance.js').Instance} Instance */
/** @typedef {import('/models/Operation.js').Operation} Operation */
/** @typedef {import('/models/RevisionSummary.js').RevisionSummary} RevisionSummary */
/** @typedef {import('/models/RuntimeEvent.js').RuntimeEvent} RuntimeEvent */
/** @typedef {import('/models/ServiceDiff.js').ServiceDiff} ServiceDiff */
/** @typedef {import('/models/StatusAnswer.js').StatusAnswer} StatusAnswer */
/** @typedef {import('/models/TreeData.js').TreeData} TreeData */
/** @typedef {import('/models/TreeService.js').TreeService} TreeService */

import FunctionDisplayUtils from '/shared/utils/FunctionDisplayUtils.js'
import FunctionInvocationUtils from '/shared/utils/FunctionInvocationUtils.js'
import { api, session, tone, shortRevision } from '/shared/scripts/api.js'
import { paginate, pageFromEvent } from '/shared/scripts/pager.js'
import '/shared/scripts/hd-apptree.js'
import '/shared/scripts/hd-timeseries.js'

@Service
export default class ApplicationService {
  /** @type {{render: () => Promise<void>}} */
  @Needs
  taskScheduler;
  /** @type {MutationObserver|null} */
  #observer = null;

  /** @private @returns {number} */
  static get _activityPageSize() { return 10 }
  /** @private @returns {number} */
  static get _activityLimit() { return 200 }
  /** @private @returns {string} */
  static get _tableHash() { return '#view=table' }
  /** @private @returns {string} */
  static get _treeHash() { return '#view=tree' }
  /** @private @returns {string} */
  static get _tableMode() { return 'table' }
  /** @private @returns {string} */
  static get _unknownStatus() { return 'Unknown' }
  /** @private @returns {string} */
  static get _missingValue() { return '—' }
  /** @private @returns {string} */
  static get _failedPhase() { return 'failed' }
  /** @private @returns {string} */
  static get _space() { return ' ' }
  constructor() {
    const [, , project, app] = window.location.pathname.split('/').map(decodeURIComponent)
    this.project = project
    this.app = app
    this.busy = true
    this.loading = true
    this.pageError = ''
    this.acting = false
    this.action = null
    /** @type {ApplicationStatusDisplay|null} */
    this.status = null
    this.view = this.#initialView()
    this.term = ''
    /** @type {ApplicationInstanceRow[]} */
    this.instances = []
    /** @type {ApplicationInstanceRow[]} */
    this.visibleInstances = []
    this.noInstances = true
 /** @type {FunctionDisplayRow[]} */
 this.functions = []
 this.noFunctions = true
 this.hasDirectFunctions = false
 this.pickedFunction = ''
 this.functionPayload = '{}'
 this.invoking = false
 /** @type {FunctionInvocationState} */
 this.functionResult = { error: '', output: '', version: '', requestID: '', outcome: '' }
    /** @type {(ActivityRow|ApplicationRuntimeActivityRow)[]} Every entry held, newest first. */
    this.activity = []
    /** @type {(ActivityRow|ApplicationRuntimeActivityRow)[]} The page the table shows. */
    this.activityRows = []
    /** @type {boolean} */
    this.noActivity = true
    /** @type {number} */
    this.activityPage = 1
    /** @type {string} Keyset cursor for the next page from the store. */
    this.activityCursor = ''
    /** @type {boolean} True when the control plane's scan stopped early. */
    this.activityTruncated = false
    /** @type {boolean} Guards against two overlapping older-page fetches. */
    this.loadingOlder = false
    /** @type {object[]} */
    this.activityPages = []
    /** @type {string} */
    this.activityRange = ''
    /** @type {boolean} */
    this.onFirstPage = true
    /** @type {boolean} */
    this.onLastPage = true
    /** @type {boolean} */
    this.showPager = false
    this.revisionOptions = []
    this.noRevisions = true
    this.pickedRevision = ''
    this.rollbackArmed = false
    /** @type {TreeData|null} */
    this.treeGraph = null
  }

  /** @returns {Promise<void>} */
  @OnStart
  async mount() {
    document.title = `${this.app} — HEIMDALL`
    this.#observer = new MutationObserver(() => this.#wireTree())
    this.#observer.observe(document.body, { childList: true, subtree: true })
    void this.#start()
  }

  /** @returns {void} */
  @OnStop
  stop() { this.#observer?.disconnect() }

  /** @returns {Promise<void>} */
  async #renderDisplay() {
    await this.taskScheduler.render()
    this.#wireTree()
  }

  /** @returns {void} */
  #wireTree() {
    const tree = /** @type {HdAppTreeElement} */ (document.getElementById('hd-apptree'))
    this.#when([Boolean(tree), Boolean(this.treeGraph), !tree?.dataset.wired].every(Boolean), () => this.#bindTree(tree))
  }

  /** @param {HdAppTreeElement} tree @returns {void} */
  #bindTree(tree) {
    tree.dataset.wired = '1'
    tree.graph(this.treeGraph)
    this.#when(Boolean(this.term), () => tree.filter(this.term.trim().toLowerCase()))
    tree.addEventListener('nodeselect', (event) => this.#selectNode(/** @type {CustomEvent} */ (event)))
  }

  /** @param {CustomEvent} event @returns {void} */
  #selectNode(event) {
    const { kind, id, service } = event.detail
    const href = this.#instanceHref(id, this.#value(service, ''))
    this.#when(kind === 'pod', () => { window.location.href = href })
  }

  /** @returns {Promise<void>} */
  async #start() {
    const active = await session.active()
    await this.#choose(active, () => this.#refresh(), () => { window.location.href = '/' })
  }

  /** @returns {Promise<void>} */
  async #refresh() {
    this.busy = true
    this.pageError = ''
    await this.#readApplication()
    this.loading = false
    this.busy = false
    await this.#renderDisplay()
  }

  /** @returns {Promise<void>} */
  async #readApplication() {
    try {
      const [detail, history, instances, events] = await Promise.all([
        api.app(this.project, this.app), api.history(this.project, this.app),
        api.instances(this.project, this.app).catch(() => ({ instances: [] })),
        api.events(this.project, this.app).catch(() => ({ events: [] })),
      ])
      this.#fold(detail, history, instances.instances, events.events)
    } catch (error) { this.pageError = error.message }
  }

  /** @param {AppDetail} detail @param {HistoryAnswer} history @param {Instance[]} instances @param {RuntimeEvent[]} events @returns {void} */
  #fold(detail, history, instances, events) {
    this.functions = FunctionDisplayUtils.rows(detail)
    this.noFunctions = FunctionDisplayUtils.empty(this.functions)
    this.hasDirectFunctions = FunctionDisplayUtils.invokable(this.functions)
    this.status = this.#status(this.#value(detail.status, {}))
    const present = this.#value(instances, [])
    this.instances = present.map((instance) => this.#instanceRow(instance))
    this.#applyFilter()
    /** @type {(ActivityRow|ApplicationRuntimeActivityRow)[]} */
    const entries = this.#value(history.activity, []).map((entry) => this.#activityRow(entry)).filter(Boolean)
    const runtime = this.#value(events, []).map((event) => this.#eventRow(event))
    this.activity = entries.concat(runtime).sort((a, b) => this.#newestFirst(a, b)).slice(0, ApplicationService._activityLimit)
    this.activityCursor = this.#value(history.next_cursor, '')
    this.activityTruncated = Boolean(history.truncated)
    this.activityPage = 1
    this.#paginateActivity()
    this.revisionOptions = this.#value(history.revisions, []).slice(0, 20).map((revision) => this.#revisionRow(revision))
    this.noRevisions = this.revisionOptions.length === 0
    const retained = this.revisionOptions.some((option) => option.value === this.pickedRevision)
    const first = this.#value(this.revisionOptions[0]?.value, '')
    this.#when(!retained, () => { this.pickedRevision = first })
    this.treeGraph = this.#foldTree(detail, present)
  }

  /** @param {StatusAnswer} status @returns {ApplicationStatusDisplay} */
  #status(status) {
    return { chips: [this.#chip(status.sync_status), this.#chip(status.health)],
      desired: shortRevision(status.desired_revision), live: shortRevision(status.live_revision),
      target: this.#value(status.target, ApplicationService._missingValue) }
  }

  /** @param {Instance} instance @returns {ApplicationInstanceRow} */
  #instanceRow(instance) {
    const health = this.#value(instance.health, ApplicationService._unknownStatus)
    const status = this.#value(instance.status, '')
    return { service: instance.ref.service, ...this.#chip(health), health, status,
      revision: shortRevision(instance.revision), restarts: this.#restarts(instance.restarts),
      started: this.#whenText(instance.started_at), href: this.#instanceHref(instance.ref.instance, instance.ref.service),
      ariaLabel: `open ${instance.ref.service} dashboard`,
      haystack: `${instance.ref.service} ${status} ${shortRevision(instance.revision)}`.toLowerCase() }
  }

  /** @param {number} value @returns {string} */
  #restarts(value) { return value ? `${value} restarts` : ApplicationService._missingValue }

  /** @param {string} id @param {string} service @returns {string} */
  #instanceHref(id, service) {
    return `/apps/${encodeURIComponent(this.project)}/${encodeURIComponent(this.app)}/i/${encodeURIComponent(id)}?service=${encodeURIComponent(service)}`
  }

  /** @param {RuntimeEvent} event @returns {ApplicationRuntimeActivityRow} */
  #eventRow(event) {
    const prefix = this.#eventPrefix(event.service)
    return { at: event.at, when: this.#whenText(event.at), source: this.#value(event.source, 'runtime'), sourceChipClass: 'w-chip',
      kind: this.#value(event.type, 'event'), phase: '', phaseChipClass: '', revision: '', principal: '', principalShort: '',
      detail: `${prefix}${this.#value(event.message, '')}` }
  }

  /** @param {string} service @returns {string} */
  #eventPrefix(service) { return service ? `${service}: ` : '' }

  /** @param {ActivityEntry} entry @returns {ActivityRow|null} */
  #activityRow(entry) {
    const available = Boolean(entry?.at)
    return this.#choose(available, () => this.#describeActivity(entry), () => null)
  }

  /** @param {ActivityEntry} entry @returns {ActivityRow} */
  #describeActivity(entry) {
    const automated = ['auto_sync', 'self_heal', 'resumed_after_reconnect'].includes(entry.reason_code)
    const message = this.#value(entry.message, '')
    const failed = [entry.phase === ApplicationService._failedPhase, Boolean(message)].every(Boolean)
    const detail = this.#activityDetail(message, { failed, automated, reason: entry.reason_code })
    const phase = this.#phase(entry.phase)
    const principal = this.#value(entry.principal_id, '')
    return { id: this.#value(entry.operation_id, ''), at: entry.at, when: this.#whenText(entry.at),
      source: this.#activitySource(automated), sourceChipClass: this.#activitySourceClass(automated),
      kind: this.#value(entry.kind, 'sync'), phase, phaseChipClass: `w-chip w-chip-filled w-chip-${tone(phase)}`,
      revision: shortRevision(entry.revision), principal, principalShort: principal.slice(0, 16), detail }
  }

  /** @param {string} message @param {{failed: boolean, automated: boolean, reason?: string}} flags @returns {string} */
  #activityDetail(message, flags) {
    const automated = this.#automatedDetail(message, flags)
    return this.#failedDetail(automated, message, flags.failed)
  }
  /** @param {string} message @param {{failed: boolean, automated: boolean, reason?: string}} flags @returns {string} */
  #automatedDetail(message, flags) { return flags.automated ? `(${flags.reason})` : message }
  /** @param {string} detail @param {string} message @param {boolean} failed @returns {string} */
  #failedDetail(detail, message, failed) { return failed ? message.slice(0, 140) : detail }
  /** @param {boolean} automated @returns {string} */
  #activitySource(automated) { return automated ? 'automation' : 'heimdall' }
  /** @param {boolean} automated @returns {string} */
  #activitySourceClass(automated) { return automated ? 'w-chip w-chip-secondary' : 'w-chip w-chip-primary' }
  /** @param {string} value @returns {string} */
  #phase(value) {
    switch (value) {
      case 'succeeded': return 'Healthy'
      case ApplicationService._failedPhase: return 'Degraded'
      default: return 'Progressing'
    }
  }

  /** @param {RevisionSummary} revision @returns {{value: string, label: string}} */
  #revisionRow(revision) {
    return { value: revision.commit, label: `${shortRevision(revision.commit)} — ${this.#value(revision.message, '')}` }
  }

  /** @param {AppDetail} detail @param {Instance[]} instances @returns {TreeData} */
  #foldTree(detail, instances) {
    const status = this.#value(detail.status, {})
    const live = Object.fromEntries(this.#value(status.services, []).map((entry) => [entry.service, entry]))
    const running = this.#runningByService(instances)
    const services = this.#value(detail.topology, []).map((service) => this.#treeService(service, live, running))
    return { app: this.app, health: status.health, syncStatus: status.sync_status, services,
      instances: instances.map((instance) => ({ service: instance.ref.service, id: instance.ref.instance, health: instance.health, status: instance.status })) }
  }

  /** @param {Instance[]} instances @returns {Record<string, number>} */
  #runningByService(instances) {
    /** @type {Record<string, number>} */
    const counts = {}
    for (const instance of instances) counts[instance.ref.service] = Number(this.#value(counts[instance.ref.service], 0)) + 1
    return counts
  }

  /** @param {ApplicationTopologyService} service @param {Record<string, Partial<ServiceDiff>>} live @param {Record<string, number>} running @returns {TreeService} */
  #treeService(service, live, running) {
    /** @type {Partial<ServiceDiff>} */
    const observed = this.#value(live[service.name], {})
    return { name: service.name, image: service.image, dependsOn: this.#value(service.depends_on, []),
      health: this.#value(observed.health, 'Missing'), ready: this.#value(running[service.name], 0),
      replicas: this.#value(service.replicas, 1), function: service.function }
  }

  /** @returns {void} */
  #applyFilter() {
    this.visibleInstances = this.#filtered(this.term.trim().toLowerCase())
    this.noInstances = this.visibleInstances.length === 0
    this.resourceHeading = this.#resourceHeading()
    this.emptyResources = this.#emptyResources()
  }
  /** @param {string} term @returns {ApplicationInstanceRow[]} */
  #filtered(term) { return term === '' ? this.instances : this.instances.filter((instance) => instance.haystack.includes(term)) }
  /** @returns {string} */
  #resourceHeading() { return this.noFunctions ? 'Instances' : 'Resources' }
  /** @returns {string} */
  #emptyResources() { return this.noFunctions ? 'Nothing is running' : 'No execution instances are reported' }

  /** @returns {void} */
  #paginateActivity() {
    const paged = paginate(this.activity, this.activityPage, ApplicationService._activityPageSize, { noun: 'entries' })
    this.activityPage = paged.page
    this.activityRows = paged.rows
    this.noActivity = paged.empty
    this.showPager = paged.show
    this.onFirstPage = paged.onFirst
    this.onLastPage = paged.onLast
    this.activityRange = paged.range
    this.activityPages = paged.pages
  }

  /** @param {Event} event @returns {Promise<void>} */
  async goToPage(event) {
    const page = pageFromEvent(event)
    await this.#when(Boolean(page), () => this.#changePage(page))
  }
  /** @param {number} page @returns {Promise<void>} */
  async #changePage(page) {
    this.activityPage = page
    this.#paginateActivity()
    this.#when(this.onLastPage, () => { void this.#loadOlderActivity() })
    await this.#renderDisplay()
    document.getElementById('hd-activity')?.scrollIntoView({ block: 'start', behavior: 'smooth' })
  }
  /** @param {Event} event @param {number} delta @returns {Promise<void>} */
  async stepActivity(event, delta) {
    this.activityPage += delta
    this.#paginateActivity()
    await this.#renderDisplay()
    this.#when(this.onLastPage, () => { void this.#loadOlderActivity() })
  }

  /** @returns {Promise<void>} */
  async #loadOlderActivity() {
    const available = [Boolean(this.activityCursor), !this.loadingOlder].every(Boolean)
    await this.#when(available, () => this.#readOlderActivity())
  }
  /** @returns {Promise<void>} */
  async #readOlderActivity() {
    this.loadingOlder = true
    await this.#fetchOlderActivity()
    this.loadingOlder = false
  }
  /** @returns {Promise<void>} */
  async #fetchOlderActivity() {
    try {
      const older = await api.history(this.project, this.app, { before: this.activityCursor })
      this.#mergeOlder(older)
      await this.#renderDisplay()
    } catch (error) {
      this.pageError = error.message
      await this.#renderDisplay()
    }
  }
  /** @param {HistoryAnswer} older @returns {void} */
  #mergeOlder(older) {
    this.activityCursor = this.#value(older.next_cursor, '')
    const rows = this.#value(older.activity, []).map((entry) => this.#activityRow(entry)).filter(Boolean)
    const known = new Set(this.activity.map((entry) => entry.id).filter(Boolean))
    this.activity = this.activity.concat(rows.filter((row) => !known.has(row.id)))
      .sort((left, right) => this.#newestFirst(left, right)).slice(0, ApplicationService._activityLimit)
    this.#paginateActivity()
  }

  /** @param {Event} event @returns {Promise<void>} */
  async filter(event) {
    this.term = this.#value(/** @type {HTMLInputElement} */ (event?.target)?.value, '')
    this.#applyFilter()
    await this.#renderDisplay()
  }
  /** @param {Event} event @param {'tree'|'table'} view @returns {Promise<void>} */
  async switchView(event, view) {
    this.view = view
    window.location.hash = this.#viewHash(view)
    await this.#renderDisplay()
  }
  /** @param {string} view @returns {string} */
  #viewHash(view) { return view === ApplicationService._tableMode ? ApplicationService._tableHash : ApplicationService._treeHash }
  /** @returns {string} */
  #initialView() { return window.location.hash === ApplicationService._tableHash ? ApplicationService._tableMode : 'tree' }

  /** @param {Event} event @returns {void} */
  openRow(event) {
    const href = /** @type {HTMLElement} */ (/** @type {Element} */ (event?.target)?.closest?.('[data-href]'))?.dataset?.href
    this.#when(Boolean(href), () => { window.location.href = href })
  }
  /** @param {KeyboardEvent} event @returns {void} */
  rowKey(event) {
    this.#when(['Enter', ApplicationService._space].includes(event.key), () => { event.preventDefault(); this.openRow(event) })
  }

  /** @param {() => Promise<Operation>} runner @param {string} title @returns {Promise<void>} */
  async #act(runner, title) {
    this.acting = true
    this.action = { working: true }
    await this.#renderDisplay()
    await this.#performAction(runner, title)
    this.acting = false
    await this.#refresh()
  }
  /** @param {() => Promise<Operation>} runner @param {string} title @returns {Promise<void>} */
  async #performAction(runner, title) {
    try { this.action = this.#operationResult(await runner(), title) }
    catch (error) { this.action = { working: false, title: `${title} — failed`, message: error.message } }
  }
  /** @param {Operation} operation @param {string} title @returns {object} */
  #operationResult(operation, title) {
    const steps = this.#value(operation.operations, []).filter((step) => step.kind !== 'noop')
    return { working: false, title: `${title} — ${operation.phase}`, message: this.#value(operation.message, ''),
      planned: steps.map((step) => ({ kind: step.kind, service: step.service, reason: this.#value(step.reason, '') })),
      applied: this.#value(operation.applied, []).map((step) => ({ kind: step.kind, service: step.service })),
      failures: Object.entries(this.#value(operation.failures, {})).map(([service, reason]) => ({ service, reason })) }
  }

  /** @returns {void} */
  dryRun() { void this.#act(() => api.sync(this.project, this.app, { dry_run: true }), 'Dry run') }
  /** @returns {void} */
  sync() { void this.#act(() => api.sync(this.project, this.app, {}), 'Sync') }
  /** @param {Event} event @returns {void} */
  pickRevision(event) { this.pickedRevision = this.#value(/** @type {HTMLSelectElement} */ (event?.target)?.value, '') }
  /** @returns {Promise<void>} */
  async rollbackArm() { await this.#when(Boolean(this.pickedRevision), () => this.#armRollback()) }
  /** @returns {Promise<void>} */
  async #armRollback() { this.rollbackArmed = true; await this.#renderDisplay() }
  /** @returns {Promise<void>} */
  async rollbackCancel() { this.rollbackArmed = false; await this.#renderDisplay() }
  /** @returns {Promise<void>} */
  async rollbackConfirm() {
    const revision = this.pickedRevision
    this.rollbackArmed = false
    await this.#when(Boolean(revision), () => this.#act(() => api.rollback(this.project, this.app, revision), 'Rollback'))
  }
  /** @returns {Promise<void>} */
  async signOut() { await session.logout(); window.location.href = '/' }
  /** @param {Event} event @returns {void} */
  pickFunction(event) { this.pickedFunction = /** @type {HTMLSelectElement} */ (event.target).value }
  /** @param {Event} event @returns {void} */
  editFunctionPayload(event) { this.functionPayload = /** @type {HTMLTextAreaElement} */ (event.target).value }
  /** @returns {Promise<void>} */
  async invokeFunction() {
    this.invoking = true
    await this.#renderDisplay()
    this.functionResult = await FunctionInvocationUtils.send(api, { project: this.project, app: this.app, service: this.pickedFunction, text: this.functionPayload })
    this.invoking = false
    await this.#renderDisplay()
  }

  /** @template T @param {T} value @param {T} fallback @returns {T} */
  #value(value, fallback) { return value || fallback }
  /** @param {boolean} condition @param {() => any} action @returns {any} */
  #when(condition, action) { if (condition) return action() }
  /** @param {boolean} condition @param {() => any} action @param {() => any} alternative @returns {any} */
  #choose(condition, action, alternative) { if (condition) return action(); return alternative() }
  /** @param {string} label @returns {{label: string, chipClass: string}} */
  #chip(label) { return { label, chipClass: `w-chip w-chip-filled w-chip-${tone(label)}` } }
  /** @param {string} value @returns {string} */
  #whenText(value) { return this.#value(value, '').replace('T', ApplicationService._space).slice(0, 19) }
  /** @param {{at: string}} left @param {{at: string}} right @returns {number} */
  #newestFirst(left, right) { return new Date(right.at).getTime() - new Date(left.at).getTime() }
}
