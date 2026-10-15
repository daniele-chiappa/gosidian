import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import type { CutView } from '@/api/preview'
import type { MarkSet } from '@/api/marks'

const open = vi.fn()
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ windows: [], open }),
}))
// A spy around the real sanitizer: the test sees what goes through it.
vi.mock('@/components/domain/sanitizePreview', async (orig) => {
  const real = await orig<typeof import('@/components/domain/sanitizePreview')>()
  return { ...real, sanitizePreviewHtml: vi.fn(real.sanitizePreviewHtml) }
})

import CutPreview from '@/components/domain/CutPreview.vue'
import { sanitizePreviewHtml } from '@/components/domain/sanitizePreview'

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })

const view = (over: Partial<CutView> = {}): CutView => ({
  sections: 1,
  paragraphs: 2,
  media: { images: 2, icons: 1, described: 1, missing: 1, word_alt: 1 },
  max_chars: 6000,
  stored: 0,
  orphans: 0,
  blocks: [
    { id: 'aaa.s1', level: 1, section: 'Setup', chars: 120, hash: 'h1', head: '<h2>Setup</h2>', html: '' },
    {
      id: 'aaa.s1.p1',
      parent: 'aaa.s1',
      level: 2,
      section: 'Setup',
      chars: 60,
      hash: 'h2',
      html: '<p>Press <img class="gosidian-cut-thumb" src="/vault-files/p/a/i.png" alt="">[icon: Search] to look. <a data-preview-path="p/other.md" href="#">other</a></p>',
    },
    {
      id: 'aaa.s1.p2',
      parent: 'aaa.s1',
      level: 2,
      section: 'Setup',
      chars: 7000,
      hash: 'h3',
      over: true,
      html: '<p><span class="gosidian-cut-missing">[image]</span></p>',
    },
  ],
  ...over,
})

