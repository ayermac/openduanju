// Environment guard for the development and acceptance scripts.
//
// The scripts must act on the intended environment: the API has to answer on
// the expected port and the server has to be connected to the expected
// database. The check runs before the script touches any data, so a wrong
// environment is reported instead of being written to.
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..')

// expectedDatabase is the database the scripts are allowed to touch. It
// comes from EXPECTED_DATABASE, otherwise from the local .env.
export function expectedDatabase() {
  if (process.env.EXPECTED_DATABASE) return process.env.EXPECTED_DATABASE
  return readLocalEnv().MYSQL_DATABASE || ''
}

// expectedPort is the port the admin API has to listen on.
export function expectedPort() {
  return process.env.EXPECTED_API_PORT || '8000'
}

// checkRuntime returns the reasons why the target environment is not the
// expected one. An empty list means the environment may be used.
export async function checkRuntime(api, token) {
  const failures = []
  const port = new URL(api).port || '80'
  if (port !== expectedPort()) {
    failures.push(`api port ${port} does not match the expected ${expectedPort()}`)
  }
  if (!token) {
    failures.push('ADMIN_AUTH_TOKEN is required to verify the target environment')
    return failures
  }
  let response
  try {
    response = await fetch(`${api}/api/admin/v1/runtime`, {
      headers: { Authorization: `Bearer ${token}` },
    })
  } catch (error) {
    failures.push(`runtime info request failed: ${error}`)
    return failures
  }
  if (response.status !== 200) {
    failures.push(`runtime info status=${response.status}`)
    return failures
  }
  const info = await response.json()
  const expected = expectedDatabase()
  if (!expected) {
    failures.push('EXPECTED_DATABASE is not set and .env does not define MYSQL_DATABASE')
  } else if (info.database !== expected) {
    failures.push(`server database "${info.database}" does not match the expected "${expected}"`)
  }
  return failures
}

function readLocalEnv() {
  try {
    const content = readFileSync(resolve(repoRoot, '.env'), 'utf8')
    const values = {}
    for (const line of content.split('\n')) {
      const trimmed = line.trim()
      if (!trimmed || trimmed.startsWith('#')) continue
      const index = trimmed.indexOf('=')
      if (index < 0) continue
      values[trimmed.slice(0, index).trim()] = trimmed.slice(index + 1).trim()
    }
    return values
  } catch {
    return {}
  }
}
