<script setup lang="ts">
/**
 * INWindow — a project's IN folder (IMP-172): the files to work on, under
 * their own names and in their own tree, which the pipelines take in. A
 * folder at a time: its subfolders with what they hold, then its files,
 * each with its status and the run that took it. A search looks under the
 * folder. Who writes in the project moves, deletes (into the trash), sets
 * a file aside or back as new, and starts a pipeline on the files chosen.
 * A scan, a move or the end of a run updates it live.
 *
 * A folder at its root may be a plugin's source (IMP-172 phase 2): its
 * files stay in the source, are read from there, and are neither deleted
 * nor moved out of it; they move inside it when the binding lets them.
 */
import { computed, inject, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { ChevronRight, Cloud, Download, FileText, Folder, Inbox, Play, RefreshCw, RotateCcw, Search, Trash2, EyeOff, FolderInput } from 'lucide-vue-next'
import {
  deleteIN,
  inFileHref,
  IN_STATUSES,
  listIN,
  moveIN,
  runIN,
  scanIN,
  setINStatus,
  sourceOf,
  type INCounts,
  type INFile,
  type INFolder,
  type INSource,
  type INStatus,
} from '@/api/in'
import { exportFolder } from '@/api/folders'
import { listPipelines, type PipelineInfo } from '@/api/pipelines'
import { errorText } from '@/api/errors'
import { formatDateTime, formatSize } from '@/api/format'
import { confirmAction } from '@/composables/useConfirm'
import { useSSE } from '@/composables/useSSE'
import { planciaKey } from '@/composables/planciaKey'
import { useAccessStore } from '@/stores/access'
import { useUIStore } from '@/stores/ui'
import { useTreeStore } from '@/stores/tree'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'

const props = defineProps<{ project: string }>()
const emit = defineEmits<{ title: [string] }>()

const { t } = useI18n()
const access = useAccessStore()
const ui = useUIStore()
const treeStore = useTreeStore()
const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))
const sse = useSSE()

const root = computed(() => `${props.project}/IN`)
const canWrite = computed(() => access.canWrite(root.value))

const dir = ref('')
const search = ref('')
const status = ref<INStatus | ''>('')
const counts = ref<INCounts | null>(null)
const folders = ref<INFolder[]>([])
const files = ref<INFile[]>([])
const next = ref('')
const loading = ref(false)
const error = ref<string | null>(null)
const message = ref<string | null>(null)
const selected = ref<Set<string>>(new Set())
const moveTo = ref('')
const pipelines = ref<PipelineInfo[]>([])
const pipeline = ref('')
const sources = ref<INSource[]>([])

/** The source of the folder shown, when it is one's. */
const here = computed(() => sourceOf(sources.value, dir.value))
/** A folder at the root of the IN folder that is a source's. */
const sourceFolder = (path: string) => (path.includes('/') ? undefined : sourceOf(sources.value, path))

const chosenFiles = computed(() => files.value.filter((f) => selected.value.has(f.path)))
/** The source of the files chosen: undefined for files of the disk, null
 *  for files of more than one place. */
const chosenSource = computed<INSource | undefined | null>(() => {
  const places = new Set(chosenFiles.value.map((f) => (f.source === 'local' ? '' : f.dir.split('/')[0])))
  if (places.size > 1) return null
  const [place] = [...places]
  return place ? sourceOf(sources.value, place) ?? null : undefined
})
const canMoveChosen = computed(() => {
  const src = chosenSource.value
  return src === undefined || (src !== null && src.write && src.move)
})
const canDeleteChosen = computed(() => chosenSource.value === undefined)
const moveBlocked = computed(() => {
  if (chosenSource.value === null) return t('in.source.mixed')
  return canMoveChosen.value ? '' : t('in.source.no_move')
})

/** A search or a status looks under the folder, without its subfolders shown. */
const filtering = computed(() => search.value.trim() !== '' || status.value !== '')

