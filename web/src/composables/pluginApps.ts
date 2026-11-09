/**
 * The apps of the plugins (IMP-226), for the notes whose type one opens:
 * asked once a minute at most, none when the pipelines are off.
 */
import { listPluginApps, type PluginApp } from '@/api/layouts'

const TTL = 60_000
let cached: { at: number; apps: Promise<PluginApp[]> } | null = null

export function pluginApps(): Promise<PluginApp[]> {
  if (!cached || Date.now() - cached.at > TTL)
    cached = { at: Date.now(), apps: listPluginApps().catch(() => []) }
  return cached.apps
}

/** The app that opens the notes of a type, when its plugin serves the
 *  manifest the owner accepted. */
export async function appForType(type: string): Promise<PluginApp | null> {
  if (!type) return null
  return (await pluginApps()).find((a) => a.note_type === type && a.status === 'ok') ?? null
}

/** Forget the apps: the next ask goes to the server (tests). */
export function forgetPluginApps(): void {
  cached = null
}
