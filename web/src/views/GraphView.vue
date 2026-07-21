<script setup lang="ts">
/**
 * GraphView — link graph as a plancia window. Two modes:
 *   - global  (no `focus` prop): full controls aside + canvas.
 *   - ego      (`focus` set, from the window "direct links" button):
 *     compact canvas of the note's neighbourhood at `depth` hops.
 *
 * Filter state lives in the component (NOT the URL): the plancia owns the URL
 * (`?w=&f=`), so this view must not call router.replace. Node clicks open the
 * target note as a sibling window.
 */
import { computed, defineAsyncComponent, inject, onMounted, ref, watch } from 'vue'
import { useDebounceFn } from '@vueuse/core'
import { fetchGraph, type GraphResponse } from '@/api/graph'
import { listProjects, type Project } from '@/api/projects'
import { listTags, type TagCount } from '@/api/tags'
import { suggestNoteTitles, type NoteTitleHit } from '@/api/noteTitles'
import SearchSelect from '@/components/primitives/SearchSelect.vue'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { planciaKey } from '@/composables/planciaKey'
import { useUIStore, type GraphRenderMode } from '@/stores/ui'

const GraphCanvas = defineAsyncComponent(() => import('@/components/graph/GraphCanvas.vue'))
const Graph3DCanvas = defineAsyncComponent(() => import('@/components/graph/Graph3DCanvas.vue'))

const props = defineProps<{
  project?: string
  tag?: string
  focus?: string
  depth?: number
}>()

const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))

// Renderer toggle: each window starts from the persisted global
// default and switching updates it (2D stays the default on fresh
// profiles; three.js loads only on the first switch to 3D).
const ui = useUIStore()
const mode = ref<GraphRenderMode>(ui.graphMode)
function setMode(m: GraphRenderMode) {
  mode.value = m
  ui.setGraphMode(m)
}

const data = ref<GraphResponse | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)

const project = ref<string>(props.project ?? '')
const tag = ref<string>(props.tag ?? '')
const focus = ref<string>(props.focus ?? '')
const depth = ref<number>(props.depth ?? 2)
const minDegree = ref<number>(0)
const limit = ref<number>(0)

/** Ego windows (opened from the "direct links" button) render compact. */
const compact = computed(() => Boolean(props.focus))
const hadInitialFilter = Boolean(project.value || tag.value || focus.value)
const base = (p: string) => (p.split('/').pop() ?? p).replace(/\.md$/, '')

const params = computed(() => ({
  project: project.value || undefined,
  tag: tag.value || undefined,
  focus: focus.value || undefined,
  depth: focus.value ? depth.value : undefined,
  min_degree: minDegree.value > 0 ? minDegree.value : undefined,
  limit: limit.value > 0 ? limit.value : undefined,
}))

async function load() {
  loading.value = true
  error.value = null
  try {
    data.value = await fetchGraph(params.value)
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load graph'
    data.value = null
  } finally {
    loading.value = false
  }
}

const debouncedLoad = useDebounceFn(load, 200)
watch(params, () => void debouncedLoad(), { deep: true })

function reset() {
  project.value = ''
  tag.value = ''
  focus.value = ''
  depth.value = 2
  minDegree.value = 0
  limit.value = 0
}

function onSelect(path: string) {
  openWindow({
    type: 'note',
    key: planciaKey('note', path),
    title: base(path),
    props: { path },
  })
}

const projects = ref<Project[]>([])
const tags = ref<TagCount[]>([])
const notes = ref<NoteTitleHit[]>([])

const sortedProjects = computed<Project[]>(() =>
  [...projects.value].sort((a, b) => {
    const am = a.mod_time ?? ''
    const bm = b.mod_time ?? ''
    if (am === bm) return a.name.localeCompare(b.name)
    if (!am) return 1
    if (!bm) return -1
    return bm.localeCompare(am)
  }),
)

onMounted(async () => {
  if (compact.value) {
    // Ego graph: no pickers needed, just load the neighbourhood.
    void load()
    return
  }
  const [pRes, tRes, nRes] = await Promise.allSettled([
    listProjects(),
    listTags(),
    suggestNoteTitles('', 50),
  ])
  if (pRes.status === 'fulfilled') projects.value = pRes.value
  if (tRes.status === 'fulfilled') tags.value = tRes.value
  if (nRes.status === 'fulfilled') notes.value = nRes.value

  const mostRecent = sortedProjects.value[0]
  if (!hadInitialFilter && mostRecent) {
    project.value = mostRecent.name
  } else {
    void load()
  }
})
</script>