describe('CutPreview (IMP-170)', () => {
  it('draws the sections with their paragraphs, the images small and those without a description marked', async () => {
    const w = mount(CutPreview, { props: { view: view() }, global: { plugins: [i18n()] } })
    expect(w.find('[data-cut-summary]').text()).toContain('1 section · 2 paragraphs')
    expect(w.find('[data-cut-summary]').text()).toContain('without a description: 1')
    const sec = w.find('[data-cut-section]')
    expect(sec.classes()).toContain('border-accent')
    expect(sec.text()).toContain('Setup')
    const paras = w.findAll('[data-cut-paragraph]')
    expect(paras).toHaveLength(2)
    expect(paras[0]!.classes()).toContain('border-info')
    expect(paras[0]!.find('img.gosidian-cut-thumb').exists()).toBe(true)
    expect(paras[1]!.find('.gosidian-cut-missing').exists()).toBe(true)
    expect(paras[1]!.text()).toContain('over 6000 characters')
    expect(w.find('[data-cut-states]').exists()).toBe(false)
    await paras[0]!.find('a').trigger('click')
    expect(open).toHaveBeenCalledWith(expect.objectContaining({ type: 'note', props: { path: 'p/other.md' } }))
  })

  it('colors each block by what the collection holds, and says when nothing matches', () => {
    const blocks = view().blocks.map((b, i) => ({ ...b, state: (['same', 'changed', 'missing'] as const)[i] }))
    let w = mount(CutPreview, {
      props: { view: view({ blocks, stored: 3, orphans: 1, states: { same: 1, changed: 1, missing: 1 } }) },
      global: { plugins: [i18n()] },
    })
    expect(w.find('[data-cut-states]').text()).toContain('the same 1 · changed 1 · missing 1 · no longer in the note 1')
    expect(w.find('[data-cut-section]').classes()).toContain('border-success')
    const paras = w.findAll('[data-cut-paragraph]')
    expect(paras[0]!.classes()).toContain('border-warning')
    expect(paras[1]!.classes()).toContain('border-text-muted')
    expect(w.find('[data-cut-hint]').exists()).toBe(false)

    w = mount(CutPreview, {
      props: { view: view({ stored: 3, states: { changed: 3 } }) },
      global: { plugins: [i18n()] },
    })
    expect(w.find('[data-cut-hint]').exists()).toBe(true)
    w = mount(CutPreview, { props: { view: view({ error: 'plugin vec-pg is not exposed' }) }, global: { plugins: [i18n()] } })
    expect(w.find('[data-cut-error]').text()).toContain('plugin vec-pg is not exposed')
  })

  it('puts the marks on the blocks they name, by base or by section and paragraph, and hides a set', async () => {
    const sets: MarkSet[] = [
      {
        path: 'p/a.md',
        set: 'cut',
        hash: 'h',
        created: '2026-10-14T12:00:00Z',
        stale: false,
        marks: [
          { base: 'aaa.s1.p2', color: 'red', label: 'no description', text: 'image 3' },
          { section: 'Setup', block: 1, color: 'orange', label: 'long' },
          { section: 'Setup', color: 'green', label: 'checked' },
          { section: 'Nowhere', color: 'grey' },
        ],
      },
      { path: 'p/a.md', set: 'old', hash: 'x', created: '2026-10-13T12:00:00Z', stale: true, marks: [{ section: 'Setup', color: 'cyan' }] },
    ]
    const w = mount(CutPreview, { props: { view: view(), marks: sets }, global: { plugins: [i18n()] } })
    const sec = w.find('[data-cut-section]')
    expect(sec.classes()).toContain('border-r-success')
    expect(sec.find('p').text()).toContain('checked')
    const paras = w.findAll('[data-cut-paragraph]')
    expect(paras[0]!.classes()).toContain('border-r-warning')
    expect(paras[1]!.classes()).toContain('border-r-danger')
    expect(paras[1]!.find('[data-mark]').attributes('title')).toBe('cut: image 3')
    expect(w.find('[data-mark-set="cut"] [data-mark-lost]').text()).toContain('1 not placed')
    expect(w.find('[data-mark-set="old"] [data-mark-stale]').exists()).toBe(true)
    await w.find('[data-mark-set="cut"] input').setValue(false)
    expect(w.findAll('[data-cut-paragraph]')[1]!.find('[data-mark]').exists()).toBe(false)
    expect(w.find('[data-cut-section]').classes()).toContain('border-r-info')
  })

  // The server renders each block as the preview does, unsanitized: what
  // a note's HTML may do is the sanitizer's to say (its own tests), and
  // this view must show nothing that did not go through it.
  it("shows each heading and block only as the preview's sanitizer gives them", async () => {
    const { sanitizePreviewHtml: real } = await vi.importActual<typeof import('@/components/domain/sanitizePreview')>(
      '@/components/domain/sanitizePreview',
    )
    const sanitize = vi.mocked(sanitizePreviewHtml)
    sanitize.mockClear()
    sanitize.mockImplementation((s: string) => `<b data-clean>${s.length}</b>`)
    try {
      const v = view()
      const w = mount(CutPreview, { props: { view: v }, global: { plugins: [i18n()] } })
      expect(sanitize).toHaveBeenCalledWith('<h2>Setup</h2>')
      expect(sanitize).toHaveBeenCalledWith(v.blocks[1]!.html)
      expect(sanitize).toHaveBeenCalledWith(v.blocks[2]!.html)
      expect(w.findAll('[data-clean]')).toHaveLength(3)
      expect(w.find('h2').exists()).toBe(false)
      expect(w.find('img').exists()).toBe(false)
    } finally {
      sanitize.mockImplementation(real)
    }
  })

  // The links of the cut open as in the preview: none navigates the SPA
  // away, which would drop the drafts of every window (BUG-117, S7-3). The
  // HTML is taken as the server wrote it: happy-dom's DOMPurify drops the
  // headings an anchor scrolls to.
  it('keeps the SPA where it is on an anchor or a web link', async () => {
    const { sanitizePreviewHtml: real } = await vi.importActual<typeof import('@/components/domain/sanitizePreview')>(
      '@/components/domain/sanitizePreview',
    )
    const sanitize = vi.mocked(sanitizePreviewHtml)
    sanitize.mockImplementation((s: string) => s)
    const tab = vi.spyOn(window, 'open').mockReturnValue(null)
    try {
      const blocks = view().blocks.map((b) =>
        b.id === 'aaa.s1.p1'
          ? { ...b, html: '<p><a id="anchor" href="#setup">up</a> <a id="web" href="https://example.com/x">web</a></p>' }
          : b,
      )
      const w = mount(CutPreview, { props: { view: view({ blocks }), notePath: 'p/a.md' }, global: { plugins: [i18n()] } })
      const click = (el: Element) => {
        const ev = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 })
        el.dispatchEvent(ev)
        return ev
      }
      const heading = w.find('h2').element as HTMLElement
      heading.scrollIntoView = vi.fn()
      expect(click(w.find('#anchor').element).defaultPrevented).toBe(true)
      expect(heading.scrollIntoView).toHaveBeenCalled()

      expect(click(w.find('#web').element).defaultPrevented).toBe(true)
      expect(tab).toHaveBeenCalledWith('https://example.com/x', '_blank', 'noopener,noreferrer')
    } finally {
      tab.mockRestore()
      sanitize.mockImplementation(real)
    }
  })
})
