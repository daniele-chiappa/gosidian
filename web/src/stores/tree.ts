/**
 * Tree store — caches /api/v1/tree responses keyed by project (empty
 * string for vault-wide). The sidebar reloads it in place on SSE `tree`
 * events, which fire on writes from MCP, the watcher or other tabs.
 *
 * A reload keeps what the tree shows until the new answer lands, and only
 * the last answer counts: an event emptied the tree and redrew it, losing
 * its scroll and focus, the loads of a burst of events landed out of
 * order, and one landing after a sign-out filled the tree again (BUG-117,
 * S7-11). invalidateAll, at sign-out, drops the tree and every load on its
 * way.
 */
import { defineStore } from 'pinia'
import type { TreeNode } from '@/api/tree'
import { fetchTree } from '@/api/tree'

interface TreeState {
  byProject: Record<string, TreeNode | null>
  loading: Record<string, boolean>
  error: Record<string, string>
}

// The last load of each key, and the sign-out it belongs to.
const generation: Record<string, number> = {}
let epoch = 0
let refreshTimer: ReturnType<typeof setTimeout> | null = null
/** How long a refresh waits for the rest of a burst of events. */
export const REFRESH_DELAY_MS = 150

export const useTreeStore = defineStore('tree', {
  state: (): TreeState => ({
    byProject: {},
    loading: {},
    error: {},
  }),

  actions: {
    async load(project = '') {
      const key = project
      const mine = (generation[key] = (generation[key] ?? 0) + 1)
      const at = epoch
      const current = () => mine === generation[key] && at === epoch
      this.loading[key] = true
      this.error[key] = ''
      try {
        const tree = await fetchTree(project || undefined)
        if (current()) this.byProject[key] = tree
      } catch (err) {
        if (current()) this.error[key] = err instanceof Error ? err.message : String(err)
      } finally {
        if (current()) this.loading[key] = false
      }
    },
    /** Reloads in place, once for a burst of calls. */
    refresh() {
      if (refreshTimer) clearTimeout(refreshTimer)
      refreshTimer = setTimeout(() => {
        refreshTimer = null
        const keys = new Set(['', ...Object.keys(this.byProject)])
        for (const key of keys) void this.load(key)
      }, REFRESH_DELAY_MS)
    },
    invalidate(project = '') {
      delete this.byProject[project]
    },
    invalidateAll() {
      epoch++
      if (refreshTimer) {
        clearTimeout(refreshTimer)
        refreshTimer = null
      }
      this.byProject = {}
      this.loading = {}
    },
  },
})
