import { beforeEach, describe, expect, it, vi } from 'vitest'

const get = vi.fn()
vi.mock('@/api/client', () => ({ default: { get: (...a: unknown[]) => get(...a) } }))

import { appOnHost, listPluginApps, type PluginApp } from '@/api/layouts'

const app: PluginApp = {
  plugin: 'layout',
  name: 'layout',
  title: 'Impaginazione',
  url: 'http://127.0.0.1:8099/app/',
  alt_urls: ['http://127.0.0.1:8099/app/', 'http://localhost:8099/app/'],
  origin: 'http://127.0.0.1:8099',
  note_type: 'layout',
  bridge: 'layout/2',
  status: 'ok',
}

describe('the address of an app', () => {
  beforeEach(() => get.mockReset())

  it('is the one on the host gosidian was opened at, with its origin', () => {
    const tunnel = appOnHost(app, '127.0.0.1')
    expect(tunnel.url).toBe('http://127.0.0.1:8099/app/')
    expect(tunnel.origin).toBe('http://127.0.0.1:8099')
    expect(appOnHost(app, 'localhost').origin).toBe('http://localhost:8099')
    expect(appOnHost(app, '127.0.0.1').url).toBe(app.url)
  })

  it('stays the main one on any other host', () => {
    expect(appOnHost(app, 'vault.example.com')).toEqual(app)
    expect(appOnHost({ ...app, alt_urls: undefined }, '127.0.0.1').url).toBe(app.url)
  })

  it('is chosen for the apps the server lists', async () => {
    get.mockResolvedValue({ data: { apps: [app] } })
    const [listed] = await listPluginApps(() => '127.0.0.1')
    expect(get).toHaveBeenCalledWith('/plugins/apps')
    expect(listed?.url).toBe('http://127.0.0.1:8099/app/')
  })
})
