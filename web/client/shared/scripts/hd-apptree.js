// <hd-apptree> — the application resource tree, ArgoCD-style.
//
// Left to right: the application node, then services in dependency order —
// a service sits one column right of everything it depends_on, so the arrow
// direction reads "starts before" — then the running instances each service
// produced. Node cards carry a kind badge, the name, a detail line, and a
// health dot in the same tone vocabulary the chips use.
//
// Rendered as inline SVG from DuVay tokens, no dependency, no canvas — the
// same decisions as <hd-timeseries>, for the same reasons.

const CARD_W = 200
const CARD_H = 56
const GAP_X = 90
const GAP_Y = 18
const PAD = 16

function token(name, fallback) {
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  return value || fallback
}

function healthColor(health) {
  switch (health) {
    case 'Healthy':
    case 'Synced':
      return token('--w-success', '#1b7f3b')
    case 'Progressing':
      return token('--w-primary', '#3b5bdb')
    case 'Degraded':
    case 'OutOfSync':
      return token('--w-warning', '#b26b00')
    case 'Missing':
      return token('--w-error', '#b3261e')
    default:
      return token('--w-text-muted', '#888')
  }
}

function truncate(text, max) {
  text = String(text ?? '')
  return text.length > max ? text.slice(0, max - 1) + '…' : text
}

