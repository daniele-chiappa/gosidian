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
import { useI18n } from 'vue-i18n'
import { computed, defineAsyncComponent, inject, onMounted, ref, watch } from 'vue'
import { useDebounceFn } from '@vueuse/core'
import { fetchGraph, fetchGraphSources, type GraphNode, type GraphResponse, type GraphSource } from '@/api/graph'
import { createNote } from '@/api/notes'
import { graphNote, isPluginSource, type GraphSpec } from '@/components/graph/graphSpec'
import { listProjects, type Project } from '@/api/projects'
import { listTags, type TagCount } from '@/api/tags'
import { suggestNoteTitles, type NoteTitleHit } from '@/api/noteTitles'
import SearchSelect from '@/components/primitives/SearchSelect.vue'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { planciaKey, base } from '@/composables/planciaKey'
import { useUIStore, type GraphRenderMode } from '@/stores/ui'
import type { ZMode } from '@/components/graph/adapter'
import { errorText } from '@/api/errors'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'

const { t } = useI18n()

const GraphCanvas = defineAsyncComponent(() => import('@/components/graph/GraphCanvas.vue'))
const Graph3DCanvas = defineAsyncComponent(() => import('@/components/graph/Graph3DCanvas.vue'))

const props = defineProps<{
  project?: string
  tag?: string
  focus?: string
  depth?: number
  minDegree?: number
  limit?: number
  /** True on a full (aside) view rehydrated from a param-form URL token —
   *  distinguishes it from an ego window, which also carries `focus`. */
  global?: boolean
  /** links (the default) or a plugin's graph, <plugin>/<graph>, with its
   *  arguments (IMP-183). */
  source?: string
  with?: Record<string, unknown>
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

// 3D-only semantic z-axis. Exploratory by nature → not persisted.
const zMode = ref<ZMode>('free')

const data = ref<GraphResponse | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)

const project = ref<string>(props.project ?? '')
const tag = ref<string>(props.tag ?? '')
const focus = ref<string>(props.focus ?? '')
const depth = ref<number>(props.depth ?? 2)
const minDegree = ref<number>(props.minDegree ?? 0)
// Empty is the server's cap, which the field shows as its placeholder: a 0
// in it hid the placeholder and read as "no nodes".
const limit = ref<number | ''>(props.limit || '')
const limitSet = computed(() => (typeof limit.value === 'number' && limit.value > 0 ? limit.value : undefined))

// The source (IMP-183): the vault's links, or a graph of a plugin exposed
// in the project, with its arguments, read from its schema.
const source = ref<string>(props.source ?? 'links')
const args = ref<Record<string, unknown>>({ ...(props.with ?? {}) })
const sources = ref<GraphSource[]>([])
const plugin = computed(() => isPluginSource(source.value))
const currentSource = computed(() => sources.value.find((s) => s.source === source.value))
const argFields = computed(() =>
  Object.entries(currentSource.value?.input_schema?.properties ?? {}).map(([name, p]) => ({
    name,
    type: p.type ?? 'string',
    description: p.description ?? '',
    options: Array.isArray(p.enum) ? p.enum.map(String) : null,
    required: currentSource.value?.input_schema?.required?.includes(name) ?? false,
  })),
)
// The arguments a plugin's graph requires that the view does not give yet:
// until they are given the graph is not asked for, since the plugin would
// refuse it (BUG-128).
const missingArgs = computed(() =>
  plugin.value
    ? (currentSource.value?.input_schema?.required ?? []).filter((n) => args.value[n] === undefined || args.value[n] === '')
    : [],
)
const selected = ref<GraphNode | null>(null)
const notice = ref<string | null>(null)
let refreshNext = false

async function loadSources() {
  if (!project.value) {
    sources.value = []
    if (plugin.value) source.value = 'links'
    return
  }
  try {
    sources.value = await fetchGraphSources(project.value)
  } catch (e) {
    sources.value = []
    notice.value = errorText(e, t, t('graph.sources_failed'))
  }
  if (plugin.value && !sources.value.some((s) => s.source === source.value)) source.value = 'links'
  else if (plugin.value) void load()
}

function setArg(name: string, type: string, raw: string | boolean) {
  const next = { ...args.value }
  if (raw === '' || raw === undefined) delete next[name]
  else if (type === 'integer' || type === 'number') next[name] = Number(raw)
  else next[name] = raw
  args.value = next
}

function refresh() {
  refreshNext = true
  void load()
}

