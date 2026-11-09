import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import type { CatalogFont } from '@/api/fonts'

vi.mock('@/api/fonts', () => ({ listFonts: vi.fn(), addFont: vi.fn(), removeFont: vi.fn() }))
vi.mock('@/composables/useConfirm', () => ({ confirmAction: vi.fn(async () => true) }))

import { addFont, listFonts, removeFont } from '@/api/fonts'
import { confirmAction } from '@/composables/useConfirm'
import AdminFontsView from '@/views/admin/AdminFontsView.vue'

const font = (over: Partial<CatalogFont> = {}): CatalogFont => ({
  id: '0123456789abcdef',
  family: 'Corpo',
  style: 'Bold',
  format: 'ttf',
  faces: 1,
  embedding: 'installable',
  bytes: 2097152,
  added_at: '2026-11-06T10:00:00Z',
  ...over,
})

async function mountView() {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
  const w = mount(AdminFontsView, { global: { plugins: [i18n] } })
  await flushPromises()
  return w
}

async function choose(w: Awaited<ReturnType<typeof mountView>>, ...files: File[]) {
  const input = w.find('[data-font-input]')
  Object.defineProperty(input.element, 'files', { value: files, configurable: true })
  await input.trigger('change')
  await flushPromises()
}

describe('AdminFontsView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(listFonts).mockResolvedValue([font()])
  })

  it('lists the catalog with what each file says of itself', async () => {
    const w = await mountView()
    const row = w.find('[data-font]')
    expect(row.text()).toContain('Corpo')
    expect(row.text()).toContain('Bold')
    expect(row.text()).toContain('Installable')
    expect(row.text()).toContain('2.0')
  })

  it('says it is empty, and that the editor keeps its own', async () => {
    vi.mocked(listFonts).mockResolvedValue([])
    const w = await mountView()
    expect(w.text()).toContain('No font added. The editor has only its own.')
  })

  it('points out a licence that limits the font, and refuses nothing for it', async () => {
    vi.mocked(listFonts).mockResolvedValue([font({ embedding: 'preview' }), font({ id: 'fedcba9876543210', embedding: undefined })])
    const w = await mountView()
    const cells = w.findAll('[data-font-licence]')
    expect(cells[0]?.text()).toBe('Preview and print only')
    expect(cells[0]?.classes()).toContain('text-warning')
    expect(cells[1]?.text()).toBe('Not stated')
    expect(cells[1]?.classes()).not.toContain('text-warning')
    expect(w.text()).toContain('Remove')
  })

  it('adds the files chosen, in order, and reads the list again', async () => {
    vi.mocked(addFont).mockResolvedValue(font())
    const w = await mountView()
    const [a, b] = [new File(['a'], 'a.ttf'), new File(['b'], 'b.otf')]
    await choose(w, a, b)
    expect(vi.mocked(addFont).mock.calls.map((c) => c[0])).toEqual([a, b])
    expect(listFonts).toHaveBeenCalledTimes(2)
  })

  it('keeps the error of a file it could not add', async () => {
    vi.mocked(addFont).mockRejectedValue(new Error('down'))
    const w = await mountView()
    await choose(w, new File(['a'], 'a.woff'))
    expect(w.text()).toContain('The font could not be added')
    expect(w.find('[data-font]').exists()).toBe(true)
  })

  it('asks before it removes, naming the font', async () => {
    const w = await mountView()
    await w.find('[data-font] button').trigger('click')
    await flushPromises()
    expect(vi.mocked(confirmAction).mock.calls.at(0)?.[0]).toContain('Corpo Bold')
    expect(removeFont).toHaveBeenCalledWith('0123456789abcdef')
  })

  it('leaves the font when the answer is no', async () => {
    vi.mocked(confirmAction).mockResolvedValueOnce(false)
    const w = await mountView()
    await w.find('[data-font] button').trigger('click')
    await flushPromises()
    expect(removeFont).not.toHaveBeenCalled()
  })
})
