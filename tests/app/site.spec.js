import { createRequire } from 'node:module'
const { expect, test } = createRequire(new URL('../../app/package.json', import.meta.url))('@playwright/test')
// The website suite: point SITE_URL at a served website/ (CI does); the
// default is the local dev server.
const SITE = process.env.SITE_URL || 'http://127.0.0.1:9304'
const DOCS = ['', 'concepts', 'registry', 'providers', 'sync', 'secrets', 'agents', 'observability', 'security', 'reference']

test('the landing page renders at three widths', async ({ browser }) => {
  test.setTimeout(90000)
  /** @type {[number, number, string][]} */
  const viewports = [[375, 812, 'phone'], [768, 1024, 'tablet'], [1440, 1000, 'desktop']]
  for (const [w, h, name] of viewports) {
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

// Hovering anything button-shaped fills it and leaves the label alone. The
// sidebar entries are the reason: they are bordered chips on small screens,
// where an underline reads as a link pretending to be a button.
test('buttons and navigation fill on hover instead of underlining', async ({ page }) => {
  await page.goto(SITE + '/docs')

  const read = async (locator) => {
    await locator.hover()
    return locator.evaluate((el) => {
      const style = getComputedStyle(el)
      return { decoration: style.textDecorationLine, background: style.backgroundColor }
    })
  }
  const opaque = (colour) => colour !== 'rgba(0, 0, 0, 0)' && colour !== 'transparent'

  const sidebar = await read(page.locator('nav[aria-label="documentation"] a', { hasText: 'Core concepts' }))
  expect(sidebar.decoration).toBe('none')
  expect(opaque(sidebar.background)).toBe(true)

  const nav = await read(page.locator('header nav a', { hasText: 'Docs' }).first())
  expect(nav.decoration).toBe('none')
  expect(opaque(nav.background)).toBe(true)

  const button = await read(page.locator('header .w-btn').first())
  expect(button.decoration).toBe('none')
  expect(opaque(button.background)).toBe(true)
})

// The theme toggle: present on every page the header is on, flips the
// attribute DuVay reads, and survives a reload — a preference that resets
// on navigation is worse than none.
test('the theme can be switched and the choice sticks', async ({ page }) => {
  await page.goto(SITE + '/')
  const toggle = page.locator('[data-theme-toggle]')
  await expect(toggle).toBeVisible()

  // The label says what a press will do, so it is the assertion too.
  const before = await toggle.getAttribute('aria-label')
  await toggle.click()
  const chosen = before.includes('dark') ? 'dark' : 'light'
  await expect(page.locator('html')).toHaveAttribute('w-theme', chosen)
  await expect(toggle).not.toHaveAttribute('aria-label', before)

  // Across a reload…
  await page.reload()
  await expect(page.locator('html')).toHaveAttribute('w-theme', chosen)

  // …and across a navigation to the documentation, whose header is the same
  // component rendered by a different page companion.
  await page.goto(SITE + '/docs')
  await expect(page.locator('html')).toHaveAttribute('w-theme', chosen)
  const icon = page.locator('[data-theme-icon]')
  await expect(icon).toHaveCount(1)
  await expect(icon).toBeVisible()
  const bounds = await icon.boundingBox()
  expect(bounds?.width).toBe(16)
  expect(bounds?.height).toBe(16)
  const mask = await icon.evaluate(element => getComputedStyle(element).maskImage)
  expect(mask).toMatch(/theme-(dark|light)\.svg/)
  const asset = mask.match(/url\("?([^")]+)"?\)/)?.[1]
  expect((await page.request.get(asset)).ok()).toBe(true)

  // Back the other way, to prove it is a toggle and not a one-way switch.
  await page.locator('[data-theme-toggle]').click()
  await expect(page.locator('html')).toHaveAttribute('w-theme', chosen === 'dark' ? 'light' : 'dark')
})


test('documentation navigation maintains one current page and matching pagination', async ({ page }) => {
  await page.goto(SITE + '/docs')
  for (const [label, slug] of [['Core concepts', 'concepts'], ['The registry in git', 'registry'], ['Providers and capabilities', 'providers']]) {
    await page.locator('nav[aria-label="documentation"]').getByRole('link', { name: label, exact: true }).click()
    await expect(page).toHaveURL(new RegExp('/docs/' + slug + '/?$'))
    const current = page.locator('nav[aria-label="documentation"] a[aria-current="page"]')
    await expect(current).toHaveCount(1)
    await expect(current).toHaveText(label)
    const pagination = page.locator('nav[aria-label="pagination"]')
    await expect(pagination).toHaveCount(1)
    await expect(pagination.getByRole('link')).toHaveCount(2)
  }
})
