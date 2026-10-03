import { api, session, tone, shortRevision } from '/shared/scripts/api.js'
import { paginate, pageFromEvent } from '/shared/scripts/pager.js'

/** @typedef {import('/shared/scripts/hd-apptree.js').HdAppTreeElement} HdAppTreeElement */

/**
 * @typedef {object} ActivityRow
 * @property {string} id
 * @property {string} at
 * @property {string} when
 * @property {string} source
 * @property {string} sourceChipClass
 * @property {string} kind
 * @property {string} phase
 * @property {string} phaseChipClass
 * @property {string} revision
 * @property {string} principal
 * @property {string} principalShort
 * @property {string} detail
 */
import '/shared/scripts/hd-apptree.js'
import '/shared/scripts/hd-timeseries.js'

document.title = `${decodeURIComponent(window.location.pathname.split('/')[3] || '')} — HEIMDALL`

// chip builds the DuVay class string for a health/status label. Classes are
// data here: the bounded template language applies them with :class.
function chip(label) {
  return { label, chipClass: `w-chip w-chip-filled w-chip-${tone(label)}` }
}

function when(stamp) {
  return (stamp || '').replace('T', ' ').slice(0, 19)
}

// The page companion, Tac's declarative shape: the markup lives in tac.html
// with <if>/<else>/<loop> control tags; this class holds data and behaviour.
// The one imperative island is <hd-apptree>, an SVG-drawing web component
// the class re-feeds after every render, because a re-render recreates it.
// Activity paging. Ten rows is what fits without scrolling on a laptop, and
// the total stays bounded — both sources are already bounded, and an
// unbounded table is a slow page nobody reads the end of.
/** @type {number} Rows per page — what fits without scrolling on a laptop. */
const ACTIVITY_PAGE_SIZE = 10
/** @type {number} How many merged entries the page holds at once. */
const ACTIVITY_MAX = 200

export default class AppPage {
  static __tachyonOnMount = ['mount']

  constructor() {
    const [, , project, app] = window.location.pathname.split('/').map(decodeURIComponent)
    this.project = project
    this.app = app
    this.busy = true
    this.loading = true
    this.pageError = ''
    this.acting = false
    this.action = null
    this.status = null
    this.view = window.location.hash === '#view=table' ? 'table' : 'tree'
    this.term = ''
    this.instances = []
    this.visibleInstances = []
    this.noInstances = true
    /** @type {ActivityRow[]} Every entry held, newest first. */
    this.activity = []
    /** @type {ActivityRow[]} The page the table shows. */
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
    /** @type {import('/shared/scripts/hd-apptree.js').TreeData|null} */
    this.treeGraph = null
  }

  /**
   * @returns {Promise<void>}
   */
  async rerender() {
    await globalThis.__tc_rerender?.()
    this.wireTree()
  }

  // wireTree feeds the tree island. The runtime re-renders after every
  // handled event — after our own rerender — so the element is recreated
  // behind our back; the MutationObserver in mount catches every fresh one
  // and feeds it. The wired flag is set before graph() so the observer does
  // not loop on the mutations the drawing itself makes.
  /**
   * @returns {void}
   */
  wireTree() {
    const tree = /** @type {HdAppTreeElement} */ (document.getElementById('hd-apptree'))
    if (!tree || !this.treeGraph || tree.dataset.wired) return
    tree.dataset.wired = '1'
    tree.graph(this.treeGraph)
    if (this.term) tree.filter(this.term.trim().toLowerCase())
    tree.addEventListener('nodeselect', (/** @type {CustomEvent} */ event) => {
      const { kind, id, service } = event.detail
      if (kind === 'pod') {
        window.location.href = `/apps/${encodeURIComponent(this.project)}/${encodeURIComponent(this.app)}/i/${encodeURIComponent(id)}?service=${encodeURIComponent(service || '')}`
      }
    })
  }

  /**
   * @returns {Promise<void>}
   */
  async mount() {
    // The runtime awaits onMount before the first render, so this kicks the
    // data flow off without blocking; the first paint shows the loading
    // state and the re-render fills in the live one.
    new MutationObserver(() => this.wireTree())
      .observe(document.body, { childList: true, subtree: true })
    void this.start()
  }

  /**
   * Loads the application and paints it.
   *
   * @returns {Promise<void>}
   */
  async start() {
    if (!(await session.active())) {
      window.location.href = '/'
      return
    }
    await this.refresh()
  }

