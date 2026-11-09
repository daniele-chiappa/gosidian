import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import itUI from '@catalogs/ui.it.json'

vi.mock('@/api/projects', () => ({ setProjectMember: vi.fn(), removeProjectMember: vi.fn() }))
vi.mock('@/api/teams', () => ({
  getProjectAccess: vi.fn(async () => ({
    project: 'p',
    visibility: 'private',
    users: [{ user_id: 'u1', username: 'alice', level: 'write' }],
    teams: [{ team_id: 't1', name: 'Editors', level: 'read' }],
    can_admin: false,
  })),
  setProjectTeam: vi.fn(),
  removeProjectTeam: vi.fn(),
}))
vi.mock('@/stores/access', () => ({ useAccessStore: () => ({ load: vi.fn() }) }))

import ProjectMembersView from '@/views/ProjectMembersView.vue'

describe('ProjectMembersView', () => {
  it("names an account's and a team's level in the reader's language, the code in the tooltip (IMP-220, F-20)", async () => {
    const i18n = createI18n({
      legacy: false,
      locale: 'it',
      fallbackLocale: 'en',
      messages: { it: itUI, en: enUI },
    })
    const w = mount(ProjectMembersView, { props: { project: 'p' }, global: { plugins: [i18n] } })
    await flushPromises()
    const badges = w.findAll('[data-level]')
    expect(badges.map((b) => b.text())).toEqual(['scrittura', 'lettura'])
    expect(badges.map((b) => b.attributes('title'))).toEqual(['write', 'read'])
  })
})