<template>
  <div class="flex h-full">
    <aside
      v-if="!compact"
      class="w-72 shrink-0 border-r border-border bg-bg-elevated p-4 space-y-4 overflow-auto"
    >
      <div class="block text-sm">
        <span class="text-text-muted text-xs">Project</span>
        <SearchSelect
          v-model="project"
          class="mt-1"
          :items="sortedProjects"
          :value-key="(p: Project) => p.name"
          :label="(p: Project) => p.name"
          :secondary="(p: Project) => String(p.note_count)"
          placeholder="(all) — type to search"
        />
      </div>

      <div class="block text-sm">
        <span class="text-text-muted text-xs">Tag</span>
        <SearchSelect
          v-model="tag"
          class="mt-1"
          :items="tags"
          :value-key="(t: TagCount) => t.tag"
          :label="(t: TagCount) => '#' + t.tag"
          :secondary="(t: TagCount) => String(t.count)"
          placeholder="(no tag) — type to search"
        />
      </div>

      <div class="block text-sm">
        <span class="text-text-muted text-xs">Focus (path)</span>
        <SearchSelect
          v-model="focus"
          class="mt-1"
          :items="notes"
          :value-key="(n: NoteTitleHit) => n.path"
          :label="(n: NoteTitleHit) => n.title || n.path"
          :secondary="(n: NoteTitleHit) => n.path"
          placeholder="(no focus) — type to search"
        />
      </div>

      <label class="block text-sm">
        <span class="text-text-muted text-xs">Depth (hops, when focus is set)</span>
        <input
          v-model.number="depth"
          type="number"
          min="1"
          max="6"
          class="mt-1 w-full rounded bg-bg border border-border px-2 py-1.5 text-sm"
        >
      </label>
      <label class="block text-sm">
        <span class="text-text-muted text-xs">Min degree (drop leaves below)</span>
        <input
          v-model.number="minDegree"
          type="number"
          min="0"
          max="20"
          class="mt-1 w-full rounded bg-bg border border-border px-2 py-1.5 text-sm"
        >
      </label>
      <label class="block text-sm">
        <span class="text-text-muted text-xs">Limit (cap nodes; top-degree wins)</span>
        <input
          v-model.number="limit"
          type="number"
          min="0"
          max="2000"
          class="mt-1 w-full rounded bg-bg border border-border px-2 py-1.5 text-sm"
        >
      </label>

      <button
        type="button"
        class="w-full text-xs px-2 py-1 rounded border border-border hover:bg-surface-hover"
        @click="reset"
      >
        Reset
      </button>

      <div
        v-if="data"
        class="text-xs text-text-muted space-y-1 pt-3 border-t border-border"
      >
        <p>Nodes: <strong class="text-text">{{ data.stats.node_count }}</strong></p>
        <p>Edges: <strong class="text-text">{{ data.stats.edge_count }}</strong></p>
        <p
          v-if="data.stats.truncated"
          class="text-warning"
        >
          Truncated by limit
        </p>
        <p
          v-if="data.stats.filter"
          class="font-mono break-all"
        >
          {{ data.stats.filter }}
        </p>
      </div>
    </aside>

    <section class="flex-1 relative min-w-0">
      <p
        v-if="loading"
        class="absolute top-3 left-3 z-10 text-xs text-text-muted bg-bg-elevated px-2 py-1 rounded border border-border"
      >
        Loading…
      </p>
      <p
        v-else-if="error"
        class="absolute top-3 left-3 z-10 text-xs text-danger bg-bg-elevated px-2 py-1 rounded border border-danger"
      >
        {{ error }}
      </p>
      <div
        class="absolute top-3 right-3 z-10 flex rounded border border-border overflow-hidden text-xs bg-bg-elevated"
        role="group"
        aria-label="Graph renderer"
      >
        <button
          type="button"
          class="px-2 py-1"
          :class="mode === '2d' ? 'bg-surface-hover text-text font-semibold' : 'text-text-muted hover:bg-surface-hover'"
          @click="setMode('2d')"
        >
          2D
        </button>
        <button
          type="button"
          class="px-2 py-1 border-l border-border"
          :class="mode === '3d' ? 'bg-surface-hover text-text font-semibold' : 'text-text-muted hover:bg-surface-hover'"
          @click="setMode('3d')"
        >
          3D
        </button>
      </div>
      <GraphCanvas
        v-if="data && mode === '2d'"
        :nodes="data.nodes"
        :edges="data.edges"
        @select="onSelect"
      />
      <Graph3DCanvas
        v-else-if="data && mode === '3d'"
        :nodes="data.nodes"
        :edges="data.edges"
        @select="onSelect"
      />
      <p
        v-else-if="!loading"
        class="p-8 text-text-muted text-sm"
      >
        No data yet.
      </p>
    </section>
  </div>
</template>
