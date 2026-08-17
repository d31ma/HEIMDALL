import { expect, test } from '@playwright/test'
// This suite drives the LOCAL DEMO stack (control plane on 18443, ty on
// 9303, the fake engine) — a developer tool, not a CI gate. CI runs
// ui.spec.js under scripts/e2e-web.sh, which stands up its own stack.
const BASE = 'http://127.0.0.1:9303'
test.skip(!!process.env.CI, 'drives the local demo stack, which CI does not run')

test('tree view renders the dependency graph and search filters it', async ({ browser }) => {
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 950 } })
  const page = await ctx.newPage()
  await page.goto(BASE + '/audit-login')
  await page.waitForURL(BASE + '/')

  // App list search bar.
  await expect(page.locator('#hd-app-search')).toBeVisible()
  await page.fill('#hd-app-search', 'leg')
  await expect(page.locator('a[href*="legacy"]')).toBeVisible()
  await expect(page.locator('a[href*="/apps/shop/site"]')).toBeHidden()
  await page.fill('#hd-app-search', '')
  await page.screenshot({ path: '/tmp/e2e-audit/V1-list-search.png', fullPage: true })

  // Detail: tree is the default view.
  await page.click('a[href*="/apps/shop/site"]')
  await page.waitForSelector('#hd-apptree svg', { timeout: 20000 })
  const svg = page.locator('#hd-apptree svg')
  // Nodes: app + 4 services + 4 instances = 9 cards.
  await expect(svg.locator('g')).toHaveCount(9)
  // The dependency chain reads left to right: cache/db before api before web.
  const positions = await page.evaluate(() => {
    const out = {}
    document.querySelectorAll('#hd-apptree g').forEach((g) => {
      const name = g.querySelector('text[font-weight="600"]')?.textContent
      const x = Number(g.querySelector('rect')?.getAttribute('x'))
      if (name) out[name] = x
    })
    return out
  })
  expect(positions['cache']).toBeLessThan(positions['api'])
  expect(positions['db']).toBeLessThan(positions['api'])
  expect(positions['api']).toBeLessThan(positions['web'])
  await page.screenshot({ path: '/tmp/e2e-audit/V2-tree.png', fullPage: true })

  // Search dims non-matching nodes.
  await page.fill('#hd-tree-search', 'redis')
  await page.waitForTimeout(200)
  const dimmed = await page.evaluate(() =>
    [...document.querySelectorAll('#hd-apptree g')].filter((g) => g.getAttribute('opacity') === '0.3').length)
  expect(dimmed).toBeGreaterThan(0)
  await page.screenshot({ path: '/tmp/e2e-audit/V3-tree-filtered.png', fullPage: true })
  await page.fill('#hd-tree-search', '')

  // Switch to table view; the toggle persists in the hash.
  await page.click('#hd-view-table')
  await expect(page.locator('#hd-view-table-panel')).toBeVisible()
  await expect(page.locator('#hd-view-tree-panel')).toBeHidden()
  expect(page.url()).toContain('#view=table')
  await expect(page.locator('.hd-instance-row')).toHaveCount(4)
  await page.screenshot({ path: '/tmp/e2e-audit/V4-table.png', fullPage: true })

  // A table row opens the instance dashboard page.
  await page.locator('.hd-instance-row').first().click()
  await page.waitForURL(/\/apps\/shop\/site\/i\//, { timeout: 15000 })
  await page.waitForSelector('.hd-tiles', { timeout: 15000 })
  await expect(page.locator('.hd-panel')).toHaveCount(11) // 10 charts + logs
  await page.waitForSelector('#hd-logs', { timeout: 15000 })
  await page.screenshot({ path: '/tmp/e2e-audit/V5-instance-dashboard.png', fullPage: true })
  // Breadcrumb returns to the app.
  await page.click(`a[href*="/apps/shop/site"]`)
  await page.waitForSelector('#hd-apptree svg', { timeout: 20000 })
  await ctx.close()
})
