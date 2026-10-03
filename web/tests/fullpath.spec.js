import { expect, test } from '@playwright/test'
// This suite drives the LOCAL DEMO stack (control plane on 18443, ty on
// 9303, the fake engine) — a developer tool, not a CI gate. CI runs
// ui.spec.js under scripts/e2e-web.sh, which stands up its own stack.
const BASE = 'http://127.0.0.1:9303'
test.skip(!!process.env.CI, 'drives the local demo stack, which CI does not run')
const shot = (name) => ({ path: `/tmp/e2e-audit/${name}.png`, fullPage: true })
test.describe.configure({ mode: 'serial' })

let page
test.beforeAll(async ({ browser }) => {
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 950 } })
  page = await ctx.newPage()
})

test('P1 bad password refuses uniformly and keeps the form', async () => {
  await page.goto(BASE + '/')
  await page.fill('#hd-user', 'ops')
  await page.fill('#hd-password', 'totally-wrong')
  await page.click('button[type=submit]')
  await expect(page.locator('#hd-login-error .w-alert')).toContainText(/authentication failed/i)
  await page.screenshot(shot('P1-bad-password'))
})

test('P2 login lands on the grouped app list with the suspended chip', async () => {
  await page.fill('#hd-password', 'correct-horse-battery-staple-9271')
  await page.click('button[type=submit]')
  await expect(page.getByRole('heading', { name: 'Applications' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'shop' })).toBeVisible()
  await expect(page.locator('.w-chip', { hasText: 'suspended' })).toBeVisible()
  await page.screenshot(shot('P2-app-list'))
})

test('P3 session survives reload', async () => {
  await page.reload()
  await expect(page.getByRole('heading', { name: 'Applications' })).toBeVisible()
})

test('P4 detail: synced, green, instances running', async () => {
  await page.click('a[href*="/apps/shop/site"]')
  await page.waitForSelector('#hd-live .hd-status', { timeout: 20000 })
  await expect(page.locator('.hd-status .w-chip', { hasText: 'Synced' })).toBeVisible()
  await expect(page.locator('.hd-status .w-chip', { hasText: 'Healthy' })).toBeVisible()
  await page.click('#hd-view-table')
  await expect(page.locator('.hd-instance-row')).toHaveCount(4)
  await page.screenshot(shot('P4-detail-healthy'))
})

