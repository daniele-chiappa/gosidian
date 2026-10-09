import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import type { TreeNode as TN } from '@/api/tree'

vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ open: vi.fn() }),
}))
vi.mock('@/stores/access', () => ({
  useAccessStore: () => ({ canWrite: () => false, visibility: () => 'internal' }),
}))
vi.mock('@/composables/useRecentlyViewed', () => ({ useRecentlyViewed: () => ({ record: vi.fn() }) }))
vi.mock('@/composables/useTreeMenu', () => ({ useTreeMenu: () => ({ open: vi.fn() }) }))

import TreeNode from '@/components/domain/TreeNode.vue'

const dir = (path: string, children: TN[] = []): TN => ({
  name: path.split('/').pop()!,
  path,
  is_dir: true,
  kind: 'dir',
  children,
})

describe('TreeNode', () => {
  beforeEach(() => localStorage.clear())

  it('turns the chevron of an open folder only, not of the closed folders inside it', async () => {
    localStorage.setItem('gosidian.tree.open:p/docs', '1')
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
    const w = mount(TreeNode, {
      props: { node: dir('p/docs', [dir('p/docs/bugs', [dir('p/docs/bugs/old')])]) },
      global: { plugins: [i18n] },
    })
    const chevrons = w.findAll('summary > svg')
    expect(chevrons).toHaveLength(3)
    expect(chevrons.map((c) => c.classes('rotate-90'))).toEqual([true, false, false])

    // Opening the subfolder turns its own chevron.
    const sub = w.findAll('details')[1]!
    ;(sub.element as HTMLDetailsElement).open = true
    await sub.trigger('toggle')
    expect(w.findAll('summary > svg').map((c) => c.classes('rotate-90'))).toEqual([true, true, false])
    expect(localStorage.getItem('gosidian.tree.open:p/docs/bugs')).toBe('1')
  })
})
