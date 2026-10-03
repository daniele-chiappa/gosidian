import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { AxiosError, AxiosHeaders } from 'axios'
import enUI from '@catalogs/ui.en.json'
import type { ViewData } from '@/api/preview'

vi.mock('@/api/notes', () => ({
  patchFrontmatter: vi.fn(),
  getNewRow: vi.fn().mockResolvedValue({ name: '', source: 'p', columns: [] }),
  createRow: vi.fn(),
}))
import { patchFrontmatter } from '@/api/notes'
import ViewBoard from '@/components/views/ViewBoard.vue'
import NewRowForm from '@/components/views/NewRowForm.vue'

const patch = vi.mocked(patchFrontmatter)

function board(over: Partial<ViewData> = {}): ViewData {
  return {
    as: 'board',
    total: 3,
    group: { name: 'status', type: 'select', options: ['todo', 'doing', 'done'] },
    groups: ['todo', 'doing', 'done'],
    columns: [{ name: 'title' }, { name: 'priority', type: 'select' }],
    rows: [
      {
        path: 'p/T-1.md',
        title: 'One',
        modified: '',
        fields: { status: 'todo', priority: 'high' },
        writable: true,
      },
      { path: 'p/T-2.md', title: 'Two', modified: '', fields: { status: 'todo' }, writable: false },
      {
        path: 'p/T-3.md',
        title: 'Three',
        modified: '',
        fields: { status: 'done' },
        writable: true,
      },
    ],
    ...over,
  }
}

function mountBoard(v = board()) {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
  return mount(ViewBoard, {
    props: { view: v },
    global: { plugins: [i18n], provide: { openWindow: vi.fn() } },
  })
}

const column = (w: ReturnType<typeof mountBoard>, value: string) =>
  w.find(`section[data-group="${value}"]`)

async function moveWithMenu(w: ReturnType<typeof mountBoard>, path: string, to: string) {
  await w.find(`li[data-card="${path}"] button`).trigger('click')
  await w.find(`li[data-card="${path}"] select`).setValue(to)
  await flushPromises()
}

describe('ViewBoard', () => {
  beforeEach(() => {
    patch.mockReset()
  })

  it('shows a column per value, empty ones included, with the cards in them', () => {
    const w = mountBoard()
    expect(w.findAll('section[data-group]').map((s) => s.attributes('data-group'))).toEqual([
      'todo',
      'doing',
      'done',
    ])
    expect(column(w, 'todo').findAll('li[data-card]')).toHaveLength(2)
    expect(column(w, 'doing').text()).toContain('No notes')
    // A card shows its fields but the group_by one.
    expect(w.find('li[data-card="p/T-1.md"]').text()).toContain('priority:')
    expect(w.find('li[data-card="p/T-1.md"]').text()).not.toContain('status:')
  })

  it('moves a card with its menu, the column it was in as expect', async () => {
    patch.mockResolvedValue({} as never)
    const w = mountBoard()
    await moveWithMenu(w, 'p/T-1.md', 'doing')
    expect(patch).toHaveBeenCalledWith('p/T-1.md', {
      set: { status: 'doing' },
      expect: { status: 'todo' },
    })
    expect(column(w, 'doing').find('li[data-card="p/T-1.md"]').exists()).toBe(true)
  })

  it('writes a bool for a checkbox column, and removes the field in the no-value column', async () => {
    patch.mockResolvedValue({} as never)
    const w = mountBoard(
      board({
        group: { name: 'done', type: 'checkbox' },
        groups: ['false', 'true', ''],
        rows: [
          {
            path: 'p/T-1.md',
            title: 'One',
            modified: '',
            fields: { done: 'false' },
            writable: true,
          },
        ],
      }),
    )
    await moveWithMenu(w, 'p/T-1.md', 'true')
    expect(patch).toHaveBeenLastCalledWith('p/T-1.md', {
      set: { done: true },
      expect: { done: 'false' },
    })
    await moveWithMenu(w, 'p/T-1.md', '')
    expect(patch).toHaveBeenLastCalledWith('p/T-1.md', {
      unset: ['done'],
      expect: { done: 'true' },
    })
  })

  it('puts a card back where it now is when it was moved meanwhile', async () => {
    const conflict = new AxiosError('failed', '409', undefined, undefined, {
      status: 409,
      statusText: '',
      headers: {},
      config: { headers: new AxiosHeaders() },
      data: { error: { details: { fields: { status: 'done' } } } },
    })
    patch.mockImplementation(async () => {
      throw conflict
    })
    const w = mountBoard()
    await moveWithMenu(w, 'p/T-1.md', 'doing')
    expect(column(w, 'done').find('li[data-card="p/T-1.md"]').exists()).toBe(true)
    expect(w.find('[data-view-message]').text()).toContain('changed meanwhile')
  })

  it('does not move a card the reader may not write', () => {
    const w = mountBoard()
    const card = w.find('li[data-card="p/T-2.md"]')
    expect(card.attributes('draggable')).toBe('false')
    expect(card.find('button').exists()).toBe(false)
  })

  it("opens a new row's form in a column, with that column's value", async () => {
    const w = mountBoard(
      board({
        creatable: true,
        database: 'p/tasks.md',
        defaults: { priority: 'high', status: 'todo' },
      }),
    )
    expect(
      w.find('section[data-group="doing"] [data-new-row-button]').attributes('aria-label'),
    ).toBe('New row in doing')
    await w.find('section[data-group="doing"] [data-new-row-button]').trigger('click')
    const form = w.findComponent(NewRowForm)
    expect(form.props('values')).toEqual({ priority: 'high', status: 'doing' })
    expect(w.find('section[data-group="doing"] [data-new-row]').exists()).toBe(true)
    form.vm.$emit('done')
    await flushPromises()
    expect(w.findComponent(NewRowForm).exists()).toBe(false)
  })

  it('offers no new row to a reader who may not add one', () => {
    expect(mountBoard().find('[data-new-row-button]').exists()).toBe(false)
  })
})
