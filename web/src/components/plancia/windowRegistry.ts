/**
 * Window-type → content-component registry for the plancia.
 *
 * Each content component receives the window's `props` (v-bind) and may emit
 * `title` / `dirty` / `tags` / `close` to the frame, and inject `openWindow`
 * to spawn siblings. Components are lazy (`defineAsyncComponent`) so heavy
 * chunks (CodeMirror editor, Cytoscape graph) load only when a window of that
 * type is first opened.
 *
 * Entries are added phase by phase as each view is decoupled from the router.
 */
import { defineAsyncComponent, markRaw, type Component } from 'vue'

const lazy = (loader: () => Promise<unknown>): Component =>
  markRaw(defineAsyncComponent(loader as () => Promise<Component>))

export const windowRegistry: Record<string, Component> = {
  // One note window with an in-place view/edit toggle (no separate edit window).
  note: lazy(() => import('@/views/NoteView.vue')),
  // Manual note creation (markdown or image media note), opened from a tree
  // folder's + button (IMP-058).
  create: lazy(() => import('@/views/NoteCreateView.vue')),
  graph: lazy(() => import('@/views/GraphView.vue')),
  search: lazy(() => import('@/views/SearchView.vue')),
  // Frontmatter query (IMP-099): the memory_query tool for people.
  query: lazy(() => import('@/views/QueryView.vue')),
  projects: lazy(() => import('@/views/ProjectsView.vue')),
  'project-members': lazy(() => import('@/views/ProjectMembersView.vue')),
  tags: lazy(() => import('@/views/TagsView.vue')),
  settings: lazy(() => import('@/views/SettingsView.vue')),
  trash: lazy(() => import('@/views/TrashView.vue')),
  history: lazy(() => import('@/views/NoteHistoryView.vue')),
  admin: lazy(() => import('@/views/admin/AdminLayout.vue')),
}
