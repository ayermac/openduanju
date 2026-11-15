#!/usr/bin/env node
// Real check for the SOURCE-001 cron schedule.
//
// Requires: migrated MySQL and the Go server on :8000. It puts a plan on a
// one-minute cron, triggers one manual run, and then verifies that
//   - a manual trigger while a run is active is rejected (no duplicate run),
//   - the scheduler fires the next slot by itself once the manual run ended,
//   - the stored next_run_at advances and the plan can be switched back to
//     manual-only.
//
// The plan is restored to a manual-only schedule before the script exits.
import { checkRuntime } from './lib/runtime-check.mjs'

const api = process.env.ADMIN_API || 'http://127.0.0.1:8000'
const token = process.env.ADMIN_AUTH_TOKEN || ''
const planId = Number(process.env.PLAN_ID || '1')
const cron = process.env.PLAN_CRON || '* * * * *'
const timezone = process.env.PLAN_TIMEZONE || 'Asia/Shanghai'
const waitMs = Number(process.env.SCHEDULE_WAIT_MS || '300000')

const environmentFailures = await checkRuntime(api, token)
if (environmentFailures.length > 0) {
  console.error(JSON.stringify({ ok: false, api, failures: environmentFailures }, null, 2))
  process.exit(1)
}

const failures = []
const headers = { 'Content-Type': 'application/json' }
if (token) headers.Authorization = `Bearer ${token}`

async function call(method, path, body) {
  const response = await fetch(`${api}${path}`, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await response.text()
  let payload = {}
  try {
    payload = text ? JSON.parse(text) : {}
  } catch {
    payload = { raw: text }
  }
  return { status: response.status, payload }
}

async function planRuns() {
  const result = await call('GET', `/api/admin/v1/ingestion/runs?plan_id=${planId}&page=1&page_size=50`)
  if (result.status !== 200) {
    failures.push(`list runs status=${result.status}`)
    return []
  }
  return result.payload.items ?? []
}

async function waitFor(predicate, timeoutMs, label) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    const runs = await planRuns()
    const found = predicate(runs)
    if (found) return { found, runs }
    await new Promise((resolve) => setTimeout(resolve, 3000))
  }
  failures.push(`timeout waiting for ${label}`)
  return { found: null, runs: await planRuns() }
}

const terminal = (status) => ['succeeded', 'failed', 'cancelled', 'budget_exhausted'].includes(status)

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

// checkSkippedSlot arms a plan on a one-minute cron so that its slot becomes
// due while a manual run is still active. The scheduler must consume the slot
// without queueing a second run for that plan.
async function checkSkippedSlot(original, timeoutMs) {
  // The previous run has to be finished, otherwise the manual trigger below
  // is rejected and the slot cannot be observed.
  const idle = await waitFor(
    (runs) => {
      const active = runs.find((run) => !terminal(run.status))
      return active ? null : true
    },
    timeoutMs,
    'the previous run to finish',
  )
  if (!idle.found) return { error: 'a previous run was still active' }

  // Arm the plan close to a minute boundary so the slot is due while the
  // manual run is still working. The rate limit is lowered for this check so
  // the manual run stays active for the whole minute.
  while (new Date().getUTCSeconds() < 40) await sleep(2000)
  const listed = await call('GET', '/api/admin/v1/ingestion/plans')
  const plan = (listed.payload.items ?? []).find((item) => item.id === planId)
  const armed = await call('PATCH', `/api/admin/v1/ingestion/plans/${planId}`, {
    ...original,
    cron,
    timezone,
    rate_per_minute: 6,
    expected_version: plan.version,
  })
  if (armed.status !== 200) return { error: `arm status=${armed.status}` }
  const slot = new Date(armed.payload.next_run_at).getTime()
  const before = (await planRuns()).map((run) => run.id)
  const manual = await call('POST', `/api/admin/v1/ingestion/plans/${planId}/runs`, {
    expected_version: armed.payload.version,
  })
  if (manual.status !== 200) return { error: `manual status=${manual.status}` }

  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    const runs = await planRuns()
    const fresh = runs.filter((run) => !before.includes(run.id))
    const active = fresh.find((run) => !terminal(run.status))
    if (fresh.length > 1) {
      return { error: `the scheduler queued a duplicate run: ${fresh.map((run) => `#${run.id}`).join(',')}` }
    }
    if (Date.now() > slot + 15000) {
      const after = await call('GET', '/api/admin/v1/ingestion/plans')
      const current = (after.payload.items ?? []).find((item) => item.id === planId)
      if (!current?.next_run_at) return { error: 'next_run_at was not kept after the slot was consumed' }
      if (new Date(current.next_run_at).getTime() <= slot) {
        return { error: 'the consumed slot was not advanced' }
      }
      return {
        manualRun: manual.payload.id,
        slot: new Date(slot).toISOString(),
        nextRunAt: current.next_run_at,
        planVersion: current.version,
        note: active
          ? 'the manual run was still active when the slot was consumed'
          : 'the manual run finished before the slot check',
      }
    }
    await sleep(2000)
  }
  return { error: 'timeout waiting for the slot to be consumed' }
}

