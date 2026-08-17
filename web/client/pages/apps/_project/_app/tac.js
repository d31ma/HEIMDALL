import { api, session, tone, shortRevision } from '/shared/scripts/api.js'
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
    this.activityRows = []
    this.noActivity = true
    this.revisionOptions = []
    this.noRevisions = true
    this.pickedRevision = ''
    this.rollbackArmed = false
    this.treeGraph = null
  }

  async rerender() {
    await globalThis.__tc_rerender?.()
    this.wireTree()
  }

  // wireTree feeds the tree island. The runtime re-renders after every
  // handled event — after our own rerender — so the element is recreated
  // behind our back; the MutationObserver in mount catches every fresh one
  // and feeds it. The wired flag is set before graph() so the observer does
  // not loop on the mutations the drawing itself makes.
  wireTree() {
    const tree = document.getElementById('hd-apptree')
    if (!tree || !this.treeGraph || tree.dataset.wired) return
    tree.dataset.wired = '1'
    tree.graph(this.treeGraph)
    if (this.term) tree.filter(this.term.trim().toLowerCase())
    tree.addEventListener('nodeselect', (event) => {
      const { kind, id, service } = event.detail
      if (kind === 'pod') {
        window.location.href = `/apps/${encodeURIComponent(this.project)}/${encodeURIComponent(this.app)}/i/${encodeURIComponent(id)}?service=${encodeURIComponent(service || '')}`
      }
    })
  }

  async mount() {
    // The runtime awaits onMount before the first render, so this kicks the
    // data flow off without blocking; the first paint shows the loading
    // state and the re-render fills in the live one.
    new MutationObserver(() => this.wireTree())
      .observe(document.body, { childList: true, subtree: true })
    void this.start()
  }

  async start() {
    if (!(await session.active())) {
      window.location.href = '/'
      return
    }
    await this.refresh()
  }

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
    const entries = []
    for (const operation of history.operations || []) {
      if (!operation.started_at) continue
      const automated = ['auto_sync', 'self_heal', 'resumed_after_reconnect'].includes(operation.reason_code)
      let detailText = operation.message || ''
      if (operation.phase === 'failed' && operation.message) {
        detailText = operation.message.slice(0, 140)
      } else if (automated) {
        detailText = `(${operation.reason_code})`
      }
      const phase = operation.phase === 'succeeded' ? 'Healthy' : operation.phase === 'failed' ? 'Degraded' : 'Progressing'
      entries.push({
        at: operation.started_at,
        when: when(operation.started_at),
        source: automated ? 'automation' : 'heimdall',
        sourceChipClass: automated ? 'w-chip w-chip-secondary' : 'w-chip w-chip-primary',
        kind: operation.dry_run ? 'dry run' : operation.rollback ? 'rollback' : 'sync',
        phase,
        phaseChipClass: `w-chip w-chip-filled w-chip-${tone(phase)}`,
        revision: shortRevision(operation.revision),
        principal: operation.principal_id || '',
        principalShort: (operation.principal_id || '').slice(0, 16),
        detail: detailText,
      })
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
    entries.sort((a, b) => new Date(b.at) - new Date(a.at))
    this.activityRows = entries.slice(0, 40)
    this.noActivity = this.activityRows.length === 0

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

  applyFilter() {
    const term = this.term.trim().toLowerCase()
    this.visibleInstances = term === ''
      ? this.instances
      : this.instances.filter((instance) => instance.haystack.includes(term))
    this.noInstances = this.visibleInstances.length === 0
  }

  async filter(event) {
    this.term = event?.target?.value || ''
    this.applyFilter()
    await this.rerender()
  }

  // Event handlers are called as (event, ...declaredArguments) — the
  // runtime prepends the DOM event to whatever the binding names.
  async switchView(event, view) {
    this.view = view
    window.location.hash = view === 'table' ? '#view=table' : '#view=tree'
    await this.rerender()
  }

  openRow(event) {
    const href = event?.target?.closest?.('[data-href]')?.dataset?.href
    if (href) window.location.href = href
  }

  rowKey(event) {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      this.openRow(event)
    }
  }

  // act runs a mutating call with the working card up, then folds the
  // operation into the result card and re-reads live state.
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

  dryRun() {
    void this.act(() => api.sync(this.project, this.app, { dry_run: true }), 'Dry run')
  }

  sync() {
    void this.act(() => api.sync(this.project, this.app, {}), 'Sync')
  }

  pickRevision(event) {
    this.pickedRevision = event?.target?.value || ''
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