  /**
   * Re-reads status, history, instances, and events.
   *
   * @returns {Promise<void>}
   */
  async refresh() {
    this.busy = true
    this.pageError = ''
    try {
      const [detail, history, instancesAnswer, eventsAnswer] = await Promise.all([
        api.app(this.project, this.app),
        api.history(this.project, this.app),
        api.instances(this.project, this.app).catch(() => ({ instances: [] })),
        api.events(this.project, this.app).catch(() => ({ events: [] })),
      ])
      this.fold(detail, history, instancesAnswer.instances || [], eventsAnswer.events || [])
      this.loading = false
    } catch (error) {
      this.pageError = error.message
      this.loading = false
    } finally {
      this.busy = false
      await this.rerender()
    }
  }

  // fold shapes the four answers into template-ready fields: the bounded
  // expression language deliberately has no calls, so every derived string
  // is computed here.
  /**
   * Turns four answers into the page's fields.
   *
   * @param {import('/shared/scripts/api.js').AppDetail} detail
   * @param {import('/shared/scripts/api.js').HistoryAnswer} history
   * @param {import('/shared/scripts/api.js').Instance[]} instances
   * @param {import('/shared/scripts/api.js').RuntimeEvent[]} events
   * @returns {void}
   */
  fold(detail, history, instances, events) {
    const status = detail.status || {}
    this.status = {
      chips: [chip(status.sync_status), chip(status.health)],
      desired: shortRevision(status.desired_revision),
      live: shortRevision(status.live_revision),
      target: status.target || '—',
    }

    this.instances = instances.map((instance) => ({
      service: instance.ref.service,
      ...chip(instance.health || 'Unknown'),
      health: instance.health || 'Unknown',
      status: instance.status || '',
      revision: shortRevision(instance.revision),
      restarts: instance.restarts ? `${instance.restarts} restarts` : '—',
      started: when(instance.started_at),
      href: `/apps/${encodeURIComponent(this.project)}/${encodeURIComponent(this.app)}/i/${encodeURIComponent(instance.ref.instance)}?service=${encodeURIComponent(instance.ref.service)}`,
      ariaLabel: `open ${instance.ref.service} dashboard`,
      haystack: `${instance.ref.service} ${instance.status || ''} ${shortRevision(instance.revision)}`.toLowerCase(),
    }))
    this.applyFilter()

    // Operations and runtime events merge into one chronological table:
    // "what happened around 14:07" is one question. Event rows leave the
    // operation-only cells empty.
    // The activity index is the history table's source: one page is a chunk
    // read on the control plane, not a scan of every operation. `operations`
    // carries the same page as whole documents and is still there for
    // anything that needs a detail field.
    const entries = []
    for (const entry of history.activity || []) {
      const row = this.activityRow(entry)
      if (row) entries.push(row)
    }
    for (const event of events) {
      entries.push({
        at: event.at, when: when(event.at),
        source: event.source || 'runtime', sourceChipClass: 'w-chip',
        kind: event.type || 'event', phase: '', phaseChipClass: '',
        revision: '', principal: '', principalShort: '',
        detail: `${event.service ? event.service + ': ' : ''}${event.message || ''}`,
      })
    }
    entries.sort((a, b) => new Date(b.at).getTime() - new Date(a.at).getTime())
    // Bounded, because both sources are bounded and an unbounded table is a
    // slow page nobody scrolls to the end of. What changes is that the tail
    // is now reachable: it pages instead of being cut off at 40.
    this.activity = entries.slice(0, ACTIVITY_MAX)
    this.activityCursor = history.next_cursor || ''
    this.activityTruncated = Boolean(history.truncated)
    this.activityPage = 1
    this.paginateActivity()

    this.revisionOptions = (history.revisions || []).slice(0, 20).map((revision) => ({
      value: revision.commit,
      label: `${shortRevision(revision.commit)} — ${revision.message || ''}`,
    }))
    this.noRevisions = this.revisionOptions.length === 0
    if (!this.revisionOptions.some((option) => option.value === this.pickedRevision)) {
      this.pickedRevision = this.revisionOptions[0]?.value || ''
    }

    this.treeGraph = this.foldTree(detail, instances)
  }

