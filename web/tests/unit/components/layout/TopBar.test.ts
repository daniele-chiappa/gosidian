import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'

const windows = vi.hoisted(() => ({ open: vi.fn((): string => '') }))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => windows,
  usePlanciaFocus: () => ({ focusWindow: vi.fn(async () => true) }),
}))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }))

import TopBar from '@/components/layout/TopBar.vue'

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })

describe('TopBar, the Search button (IMP-218, F-06)', () => {
  it('opens Search and shows no shortcut: Ctrl+K opens the palette, not Search', async () => {
    const w = mount(TopBar, {
      global: { plugins: [createPinia(), i18n()], stubs: { InsightsBadge: true, UploadsBadge: true } },
    })
    const button = w.find('button')
    expect(button.text()).toBe('Search')
    expect(button.find('kbd').exists()).toBe(false)
    await button.trigger('click')
    expect(windows.open).toHaveBeenCalledWith(expect.objectContaining({ type: 'search' }))
  })
})
