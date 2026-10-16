import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'

const api = vi.hoisted(() => ({
  status: vi.fn(),
  token: vi.fn(),
  configure: vi.fn(),
}))
vi.mock('@/api/chat', () => ({
  fetchChatStatus: api.status,
  chatToken: api.token,
  configureChat: api.configure,
}))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ open: vi.fn(), identify: vi.fn() }),
}))

import MarkdownPreview from '@/components/domain/MarkdownPreview.vue'
import { hasChat, splitViews } from '@/components/views/segments'

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
const html = '<p>The bot.</p><pre><code class="language-chat">pipeline: pipelines/chat\ntitle: Docs\n</code></pre>'

const configured = {
  bot: 'docs',
  plugin: 'chat',
  pipeline: 'pipelines/chat.md',
  title: 'Docs',
  access: { users: true, keys: ['site'], signed: false, open: false },
  problems: null,
  state: {
    bot: 'docs', plugin: 'chat', note: 'p/bots/docs.md', project: 'p', configured: true, updated: '',
    users: true, embed_url: 'https://chat.example.com/embed/docs', script_url: 'https://chat.example.com/chat.js',
  },
}

describe('ChatBlock (IMP-191)', () => {
  it('cuts a ```chat block out of the note', () => {
    expect(hasChat(html)).toBe(true)
    const s = splitViews(html)
    expect(s.map((x) => x.kind)).toEqual(['html', 'chat'])
    expect(s[1]).toMatchObject({ kind: 'chat', index: 0, source: 'pipeline: pipelines/chat\ntitle: Docs\n' })
  })

  it('shows where the bot stands, opens its chat with a signed token, and gives the code for a site', async () => {
    api.status.mockResolvedValue(configured)
    api.token.mockResolvedValue({ token: 'a.b.c', bot: 'docs', embed_url: 'https://chat.example.com/embed/docs', expires: '' })
    const w = mount(MarkdownPreview, {
      props: { html, notePath: 'p/bots/docs.md' },
      global: { plugins: [i18n()], provide: { openWindow: vi.fn() } },
    })
    await flushPromises()
    expect(api.status).toHaveBeenCalledWith('p/bots/docs.md')
    expect(w.find('[data-chat-state]').text()).toContain('Configured in the plugin chat')
    await w.find('[data-chat-open]').trigger('click')
    await flushPromises()
    expect(w.find('[data-chat-frame]').attributes('src')).toBe('https://chat.example.com/embed/docs#token=a.b.c')
    await w.find('[data-chat-snippet]').trigger('click')
    const code = w.find('[data-chat-snippet-code]').text()
    expect(code).toContain('<div id="chat-docs"></div>')
    expect(code).toContain('src="https://chat.example.com/chat.js" data-bot="docs" data-container="chat-docs" data-key="<value of the key site>"')
    await w.find('[data-chat-code]').trigger('click')
    expect(w.find('[data-chat-source]').text()).toContain('pipeline: pipelines/chat')
  })

  it('says why a bot is not configured, and configures it again on demand', async () => {
    api.status.mockResolvedValue({ ...configured, state: { ...configured.state, configured: false, error: 'plugin chat: down' } })
    api.configure.mockResolvedValue({ state: configured.state })
    const w = mount(MarkdownPreview, {
      props: { html, notePath: 'p/bots/docs.md' },
      global: { plugins: [i18n()], provide: { openWindow: vi.fn() } },
    })
    await flushPromises()
    expect(w.find('[data-chat-state]').text()).toContain('Not configured: plugin chat: down')
    expect(w.find('[data-chat-open]').exists()).toBe(false)
    api.status.mockResolvedValue(configured)
    await w.find('[data-chat-configure]').trigger('click')
    await flushPromises()
    expect(api.configure).toHaveBeenCalledWith('p/bots/docs.md')
    expect(w.find('[data-chat-state]').text()).toContain('Configured')
  })

  it('lists the problems of a block that does not read', async () => {
    api.status.mockResolvedValue({ ...configured, state: undefined, problems: [{ line: 9, message: 'pipeline names the pipeline' }] })
    const w = mount(MarkdownPreview, {
      props: { html, notePath: 'p/bots/docs.md' },
      global: { plugins: [i18n()], provide: { openWindow: vi.fn() } },
    })
    await flushPromises()
    expect(w.find('[role="alert"]').text()).toBe('The block does not read: line 9: pipeline names the pipeline')
  })
})
