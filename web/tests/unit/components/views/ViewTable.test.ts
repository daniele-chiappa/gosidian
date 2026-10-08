import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { AxiosError, AxiosHeaders } from 'axios'
import enUI from '@catalogs/ui.en.json'
import type { ViewData } from '@/api/preview'

vi.mock('@/api/notes', () => ({ patchFrontmatter: vi.fn() }))
import { patchFrontmatter } from '@/api/notes'
import ViewTable from '@/components/views/ViewTable.vue'

const patch = vi.mocked(patchFrontmatter)

function view(): ViewData {
  return {
    as: 'table',
    total: 2,
    database: 'p/docs/tasks.md',
    columns: [
      { name: 'title' },
      { name: 'status', type: 'select', options: ['todo', 'doing', 'done'], required: true },
      { name: 'done', type: 'checkbox' },
      { name: 'note' },
    ],
    rows: [
      {
        path: 'p/docs/tasks/T-1.md',
        title: 'One',
        modified: '2026-10-03',
        fields: { status: 'todo', note: 'n' },
        writable: true,
      },
      {
        path: 'p/docs/tasks/T-2.md',
        title: 'Two',
        modified: '2026-10-03',
        fields: { status: 'doing' },
        writable: false,
      },
    ],
  }
}

function mountTable(v = view()) {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
  return mount(ViewTable, { props: { view: v }, global: { plugins: [i18n] } })
}

function httpError(status: number, error: unknown) {
  return new AxiosError('failed', String(status), undefined, undefined, {
    status,
    statusText: '',
    headers: {},
    config: { headers: new AxiosHeaders() },
    data: { error },
  })
}

describe('ViewTable', () => {
  beforeEach(() => {
    patch.mockReset()
  })

  it('renders the rows like the server table, with links and read-only cells', () => {
    const w = mountTable()
    expect(w.findAll('tbody tr')).toHaveLength(2)
    const link = w.find('a.wikilink')
    expect(link.attributes('data-preview-path')).toBe('p/docs/tasks/T-1.md')
    expect(link.attributes('href')).toBe('/notes/p/docs/tasks/T-1.md')
    // Row two is not writable: its status is plain text, not a button.
    const rows = w.findAll('tbody tr')
    expect(rows[1]?.find('td[data-field="status"] button').exists()).toBe(false)
    expect(rows[1]?.find('td[data-field="status"]').text()).toBe('doing')
    // An undeclared field is never editable.
    expect(rows[0]?.find('td[data-field="note"] button').exists()).toBe(false)
  })

  it('saves a select with the value it showed as expect', async () => {
    patch.mockResolvedValue({} as never)
    const w = mountTable()
    await w.find('tbody tr td[data-field="status"] button').trigger('click')
    const select = w.find('select')
    expect(select.findAll('option').map((o) => o.text())).toEqual(['todo', 'doing', 'done'])
    await select.setValue('done')
    await flushPromises()
    expect(patch).toHaveBeenCalledWith('p/docs/tasks/T-1.md', {
      set: { status: 'done' },
      expect: { status: 'todo' },
    })
    expect(w.find('tbody tr td[data-field="status"]').text()).toBe('done')
  })

  it('toggles a checkbox at once', async () => {
    patch.mockResolvedValue({} as never)
    const w = mountTable()
    await w.find('tbody tr td[data-field="done"] input').setValue(true)
    await flushPromises()
    expect(patch).toHaveBeenCalledWith('p/docs/tasks/T-1.md', {
      set: { done: true },
      expect: { done: null },
    })
  })

  it('shows the current value when the field changed meanwhile', async () => {
    const conflict = httpError(409, {
      code: 'resource.conflict',
      details: { fields: { status: 'doing' } },
    })
    patch.mockImplementation(async () => {
      throw conflict
    })
    const w = mountTable()
    await w.find('tbody tr td[data-field="status"] button').trigger('click')
    await w.find('select').setValue('done')
    await flushPromises()
    expect(w.find('tbody tr td[data-field="status"]').text()).toBe('doing')
    expect(w.find('[data-view-message]').text()).toContain(
      'status was changed meanwhile and is now “doing”',
    )
  })

  it('keeps the old value and says why when the schema refuses it', async () => {
    const refused = httpError(422, {
      code: 'validation.invalid_format',
      details: {
        problems: [{ field: 'status', message: 'status: "x" is not one of todo, doing, done' }],
      },
    })
    patch.mockImplementation(async () => {
      throw refused
    })
    const w = mountTable()
    await w.find('tbody tr td[data-field="status"] button').trigger('click')
    await w.find('select').setValue('done')
    await flushPromises()
    expect(w.find('tbody tr td[data-field="status"]').text()).toBe('todo')
    expect(w.find('[data-view-message]').text()).toContain('Not saved: status: "x" is not one of')
  })

  it('checks the value the cell showed when it opened, even after a refresh', async () => {
    patch.mockResolvedValue({} as never)
    const w = mountTable()
    await w.find('tbody tr td[data-field="status"] button').trigger('click')
    // The view is computed again while the editor is open: status is now doing.
    const v = view()
    v.rows![0]!.fields.status = 'doing'
    await w.setProps({ view: v })
    await w.find('select').setValue('done')
    await flushPromises()
    expect(patch).toHaveBeenCalledWith('p/docs/tasks/T-1.md', {
      set: { status: 'done' },
      expect: { status: 'todo' },
    })
  })

  it('cancels with Escape without saving', async () => {
    const w = mountTable()
    await w.find('tbody tr td[data-field="status"] button').trigger('click')
    await w.find('select').trigger('keydown', { key: 'Escape' })
    expect(w.find('select').exists()).toBe(false)
    expect(patch).not.toHaveBeenCalled()
  })

  // BUG-110: an editor opened and left saved its text split anew on every
  // comma, a link's included.
  it('saves nothing when a list or relation cell is left as it was', async () => {
    const v = view()
    const row = v.rows![0]!
    v.columns!.push({ name: 'see', type: 'relation' }, { name: 'tags', type: 'list' })
    row.fields.see = '[[people/Rossi, Mario]]'
    row.fields.tags = ['Rossi, Mario']
    const w = mountTable(v)
    for (const field of ['see', 'tags']) {
      await w.find(`tbody tr td[data-field="${field}"] button`).trigger('click')
      await w.find(`tbody tr td[data-field="${field}"] input`).trigger('blur')
      await w.find(`tbody tr td[data-field="${field}"]`).trigger('focusout')
      await flushPromises()
    }
    expect(patch).not.toHaveBeenCalled()
    expect(row.fields).toMatchObject({ see: '[[people/Rossi, Mario]]', tags: ['Rossi, Mario'] })
  })

  it('says when no note matches, and when the table is cut', () => {
    expect(
      mountTable({ as: 'table', total: 0, rows: [], columns: [{ name: 'title' }] }).text(),
    ).toContain('No matching notes.')
    const v = view()
    v.total = 5
    expect(mountTable(v).text()).toContain('Showing 2 of 5 notes.')
  })
})