function escapeXML(value) {
  return String(value ?? '').replace(/[&<>"']/g, (c) =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c])
}

// depthOf assigns each service a column: one right of its deepest
// dependency. A cycle cannot happen — render rejects dependency cycles — but
// the guard bounds the walk anyway rather than trusting that forever.
function depthOf(name, byName, memo, guard = 0) {
  if (memo.has(name)) return memo.get(name)
  const service = byName.get(name)
  if (!service || guard > 32) return 0
  let depth = 0
  for (const dependency of service.dependsOn || []) {
    depth = Math.max(depth, depthOf(dependency, byName, memo, guard + 1) + 1)
  }
  memo.set(name, depth)
  return depth
}

class HdAppTree extends HTMLElement {
  graph(input) {
    this._data = input
    this.render()
  }

  // filter dims nodes that do not match a search term.
  filter(term) {
    this._term = (term || '').toLowerCase()
    this.render()
  }

  render() {
    const data = this._data
    if (!data) return
    const term = this._term || ''

    const services = data.services || []
    const byName = new Map(services.map((s) => [s.name, s]))
    const memo = new Map()
    const columns = new Map() // depth → [nodes]

    for (const service of services) {
      const depth = depthOf(service.name, byName, memo)
      if (!columns.has(depth)) columns.set(depth, [])
      columns.get(depth).push({ kind: 'svc', ...service })
    }
    const serviceDepths = Math.max(0, ...columns.keys()) + 1

    // Instances live one column right of the deepest services, grouped under
    // their service.
    const instanceColumn = serviceDepths + 1
    const nodes = []
    const edges = []

    // Root application node, vertically centred later.
    const root = {
      kind: 'app', name: data.app, detail: data.syncStatus || '',
      health: data.health || 'Unknown', column: 0, id: 'app',
    }
    nodes.push(root)

    // Service nodes: columns shift right by one to leave room for the root.
    const yCursor = new Map()
    const place = (node, column) => {
      const y = (yCursor.get(column) || 0)
      node.x = PAD + column * (CARD_W + GAP_X)
      node.y = PAD + y * (CARD_H + GAP_Y)
      yCursor.set(column, y + 1)
      nodes.push(node)
    }

    for (const [depth, list] of [...columns.entries()].sort((a, b) => a[0] - b[0])) {
      for (const service of list.sort((a, b) => a.name.localeCompare(b.name))) {
        const node = {
          kind: 'svc', id: 'svc:' + service.name, name: service.name,
          detail: truncate(service.image, 26), health: service.health || 'Unknown',
          ready: service.ready, replicas: service.replicas,
        }
        place(node, depth + 1)
        const dependencies = service.dependsOn || []
        if (dependencies.length === 0) {
          edges.push(['app', node.id])
        }
        for (const dependency of dependencies) {
          edges.push(['svc:' + dependency, node.id])
        }
      }
    }

    for (const instance of data.instances || []) {
      const node = {
        kind: 'pod', id: 'ins:' + instance.id, name: truncate(instance.id, 22),
        detail: instance.status || '', health: instance.health || 'Unknown',
        rawID: instance.id, service: instance.service,
      }
      place(node, instanceColumn)
      edges.push(['svc:' + instance.service, node.id])
    }

    // Root position: centred against the first service column.
    const firstColumnCount = yCursor.get(1) || 1
    root.x = PAD
    root.y = PAD + ((firstColumnCount - 1) * (CARD_H + GAP_Y)) / 2

    const width = PAD * 2 + (instanceColumn + 1) * (CARD_W + GAP_X) - GAP_X
    const height = PAD * 2 + Math.max(1, ...yCursor.values()) * (CARD_H + GAP_Y) - GAP_Y

    const surface = token('--w-surface', '#fff')
    const border = token('--w-border', '#ccc')
    const text = token('--w-text', '#111')
    const muted = token('--w-text-muted', '#666')

    const byID = new Map(nodes.map((n) => [n.id, n]))
    const matches = (node) =>
      !term || node.name.toLowerCase().includes(term) || (node.detail || '').toLowerCase().includes(term)

    const edgeSVG = edges.map(([from, to]) => {
      const a = byID.get(from)
      const b = byID.get(to)
      if (!a || !b) return ''
      const x1 = a.x + CARD_W
      const y1 = a.y + CARD_H / 2
      const x2 = b.x
      const y2 = b.y + CARD_H / 2
      const mid = (x1 + x2) / 2
      const dim = term && !(matches(a) && matches(b))
      return `<path d="M${x1},${y1} C${mid},${y1} ${mid},${y2} ${x2},${y2}"
        fill="none" stroke="${border}" stroke-width="1.5" opacity="${dim ? 0.25 : 1}"/>`
    }).join('')

    const kindLabel = { app: 'APP', svc: 'SVC', pod: 'RUN' }
    const nodeSVG = nodes.map((node) => {
      const color = healthColor(node.health)
      const dim = term && !matches(node)
      const readyLine = node.kind === 'svc' && node.replicas
        ? `<text x="${node.x + CARD_W - 10}" y="${node.y + 22}" text-anchor="end" fill="${muted}" font-size="10">${node.ready ?? '?'}/${node.replicas}</text>`
        : ''
      return `<g opacity="${dim ? 0.3 : 1}" data-kind="${node.kind}" data-node="${escapeXML(node.rawID || node.name)}" data-service="${escapeXML(node.service || '')}" class="${node.kind === 'pod' ? 'hd-node--link' : ''}">
        <rect x="${node.x}" y="${node.y}" width="${CARD_W}" height="${CARD_H}" rx="8"
          fill="${surface}" stroke="${dim ? border : color}" stroke-width="1.5"/>
        <rect x="${node.x}" y="${node.y}" width="34" height="${CARD_H}" rx="8" fill="${color}" opacity="0.12"/>
        <text x="${node.x + 17}" y="${node.y + CARD_H / 2 + 3}" text-anchor="middle"
          fill="${color}" font-size="9" font-weight="700">${kindLabel[node.kind]}</text>
        <text x="${node.x + 42}" y="${node.y + 22}" fill="${text}" font-size="12" font-weight="600">${escapeXML(truncate(node.name, 20))}</text>
        <text x="${node.x + 42}" y="${node.y + 40}" fill="${muted}" font-size="10">${escapeXML(node.detail || '')}</text>
        <circle cx="${node.x + CARD_W - 12}" cy="${node.y + CARD_H - 12}" r="5" fill="${color}">
          <title>${escapeXML(node.health)}</title>
        </circle>
        ${readyLine}
      </g>`
    }).join('')

    this.innerHTML = `
      <div class="hd-tree-scroll overflow-x-auto pb-2">
        <svg class="max-w-none" viewBox="0 0 ${width} ${height}" width="${width}" height="${height}"
             role="img" aria-label="resource tree for ${escapeXML(data.app)}">
          ${edgeSVG}
          ${nodeSVG}
        </svg>
      </div>`

    // One listener for the whole tree: instance cards open their dashboard.
    this.querySelector('svg').addEventListener('click', (event) => {
      const group = event.target.closest('g[data-kind]')
      if (!group) return
      this.dispatchEvent(new CustomEvent('nodeselect', {
        detail: {
          kind: group.dataset.kind,
          id: group.dataset.node,
          service: group.dataset.service,
        },
      }))
    })
  }
}

if (!customElements.get('hd-apptree')) {
  customElements.define('hd-apptree', HdAppTree)
}