function viewSpec(): GraphSpec {
  if (plugin.value) {
    return { source: source.value, with: { ...args.value }, project: project.value || undefined, mode: mode.value }
  }
  return {
    source: 'links',
    project: project.value || undefined,
    tag: tag.value || undefined,
    focus: focus.value || undefined,
    depth: focus.value ? depth.value : undefined,
    min_degree: minDegree.value || undefined,
    limit: limitSet.value,
    mode: mode.value,
  }
}

async function saveAsNote() {
  const spec = viewSpec()
  const name = plugin.value ? source.value.replace('/', '-') : project.value || 'grafo'
  const dir = project.value || 'grafi'
  const path = window.prompt(t('graph.save_path'), `${dir}/grafi/${name}.md`)?.trim()
  if (!path) return
  try {
    const title = base(path).replace(/\.md$/, '')
    await createNote(path.endsWith('.md') ? path : `${path}.md`, graphNote(title, spec))
    notice.value = t('graph.saved', { path })
    onSelect(path.endsWith('.md') ? path : `${path}.md`)
  } catch (e) {
    notice.value = errorText(e, t, t('graph.save_failed'))
  }
}

/** Ego windows (opened from the "direct links" button) render compact. */
const compact = computed(() => Boolean(props.focus) && !props.global)

// URL persistence for the full view: plancia has no "current window"
// injection, but graph keys are deterministic (sidebar singleton `graph`,
// ProjectsView `graph:project:<name>`), so the view can find its own
// window once and push filter changes back via identify() — the codec
// then re-encodes them into the `?w=` token. Ego windows never sync
// (their props are static). Promote a proper CURRENT_WINDOW injection
// to plancia if a second window type ever needs this (ADR-004 gate).
const selfId = ref<string | null>(null)
function resolveSelf() {
  if (compact.value) return
  const candidates = props.project
    ? [`graph:project:${props.project}`, planciaKey('graph')]
    : [planciaKey('graph')]
  for (const k of candidates) {
    const w = store.windows.find((w) => w.type === 'graph' && w.key === k)
    if (w) {
      selfId.value = w.id
      return
    }
  }
}
function syncWindow() {
  if (!selfId.value) return
  const w = store.windows.find((w) => w.id === selfId.value)
  if (!w) {
    selfId.value = null
    return
  }
  store.identify(w.id, w.key, {
    ...(plugin.value ? { source: source.value, with: { ...args.value } } : {}),
    ...(project.value ? { project: project.value } : {}),
    ...(tag.value ? { tag: tag.value } : {}),
    ...(focus.value ? { focus: focus.value, depth: depth.value } : {}),
    ...(minDegree.value > 0 ? { minDegree: minDegree.value } : {}),
    ...(limitSet.value ? { limit: limitSet.value } : {}),
    global: true,
  })
  store.setTitle(w.id, project.value ? `${t('nav.graph')} · ${project.value}` : t('nav.graph'))
}
const hadInitialFilter = Boolean(project.value || tag.value || focus.value)
const params = computed(() => plugin.value
  ? { source: source.value, project: project.value || undefined, with: { ...args.value } }
  : {
  project: project.value || undefined,
  tag: tag.value || undefined,
  focus: focus.value || undefined,
  depth: focus.value ? depth.value : undefined,
  min_degree: minDegree.value > 0 ? minDegree.value : undefined,
  limit: limitSet.value,
})

// Only the last request draws the graph: a slow answer to older filters
// replaced the one asked after (BUG-116, S6-10).
let gen = 0

async function load() {
  syncWindow()
  const mine = ++gen
  // A plugin's graph waits for the project's sources, which say what it
  // requires, then for those arguments.
  if (plugin.value && (!currentSource.value || missingArgs.value.length)) {
    data.value = null
    error.value = null
    loading.value = false
    return
  }
  loading.value = true
  error.value = null
  try {
    const refreshing = refreshNext
    refreshNext = false
    const graph = await fetchGraph({ ...params.value, refresh: refreshing })
    if (mine === gen) {
      data.value = graph
      selected.value = null
    }
  } catch (e) {
    if (mine !== gen) return
    error.value = errorText(e, t, t('graph.load_failed'))
    data.value = null
  } finally {
    if (mine === gen) loading.value = false
  }
}

const debouncedLoad = useDebounceFn(load, 200)
watch(params, () => void debouncedLoad(), { deep: true })
watch(project, () => void loadSources())

function reset() {
  source.value = 'links'
  args.value = {}
  project.value = ''
  tag.value = ''
  focus.value = ''
  depth.value = 2
  minDegree.value = 0
  limit.value = ''
}

