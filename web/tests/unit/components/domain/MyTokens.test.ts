import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import type { MCPTokenCreated } from '@/api/admin'

vi.mock('@/api/me', () => ({ listMyTokens: vi.fn(async () => []), createMyToken: vi.fn(), revokeMyToken: vi.fn() }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ canWrite: true }) }))
vi.mock('@/stores/access', () => ({ useAccessStore: () => ({ list: [], loaded: true, load: vi.fn() }) }))

import { createMyToken } from '@/api/me'
import MyTokens from '@/components/domain/MyTokens.vue'

describe('MyTokens', () => {
  it('mints one token for a double submit, keeping its secret', async () => {
    let reply: (v: MCPTokenCreated) => void = () => {}
    vi.mocked(createMyToken).mockReturnValue(new Promise((r) => (reply = r)))
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
    const w = mount(MyTokens, { global: { plugins: [i18n] } })
    await flushPromises()
    await w.find('input[type="text"]').setValue('laptop')
    await w.find('input[data-token-password]').setValue('my-password-1')
    const form = w.find('form').element
    form.dispatchEvent(new Event('submit'))
    form.dispatchEvent(new Event('submit'))
    reply({ id: 'tok1', name: 'laptop', token: 'gosidian_secret' } as unknown as MCPTokenCreated)
    await flushPromises()
    expect(createMyToken).toHaveBeenCalledTimes(1)
  })
})