const crumbs = computed(() => {
  const out = [{ label: 'IN', path: '' }]
  let acc = ''
  for (const seg of dir.value.split('/').filter(Boolean)) {
    acc = acc ? `${acc}/${seg}` : seg
    out.push({ label: seg, path: acc })
  }
  return out
})

const allSelected = computed(() => files.value.length > 0 && files.value.every((f) => selected.value.has(f.path)))

function query(cursor = '') {
  const text = search.value.trim()
  return {
    dir: dir.value,
    recursive: filtering.value,
    match: text ? `*${text.replace(/[*?[\]]/g, '')}*` : '',
    status: status.value,
    cursor,
  }
}

let loadSeq = 0
async function load() {
  const mine = ++loadSeq
  loading.value = true
  error.value = null
  try {
    const res = await listIN(props.project, query())
    if (mine !== loadSeq) return
    counts.value = res.counts
    sources.value = res.sources ?? []
    folders.value = filtering.value ? [] : res.folders
    files.value = res.files
    next.value = res.next
    const shown = new Set(res.files.map((f) => f.path))
    selected.value = new Set([...selected.value].filter((p) => shown.has(p)))
  } catch (e) {
    if (mine === loadSeq) error.value = errorText(e, t, t('in.load_failed'))
  } finally {
    if (mine === loadSeq) loading.value = false
  }
}

async function more() {
  if (!next.value) return
  try {
    const res = await listIN(props.project, query(next.value))
    files.value = [...files.value, ...res.files]
    next.value = res.next
  } catch (e) {
    error.value = errorText(e, t, t('in.load_failed'))
  }
}

function go(path: string) {
  dir.value = path
  search.value = ''
  status.value = ''
  selected.value = new Set()
  void load()
}

function toggle(path: string) {
  const s = new Set(selected.value)
  if (s.has(path)) s.delete(path)
  else s.add(path)
  selected.value = s
}

function toggleAll() {
  selected.value = allSelected.value ? new Set() : new Set(files.value.map((f) => f.path))
}

const chosen = () => [...selected.value]

async function act(fn: () => Promise<string | void>) {
  error.value = null
  message.value = null
  try {
    const msg = await fn()
    if (msg) message.value = msg
    await load()
    treeStore.refresh()
  } catch (e) {
    error.value = errorText(e, t, t('in.action_failed'))
  }
}

function move() {
  const to = moveTo.value.trim()
  if (!to || !selected.value.size || !canMoveChosen.value) return
  // A source's files move inside it: the server reads done/ as its done/.
  const target = to.endsWith('/') ? to : to + '/'
  void act(async () => {
    for (const p of chosen()) await moveIN(props.project, p, target)
    moveTo.value = ''
    return t('in.moved', { count: selected.value.size, to: target }, selected.value.size)
  })
}

function mark(s: INStatus) {
  if (!selected.value.size) return
  void act(async () => {
    await setINStatus(props.project, chosen(), s)
  })
}

async function remove(paths: string[], what: string) {
  if (!paths.length) return
  const msg = access.trash === false ? t('in.confirm_delete_forever', { what }) : t('in.confirm_delete', { what })
  if (!(await confirmAction(msg, { confirmLabel: t('common.delete'), danger: true }))) return
  void act(async () => {
    const res = await deleteIN(props.project, paths)
    return res.trash ? t('in.deleted_trash', { count: res.removed.length }, res.removed.length) : t('in.deleted', { count: res.removed.length }, res.removed.length)
  })
}

function removeSelected() {
  if (!canDeleteChosen.value) return
  void remove(chosen(), t('in.files_count', { count: selected.value.size }, selected.value.size))
}

/** Asks the server to check a source now; the window hears when it ends. */
function refresh(folder: string) {
  void act(async () => {
    await scanIN(props.project, folder)
    return t('in.source.refreshing')
  })
}

function removeFolder(f: INFolder) {
  void remove([f.path], f.path)
}

