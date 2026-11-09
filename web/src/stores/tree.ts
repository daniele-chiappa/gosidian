/**
 * Tree store — the vault's tree as the sidebar shows it (byProject['']).
 * The sidebar reloads it in place on SSE `tree` events, which fire on
 * writes from MCP, the watcher or other tabs.
 *
 * A reload keeps what the tree shows until the new answer lands, and only
 * the last answer counts: an event emptied the tree and redrew it, losing
 * its scroll and focus, the loads of a burst of events landed out of
 * order, and one landing after a sign-out filled the tree again (BUG-117,
 * S7-11). invalidateAll, at sign-out, drops the tree and every load on its
 * way.
 *
 * An event that names a note reloads the tree of its project alone, asked
 * with the ETag of the last answer: unchanged, as after an edit of a
 * note's body, it costs a 304 (IMP-225). Each write reloaded the whole
 * tree, 287 KB for an account of the lab. A call that names no project, or
 * an event about a whole project, reloads the whole tree.
 */
import { defineStore } from 'pinia'
import type { TreeNode } from '@/api/tree'
import { fetchTree } from '@/api/tree'

interface TreeState {
  byProject: Record<string, TreeNode | null>
  loading: Record<string, boolean>
  error: Record<string, string>
}

// The last load of each key ('' the whole tree, '/<name>' a project's),
// and the sign-out it belongs to.
const generation: Record<string, number> = {}
let epoch = 0
let refreshTimer: ReturnType<typeof setTimeout> | null = null
// When the first event of the burst a refresh waits for came.
let burstAt = 0
// What the refresh reloads: the projects the events named, or everything.
let pendingAll = false
const pendingProjects = new Set<string>()
// The ETag of the whole tree ('') and of each project's.
const etags: Record<string, string> = {}
// Every load takes a ticket. A project's tree landing after a whole one
// asked later is older than it; a whole one landing after a project's
// asked later keeps that project's.
let ticket = 0
let wholeAt = 0
const reloaded: Record<string, { at: number; node: TreeNode | null }> = {}

/** How long a refresh waits for the rest of a burst of events. */
export const REFRESH_DELAY_MS = 150
/**
 * How long a burst may hold a refresh back: events closer than the delay
 * held it back for good, with many writers in the vault (BUG-146).
 */
export const REFRESH_MAX_WAIT_MS = 1000

// Events about a whole project: its tree may come or go, or change name.
const PROJECT_WIDE = new Set(['create_project', 'rename_project', 'delete_project'])

/** The project a path is in; undefined for a project itself, or the vault. */
export function projectOfPath(path?: string): string | undefined {
  const at = path?.indexOf('/') ?? -1
  return at > 0 ? path!.slice(0, at) : undefined
}

/** The project a `tree` event is about, or undefined for the whole vault. */
export function eventProject(payload: unknown): string | undefined {
  const e = (payload ?? {}) as { action?: string; path?: string; project?: string }
  if (e.action && PROJECT_WIDE.has(e.action)) return undefined
  return e.project || projectOfPath(e.path)
}

/** The whole tree with a project's tree in its place, or without it. */
function withProject(root: TreeNode, name: string, node: TreeNode | null): TreeNode {
  const children = (root.children ?? []).filter((c) => !(c.is_dir && c.name === name))
  if (node) {
    children.push(node)
    // As the server orders them: folders first, then by name.
    children.sort((a, b) =>
      a.is_dir !== b.is_dir ? (a.is_dir ? -1 : 1) : a.name < b.name ? -1 : a.name > b.name ? 1 : 0,
    )
  }
  const noteCount = children.reduce((n, c) => n + (c.is_dir ? (c.note_count ?? 0) : 1), 0)
  return { ...root, children, note_count: noteCount }
}

export const useTreeStore = defineStore('tree', {
  state: (): TreeState => ({
    byProject: {},
    loading: {},
    error: {},
  }),

  actions: {
    /** Loads the whole tree, asked with its ETag while one is shown. */
    async load() {
      const mine = (generation[''] = (generation[''] ?? 0) + 1)
      const at = epoch
      const asked = ++ticket
      const current = () => mine === generation[''] && at === epoch
      this.loading[''] = true
      this.error[''] = ''
      try {
        const got = await fetchTree(undefined, this.byProject[''] ? etags[''] : undefined)
        if (!current()) return
        if (got) {
          etags[''] = got.etag
          let root = got.root
          for (const [name, r] of Object.entries(reloaded))
            if (r.at > asked) root = withProject(root, name, r.node)
          this.byProject[''] = root
        }
        wholeAt = asked
      } catch (err) {
        if (current()) this.error[''] = err instanceof Error ? err.message : String(err)
      } finally {
        if (current()) this.loading[''] = false
      }
    },
    /** Reloads a project's tree, asked with its ETag, into the whole one. */
    async loadProject(name: string) {
      const key = `/${name}`
      const mine = (generation[key] = (generation[key] ?? 0) + 1)
      const at = epoch
      const asked = ++ticket
      try {
        const got = await fetchTree(name, etags[name])
        if (mine !== generation[key] || at !== epoch || asked < wholeAt || !got) return
        etags[name] = got.etag
        const node = got.root.children?.find((c) => c.is_dir && c.name === name) ?? null
        reloaded[name] = { at: asked, node }
        const root = this.byProject['']
        if (root) this.byProject[''] = withProject(root, name, node)
        // The whole tree shown is no longer the one its ETag names.
        delete etags['']
      } catch {
        /* the next event, or a reload, brings it */
      }
    },
    /**
     * Reloads in place, once for a burst of calls, and every second while
     * it lasts: the projects the calls name, or the whole tree when one
     * names none.
     */
    refresh(project?: string) {
      if (project) pendingProjects.add(project)
      else pendingAll = true
      const now = Date.now()
      if (refreshTimer) clearTimeout(refreshTimer)
      else burstAt = now
      const wait = Math.max(0, Math.min(REFRESH_DELAY_MS, burstAt + REFRESH_MAX_WAIT_MS - now))
      refreshTimer = setTimeout(() => {
        refreshTimer = null
        const projects = [...pendingProjects]
        const all = pendingAll || !this.byProject['']
        pendingProjects.clear()
        pendingAll = false
        if (all) void this.load()
        else for (const name of projects) void this.loadProject(name)
      }, wait)
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
      pendingProjects.clear()
      pendingAll = false
      for (const k of Object.keys(etags)) delete etags[k]
      for (const k of Object.keys(reloaded)) delete reloaded[k]
      this.byProject = {}
      this.loading = {}
    },
  },
})
