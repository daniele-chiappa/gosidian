import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import AnnounceHost from '@/components/primitives/AnnounceHost.vue'
import { announce, announcement } from '@/composables/useAnnounce'

describe('AnnounceHost (IMP-217, F-10)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    announcement.value = ''
  })
  afterEach(() => vi.useRealTimers())

  it('is a polite region in the page before it has anything to say', async () => {
    const w = mount(AnnounceHost)
    const region = w.find('[data-announce]')
    expect(region.attributes('role')).toBe('status')
    expect(region.attributes('aria-live')).toBe('polite')
    expect(region.classes()).toContain('sr-only')
    expect(region.text()).toBe('')
    announce('saved at 10:00')
    vi.advanceTimersByTime(100)
    await nextTick()
    expect(region.text()).toBe('saved at 10:00')
    w.unmount()
  })

  it('empties itself first, so that the same text twice is said twice', () => {
    announce('saved at 10:00')
    vi.advanceTimersByTime(100)
    expect(announcement.value).toBe('saved at 10:00')
    announce('saved at 10:00')
    expect(announcement.value).toBe('')
    vi.advanceTimersByTime(100)
    expect(announcement.value).toBe('saved at 10:00')
  })

  it('says the last of texts that come together', () => {
    announce('first')
    announce('second')
    vi.advanceTimersByTime(100)
    expect(announcement.value).toBe('second')
  })
})
