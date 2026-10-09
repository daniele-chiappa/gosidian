import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ open: vi.fn() }),
}))

import HTMLPreview from '@/components/domain/HTMLPreview.vue'

describe('HTMLPreview (BUG-117, S7-12)', () => {
  it('keeps the newest version when an older rebuild finishes last', async () => {
    let release: (v: Response) => void = () => {}
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((r) => (release = r))))
    const w = mount(HTMLPreview, {
      props: { html: '<p>v1</p><img src="/vault-files/p/attachments/a.png">', path: 'p/n.html' },
      global: { provide: { openWindow: vi.fn() } },
    })
    await flushPromises()
    await w.setProps({ html: '<p>v2</p>' })
    await flushPromises()
    release(new Response('data:image/png;base64,AAAA'))
    await flushPromises()
    const srcdoc = w.find('iframe').attributes('srcdoc') ?? ''
    expect(srcdoc).toContain('v2')
    expect(srcdoc).not.toContain('v1')
    vi.unstubAllGlobals()
  })
})
