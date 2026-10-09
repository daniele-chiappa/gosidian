/**
 * The modifier a keyboard shortcut shows: ⌘ on an Apple system, Ctrl
 * elsewhere, where the web UI showed ⌘K to everyone. The handlers take
 * either key on every system; only the label differs.
 */
export function isApple(nav: Pick<Navigator, 'platform' | 'userAgent'> = navigator): boolean {
  const data = (nav as Navigator & { userAgentData?: { platform?: string } }).userAgentData
  return /mac|iphone|ipad|ipod/i.test(data?.platform || nav.platform || nav.userAgent || '')
}

/** "⌘K" on an Apple system, "Ctrl+K" elsewhere. */
export function shortcutLabel(key: string, apple = isApple()): string {
  return apple ? `⌘${key}` : `Ctrl+${key}`
}
