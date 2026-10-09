import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

// The views are lazy; none is rendered here.
vi.mock('@/views/LoginView.vue', () => ({ default: {} }))
vi.mock('@/components/layout/AppShell.vue', () => ({ default: {} }))
vi.mock('@/views/OAuthConsentView.vue', () => ({ default: {} }))

import { router } from '@/router'
import { useAuthStore } from '@/stores/auth'

describe('router guard and invites (BUG-099)', () => {
  beforeEach(async () => {
    setActivePinia(createPinia())
    const auth = useAuthStore()
    auth.token = 'tok'
    auth.user = { id: 'owner', username: 'owner', role: 'owner' }
    await router.push('/')
  })

  it('opens an invite link in a signed-in browser', async () => {
    await router.push('/login?invite=inv_x')
    expect(router.currentRoute.value.name).toBe('login')
  })

  it('keeps the sign-in that follows the sign-up', async () => {
    await router.push('/login?invite=inv_x')
    await router.replace('/login')
    expect(router.currentRoute.value.fullPath).toBe('/login')
  })

  it('sends a session home from the plain login page', async () => {
    await router.push('/login')
    expect(router.currentRoute.value.name).toBe('home')
  })
})
