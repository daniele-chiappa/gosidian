import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { AxiosError, AxiosHeaders } from 'axios'
import enUI from '@catalogs/ui.en.json'

vi.mock('@/api/notes', () => ({ getNewRow: vi.fn(), createRow: vi.fn() }))
import { createRow, getNewRow, type Note } from '@/api/notes'
import NewRowForm from '@/components/views/NewRowForm.vue'

const suggest = vi.mocked(getNewRow)
const create = vi.mocked(createRow)

const COLUMNS = [
  { name: 'id', type: 'text', required: true },
  { name: 'title', type: 'text', required: true },
  { name: 'status', type: 'select', options: ['todo', 'doing'], required: true },
  { name: 'owner', type: 'text', required: true },
  { name: 'points', type: 'number', required: true },
  { name: 'due', type: 'date' },
]

function httpError(status: number, details: Record<string, unknown>) {
  const res = {
    status,
    data: { error: { message: 'refused', details } },
    statusText: '',
    headers: {},
    config: { headers: new AxiosHeaders() },
  }
  return new AxiosError('refused', String(status), undefined, undefined, res)
}

function mountForm(values: Record<string, unknown> = { status: 'doing' }) {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
  const openWindow = vi.fn()
  const w = mount(NewRowForm, {
    props: { database: 'p/tasks.md', values: values as never },
    global: { plugins: [i18n], provide: { openWindow } },
    attachTo: document.body,
  })
  return { w, openWindow }
}

describe('NewRowForm', () => {
  beforeEach(() => {
    suggest.mockReset()
    create.mockReset()
    suggest.mockResolvedValue({
      name: 'T-010',
      source: 'p/tasks',
      columns: COLUMNS,
      preset: ['owner'],
    })
  })

  it('suggests the name, asks only the required fields nothing gives, and creates the row', async () => {
    create.mockResolvedValue({ path: 'p/tasks/T-010.md' } as Note)
    const { w, openWindow } = mountForm()
    await flushPromises()
    expect(suggest).toHaveBeenCalledWith('p/tasks.md')
    expect((w.find('[data-row-name]').element as HTMLInputElement).value).toBe('T-010')
    expect(document.activeElement).toBe(w.find('[data-row-title]').element)
    // status comes from the view, owner from the template: only points is asked.
    expect(w.findAll('[data-field]').map((e) => e.attributes('data-field'))).toEqual(['points'])
    expect(w.find('[data-row-values]').text()).toContain('status: doing')

    await w.find('[data-row-title]').setValue('Fix the menu')
    await w.find('[data-field="points"]').setValue('3')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(create).toHaveBeenCalledWith('p/tasks.md', {
      name: 'T-010',
      title: 'Fix the menu',
      values: { status: 'doing', points: 3 },
    })
    expect(openWindow).toHaveBeenCalledWith(
      expect.objectContaining({ props: { path: 'p/tasks/T-010.md' } }),
    )
    expect(w.emitted('done')).toEqual([['p/tasks/T-010.md']])
    w.unmount()
  })

  it('leaves out a value set to null, and asks for that field if required', async () => {
    const { w } = mountForm({ status: null })
    await flushPromises()
    expect(w.findAll('[data-field]').map((e) => e.attributes('data-field'))).toEqual([
      'status',
      'points',
    ])
    expect(w.find('[data-row-values]').exists()).toBe(false)
    w.unmount()
  })

  it('proposes the next name when the one given was taken meanwhile', async () => {
    create.mockRejectedValue(httpError(409, { name: 'T-011' }))
    const { w } = mountForm()
    await flushPromises()
    await w.find('[data-field="points"]').setValue('1')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect((w.find('[data-row-name]').element as HTMLInputElement).value).toBe('T-011')
    expect(w.find('[data-view-message]').text()).toBe(
      'T-010 already exists: T-011 proposed instead.',
    )
    expect(w.emitted('done')).toBeUndefined()
    w.unmount()
  })

  it('shows why the schema refuses the row', async () => {
    create.mockRejectedValue(httpError(422, { problems: [{ message: 'a' }, { message: 'b' }] }))
    const { w } = mountForm()
    await flushPromises()
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(w.find('[data-view-message]').text()).toBe('Row not created: a; b')
    w.unmount()
  })

  it('closes on Cancel and on Escape without creating', async () => {
    const { w } = mountForm()
    await flushPromises()
    await w.find('button[type="button"]').trigger('click')
    await w.find('form').trigger('keydown', { key: 'Escape' })
    expect(w.emitted('done')).toEqual([[], []])
    expect(create).not.toHaveBeenCalled()
    w.unmount()
  })
})
