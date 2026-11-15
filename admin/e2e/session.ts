import type { Page } from '@playwright/test'

const roles: Record<string, string[]> = {
  admin: [
    'read',
    'staff_manage',
    'authorization_write',
    'content_write',
    'ingestion_write',
    'media_write',
    'review',
    'publish',
  ],
  operator: ['read', 'content_write', 'ingestion_write', 'media_write', 'publish'],
  reviewer: ['read', 'review'],
}

// sessionBody builds the /auth/me payload for one role.
export function sessionBody(role: string) {
  return {
    staff: {
      id: 1,
      username: role === 'admin' ? 'admin' : role,
      display_name: role === 'admin' ? '管理员' : role,
      role,
      enabled: true,
      version: 1,
      created_at: '2026-11-14T10:00:00Z',
      updated_at: '2026-11-14T10:00:00Z',
    },
    permissions: roles[role] ?? [],
    csrf_token: 'csrf-test-token',
    expires_at: '2026-11-15T10:00:00Z',
  }
}

// mockSession makes every guarded page render as the given role.
export async function mockSession(page: Page, role = 'admin') {
  await page.route('**/api/admin/v1/auth/me', (route) =>
    route.fulfill({ status: 200, json: sessionBody(role) }),
  )
}
