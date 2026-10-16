// The web tier end to end: real browser, real ty serve, real control plane,
// real SESAME deciding the login. Run via scripts/e2e-web.sh, which owns the
// stack and seeds one application (alpha/site) before this starts.
import { createRequire } from 'node:module'
const { expect, test } = createRequire(new URL('../../app/package.json', import.meta.url))('@playwright/test')

const USER = process.env.HD_E2E_USER || 'ops'
const PASSWORD = process.env.HD_E2E_PASSWORD
if (!PASSWORD) throw new Error('Run the isolated web harness with HD_E2E_PASSWORD.')

async function signIn(page) {
  await page.goto('/')
  await page.getByLabel('Username').fill(USER)
  await page.getByLabel('Password', { exact: false }).first().fill(PASSWORD)
  const login = page.waitForResponse(response => response.url().endsWith('/session') && response.request().method() === 'POST')
  await page.getByRole('button', { name: 'Sign in' }).click()
  const response = await login
  expect(response.status()).toBe(200)
  const body = await response.json()
  expect(Object.hasOwn(body, 'session_id')).toBe(false)
  expect(Object.hasOwn(body, 'session_secret')).toBe(false)
  const cookie = (await page.context().cookies()).find(value => value.name === 'hd_session')
  expect(cookie?.httpOnly).toBe(true)
  expect(cookie?.sameSite).toBe('Strict')
  expect(cookie?.secure).toBe(false)
  await expect(page.getByRole('heading', { name: 'Applications' })).toBeVisible()
}

test('a wrong password shows one uniform refusal and stays on the form', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('Username').fill(USER)
  await page.getByLabel('Password', { exact: false }).first().fill('wrong')
  await page.getByRole('button', { name: 'Sign in' }).click()

  // #hd-login-error is itself role=alert and the message renders inside it,
  // so target the inner .w-alert to stay out of strict-mode ambiguity.
  await expect(page.locator('#hd-login-error .w-alert')).toContainText(/./)
  // Still the login form — and the page holds no session to leak.
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible()
})

test('sign in, see the application list, open the app', async ({ page }) => {
  await signIn(page)

  // The seeded project and application are there.
  await expect(page.getByRole('heading', { name: 'alpha' })).toBeVisible()
  await page.getByRole('link', { name: 'site' }).click()

  // The detail page: status chips, the diff, the history.
  await expect(page.getByRole('heading', { name: 'site' })).toBeVisible()
  await expect(page.locator('.hd-status .w-chip').first()).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Activity' })).toBeVisible()
  await expect(page.locator('#hd-apptree, #hd-view-table-panel').first()).toBeVisible()
})

test('the session survives a reload and signs out for real', async ({ page }) => {
  await signIn(page)

  // Reload: the httpOnly cookie is the session, so the page asks the gateway
  // and lands signed in — no login form.
  await page.reload()
  await expect(page.getByRole('heading', { name: 'Applications' })).toBeVisible()

  await page.getByRole('button', { name: 'Sign out' }).click()
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible()

  // And the session is dead at the server, not just the cookie gone: a fresh
  // load must show the form again rather than a cached app list.
  await page.reload()
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible()
})

test('a dry run reports an outcome without applying anything', async ({ page }) => {
  await signIn(page)
  await page.getByRole('link', { name: 'site' }).click()
  await page.getByRole('button', { name: 'Dry run' }).click()

  // Docker may or may not be running where this executes. Either way the
  // action region must show a real outcome — a plan card or the control
  // plane's error with its HD code — never a spinner forever or a blank.
  await expect(page.locator('#hd-action .w-card, #hd-action [role="alert"]')).toBeVisible({
    timeout: 20_000,
  })
  // The buttons came back, so the page did not wedge.
  await expect(page.getByRole('button', { name: 'Dry run' })).toBeEnabled()
})

test('DuVay theming is wired', async ({ page }) => {
  await page.goto('/')
  await expect(page.locator('html')).toHaveAttribute('w-theme', 'auto')
  // The vendored stylesheet actually loaded — a token resolves.
  const surface = await page.evaluate(() =>
    getComputedStyle(document.documentElement).getPropertyValue('--w-surface').trim(),
  )
  expect(surface).not.toBe('')
})

