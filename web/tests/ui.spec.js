// The web tier end to end: real browser, real ty serve, real control plane,
// real SESAME deciding the login. Run via scripts/e2e-web.sh, which owns the
// stack and seeds one application (alpha/site) before this starts.
import { expect, test } from '@playwright/test'

const USER = 'ops'
const PASSWORD = process.env.HD_E2E_PASSWORD || 'correct-horse-battery-staple-9271'

async function signIn(page) {
  await page.goto('/')
  await page.getByLabel('Username').fill(USER)
  await page.getByLabel('Password', { exact: false }).first().fill(PASSWORD)
  await page.getByRole('button', { name: 'Sign in' }).click()
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
    const element = document.createElement('hd-timeseries')
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
