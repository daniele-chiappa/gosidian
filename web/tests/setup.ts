/**
 * Global Vitest setup. Resets the Pinia store registration before
 * each test so state from one spec never leaks to the next, and
 * stubs localStorage so the persisted-state plugin doesn't throw on
 * boot in the happy-dom env.
 */
import { afterAll, afterEach, beforeEach } from 'vitest'

// A unit test never talks to a server (IMP-235). A request that reaches
// happy-dom's XMLHttpRequest (axios) or fetch over http is a mock gone
// missing: it is refused at once, and the test fails naming it, where it
// used to go out to localhost:3000 and print an ECONNREFUSED after the
// test had passed. A test that stubs fetch, or gives axios its own
// adapter, never gets here.
const reached: string[] = []
const overHttp = (url: string) => new URL(url, window.location.href).protocol.startsWith('http')

const xhrOpen = XMLHttpRequest.prototype.open
XMLHttpRequest.prototype.open = function (this: XMLHttpRequest, method: string, url: string | URL, ...rest: unknown[]) {
  if (overHttp(String(url))) {
    reached.push(`${method.toUpperCase()} ${String(url)}`)
    throw new Error(`unit test: no server, mock ${method.toUpperCase()} ${String(url)}`)
  }
  return (xhrOpen as (...a: unknown[]) => void).call(this, method, url, ...rest)
} as typeof XMLHttpRequest.prototype.open

const realFetch = globalThis.fetch
globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
  const url = typeof input === 'string' || input instanceof URL ? String(input) : input.url
  if (overHttp(url)) {
    const method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase()
    reached.push(`${method} ${url}`)
    return Promise.reject(new TypeError(`unit test: no server, mock ${method} ${url}`))
  }
  return realFetch(input, init)
}) as typeof fetch

function noRequests() {
  const got = reached.splice(0)
  if (got.length) throw new Error(`a unit test reached for the server, mock: ${got.join(', ')}`)
}

beforeEach(() => {
  // happy-dom ships localStorage but reset between tests for sanity.
  window.localStorage.clear()
  // dataset clean-up so theme assertions start from a known state.
  document.documentElement.removeAttribute('data-preset')
  document.documentElement.lang = 'en'
})

afterEach(() => {
  window.localStorage.clear()
  noRequests()
})

// A request made after a file's last test.
afterAll(noRequests)