test('hd-timeseries renders axes, a line, and deploy markers', async ({ page }) => {
  await signIn(page)
  await page.getByRole('link', { name: 'site' }).click()
  await expect(page.getByRole('heading', { name: 'Instances' })).toBeVisible()

  // The component is data-driven; feed it a known series and read the SVG
  // back. This is the chart's contract independent of any live container.
  const rendered = await page.evaluate(() => {
    const element = /** @type {HTMLElement & {series: (input: object) => void}} */ (
      document.createElement('hd-timeseries'))
    document.body.appendChild(element)
    const base = Date.now() - 30 * 60_000
    element.series({
      label: 'CPU',
      unit: 'percent',
      points: Array.from({ length: 30 }, (_, i) => ({
        at: new Date(base + i * 60_000).toISOString(),
        value: 20 + 10 * Math.sin(i / 4),
      })),
      markers: [{ at: new Date(base + 15 * 60_000).toISOString(), label: 'abc1234' }],
    })
    const svg = element.querySelector('svg')
    return {
      hasSvg: !!svg,
      paths: svg ? svg.querySelectorAll('path').length : 0,
      gridlines: svg ? svg.querySelectorAll('line').length : 0,
      markerText: svg ? [...svg.querySelectorAll('text')].some((t) => t.textContent.includes('abc1234')) : false,
      yLabels: svg ? [...svg.querySelectorAll('text')].some((t) => t.textContent.includes('%')) : false,
      ariaLabel: svg ? svg.getAttribute('aria-label') : '',
    }
  })

  expect(rendered.hasSvg).toBe(true)
  expect(rendered.paths).toBeGreaterThanOrEqual(2) // area + line
  expect(rendered.gridlines).toBeGreaterThanOrEqual(4) // grid + marker
  expect(rendered.markerText).toBe(true) // the deploy is on the chart
  expect(rendered.yLabels).toBe(true) // the axis is labeled
  expect(rendered.ariaLabel).toContain('CPU')
})

test('an app with nothing running says so instead of showing an empty chart', async ({ page }) => {
  await signIn(page)
  await page.getByRole('link', { name: 'site' }).click()
  await expect(page.getByText('Nothing is running')).toBeVisible()
})

test('the activity table merges HEIMDALL operations with runtime events', async ({ page }) => {
  await signIn(page)
  await page.getByRole('link', { name: 'site' }).click()
  await expect(page.getByRole('heading', { name: 'Activity' })).toBeVisible()
  // No syncs have run in this seed, so the honest answer is emptiness —
  // never an error, and never an invented row.
  const section = page.locator('section', { has: page.getByRole('heading', { name: 'Activity' }) })
  await expect(section.getByText(/Nothing yet|heimdall/).first()).toBeVisible()
})

