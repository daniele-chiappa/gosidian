import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import type { Team } from '@/api/teams'

vi.mock('@/api/teams', () => ({
  listTeams: vi.fn(),
  createTeam: vi.fn(),
  updateTeam: vi.fn(),
  deleteTeam: vi.fn(),
  addTeamUser: vi.fn(),
  removeTeamUser: vi.fn(),
  setTeamGrant: vi.fn(),
  removeTeamGrant: vi.fn(),
}))
vi.mock('@/api/admin', () => ({ listUsers: vi.fn(async () => []) }))
vi.mock('@/api/projects', () => ({ listProjects: vi.fn(async () => []) }))
vi.mock('@/stores/access', () => ({ useAccessStore: () => ({ load: vi.fn() }) }))

import { listTeams, removeTeamUser, setTeamGrant, updateTeam } from '@/api/teams'
import AdminTeamsView from '@/views/admin/AdminTeamsView.vue'

const team = (over: Partial<Team> = {}): Team => ({
  id: 't1',
  name: 'Editors',
  users: [{ id: 'u1', username: 'alice', role: 'member' }],
  grants: [{ project: 'p', level: 'read' }],
  created_at: '2026-10-01T00:00:00Z',
  ...over,
})

async function mountView() {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
  const w = mount(AdminTeamsView, { global: { plugins: [i18n] } })
  await flushPromises()
  return w
}

const button = (w: Awaited<ReturnType<typeof mountView>>, text: string) =>
  w.findAll('button').find((b) => b.text() === text)!

describe('AdminTeamsView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(listTeams).mockResolvedValue([team()])
  })

  it('keeps the rename form, and what was typed, when the rename is refused', async () => {
    vi.mocked(updateTeam).mockRejectedValue(new Error('taken'))
    const w = await mountView()
    await button(w, 'Rename').trigger('click')
    // The first section creates a team; the team's own follows.
    const rename = () => w.findAll('section')[1]!.find('form input[required]')
    await rename().setValue('Writers')
    await w.findAll('section')[1]!.find('form').trigger('submit')
    await flushPromises()
    expect(updateTeam).toHaveBeenCalledTimes(1)
    expect(rename().exists()).toBe(true)
    expect((rename().element as HTMLInputElement).value).toBe('Writers')
  })

  it('changes the team in place, without reading every list again', async () => {
    vi.mocked(removeTeamUser).mockResolvedValue(undefined)
    vi.mocked(setTeamGrant).mockResolvedValue(team({ grants: [{ project: 'p', level: 'write' }] }))
    const w = await mountView()
    expect(listTeams).toHaveBeenCalledTimes(1)
    await w.find('select[class*="text-xs"]').setValue('write')
    await flushPromises()
    expect(setTeamGrant).toHaveBeenCalledWith('t1', 'p', 'write')
    // The level badge, not the option of the select that names it too.
    expect(w.findAll('li span').some((s) => s.text() === 'write')).toBe(true)
    await w.findAll('li button').find((b) => b.text() === '×')!.trigger('click')
    await flushPromises()
    expect(w.text()).not.toContain('alice')
    expect(listTeams).toHaveBeenCalledTimes(1)
  })

  // Vue sets :value again on the redraw that shows the error: a select bound
  // that way never kept the level tried (a v-model one did, SettingsView).
  it('puts the level select back when a change is refused', async () => {
    vi.mocked(setTeamGrant).mockRejectedValue(new Error('no'))
    const w = await mountView()
    const select = w.find('select[class*="text-xs"]')
    await select.setValue('admin')
    await flushPromises()
    expect((select.element as HTMLSelectElement).value).toBe('read')
  })
})
