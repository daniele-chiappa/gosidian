import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useSSE, _resetSSEForTests } from '@/composables/useSSE'
import { useAuthStore } from '@/stores/auth'

// Minimal EventSource: records every instance so a test can drive its
// readyState and handlers the way a browser would.
class FakeEventSource {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSED = 2
  static instances: FakeEventSource[] = []
  readyState = FakeEventSource.CONNECTING
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  constructor(public url: string) {
    FakeEventSource.instances.push(this)
  }
  addEventListener() {}
  close() {
    this.readyState = FakeEventSource.CLOSED
  }
}

const last = () => FakeEventSource.instances[FakeEventSource.instances.length - 1]!

/** What a browser does on a non-200 answer: close for good, fire error. */
function refuse(es: FakeEventSource) {
  es.readyState = FakeEventSource.CLOSED
  es.onerror?.()
}

describe('useSSE reconnect', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.stubGlobal('EventSource', FakeEventSource)
    FakeEventSource.instances = []
    setActivePinia(createPinia())
    useAuthStore().token = 'tok-1'
  })
  afterEach(() => {
    _resetSSEForTests()
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('leaves a network error to the browser', () => {
    useSSE()
    expect(FakeEventSource.instances).toHaveLength(1)
    last().onerror?.() // still CONNECTING: the browser retries by itself
    vi.advanceTimersByTime(60_000)
    expect(FakeEventSource.instances).toHaveLength(1)
  })

  it('reopens a connection the browser closed, with a growing delay', () => {
    useSSE()
    refuse(last()) // e.g. the 503 while the server starts
    vi.advanceTimersByTime(1999)
    expect(FakeEventSource.instances).toHaveLength(1)
    vi.advanceTimersByTime(1)
    expect(FakeEventSource.instances).toHaveLength(2)

    refuse(last())
    vi.advanceTimersByTime(2000)
    expect(FakeEventSource.instances).toHaveLength(2) // now 4 s
    vi.advanceTimersByTime(2000)
    expect(FakeEventSource.instances).toHaveLength(3)

    // An open connection resets the delay.
    last().onopen?.()
    refuse(last())
    vi.advanceTimersByTime(2000)
    expect(FakeEventSource.instances).toHaveLength(4)
  })

  it('stops retrying on disconnect', () => {
    const sse = useSSE()
    refuse(last())
    sse.disconnect()
    vi.advanceTimersByTime(60_000)
    expect(FakeEventSource.instances).toHaveLength(1)
  })
})