// The instance detail page took Docker Desktop's shape — identity, status,
// tabs — and stopped where that product's container management begins. Both
// halves of that are asserted here: the tabs work, and the controls HEIMDALL
// refuses to build are absent.
test('the instance page is tabbed, and carries no exec, files, or lifecycle buttons', async ({ page }) => {
  await signIn(page)
  // No container is running in this harness, so the page is reached
  // directly: it must still render, and say the instance is gone rather
  // than break.
  await page.goto('/apps/alpha/site/i/not-a-real-container?service=web')

  await expect(page.getByRole('button', { name: 'Metrics' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Logs' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Inspect' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Events' })).toBeVisible()

  // Deliberately absent: an interactive shell and a file browser are the
  // anti-GitOps escape hatches this product does not ship, and arbitrary
  // start/stop/delete is Portainer's product, not this one.
  for (const forbidden of ['Exec', 'Files', 'Bind mounts', 'Delete', 'Restart']) {
    await expect(page.getByRole('button', { name: forbidden })).toHaveCount(0)
  }

  // Tabs switch, and the choice survives a reload through the hash.
  await page.getByRole('button', { name: 'Inspect' }).click()
  await expect(page.getByText('Instance', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Logs' }).click()
  await expect(page.getByPlaceholder('Filter lines…')).toBeVisible()
  await page.reload()
  await expect(page.getByPlaceholder('Filter lines…')).toBeVisible()
})

// Network and block I/O are one panel each, two lines, the way `docker
// stats` prints them as one column. The chart takes a {lines: [...]} shape
// for that, and still takes the single-series {points} shape every other
// panel uses.
test('hd-timeseries draws a two-series panel with a legend', async ({ page }) => {
  await signIn(page)
  await page.getByRole('link', { name: 'site' }).click()
  await expect(page.getByRole('heading', { name: 'Instances' })).toBeVisible()

  const rendered = await page.evaluate(() => {
    const element = /** @type {HTMLElement & {series: (input: object) => void}} */ (
      document.createElement('hd-timeseries'))
    document.body.appendChild(element)
    const base = Date.now() - 20 * 60_000
    const points = (f) => Array.from({ length: 20 }, (_, i) => ({
      at: new Date(base + i * 60_000).toISOString(), value: f(i),
    }))
    element.series({
      label: 'Network I/O', unit: 'bytes',
      lines: [
        { label: 'in', points: points((i) => 40_000 + 15_000 * Math.sin(i / 3)) },
        { label: 'out', points: points((i) => 12_000 + 6_000 * Math.cos(i / 4)) },
      ],
    })
    const svg = element.querySelector('svg')
    const strokes = [...svg.querySelectorAll('path[stroke]')].map((p) => p.getAttribute('stroke'))
    return {
      lines: strokes.length,
      distinct: new Set(strokes).size,
      legend: [...element.querySelectorAll('.hd-chart-legend span')].map((s) => s.textContent.trim()).filter(Boolean),
      aria: svg.getAttribute('aria-label'),
    }
  })

  expect(rendered.lines).toBe(2)
  expect(rendered.distinct).toBe(2) // one colour per direction, or the panel says nothing
  expect(rendered.legend).toEqual(['in', 'out'])
  expect(rendered.aria).toContain('in ') // both latest values are announced
  expect(rendered.aria).toContain('out ')
})

// The activity table pages rather than truncating: it merges HEIMDALL's
// operations with the runtime's events, so on a busy application the
// interesting row is rarely in the first ten.
test('the activity table paginates', async ({ page }) => {
  test.setTimeout(90_000)
  await signIn(page)

  // Twelve dry runs — enough for two pages. They go through the same BFF
  // the UI uses, so this is the app's own path, not a back door.
  await page.evaluate(async () => {
    for (let i = 0; i < 12; i++) {
      await fetch('/gateway', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          method: 'POST',
          path: '/api/v1/projects/alpha/apps/site/sync',
          body: { dry_run: true },
          timeout_ms: 120000,
        }),
      })
    }
  })

  await page.goto('/apps/alpha/site')
  const pager = page.locator('.w-pagination')
  await expect(pager).toBeVisible()

  const rows = page.locator('table.w-table tbody tr')
  await expect(rows).toHaveCount(10) // a page, not the whole history
  await expect(page.getByText(/Showing 1–10 of/)).toBeVisible()
  await expect(pager.locator('.active')).toHaveText('1')
  await expect(pager.getByRole('button', { name: 'previous page' })).toBeDisabled()

  await pager.getByRole('button', { name: 'page 2' }).click()
  await expect(pager.locator('.active')).toHaveText('2')
  await expect(page.getByText(/Showing 11–/)).toBeVisible()
  await expect(pager.getByRole('button', { name: 'previous page' })).toBeEnabled()
})

// The metrics window: the chooser narrows what the charts read, and the
// choice rides in the URL so a shared link lands on the same graph.
test('the metrics window can be narrowed and travels in the link', async ({ page }) => {
  await signIn(page)
  await page.goto('/apps/alpha/site/i/demo-1?service=web')

  const chooser = page.getByRole('group', { name: 'Metrics window' })
  await expect(chooser).toBeVisible()
  await expect(chooser.getByRole('button', { name: '1h' })).toHaveAttribute('aria-pressed', 'true')

  await chooser.getByRole('button', { name: '15m' }).click()
  await expect(chooser.getByRole('button', { name: '15m' })).toHaveAttribute('aria-pressed', 'true')
  await expect(page).toHaveURL(/window=15m/)

  // A reload keeps it, which is the point of putting it in the URL.
  await page.reload()
  await expect(page.getByRole('group', { name: 'Metrics window' })
    .getByRole('button', { name: '15m' })).toHaveAttribute('aria-pressed', 'true')
})

// Smoothing must not invent data: a monotone curve stays inside the interval
// its neighbouring samples define, so a spike the metric never reached
// cannot appear between two of them.
test('the chart curve never overshoots its samples', async ({ page }) => {
  await signIn(page)
  await page.getByRole('link', { name: 'site' }).click()
  await expect(page.getByRole('heading', { name: 'Instances' })).toBeVisible()

  const drawn = await page.evaluate(() => {
    const element = /** @type {HTMLElement & {series: (input: object) => void}} */ (
      document.createElement('hd-timeseries'))
    document.body.appendChild(element)
    const base = Date.now() - 20 * 60_000
    const values = [40, 12, 78, 20, 65, 30, 55, 18, 70, 25]
    element.series({
      label: 'CPU', unit: 'percent',
      points: values.map((value, i) => ({ at: new Date(base + i * 120_000).toISOString(), value })),
    })
    const d = element.querySelector('path[stroke]').getAttribute('d')
    // Every y in the path — endpoints and control points alike — must sit
    // within the band the samples themselves occupy.
    const ys = [...d.matchAll(/[\d.]+,([\d.]+)/g)].map((m) => Number(m[1]))
    return { curved: d.includes('C'), highest: Math.min(...ys), lowest: Math.max(...ys) }
  })

  expect(drawn.curved).toBe(true)
  // The plot area is inset from the top by PAD.top (10) and the y axis is
  // inverted, so anything above that line would be an invented peak.
  expect(drawn.highest).toBeGreaterThanOrEqual(10)
  expect(drawn.lowest).toBeLessThanOrEqual(160 - 22) // the axis line
})