try {
  const listed = await call('GET', '/api/admin/v1/ingestion/plans')
  if (listed.status !== 200) throw new Error(`list plans status=${listed.status}`)
  const plan = (listed.payload.items ?? []).find((item) => item.id === planId)
  if (!plan) throw new Error(`plan ${planId} not found`)
  const original = {
    name: plan.name,
    page_start: plan.page_start,
    page_end: plan.page_end,
    request_budget: plan.request_budget,
    item_budget: plan.item_budget,
    rate_per_minute: plan.rate_per_minute,
    fetch_details: plan.fetch_details,
    fetch_episodes: plan.fetch_episodes,
    enabled: plan.enabled,
    cron: plan.cron ?? '',
    timezone: plan.timezone || timezone,
  }

  const scheduled = await call('PATCH', `/api/admin/v1/ingestion/plans/${planId}`, {
    ...original,
    expected_version: plan.version,
    cron,
    timezone,
  })
  if (scheduled.status !== 200) {
    throw new Error(`schedule plan status=${scheduled.status} ${JSON.stringify(scheduled.payload)}`)
  }
  if (!scheduled.payload.next_run_at) failures.push('plan has no next_run_at after scheduling')
  console.log(
    `scheduled plan ${planId}: cron=${scheduled.payload.cron} tz=${scheduled.payload.timezone} next=${scheduled.payload.next_run_at}`,
  )

  const before = (await planRuns()).map((run) => run.id)
  const idle = await waitFor(
    (runs) => {
      const active = runs.find((run) => !terminal(run.status))
      return active ? null : true
    },
    waitMs,
    'the plan to become idle',
  )
  if (!idle.found) throw new Error('the plan still has an active run')
  const manual = await call('POST', `/api/admin/v1/ingestion/plans/${planId}/runs`, {
    expected_version: scheduled.payload.version,
  })
  if (manual.status !== 200) {
    throw new Error(`manual trigger status=${manual.status} ${JSON.stringify(manual.payload)}`)
  }
  console.log(`manual run #${manual.payload.id} created`)

  const duplicate = await call('POST', `/api/admin/v1/ingestion/plans/${planId}/runs`, {
    expected_version: scheduled.payload.version,
  })
  if (duplicate.status !== 409) failures.push(`second manual trigger status=${duplicate.status}, want 409`)

  // While the manual run is active the scheduler must not add a second run.
  const active = await waitFor(
    (runs) => {
      const current = runs.filter((run) => !before.includes(run.id))
      return current.length === 1 && terminal(current[0].status) ? current : null
    },
    waitMs,
    'the manual run to finish',
  )
  const during = active.runs.filter((run) => !before.includes(run.id))
  if (during.length !== 1) failures.push(`expected exactly one run during the manual window, got ${during.length}`)
  console.log(`manual run finished: ${during.map((run) => `#${run.id} ${run.status}`).join(', ')}`)

  // The next slot must fire on its own, without any manual trigger.
  const fired = await waitFor(
    (runs) => {
      const createdRuns = runs.filter((run) => !before.includes(run.id))
      return createdRuns.length >= 2 ? createdRuns : null
    },
    waitMs,
    'the scheduler to fire the next slot',
  )
  const created = fired.runs.filter((run) => !before.includes(run.id))
  console.log(`runs for the plan: ${created.map((run) => `#${run.id} ${run.status}`).join(', ')}`)
  if (created.length < 2) {
    failures.push(`the scheduler did not fire a second run (${created.length} run(s) created)`)
  } else {
    const scheduledRun = created[created.length - 1]
    const manualRun = created[0]
    if (manualRun.finished_at && scheduledRun.started_at <= manualRun.finished_at) {
      failures.push('the scheduled run started before the manual run ended')
    }
  }

  const after = await call('GET', '/api/admin/v1/ingestion/plans')
  const currentPlan = (after.payload.items ?? []).find((item) => item.id === planId)
  if (!currentPlan?.next_run_at) failures.push('next_run_at was not kept after the scheduler fired')

  // A slot that becomes due while a run is active must be skipped, not
  // queued as a second run. The slot is consumed, so next_run_at advances.
  const skip = await checkSkippedSlot(original, waitMs)
  console.log(`skip-while-active: ${JSON.stringify(skip)}`)
  if (skip.error) failures.push(skip.error)

  const latest = await call('GET', '/api/admin/v1/ingestion/plans')
  const latestPlan = (latest.payload.items ?? []).find((item) => item.id === planId)
  const restored = await call('PATCH', `/api/admin/v1/ingestion/plans/${planId}`, {
    ...original,
    cron: '',
    expected_version: latestPlan?.version ?? skip.planVersion ?? currentPlan?.version,
  })
  if (restored.status !== 200) {
    failures.push(`restore manual-only plan status=${restored.status}`)
  } else if (restored.payload.next_run_at) {
    failures.push('next_run_at must be cleared when the cron expression is removed')
  }

  console.log(
    JSON.stringify({ ok: failures.length === 0, api, planId, failures }, null, 2),
  )
  process.exitCode = failures.length === 0 ? 0 : 1
} catch (error) {
  console.error(JSON.stringify({ ok: false, api, planId, error: String(error), failures }, null, 2))
  process.exitCode = 1
}
