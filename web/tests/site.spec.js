import { expect, test } from '@playwright/test'
// The website suite: point SITE_URL at a served website/ (CI does); the
// default is the local dev server.
const SITE = process.env.SITE_URL || 'http://127.0.0.1:9304'
const DOCS = ['', 'concepts', 'registry', 'providers', 'sync', 'secrets', 'agents', 'observability', 'security', 'reference']

test('the landing page renders at three widths', async ({ browser }) => {
  test.setTimeout(90000)
  for (const [w, h, name] of [[375, 812, 'phone'], [768, 1024, 'tablet'], [1440, 1000, 'desktop']]) {
    const ctx = await browser.newContext({ viewport: { width: w, height: h } })
    const page = await ctx.newPage()
    const errors = []
    page.on('pageerror', (e) => errors.push(e.message))
    await page.goto(SITE + '/')
    await expect(page.locator('h1')).toContainText('watchman')
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBe(0)
    expect(errors).toEqual([])
    await ctx.close()
  }
})

test('every documentation page renders, decodes, and marks itself', async ({ browser }) => {
  test.setTimeout(120000)
  for (const [w] of [[375], [1440]]) {
    const ctx = await browser.newContext({ viewport: { width: w, height: 900 } })
    const page = await ctx.newPage()
    for (const slug of DOCS) {
      const errors = []
      page.on('pageerror', (e) => errors.push(e.message))
      await page.goto(`${SITE}/docs/${slug}`)
      await expect(page.locator('article h1')).toBeVisible()
      await page.waitForTimeout(150)
      const audit = await page.evaluate(() => ({
        overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        undecoded: [...document.querySelectorAll('.site-code')].some((b) => b.textContent.includes('&#123;')),
        current: document.querySelectorAll('nav[aria-label="documentation"] a[aria-current="page"]').length,
        sidebarLinks: document.querySelectorAll('nav[aria-label="documentation"] a').length,
      }))
      expect(audit.overflow, slug).toBe(0)
      expect(audit.undecoded, slug).toBe(false)
      expect(audit.sidebarLinks, slug).toBe(10)
      expect(audit.current, slug).toBe(1)
      expect(errors).toEqual([])
    }
    await ctx.close()
  }
})