function onNode(id: string) {
  if (!plugin.value) {
    onSelect(id)
    return
  }
  const node = data.value?.nodes.find((n) => n.id === id) ?? null
  selected.value = node
  if (node?.link) onSelect(node.link)
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
  resolveSelf()
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
  void loadSources()
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
        <span class="text-text-muted text-xs">{{ t('graph.project') }}</span>
        <SearchSelect
          v-model="project"
          class="mt-1"
          :items="sortedProjects"
          :value-key="(p: Project) => p.name"
          :label="(p: Project) => p.name"
          :secondary="(p: Project) => String(p.note_count)"
          :placeholder="t('graph.project_placeholder')"
        />
      </div>

      <label class="block text-sm">
        <span class="text-text-muted text-xs">{{ t('graph.source') }}</span>
        <select
          v-model="source"
          class="h-control py-0 mt-1 w-full rounded bg-bg border border-border px-2 text-sm"
          data-graph-source
        >
          <option value="links">
            {{ t('graph.source_links') }}
          </option>
          <option
            v-for="s in sources.filter((x) => x.source !== 'links')"
            :key="s.source"
            :value="s.source"
          >
            {{ s.source }}
          </option>
        </select>
        <span
          v-if="currentSource?.description"
          class="block mt-1 text-xs text-text-muted"
        >{{ currentSource.description }}</span>
      </label>

      <template v-if="plugin">
        <label
          v-for="f in argFields"
          :key="f.name"
          class="block text-sm"
          :title="f.description"
        >
          <!-- The * is for the eye; the field itself says it is required -->
          <span class="text-text-muted text-xs">{{ f.name }}<span v-if="f.required" aria-hidden="true"> *</span></span>
          <select
            v-if="f.options"
            :value="String(args[f.name] ?? '')"
            class="h-control py-0 mt-1 w-full rounded bg-bg border border-border px-2 text-sm"
            :required="f.required"
            :aria-required="f.required ? 'true' : undefined"
            @change="setArg(f.name, f.type, ($event.target as HTMLSelectElement).value)"
          >
            <option value="" />
            <option
              v-for="o in f.options"
              :key="o"
              :value="o"
            >
              {{ o }}
            </option>
          </select>
          <input
            v-else-if="f.type === 'boolean'"
            type="checkbox"
            class="ml-2"
            :checked="Boolean(args[f.name])"
            @change="setArg(f.name, f.type, ($event.target as HTMLInputElement).checked)"
          >
          <input
            v-else
            :type="f.type === 'integer' || f.type === 'number' ? 'number' : 'text'"
            :value="args[f.name] ?? ''"
            class="h-control py-0 mt-1 w-full rounded bg-bg border border-border px-2 text-sm"
            :required="f.required"
            :aria-required="f.required ? 'true' : undefined"
            @change="setArg(f.name, f.type, ($event.target as HTMLInputElement).value)"
          >
        </label>
        <button
          type="button"
          class="h-control w-full text-xs px-2 rounded border border-border hover:bg-surface-hover"
          data-graph-refresh
          @click="refresh"
        >
          {{ t('graph.refresh') }}
        </button>
      </template>

      <template v-if="!plugin">
        <div class="block text-sm">
          <span class="text-text-muted text-xs">{{ t('graph.tag') }}</span>
          <SearchSelect
            v-model="tag"
            class="mt-1"
            :items="tags"
            :value-key="(t: TagCount) => t.tag"
            :label="(t: TagCount) => '#' + t.tag"
            :secondary="(t: TagCount) => String(t.count)"
            :placeholder="t('graph.tag_placeholder')"
          />
        </div>

        <div class="block text-sm">
          <span class="text-text-muted text-xs">{{ t('graph.focus') }}</span>
          <SearchSelect
            v-model="focus"
            class="mt-1"
            :items="notes"
            :value-key="(n: NoteTitleHit) => n.path"
            :label="(n: NoteTitleHit) => n.title || n.path"
            :secondary="(n: NoteTitleHit) => n.path"
            :placeholder="t('graph.focus_placeholder')"
          />
        </div>

        <label class="block text-sm">
          <span class="text-text-muted text-xs">{{ t('graph.depth') }}</span>
          <input
            v-model.number="depth"
            type="number"
            min="1"
            max="6"
            class="h-control py-0 mt-1 w-full rounded bg-bg border border-border px-2 text-sm"
          >
        </label>
        <label class="block text-sm">
          <span class="text-text-muted text-xs">{{ t('graph.min_degree') }}</span>
          <input
            v-model.number="minDegree"
            type="number"
            min="0"
            max="20"
            class="h-control py-0 mt-1 w-full rounded bg-bg border border-border px-2 text-sm"
          >
        </label>
        <label class="block text-sm">
          <span class="text-text-muted text-xs">{{ t('graph.limit', { default: ui.graphDefaultNodes }) }}</span>
          <input
            v-model.number="limit"
            type="number"
            min="0"
            :max="ui.graphMaxNodes"
            :placeholder="String(ui.graphDefaultNodes)"
            class="h-control py-0 mt-1 w-full rounded bg-bg border border-border px-2 text-sm"
          >
        </label>
      </template>

      <button
        type="button"
        class="h-control w-full text-xs px-2 rounded border border-border hover:bg-surface-hover"
        @click="reset"
      >
        {{ t('graph.reset') }}
      </button>
      <button
        type="button"
        class="h-control w-full text-xs px-2 rounded border border-border hover:bg-surface-hover"
        data-graph-save
        @click="saveAsNote"
      >
        {{ t('graph.save_note') }}
      </button>
      <p
        v-if="notice"
        class="text-xs text-text-muted break-words"
      >
        {{ notice }}
      </p>

      <div
        v-if="selected"
        class="text-xs space-y-1 pt-3 border-t border-border"
        data-graph-selected
      >
        <p class="text-text-muted">
          {{ t('graph.selected') }}
        </p>
        <p class="font-semibold text-text break-words">
          {{ selected.label }}
        </p>
        <p
          v-if="selected.group"
          class="text-text-muted break-words"
        >
          {{ selected.group }}
        </p>
        <p
          v-if="selected.text"
          class="text-text whitespace-pre-wrap break-words max-h-48 overflow-auto"
        >
          {{ selected.text }}
        </p>
        <button
          v-if="selected.link"
          type="button"
          class="h-control-sm px-2 rounded border border-border hover:bg-surface-hover"
          @click="onSelect(selected.link)"
        >
          {{ t('graph.open_note') }}
        </button>
      </div>

      <div
        v-if="data"
        class="text-xs text-text-muted space-y-1 pt-3 border-t border-border"
      >
        <p>{{ t('graph.nodes') }} <strong class="text-text">{{ data.stats.node_count }}</strong></p>
        <p>{{ t('graph.edges') }} <strong class="text-text">{{ data.stats.edge_count }}</strong></p>
        <p
          v-if="data.stats.truncated"
          class="text-warning"
          data-graph-truncated
        >
          {{ t('graph.truncated', { shown: data.stats.node_count, total: data.stats.total ?? data.stats.node_count, max: ui.graphMaxNodes }) }}
        </p>
        <p v-if="data.cached">
          {{ t('graph.cached') }}
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
        {{ t('common.loading') }}
      </p>
      <ErrorMessage
        v-else-if="error"
        :text="error"
        class="absolute top-3 left-3 z-10 max-w-[80%] text-xs bg-bg-elevated px-2 py-1 rounded border border-danger"
      />
      <select
        v-if="mode === '3d'"
        v-model="zMode"
        class="h-control-sm py-0 absolute top-3 right-24 z-10 rounded border border-border bg-bg-elevated text-xs text-text-muted px-1.5"
        :aria-label="t('graph.z_mode')"
      >
        <option value="free">
          {{ t('graph.z_free') }}
        </option>
        <option value="groups">
          {{ t('graph.z_groups') }}
        </option>
        <option value="recency">
          {{ t('graph.z_recency') }}
        </option>
      </select>
      <div
        class="absolute top-3 right-3 z-10 flex rounded border border-border overflow-hidden text-xs bg-bg-elevated"
        role="group"
        :aria-label="t('graph.renderer')"
      >
        <button
          type="button"
          class="h-control-sm px-2"
          :class="mode === '2d' ? 'bg-surface-hover text-text font-semibold' : 'text-text-muted hover:bg-surface-hover'"
          @click="setMode('2d')"
        >
          2D
        </button>
        <button
          type="button"
          class="h-control-sm px-2 border-l border-border"
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
        @select="onNode"
      />
      <Graph3DCanvas
        v-else-if="data && mode === '3d'"
        :nodes="data.nodes"
        :edges="data.edges"
        :z-mode="zMode"
        @select="onNode"
      />
      <p
        v-else-if="missingArgs.length"
        class="p-8 text-text-muted text-sm"
        data-graph-args-needed
      >
        {{ t('graph.args_needed', { names: missingArgs.join(', ') }) }}
      </p>
      <p
        v-else-if="!loading"
        class="p-8 text-text-muted text-sm"
      >
        {{ t('graph.no_data') }}
      </p>
    </section>
  </div>
</template>