test('P5 instance dashboard: tiles, chart panels, logs', async () => {
  await page.locator('.hd-instance-row').first().click()
  await page.waitForURL(/\/i\//, { timeout: 15000 })
  await page.waitForSelector('.hd-tiles', { timeout: 15000 })
  await expect(page.locator('.hd-panel')).toHaveCount(11)
  await page.waitForSelector('#hd-logs', { timeout: 15000 })
  await expect(page.locator('#hd-logs')).toContainText(/listening|ready/, { timeout: 10000 })
  await page.waitForSelector('hd-timeseries svg, .hd-chart-empty', { timeout: 20000 })
  await page.screenshot(shot('P5-instance-dashboard'))
  await page.goBack()
  await page.waitForSelector('#hd-apptree svg', { timeout: 20000 })
})

test('P6 dry run against a synced app applies nothing and says so', async () => {
  await page.click('#hd-dry')
  await page.waitForSelector('#hd-action .w-card', { timeout: 25000 })
  await expect(page.locator('#hd-action')).toContainText(/dry run/i)
  await page.screenshot(shot('P6-dryrun-clean'))
})

test('P7 out-of-band kill shows drift, sync heals it', async () => {
  // Kill the cache container straight on the fake Engine, as an operator with
  // docker CLI would.
  const engineURL = process.env.ENGINE_URL
  const listed = await page.request.get(engineURL + '/v1.43/containers/json?all=true')
  const containers = await listed.json()
  const cache = containers.find((c) => (c.Labels || {})['dev.delma.heimdall.service'] === 'cache')
  expect(cache).toBeTruthy()
  await page.request.delete(engineURL + '/v1.43/containers/' + cache.Id)

  await page.reload()
  await page.waitForSelector('#hd-live .hd-status', { timeout: 20000 })
  await expect(page.locator('.hd-status .w-chip', { hasText: 'OutOfSync' })).toBeVisible()
  await expect(page.locator('.hd-status .w-chip', { hasText: 'Missing' })).toBeVisible()
  await page.screenshot(shot('P7-drift'))

  await page.click('#hd-sync')
  await page.waitForSelector('#hd-action .w-card', { timeout: 30000 })
  await page.waitForTimeout(500)
  await expect(page.locator('.hd-status .w-chip', { hasText: 'Synced' })).toBeVisible({ timeout: 15000 })
  await page.screenshot(shot('P7-healed'))
})

test('P8 rollback needs the confirm step, cancel works, confirm rolls back', async () => {
  const options = page.locator('#hd-revision option')
  await expect(options).toHaveCount(2, { timeout: 10000 })

  await page.click('#hd-rollback')
  await expect(page.locator('#hd-rollback-confirm')).toBeVisible()
  await page.click('#hd-rollback-cancel')
  await expect(page.locator('#hd-rollback-confirm')).toBeHidden()
  await expect(page.locator('#hd-rollback')).toBeVisible()

  // Pick the oldest revision (v1) and confirm for real.
  const value = await options.last().getAttribute('value')
  await page.selectOption('#hd-revision', value)
  await page.click('#hd-rollback')
  await page.screenshot(shot('P8-rollback-armed'))
  await page.click('#hd-rollback-confirm')
  await page.waitForSelector('#hd-action .w-card', { timeout: 30000 })
  await expect(page.locator('#hd-action')).toContainText(/rollback/i)
  await page.screenshot(shot('P8-rolled-back'))
})

test('P9 the activity table gained the rollback', async () => {
  await expect(page.locator('section', { has: page.getByRole('heading', { name: 'Activity' }) }))
    .toContainText('rollback')
  await page.screenshot(shot('P9-activity'))
})

test('P10 self-heal shows up as automation in the timeline', async () => {
  // Opt the app into self-heal via the gateway (as the UI would).
  const response = await page.request.post(BASE + '/gateway', {
    data: { method: 'PATCH', path: '/api/v1/projects/shop/apps/site', body: { sync_policy: { self_heal: true } } },
  })
  expect(response.ok()).toBeTruthy()

  // Kill a container; the 10s auto loop should heal without a human.
  const engineURL = process.env.ENGINE_URL
  const listed = await page.request.get(engineURL + '/v1.43/containers/json?all=true')
  const containers = await listed.json()
  const web = containers.find((c) => (c.Labels || {})['dev.delma.heimdall.service'] === 'web')
  await page.request.delete(engineURL + '/v1.43/containers/' + web.Id)

  await expect(async () => {
    await page.reload()
    await page.waitForSelector('#hd-live .hd-status', { timeout: 20000 })
    const timeline = await page
      .locator('section', { has: page.getByRole('heading', { name: 'Activity' }) }).textContent()
    expect(timeline).toContain('self_heal')
    // Converged too — asserted inside the retry so a rerun with an old
    // self_heal row still waits for the actual heal.
    await expect(page.locator('.hd-status .w-chip', { hasText: 'Synced' })).toBeVisible({ timeout: 1000 })
  }).toPass({ timeout: 90000, intervals: [5000] })
  await page.screenshot(shot('P10-self-heal'))
})

test('P11 a suspended app refuses to sync with a readable message', async () => {
  await page.goto(BASE + '/apps/shop/legacy')
  await page.waitForSelector('#hd-sync', { timeout: 20000 })
  await page.click('#hd-sync')
  await page.waitForSelector('#hd-action [role=alert]', { timeout: 20000 })
  await expect(page.locator('#hd-action')).toContainText(/suspended/i)
  await page.screenshot(shot('P11-suspended'))
})

test('P12 control plane outage surfaces as a clear gateway error, and recovers', async () => {
  // The audit harness stops serve out-of-band; here we simulate by asking the
  // gateway for a path while the plane is paused via SIGSTOP.
  const { execSync } = await import('node:child_process')
  // Self-contained: a --grep run starts with no cookie, and without one the
  // detail page correctly redirects to login instead of showing the outage.
  await page.goto(BASE + '/audit-login')
  await page.waitForURL(BASE + '/')
  execSync('pkill -STOP -f "hdemo serve|hd-paths serve"')
  await page.goto(BASE + '/apps/shop/site')
  await page.waitForSelector('#hd-main [role=alert], #hd-live [role=alert], .w-alert', { timeout: 30000 })
  await page.screenshot(shot('P12-outage'))
  execSync('pkill -CONT -f "hdemo serve|hd-paths serve"')
  await page.reload()
  await page.waitForSelector('#hd-live .hd-status', { timeout: 30000 })
})

test('P13 sign out really signs out', async () => {
  await page.goto(BASE + '/')
  await page.click('#hd-logout')
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible()
  await page.reload()
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible()
})
