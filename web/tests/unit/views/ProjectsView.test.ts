import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import itUI from '@catalogs/ui.it.json'
import type { Project } from '@/api/projects'

vi.mock('@/api/projects', () => ({
  listProjects: vi.fn(),
  createProject: vi.fn(),
  updateProject: vi.fn(),
  deleteProject: vi.fn(),
  exportProject: vi.fn(),
}))
vi.mock('@/api/settings', () => ({ getSettings: vi.fn().mockResolvedValue({ anchors_enabled: false, globals_enabled: false }) }))
vi.mock('@/composables/useConfirm', () => ({ askText: vi.fn(), confirmAction: vi.fn() }))
vi.mock('@/stores/tree', () => ({ useTreeStore: () => ({ refresh: vi.fn() }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ canWrite: true, isAnonymous: false, isOwner: true }) }))
vi.mock('@/stores/access', () => ({ useAccessStore: () => ({ canCreateProjects: true, load: vi.fn() }) }))
vi.mock('@/stores/ui', () => ({ useUIStore: () => ({ pipelinesEnabled: false }) }))
vi.mock('plancia', () => ({ useWindowsStore: () => ({ open: vi.fn() }) }))

import { listProjects, updateProject } from '@/api/projects'
import { askText } from '@/composables/useConfirm'
import ProjectsView from '@/views/ProjectsView.vue'

const alpha: Project = {
  name: 'alpha',
  note_count: 2,
  hidden_from_mcp: false,
  skip_git_sync: false,
  visibility: 'private',
  public: false,
  access: 'admin',
  members_count: 0,
  teams_count: 0,
  use_globals: false,
  use_anchors: false,
  use_tag_vocabulary: false,
  lean_read_bootstrap: false,
  allow_local_mirror: false,
}

function mountView() {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
  return mount(ProjectsView, { global: { plugins: [i18n] } })
}

async function renameTo(links?: { rewritten: number; not_rewritten: number }) {
  vi.mocked(listProjects).mockResolvedValueOnce([alpha]).mockResolvedValue([{ ...alpha, name: 'delta' }])
  vi.mocked(askText).mockResolvedValue('delta')
  vi.mocked(updateProject).mockResolvedValue({ ...alpha, name: 'delta', links } as Project)
  const w = mountView()
  await flushPromises()
  await w.findAll('button').find((b) => b.text() === 'Rename')!.trigger('click')
  await flushPromises()
  return w
}

describe('ProjectsView rename (BUG-153)', () => {
  beforeEach(() => vi.clearAllMocks())

  it('says how many notes the rename rewrote, and how many keep the old name', async () => {
    const w = await renameTo({ rewritten: 3, not_rewritten: 2 })
    expect(updateProject).toHaveBeenCalledWith('alpha', { new_name: 'delta' })
    const status = w.find('[role="status"]')
    expect(status.exists()).toBe(true)
    expect(status.text()).toContain('3 note(s)')
    expect(status.text()).toContain('2 note(s) in projects you may not write')
  })

  it('says nothing when no note named the project', async () => {
    const w = await renameTo({ rewritten: 0, not_rewritten: 0 })
    expect(w.find('[role="status"]').exists()).toBe(false)
  })
})

describe('ProjectsView, what its buttons say (IMP-220, F-25)', () => {
  beforeEach(() => vi.clearAllMocks())

  it('says what the grants button counts', async () => {
    vi.mocked(listProjects).mockResolvedValue([{ ...alpha, members_count: 1, teams_count: 0 }])
    const w = mountView()
    await flushPromises()
    expect(w.find('[data-grants]').text()).toBe('Members: 1 · teams: 0')
  })

  it("names the reader's level in their language, the code in the tooltip (IMP-220, F-20)", async () => {
    vi.mocked(listProjects).mockResolvedValue([{ ...alpha, access: 'write' }])
    const i18n = createI18n({ legacy: false, locale: 'it', fallbackLocale: 'en', messages: { it: itUI, en: enUI } })
    const w = mount(ProjectsView, { global: { plugins: [i18n] } })
    await flushPromises()
    const level = w.find('[data-level]')
    expect(level.text()).toBe('scrittura')
    expect(level.attributes('title')).toBe('write — Il tuo livello su questo progetto: scrittura')
  })

  it('names its flags in words, the code in the tooltip and the legend', async () => {
    vi.mocked(listProjects).mockResolvedValue([alpha])
    const w = mountView()
    await flushPromises()
    const chip = w.findAll('button').find((b) => b.text() === 'not in git')
    expect(chip).toBeDefined()
    expect(chip!.attributes('title')).toMatch(/^skip-git — /)
    expect(w.findAll('button').some((b) => b.text() === 'skip-git')).toBe(false)
    const terms = w.findAll('[data-flags-legend] dt').map((d) => d.text())
    expect(terms[0]).toBe('not in git skip-git')
    expect(terms).toHaveLength(11)
  })

  it('names the flag a failed change was about in words', async () => {
    vi.mocked(listProjects).mockResolvedValue([alpha])
    vi.mocked(updateProject).mockRejectedValue(new Error('boom'))
    const w = mountView()
    await flushPromises()
    await w.findAll('button').find((b) => b.text() === 'short read')!.trigger('click')
    await flushPromises()
    expect(w.text()).toContain('short read: ')
  })
})

describe('ProjectsView, a project chosen in the palette (BUG-160)', () => {
  beforeEach(() => vi.clearAllMocks())

  it('gives the focus to its name, and to the next one chosen while open', async () => {
    vi.mocked(listProjects).mockResolvedValue([alpha, { ...alpha, name: 'beta' }])
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
    const w = mount(ProjectsView, { props: { project: 'beta', projectAt: 1 }, global: { plugins: [i18n] }, attachTo: document.body })
    await flushPromises()
    const name = (n: string) => w.findAll('button').find((b) => b.text() === n)!.element
    expect(document.activeElement).toBe(name('beta'))
    await w.setProps({ project: 'alpha', projectAt: 2 })
    await flushPromises()
    expect(document.activeElement).toBe(name('alpha'))
    // The same one chosen again, the focus gone meanwhile: it comes back.
    name('alpha').blur()
    await w.setProps({ project: 'alpha', projectAt: 3 })
    await flushPromises()
    expect(document.activeElement).toBe(name('alpha'))
    w.unmount()
  })
})
