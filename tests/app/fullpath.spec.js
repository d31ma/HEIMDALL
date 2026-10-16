import { createRequire } from 'node:module'
const { expect, test } = createRequire(new URL('../../app/package.json', import.meta.url))('@playwright/test')
const BASE = (process.env.HD_WEB_URL || '').replace(/\/+$/, '')
const USER = process.env.HD_E2E_USER || ''
const PASSWORD = process.env.HD_E2E_PASSWORD || ''
const CONTROL_PLANE_PID = Number(process.env.HD_E2E_CP_PID)
const ENGINE = (process.env.ENGINE_URL || '').replace(/\/+$/, '')
test.skip(process.env.HD_FULLPATH !== '1', 'requires HD_FULLPATH=1 and an explicitly owned local Docker protocol fixture stack')
const shot = (name) => ({
  path: test.info().outputPath(`${name}.png`),
  fullPage: true,
  mask: [page.locator('input[type="password"], #hd-password')],
})
test.describe.configure({ mode: 'serial' })

let page
test.beforeAll(async ({ browser }) => {
  expect(USER, 'the fixture harness must set HD_E2E_USER').not.toBe('')
  expect(PASSWORD, 'the fixture harness must set HD_E2E_PASSWORD').not.toBe('')
  expect(Number.isSafeInteger(CONTROL_PLANE_PID) && CONTROL_PLANE_PID > 1,
    'HD_E2E_CP_PID must name the exact owned control-plane process').toBe(true)
  expect(CONTROL_PLANE_PID).not.toBe(process.pid)
  expect(CONTROL_PLANE_PID).not.toBe(process.ppid)
  for (const [name, address] of [['HD_WEB_URL', BASE], ['ENGINE_URL', ENGINE]]) {
    let parsed
    try { parsed = new URL(address) }
    catch { throw new Error(`${name} must name the owned local protocol fixture`) }
    expect(['http:', 'https:'].includes(parsed.protocol), `${name} must use HTTP`).toBe(true)
    expect(['127.0.0.1', 'localhost', '[::1]'].includes(parsed.hostname),
      `${name} must name the owned local protocol fixture`).toBe(true)
    expect(parsed.username === '' && parsed.password === '' && parsed.search === '' && parsed.hash === '',
      `${name} must not contain credentials, a query, or a fragment`).toBe(true)
    expect(parsed.pathname, `${name} must name the fixture root`).toBe('/')
  }
  process.kill(CONTROL_PLANE_PID, 0)
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 950 } })
  page = await ctx.newPage()
})

test.afterAll(async () => {
  await page?.context().close()
})

test('P1 bad password refuses uniformly and keeps the form', async () => {
  await page.goto(BASE + '/')
  await page.fill('#hd-user', USER)
  await page.fill('#hd-password', 'totally-wrong')
  await page.click('button[type=submit]')
  await expect(page.locator('#hd-login-error .w-alert')).toContainText(/authentication failed/i)
  await page.screenshot(shot('P1-bad-password'))
})

test('P2 login lands on the grouped app list with the suspended chip', async () => {
  await page.fill('#hd-password', PASSWORD)
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
  const engineURL = ENGINE
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
  const engineURL = ENGINE
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
  test.setTimeout(90000)
  await page.goto(BASE + '/audit-login')
  await page.waitForURL(BASE + '/')
  let paused = false
  try {
    process.kill(CONTROL_PLANE_PID, 'SIGSTOP')
    paused = true
    await page.goto(BASE + '/apps/shop/site')
    await page.waitForSelector('#hd-main [role=alert], #hd-live [role=alert], .w-alert', { timeout: 30000 })
    await page.screenshot(shot('P12-outage'))
  } finally {
    if (paused) process.kill(CONTROL_PLANE_PID, 'SIGCONT')
  }
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