function startRun() {
  if (!pipeline.value || !selected.value.size) return
  void act(async () => {
    const res = await runIN(props.project, pipeline.value, chosen())
    const parts = [
      res.runs.length
        ? t('in.runs_started', { count: res.runs.length, id: res.runs[0]!.id }, res.runs.length)
        : t('in.no_run'),
    ]
    for (const s of res.skipped) {
      parts.push(t(`in.skipped_${s.reason}`, { name: s.path.split('/').pop(), value: s.value }))
    }
    return parts.join(' ')
  })
}

function openRuns() {
  openWindow({ type: 'runs', key: planciaKey('runs'), title: t('nav.runs'), props: {} })
}

function downloadFolder(path: string) {
  void exportFolder(path ? `${root.value}/${path}` : root.value).catch((e) => {
    error.value = errorText(e, t, t('tree.menu.download_failed'))
  })
}

const sourceClass: Record<INSource['status'], string> = {
  ok: 'bg-success/15 text-success',
  checking: 'bg-info/15 text-info',
  error: 'bg-danger/15 text-danger',
  off: 'bg-surface-hover text-text-muted',
}

const statusClass: Record<INStatus, string> = {
  new: 'bg-info/15 text-info',
  working: 'bg-warning/15 text-warning',
  done: 'bg-success/15 text-success',
  error: 'bg-danger/15 text-danger',
  ignored: 'bg-surface-hover text-text-muted',
}

async function loadPipelines() {
  if (!ui.pipelinesEnabled || !canWrite.value) return
  try {
    pipelines.value = (await listPipelines(props.project)).filter((p) => !p.problem)
  } catch {
    pipelines.value = []
  }
}

let reloadTimer: ReturnType<typeof setTimeout> | null = null
sse.on('tree', (p) => {
  if (p.action !== 'in' || p.project !== props.project) return
  if (reloadTimer) clearTimeout(reloadTimer)
  reloadTimer = setTimeout(() => {
    reloadTimer = null
    void load()
  }, 200)
})

watch(search, () => void load())
watch(status, () => void load())

onMounted(() => {
  emit('title', t('in.window_title', { name: props.project }))
  void load()
  void loadPipelines()
})
</script>

