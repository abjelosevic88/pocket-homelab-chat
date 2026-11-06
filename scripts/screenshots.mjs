// README screenshots and a browser smoke test, against a demo instance (DEMO_DATA=true seeds a family that chats):
//
//   DATA_DIR=/tmp/chat-demo DEMO_DATA=true ADMIN_EMAIL=demo@example.com ADMIN_PASSWORD=demodemo1 ADMIN_NAME="Emir Kovač" PORT=18247 ./pocket-chat
//   CDP=http://127.0.0.1:9336 BASE=http://127.0.0.1:18247 node scripts/screenshots.mjs   # any Chrome with remote debugging
//   SHOTS=0 … node scripts/screenshots.mjs                                                # checks only
// playwright-core comes from web/node_modules, or set PW_FROM to a package.json next to an install of it.
import { createRequire } from 'node:module'
import assert from 'node:assert/strict'

const require = createRequire(process.env.PW_FROM || new URL('../web/package.json', import.meta.url))
let pw
try { pw = require('playwright') } catch { pw = require('playwright-core') }
const { chromium } = pw
const base = process.env.BASE || 'http://127.0.0.1:18247'
const shots = process.env.SHOTS !== '0'
const only = process.env.ONLY || ''
const dir = process.env.OUT || new URL('../docs/screenshots/', import.meta.url).pathname
const browser = process.env.CDP ? await chromium.connectOverCDP(process.env.CDP) : await chromium.launch()
const errors = []

async function session({ phone = false, dark = false } = {}) {
  const ctx = await browser.newContext({
    viewport: phone ? { width: 390, height: 844 } : { width: 1440, height: 900 },
    deviceScaleFactor: phone ? 3 : 2, isMobile: phone, hasTouch: phone, colorScheme: dark ? 'dark' : 'light',
    userAgent: phone ? 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1' : undefined,
  })
  const page = await ctx.newPage()
  page.on('pageerror', e => errors.push(`pageerror: ${e}`))
  page.on('console', m => { if (m.type() === 'error' && !/Failed to load resource|Content Security Policy|EventSource/.test(m.text())) errors.push(m.text()) })
  await page.goto(base + '/login')
  const res = await page.evaluate(() => fetch('/api/v1/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ email: 'demo@example.com', password: 'demodemo1' }) }).then(r => r.status))
  assert.equal(res, 200, 'demo sign-in')
  return { ctx, page }
}
async function go(page, path, wait = 1500) {
  await page.goto(base + path, { waitUntil: 'domcontentloaded' })
  await page.addStyleTag({ content: '*, *::before, *::after { transition: none !important; animation: none !important; }' }).catch(() => {})
  await page.evaluate(() => document.fonts.ready)
  await page.waitForTimeout(wait)
}
async function shot(page, name) { if (shots && (!only || only.split(',').includes(name))) await page.screenshot({ path: dir + name + '.png' }) }
const convId = async (page, name) => page.evaluate(n => fetch('/api/v1/conversations').then(r => r.json()).then(l => l.find(c => c.name === n).id), name)
const scroll = (page, f) => page.locator('.chat-scroll').evaluate((s, f) => { s.scrollTop = (s.scrollHeight - s.clientHeight) * f }, f)

// ---- desktop ----
{
  const { ctx, page } = await session()
  const family = await convId(page, 'Family')
  const hikers = await convId(page, 'Weekend hikers')
  await go(page, `/c/${family}`, 2000)
  const html = await page.content()
  assert.ok(html.includes('Weekend at the cottage'), 'the to-do list shows')
  assert.ok(html.includes('Where for Saturday lunch?'), 'the poll shows')
  assert.ok(html.includes('Add to calendar'), 'suggestions show under a plan')
  await scroll(page, 1)
  await page.waitForTimeout(400)
  await shot(page, 'desktop-chat')
  await scroll(page, 0.42)
  await page.waitForTimeout(400)
  await shot(page, 'desktop-cards')
  await go(page, `/c/${hikers}`, 1800)
  await page.locator('.chat-head').getByRole('button', { name: 'Details' }).last().click()
  await page.waitForTimeout(700)
  await shot(page, 'desktop-details')
  await page.locator('.chat-head').getByRole('button', { name: 'Details' }).last().click()
  await go(page, '/', 2000)
  assert.ok((await page.content()).includes('What did I miss?'), 'catch up shows')
  await shot(page, 'desktop-catchup')
  await go(page, '/search?q=thermos', 1500)
  assert.ok((await page.content()).includes('thermos'), 'search finds a message')
  await shot(page, 'desktop-search')
  await go(page, '/settings/apps', 1500)
  await shot(page, 'desktop-apps')
  await ctx.close()
}

// ---- dark ----
{
  const { ctx, page } = await session({ dark: true })
  const family = await convId(page, 'Family')
  await go(page, `/c/${family}`, 2000)
  await scroll(page, 1)
  await page.waitForTimeout(300)
  await shot(page, 'desktop-dark')
  await ctx.close()
}

// ---- phone ----
{
  const { ctx, page } = await session({ phone: true })
  await go(page, '/', 1800)
  await shot(page, 'phone-chats')
  const family = await convId(page, 'Family')
  await go(page, `/c/${family}`, 2000)
  await scroll(page, 1)
  await page.waitForTimeout(300)
  await shot(page, 'phone-chat')
  await scroll(page, 0.45)
  await page.waitForTimeout(300)
  await shot(page, 'phone-poll')
  await page.getByRole('button', { name: 'Add', exact: true }).click()
  await page.waitForTimeout(400)
  await shot(page, 'phone-plus')
  await ctx.close()
}

await browser.close()
if (errors.length) { console.error(errors.join('\n')); process.exit(1) }
console.log('ok')