  /**
   * Builds the resource tree's graph.
   *
   * @param {import('/shared/scripts/api.js').AppDetail} detail
   * @param {import('/shared/scripts/api.js').Instance[]} instances
   * @returns {import('/shared/scripts/hd-apptree.js').TreeData}
   */
  foldTree(detail, instances) {
    const status = detail.status || {}
    // status.services is the diff summary's ARRAY of per-service entries —
    // index it by name before joining with the topology, or every lookup
    // silently misses and a healthy app reads Missing-red.
    const liveByName = {}
    for (const entry of status.services || []) {
      liveByName[entry.service] = entry
    }
    const runningByService = {}
    for (const instance of instances) {
      runningByService[instance.ref.service] = (runningByService[instance.ref.service] || 0) + 1
    }
    const services = (detail.topology || []).map((service) => {
      const live = liveByName[service.name] || {}
      return {
        name: service.name, image: service.image, dependsOn: service.depends_on || [],
        health: live.health || 'Missing',
        ready: runningByService[service.name] || 0,
        replicas: service.replicas || 1,
      }
    })
    return {
      app: this.app, health: status.health, syncStatus: status.sync_status, services,
      instances: instances.map((instance) => ({
        service: instance.ref.service, id: instance.ref.instance,
        health: instance.health, status: instance.status,
      })),
    }
  }

  /**
   * @returns {void}
   */
  applyFilter() {
    const term = this.term.trim().toLowerCase()
    this.visibleInstances = term === ''
      ? this.instances
      : this.instances.filter((instance) => instance.haystack.includes(term))
    this.noInstances = this.visibleInstances.length === 0
  }

  // activityRow renders one index entry as a table row. It is a method
  // because two paths build them: the first read, and every older page the
  // cursor pulls in.
  activityRow(entry) {
    if (!entry?.at) return null
    const automated = ['auto_sync', 'self_heal', 'resumed_after_reconnect'].includes(entry.reason_code)
    let detailText = entry.message || ''
    if (entry.phase === 'failed' && entry.message) {
      detailText = entry.message.slice(0, 140)
    } else if (automated) {
      detailText = `(${entry.reason_code})`
    }
    const phase = entry.phase === 'succeeded' ? 'Healthy' : entry.phase === 'failed' ? 'Degraded' : 'Progressing'
    return {
      id: entry.operation_id || '',
      at: entry.at,
      when: when(entry.at),
      source: automated ? 'automation' : 'heimdall',
      sourceChipClass: automated ? 'w-chip w-chip-secondary' : 'w-chip w-chip-primary',
      kind: entry.kind || 'sync',
      phase,
      phaseChipClass: `w-chip w-chip-filled w-chip-${tone(phase)}`,
      revision: shortRevision(entry.revision),
      principal: entry.principal_id || '',
      principalShort: (entry.principal_id || '').slice(0, 16),
      detail: detailText,
    }
  }

  // paginateActivity derives everything the template branches on: the rows
  // for this page, the numbered items with their DuVay classes, and the
  // "showing 1–10 of 63" line. Tac renders fields, not expressions, so the
  // active class and the aria-current string are computed here.
  /**
   * Derives the rows, page items, and range line for the current page.
   *
   * @returns {void}
   */
  paginateActivity() {
    const paged = paginate(this.activity, this.activityPage, ACTIVITY_PAGE_SIZE, { noun: 'entries' })
    this.activityPage = paged.page
    this.activityRows = paged.rows
    this.noActivity = paged.empty
    this.showPager = paged.show
    this.onFirstPage = paged.onFirst
    this.onLastPage = paged.onLast
    this.activityRange = paged.range
    this.activityPages = paged.pages
  }

  /**
   * @param {Event} event
   * @returns {Promise<void>}
   */
  async goToPage(event) {
    const page = pageFromEvent(event)
    if (!page) return
    this.activityPage = page
    this.paginateActivity()
    // Reaching the last page pulls the next one from the store, so the table
    // walks the whole history rather than the slice the first read returned.
    if (this.onLastPage) void this.loadOlderActivity()
    await this.rerender()
    document.getElementById('hd-activity')?.scrollIntoView({ block: 'start', behavior: 'smooth' })
  }

  /**
   * Handlers are called as (event, ...declaredArguments).
   *
   * @param {Event} event
   * @param {number} delta
   * @returns {Promise<void>}
   */
  async stepActivity(event, delta) {
    this.activityPage += delta
    this.paginateActivity()
    await this.rerender()
    if (this.onLastPage) void this.loadOlderActivity()
  }

