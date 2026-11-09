import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'

vi.mock('@/api/query', async (orig) => ({
  ...(await orig<typeof import('@/api/query')>()),
  runQuery: vi.fn(async () => ({ notes: [], total: 0, truncated: false })),
}))
vi.mock('@/api/projects', () => ({ listProjects: vi.fn(async () => []) }))
const { store } = vi.hoisted(() => {
  const store = {
    windows: [] as { id: string; type: string; key: string; title: string; props: Record<string, unknown> }[],
    open: vi.fn(),
    // As plancia's, identify merges the props it is given into the window's.
    identify: vi.fn((id: string, _key: string, props: Record<string, unknown>) => {
      const win = store.windows.find((w) => w.id === id)
      if (win) Object.assign(win.props, props)
    }),
  }
  return { store }
})
vi.mock('plancia', async (orig) => ({ ...(await orig<typeof import('plancia')>()), useWindowsStore: () => store }))

import type { WindowInstance } from 'plancia'
import type { QueryCondition } from '@/api/query'
import { codec } from '@/composables/planciaKey'
import QueryView from '@/views/QueryView.vue'

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
/** A query window's token in the address. */
/** The order's select, after the operators' ones. */
const order = (w: ReturnType<typeof mount>) => w.findAll('select').at(-1)!
const addressOf = (props: Record<string, unknown>) =>
  decodeURIComponent(codec.encode({ type: 'query', props } as unknown as WindowInstance) ?? '')

describe('QueryView, its query in the address (BUG-158)', () => {
  afterEach(() => {
    store.windows = []
  })

  it('takes the project, the sort and the fields out of the address when they are cleared', async () => {
    const where: QueryCondition[] = [{ field: 'type', op: 'exists' }]
    const win = { id: 'q1', type: 'query', key: 'query', title: '', props: { where, project: 'agenti' } as Record<string, unknown> }
    store.windows = [win]
    const w = mount(QueryView, { props: { where, project: 'agenti' }, global: { plugins: [i18n()] } })
    await flushPromises()
    await w.find('#query-sort-field').setValue('title')
    await order(w).setValue('desc')
    await w.find('#query-fields-input').setValue('status')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(addressOf(win.props)).toBe(
      addressOf({ where, project: 'agenti', sort: 'title', order: 'desc', fields: ['status'] }),
    )

    await w.find('#query-project').setValue('')
    await w.find('#query-sort-field').setValue('')
    await order(w).setValue('')
    await w.find('#query-fields-input').setValue('')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(addressOf(win.props)).toBe(addressOf({ where }))
  })
})
