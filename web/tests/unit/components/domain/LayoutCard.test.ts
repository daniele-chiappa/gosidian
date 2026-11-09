import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'

const apps = vi.hoisted(() => ({ forType: vi.fn() }))
vi.mock('@/composables/pluginApps', () => ({ appForType: apps.forType }))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ open: vi.fn(), identify: vi.fn() }),
}))

import LayoutCard from '@/components/domain/LayoutCard.vue'

// The card of a note with a design (IMP-226): its first page and the way
// to its editor. The design itself is a file of its own, out of the note.

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
const app = { plugin: 'layout', name: 'layout', title: 'Impaginazione', status: 'ok' }

function mountCard(preview = 'r/attachments/ab12.png', open = vi.fn(() => 'id'), more: Record<string, unknown> = {}) {
  const w = mount(LayoutCard, {
    props: { path: 'r/n1.md', preview, ...more },
    global: { plugins: [i18n()], provide: { openWindow: open } },
  })
  return { w, open }
}

describe('LayoutCard (IMP-226)', () => {
  beforeEach(() => {
    apps.forType.mockReset().mockResolvedValue(app)
  })

  it('shows the first page', async () => {
    const { w } = mountCard()
    await flushPromises()
    expect(w.text()).toContain('Page layout')
    const img = w.find('[data-layout-preview]')
    expect(img.attributes('src')).toBe('/vault-files/r/attachments/ab12.png')
    expect(img.attributes('alt')).toBe('First page of the layout')
  })

  it('has no picture without a preview', async () => {
    const { w } = mountCard('')
    await flushPromises()
    expect(w.find('[data-layout-preview]').exists()).toBe(false)
    expect(w.find('[data-layout-open]').exists()).toBe(true)
  })

  it('opens the note in its app', async () => {
    const { w, open } = mountCard()
    await flushPromises()
    expect(apps.forType).toHaveBeenCalledWith('layout')
    await w.find('[data-layout-open]').trigger('click')
    expect(open).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'app', key: 'app:r/n1.md', props: { path: 'r/n1.md' } }),
    )
  })

  it('says what it takes when no app opens it', async () => {
    apps.forType.mockResolvedValue(null)
    const { w } = mountCard()
    await flushPromises()
    expect(w.find('[data-layout-open]').exists()).toBe(false)
    expect(w.find('[data-layout-no-app]').exists()).toBe(true)
  })
})

describe('LayoutCard, the PDF beside the note (IMP-229)', () => {
  beforeEach(() => {
    apps.forType.mockReset().mockResolvedValue(app)
  })
  const pdfs = [
    { label: 'V3', path: 'r/attachments/v3.pdf' },
    { label: 'draft', path: 'r/attachments/d.pdf' },
  ]
  const links = (w: ReturnType<typeof mountCard>['w']) =>
    w.findAll('[data-layout-pdf]').map((a) => [a.text(), a.attributes('href'), a.attributes('download')])

  it('links the pinned version\'s PDF for readers, named after the note and the version', async () => {
    const { w } = mountCard(undefined, undefined, { pdfs, pinned: 'V3' })
    await flushPromises()
    expect(links(w)).toEqual([['PDF V3', '/vault-files/r/attachments/v3.pdf', 'n1-V3.pdf']])
  })

  it('adds the draft\'s for who can edit', async () => {
    const { w } = mountCard(undefined, undefined, { pdfs, pinned: 'V3', canEdit: true })
    await flushPromises()
    expect(links(w)).toEqual([
      ['PDF V3', '/vault-files/r/attachments/v3.pdf', 'n1-V3.pdf'],
      ['PDF draft', '/vault-files/r/attachments/d.pdf', 'n1-draft.pdf'],
    ])
  })

  it('links the draft\'s when no version is pinned, and nothing without a PDF', async () => {
    const { w } = mountCard(undefined, undefined, { pdfs, canEdit: true })
    await flushPromises()
    expect(links(w)).toEqual([['PDF draft', '/vault-files/r/attachments/d.pdf', 'n1-draft.pdf']])
    const none = mountCard(undefined, undefined, { pdfs: [], pinned: 'V3' })
    await flushPromises()
    expect(none.w.find('[data-layout-pdf]').exists()).toBe(false)
  })
})