<template>
  <div class="p-6 max-w-5xl mx-auto text-sm" data-in-window>
    <h1 class="sr-only">{{ t('in.window_title', { name: project }) }}</h1>

    <div class="flex flex-wrap items-center gap-3 mb-4">
      <Inbox class="w-5 h-5 text-text-muted" aria-hidden="true" />
      <nav class="flex items-center gap-1 font-mono" :aria-label="t('in.path')">
        <template v-for="(c, i) in crumbs" :key="c.path">
          <ChevronRight v-if="i" class="w-3 h-3 opacity-50" aria-hidden="true" />
          <button
            type="button"
            class="rounded px-1 hover:bg-surface-hover"
            :class="{ 'font-semibold': i === crumbs.length - 1 }"
            :data-in-crumb="c.path"
            @click="go(c.path)"
          >{{ c.label }}</button>
        </template>
      </nav>
      <span v-if="counts" class="text-xs text-text-muted" data-in-counts>
        {{ t('in.counts', { files: counts.files, size: formatSize(counts.bytes) }, counts.files) }}
        <span v-if="counts.new" class="text-info"> · {{ t('in.count_new', { count: counts.new }) }}</span>
        <span v-if="counts.working" class="text-warning"> · {{ t('in.count_working', { count: counts.working }) }}</span>
        <span v-if="counts.errors" class="text-danger"> · {{ t('in.count_errors', { count: counts.errors }) }}</span>
      </span>
      <span class="flex-1" />
      <button
        v-if="!here"
        type="button"
        class="h-control-sm text-xs px-2 rounded border border-border hover:bg-surface-hover inline-flex items-center gap-1"
        :title="t('in.download_folder')"
        @click="downloadFolder(dir)"
      ><Download class="w-3 h-3" /> {{ t('tree.menu.download_zip') }}</button>
    </div>

    <ul v-if="!dir && sources.length && !filtering" class="mb-4 space-y-1" data-in-sources>
      <li
        v-for="src in sources"
        :key="src.folder"
        class="flex items-start gap-2 rounded border border-border px-3 py-2 text-xs"
        :data-in-source="src.folder"
      >
        <Cloud class="w-3.5 h-3.5 mt-0.5 shrink-0 text-text-muted" aria-hidden="true" />
        <div class="flex-1 min-w-0 space-y-1">
          <p class="flex flex-wrap items-center gap-x-2 gap-y-1">
            <button type="button" class="font-mono hover:underline" :data-in-source-open="src.folder" @click="go(src.folder)">IN/{{ src.folder }}</button>
            <span class="text-text-muted">{{ t('in.source.from', { source: `${src.plugin}/${src.source}` }) }}<template v-if="src.kind"> · {{ src.kind }}</template></span>
            <span class="rounded px-1.5 py-0.5" :class="sourceClass[src.status]" data-in-source-status>{{ t(`in.source.status.${src.status}`) }}</span>
          </p>
          <p v-if="src.checked" class="text-text-muted">{{ t('in.source.checked', { when: formatDateTime(src.checked) }) }}</p>
          <p v-if="src.status === 'error' && src.error" class="text-danger break-words" data-in-source-error>{{ src.error }}</p>
          <p v-else-if="src.status === 'off'" class="text-text-muted">{{ t('in.source.off') }}</p>
        </div>
        <button
          v-if="canWrite && src.status !== 'off'"
          type="button"
          class="h-control-sm shrink-0 px-2 rounded border border-border hover:bg-surface-hover inline-flex items-center gap-1"
          :data-in-refresh="src.folder"
          @click="refresh(src.folder)"
        ><RefreshCw class="w-3 h-3" /> {{ t('in.source.refresh') }}</button>
      </li>
    </ul>

    <div v-if="here" class="mb-4 flex items-start gap-2 rounded border border-border px-3 py-2 text-xs" data-in-source-banner>
      <Cloud class="w-3.5 h-3.5 mt-0.5 shrink-0 text-text-muted" aria-hidden="true" />
      <div class="flex-1 min-w-0 space-y-1">
        <p>
          {{ t('in.source.banner', { source: `${here.plugin}/${here.source}` }) }}
          <span class="ml-1 rounded px-1.5 py-0.5 whitespace-nowrap" :class="sourceClass[here.status]">{{ t(`in.source.status.${here.status}`) }}</span>
        </p>
        <p class="text-text-muted">{{ here.write && here.move ? t('in.source.can_move') : t('in.source.read_only') }}</p>
        <p v-if="here.status === 'error' && here.error" class="text-danger break-words">{{ here.error }}</p>
      </div>
      <button
        v-if="canWrite && here.status !== 'off'"
        type="button"
        class="h-control-sm shrink-0 px-2 rounded border border-border hover:bg-surface-hover inline-flex items-center gap-1"
        :data-in-refresh="here.folder"
        @click="refresh(here.folder)"
      ><RefreshCw class="w-3 h-3" /> {{ t('in.source.refresh') }}</button>
    </div>

    <div class="flex flex-wrap items-center gap-2 mb-3">
      <label class="relative">
        <span class="sr-only">{{ t('in.search') }}</span>
        <Search class="w-3.5 h-3.5 absolute left-2 top-1/2 -translate-y-1/2 text-text-muted" aria-hidden="true" />
        <input
          v-model.trim="search"
          type="search"
          class="h-control-sm text-xs pl-7 pr-2 rounded border border-border bg-surface w-56"
          :placeholder="t('in.search_placeholder')"
          data-in-search
        />
      </label>
      <select v-model="status" class="h-control-sm text-xs rounded border border-border bg-surface" data-in-status-filter>
        <option value="">{{ t('in.any_status') }}</option>
        <option v-for="s in IN_STATUSES" :key="s" :value="s">{{ t(`in.status.${s}`) }}</option>
      </select>
      <p class="text-xs text-text-muted">{{ t('in.intro') }}</p>
    </div>

    <ErrorMessage v-if="error" :text="error" />
    <p v-if="message" class="text-success text-xs mb-2" data-in-message>{{ message }}</p>

    <div
      v-if="canWrite && selected.size"
      class="sticky top-0 z-10 flex flex-wrap items-center gap-2 mb-2 rounded border border-border bg-surface px-3 py-2"
      data-in-actions
    >
      <span class="text-xs">{{ t('in.selected', { count: selected.size }, selected.size) }}</span>
      <label class="inline-flex items-center gap-1">
        <span class="sr-only">{{ t('in.move_to') }}</span>
        <input
          v-model="moveTo"
          type="text"
          class="h-control-sm text-xs px-2 rounded border border-border bg-surface w-40 font-mono"
          :placeholder="t('in.move_placeholder')"
          data-in-move-target
          @keydown.enter="move"
        />
        <button
          type="button"
          class="h-control-sm text-xs px-2 rounded border border-border hover:bg-surface-hover inline-flex items-center gap-1"
          :disabled="!moveTo.trim() || !canMoveChosen"
          :title="moveBlocked || undefined"
          data-in-action="move"
          @click="move"
        ><FolderInput class="w-3 h-3" /> {{ t('in.move') }}</button>
      </label>
      <button
        type="button"
        class="h-control-sm text-xs px-2 rounded border border-border hover:bg-surface-hover inline-flex items-center gap-1"
        data-in-action="new"
        @click="mark('new')"
      ><RotateCcw class="w-3 h-3" /> {{ t('in.mark_new') }}</button>
      <button
        type="button"
        class="h-control-sm text-xs px-2 rounded border border-border hover:bg-surface-hover inline-flex items-center gap-1"
        data-in-action="ignore"
        @click="mark('ignored')"
      ><EyeOff class="w-3 h-3" /> {{ t('in.mark_ignored') }}</button>
      <template v-if="pipelines.length">
        <select v-model="pipeline" class="h-control-sm text-xs rounded border border-border bg-surface max-w-56" data-in-pipeline>
          <option value="">{{ t('in.choose_pipeline') }}</option>
          <option v-for="p in pipelines" :key="p.path" :value="p.path">{{ p.title || p.path }}</option>
        </select>
        <button
          type="button"
          class="h-control-sm text-xs px-2 rounded border border-border hover:bg-surface-hover inline-flex items-center gap-1"
          :disabled="!pipeline"
          data-in-action="run"
          @click="startRun"
        ><Play class="w-3 h-3" /> {{ t('in.run') }}</button>
      </template>
      <button
        v-if="canDeleteChosen"
        type="button"
        class="h-control-sm text-xs px-2 rounded text-danger hover:bg-surface-hover inline-flex items-center gap-1"
        data-in-action="delete"
        @click="removeSelected"
      ><Trash2 class="w-3 h-3" /> {{ t('common.delete') }}</button>
      <span v-else class="text-xs text-text-muted" data-in-no-delete>{{ chosenSource === null ? t('in.source.mixed') : t('in.source.no_delete') }}</span>
    </div>

    <p v-if="loading && !files.length && !folders.length" class="text-text-muted">{{ t('common.loading') }}</p>
    <p v-else-if="!loading && !files.length && !folders.length" class="text-text-muted" data-in-empty>
      {{ filtering ? t('in.nothing_found') : t('in.empty') }}
    </p>

    <table v-if="files.length || folders.length" class="w-full text-xs border-collapse">
      <thead class="text-text-muted text-left">
        <tr class="border-b border-border">
          <th class="w-6 py-1">
            <input
              v-if="canWrite && files.length"
              type="checkbox"
              :checked="allSelected"
              :aria-label="t('in.select_all')"
              @change="toggleAll"
            />
          </th>
          <th class="py-1 font-normal">{{ t('in.name') }}</th>
          <th class="py-1 font-normal text-right">{{ t('in.size') }}</th>
          <th class="py-1 font-normal">{{ t('in.modified') }}</th>
          <th class="py-1 font-normal">{{ t('in.status_label') }}</th>
          <th class="w-16 py-1" />
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="f in folders"
          :key="'d:' + f.path"
          class="border-b border-border/50 hover:bg-surface-hover"
          :data-in-folder="f.path"
        >
          <td />
          <td class="py-1">
            <button type="button" class="inline-flex items-center gap-1.5 hover:underline" data-in-open @click="go(f.path)">
              <Cloud v-if="sourceFolder(f.path)" class="w-3.5 h-3.5 text-text-muted" :aria-label="t('in.source.label')" data-in-cloud />
              <Folder v-else class="w-3.5 h-3.5 text-text-muted" aria-hidden="true" />
              <span class="font-mono">{{ f.path.split('/').pop() }}</span>
            </button>
          </td>
          <td class="py-1 text-right text-text-muted">{{ formatSize(f.bytes) }}</td>
          <td class="py-1 text-text-muted">{{ t('in.files_count', { count: f.files }, f.files) }}</td>
          <td class="py-1">
            <span v-if="f.new" class="rounded px-1.5 py-0.5 bg-info/15 text-info">{{ t('in.count_new', { count: f.new }) }}</span>
          </td>
          <td class="py-1 text-right whitespace-nowrap">
            <button
              v-if="!here && !sourceFolder(f.path)"
              type="button"
              class="rounded p-0.5 text-text-muted hover:text-text"
              :title="t('tree.menu.download_zip')"
              :aria-label="t('tree.menu.download_zip')"
              @click="downloadFolder(f.path)"
            ><Download class="w-3.5 h-3.5" /></button>
            <button
              v-if="canWrite && !here && !sourceFolder(f.path)"
              type="button"
              class="rounded p-0.5 text-text-muted hover:text-danger"
              :title="t('common.delete')"
              :aria-label="t('common.delete')"
              :data-in-delete-folder="f.path"
              @click="removeFolder(f)"
            ><Trash2 class="w-3.5 h-3.5" /></button>
          </td>
        </tr>
        <tr
          v-for="f in files"
          :key="f.path"
          class="border-b border-border/50 hover:bg-surface-hover"
          :data-in-file="f.path"
        >
          <td class="py-1">
            <input
              v-if="canWrite"
              type="checkbox"
              :checked="selected.has(f.path)"
              :aria-label="t('in.select', { name: f.name })"
              data-in-select
              @change="toggle(f.path)"
            />
          </td>
          <td class="py-1">
            <span class="inline-flex items-center gap-1.5">
              <FileText class="w-3.5 h-3.5 text-text-muted" aria-hidden="true" />
              <span class="font-mono">{{ filtering && f.dir !== dir ? f.path.slice(root.length + 1) : f.name }}</span>
            </span>
          </td>
          <td class="py-1 text-right text-text-muted whitespace-nowrap">{{ formatSize(f.size) }}</td>
          <td class="py-1 text-text-muted whitespace-nowrap">{{ formatDateTime(f.modified) }}</td>
          <td class="py-1 whitespace-nowrap">
            <span class="rounded px-1.5 py-0.5" :class="statusClass[f.status]" :data-in-status="f.status">{{ t(`in.status.${f.status}`) }}</span>
            <button
              v-if="f.run"
              type="button"
              class="ml-1 text-text-muted hover:underline font-mono"
              :title="t('in.open_runs')"
              @click="openRuns"
            >{{ f.run.slice(0, 8) }}</button>
          </td>
          <td class="py-1 text-right">
            <a
              :href="inFileHref(f.path)"
              class="inline-flex rounded p-0.5 text-text-muted hover:text-text"
              :title="t('tree.menu.download')"
              :aria-label="t('tree.menu.download')"
              download
            ><Download class="w-3.5 h-3.5" /></a>
          </td>
        </tr>
      </tbody>
    </table>
    <button
      v-if="next"
      type="button"
      class="mt-3 h-control-sm text-xs px-3 rounded border border-border hover:bg-surface-hover"
      data-in-more
      @click="more"
    >{{ t('in.more') }}</button>
  </div>
</template>