  // loadOlderActivity fetches the next page of operations by cursor and
  // appends it. Runtime events are not paged — they are a bounded read from
  // the runtime itself, and there is no older page of them to ask for.
  async loadOlderActivity() {
    if (!this.activityCursor || this.loadingOlder) return
    this.loadingOlder = true
    try {
      const older = await api.history(this.project, this.app, { before: this.activityCursor })
      this.activityCursor = older.next_cursor || ''
      const rows = (older.activity || []).map((entry) => this.activityRow(entry)).filter(Boolean)
      if (rows.length === 0) return
      const known = new Set(this.activity.map((entry) => entry.id).filter(Boolean))
      this.activity = this.activity
        .concat(rows.filter((row) => !known.has(row.id)))
        .sort((left, right) => new Date(right.at).getTime() - new Date(left.at).getTime())
        .slice(0, ACTIVITY_MAX)
      this.paginateActivity()
      await this.rerender()
    } catch (error) {
      this.pageError = error.message
      await this.rerender()
    } finally {
      this.loadingOlder = false
    }
  }

  /**
   * @param {Event} event
   * @returns {Promise<void>}
   */
  async filter(event) {
    this.term = /** @type {HTMLInputElement} */ (event?.target)?.value || ''
    this.applyFilter()
    await this.rerender()
  }

  // Event handlers are called as (event, ...declaredArguments) — the
  // runtime prepends the DOM event to whatever the binding names.
  /**
   * @param {Event} event
   * @param {'tree'|'table'} view
   * @returns {Promise<void>}
   */
  async switchView(event, view) {
    this.view = view
    window.location.hash = view === 'table' ? '#view=table' : '#view=tree'
    await this.rerender()
  }

  /**
   * @param {Event} event
   * @returns {void}
   */
  openRow(event) {
    const href = /** @type {HTMLElement} */ (
      /** @type {Element} */ (event?.target)?.closest?.('[data-href]'))?.dataset?.href
    if (href) window.location.href = href
  }

  /**
   * @param {KeyboardEvent} event
   * @returns {void}
   */
  rowKey(event) {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      this.openRow(event)
    }
  }

  // act runs a mutating call with the working card up, then folds the
  // operation into the result card and re-reads live state.
  /**
   * Runs one action and reports its outcome in the action region.
   *
   * @param {() => Promise<import('/shared/scripts/api.js').Operation>} runner
   * @param {string} title
   * @returns {Promise<void>}
   */
  async act(runner, title) {
    this.acting = true
    this.action = { working: true }
    await this.rerender()
    try {
      const operation = await runner()
      this.action = {
        working: false,
        title: `${title} — ${operation.phase}`,
        message: operation.message || '',
        planned: (operation.operations || [])
          .filter((step) => step.kind !== 'noop')
          .map((step) => ({ kind: step.kind, service: step.service, reason: step.reason || '' })),
        applied: (operation.applied || []).map((step) => ({ kind: step.kind, service: step.service })),
        failures: Object.entries(operation.failures || {}).map(([service, reason]) => ({ service, reason })),
      }
    } catch (error) {
      // The outcome belongs in the action card, not the page banner: the
      // operator asked for this run and looks here for its answer.
      this.action = { working: false, title: `${title} — failed`, message: error.message }
    } finally {
      this.acting = false
      await this.refresh()
    }
  }

  /**
   * @returns {void}
   */
  dryRun() {
    void this.act(() => api.sync(this.project, this.app, { dry_run: true }), 'Dry run')
  }

  /**
   * @returns {void}
   */
  sync() {
    void this.act(() => api.sync(this.project, this.app, {}), 'Sync')
  }

  pickRevision(event) {
    this.pickedRevision = /** @type {HTMLSelectElement} */ (event?.target)?.value || ''
  }

  // Rolling back is deliberate enough to earn a second click: the first
  // exposes a confirm beside it. Inline rather than window.confirm, which
  // blocks the page and cannot say which revision it means.
  async rollbackArm() {
    if (!this.pickedRevision) return
    this.rollbackArmed = true
    await this.rerender()
  }

  async rollbackCancel() {
    this.rollbackArmed = false
    await this.rerender()
  }

  async rollbackConfirm() {
    const revision = this.pickedRevision
    this.rollbackArmed = false
    if (revision) await this.act(() => api.rollback(this.project, this.app, revision), 'Rollback')
  }

  async signOut() {
    await session.logout()
    window.location.href = '/'
  }
}
