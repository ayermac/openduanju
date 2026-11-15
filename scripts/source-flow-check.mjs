#!/usr/bin/env node
// Real-browser check for the SOURCE-001 admin vertical flow.
//
// Requires: migrated MySQL, Go server on :8000, Vite dev on :5173. It submits
// one persisted run, waits for the checkpointed run to finish, opens a local
// series, toggles one episode, saves and verifies the selection after reload.
import { chromium } from 'playwright'
import { checkRuntime } from './lib/runtime-check.mjs'

const base = process.argv[2] || 'http://127.0.0.1:5173'
const screenshot = process.argv[3]
const api = process.env.ADMIN_API || 'http://127.0.0.1:8000'
const token = process.env.ADMIN_AUTH_TOKEN || ''

// The flow may only run against the intended environment: the API has to be
// on the expected port and connected to the expected database.
const environmentFailures = await checkRuntime(api, token)
if (environmentFailures.length > 0) {
  console.error(JSON.stringify({ ok: false, api, failures: environmentFailures }, null, 2))
  process.exit(1)
}

const browser = await chromium.launch({ headless: true })
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
if (process.env.ADMIN_AUTH_TOKEN) {
  await page.addInitScript((token) => sessionStorage.setItem('admin_token', token), process.env.ADMIN_AUTH_TOKEN)
}
const failures = []
try {
  await page.goto(`${base}/source`, { waitUntil: 'networkidle' })
  await page.getByRole('heading', { name: '来源采集与本地选剧' }).waitFor({ timeout: 15000 })

  await page.getByRole('button', { name: '提交采集' }).click()
  await expectText(page, /已提交运行 #\d+/, 15000)
  const runCard = page.locator('.run-card')
  await runCard.waitFor({ timeout: 15000 })
  const runId = Number(/运行 #(\d+)/.exec(await runCard.innerText())?.[1] ?? '-1')
  await page.getByText('succeeded', { exact: true }).first().waitFor({ timeout: 120000 })

  const rows = page.locator('.el-table__body tr')
  const rowCount = await rows.count()
  if (rowCount === 0) failures.push('no local candidates rendered')

  await page.getByRole('button', { name: /查看分集|选剧回填/ }).first().click()
  await page.getByText('分集勾选').waitFor({ timeout: 30000 })

  const before = await page.locator('.panel-title').last().innerText()
  const beforeSelected = Number(/已选 (\d+)/.exec(before)?.[1] ?? '-1')
  if (beforeSelected < 0) failures.push(`cannot read selected count from "${before}"`)

  const episodes = page.locator('.episode')
  const episodeCount = await episodes.count()
  if (episodeCount < 4) failures.push(`only ${episodeCount} episodes rendered`)

  const target = episodes.nth(3)
  const isChecked = await target.locator('input[type=checkbox]').isChecked()
  await target.click()
  const delta = isChecked ? -1 : 1
  const save = page.getByRole('button', { name: /保存勾选/ })
  await save.click()
  await expectText(page, new RegExp(`已保存 ${beforeSelected + delta} 集`), 15000)

  await page.reload({ waitUntil: 'networkidle' })
  await page.getByRole('button', { name: /查看分集|选剧回填/ }).first().click()
  await page.getByText('分集勾选').waitFor({ timeout: 30000 })
  const after = await page.locator('.panel-title').last().innerText()
  const afterSelected = Number(/已选 (\d+)/.exec(after)?.[1] ?? '-1')
  if (afterSelected !== beforeSelected + delta) {
    failures.push(`selected count after reload = ${afterSelected}, want ${beforeSelected + delta}`)
  }

  if (screenshot) await page.screenshot({ path: screenshot, fullPage: true })
  console.log(JSON.stringify({ ok: failures.length === 0, base, runId, rowCount, episodeCount, beforeSelected, afterSelected, failures }, null, 2))
  process.exitCode = failures.length === 0 ? 0 : 1
} catch (error) {
  console.error(JSON.stringify({ ok: false, base, error: String(error), failures }))
  process.exitCode = 1
} finally {
  await browser.close()
}

async function expectText(page, pattern, timeout) {
  const deadline = Date.now() + timeout
  while (Date.now() < deadline) {
    const text = await page.locator('body').innerText()
    if (pattern.test(text)) return
    await page.waitForTimeout(250)
  }
  throw new Error(`text ${pattern} not found within ${timeout}ms`)
}
