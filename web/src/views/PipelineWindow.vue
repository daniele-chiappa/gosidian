<script setup lang="ts">
/**
 * PipelineWindow — a pipeline note in the editor (IMP-169, F4): the
 * ```pipeline block of the note at `path`, edited by plancia-diagrams'
 * PipelineEditor with the node types of the server (GET
 * /api/v1/pipeline-nodes, the base ones with the pipelines off). Each
 * change of the YAML is checked by the server (POST /api/v1/pipeline-check)
 * and its problem shown. Saving puts the block back in place, with the
 * note's etag; a note changed meanwhile gets the conflict banner, as in the
 * diagram window. Ctrl+S saves.
 *
 * Where the pipelines run, the window shows a run on the drawing, the last
 * one or one chosen, live (the job events), and starts a run of the saved
 * pipeline, resumes a waiting one or cancels it.
 */
import {
  computed,
  defineAsyncComponent,
  inject,
  onBeforeUnmount,
  onMounted,
  ref,
  shallowRef,
  watch,
} from 'vue'
import { useI18n } from 'vue-i18n'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { getNote, updateNote, type Note } from '@/api/notes'
import {
  cancelRun,
  checkPipeline,
  getRun,
  listRuns,
  resumeRun,
  runEnded,
  runStateOf,
  startRun,
  type RunBrief,
} from '@/api/pipelines'
import { isConcurrencyConflict } from '@/api/client'
import { errorText } from '@/api/errors'
import { formatDateTime } from '@/api/format'
import { useAccessStore } from '@/stores/access'
import { useSSE, type SSEPayload } from '@/composables/useSSE'
import { planciaKey, base } from '@/composables/planciaKey'
import { usePipelineCatalog } from '@/composables/pipelineCatalog'
import { useDiagramLabels } from '@/components/domain/diagramLabels'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'

type Lib = typeof import('@/components/domain/diagramView')

const props = defineProps<{ path: string }>()
const emit = defineEmits<{ title: [string]; dirty: [boolean]; close: [] }>()

const { t } = useI18n()
const access = useAccessStore()
const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))
const labels = useDiagramLabels().pipeline
const { catalog, load: loadCatalog } = usePipelineCatalog()

const PipelineEditor = defineAsyncComponent(() =>
  import('@/components/domain/diagramView').then((m) => m.PipelineEditor),
)
const PipelineView = defineAsyncComponent(() =>
  import('@/components/domain/diagramView').then((m) => m.PipelineView),
)

const lib = shallowRef<Lib | null>(null)
const note = ref<Note | null>(null)
const saved = ref('')
const draft = ref('')
const loading = ref(true)
const saving = ref(false)
const error = ref<string | null>(null)
const missing = ref(false)
const conflict = ref(false)
const remoteChanged = ref(false)
const savedAt = ref<string | null>(null)

const dirty = computed(() => draft.value !== saved.value)
watch(dirty, (d) => emit('dirty', d))
const writable = computed(() => access.canWrite(props.path))
const enabled = computed(() => !!catalog.value?.pipelines_enabled)

/** The note's ```pipeline block, or null. */
function blockOf(content: string) {
  return lib.value?.findDiagramBlocks(content, 'pipeline')[0] ?? null
}

async function load() {
  loading.value = true
  error.value = null
  missing.value = false
  try {
    lib.value ??= await import('@/components/domain/diagramView')
    const n = await getNote(props.path)
    note.value = n
    const b = blockOf(n.content)
    if (!b) {
      missing.value = true
      saved.value = draft.value = ''
    } else {
      saved.value = draft.value = b.source
    }
    conflict.value = false
    remoteChanged.value = false
    emit('title', t('pipeline_editor.window_title', { name: base(props.path) }))
  } catch (e) {
    error.value = errorText(e, t, t('note.load_failed'))
  } finally {
    loading.value = false
  }
}

async function write(into: Note, ifMatch: string) {
  const b = blockOf(into.content)
  if (!b || !lib.value) {
    missing.value = true
    return
  }
  const sent = draft.value
  const updated = await updateNote(props.path, {
    content: lib.value.replaceDiagramBlock(into.content, b, sent),
    ifMatch,
  })
  note.value = updated
  saved.value = blockOf(updated.content)?.source ?? sent
  // What was typed while the save ran stays a draft.
  if (draft.value === sent) draft.value = saved.value
  savedAt.value = new Date().toLocaleTimeString()
  conflict.value = false
  remoteChanged.value = false
}

async function save() {
  if (!note.value || !dirty.value || saving.value || !writable.value) return
  saving.value = true
  error.value = null
  try {
    await write(note.value, note.value.etag)
  } catch (e) {
    if (isConcurrencyConflict(e)) conflict.value = true
    else error.value = errorText(e, t, t('pipeline_editor.save_failed'))
  } finally {
    saving.value = false
  }
}

async function overwrite() {
  saving.value = true
  error.value = null
  try {
    const fresh = await getNote(props.path)
    await write(fresh, fresh.etag)
  } catch (e) {
    if (isConcurrencyConflict(e)) conflict.value = true
    else error.value = errorText(e, t, t('pipeline_editor.save_failed'))
  } finally {
    saving.value = false
  }
}

// --- the server's check of the text, as it is typed ---

const problems = ref<string[]>([])
let checkTimer: ReturnType<typeof setTimeout> | null = null
let checkSeq = 0
watch(draft, (src) => {
  if (checkTimer) clearTimeout(checkTimer)
  if (!src) {
    problems.value = []
    return
  }
  checkTimer = setTimeout(async () => {
    const mine = ++checkSeq
    try {
      const r = await checkPipeline(src)
      if (mine === checkSeq) problems.value = r.ok ? [] : [r.problem ?? '']
    } catch {
      if (mine === checkSeq) problems.value = []
    }
  }, 400)
})

// --- runs ---

const runs = ref<RunBrief[]>([])
const runId = ref<string>('')
const run = shallowRef<ReturnType<typeof runStateOf> | null>(null)
const runError = ref<string | null>(null)
const form = ref<'' | 'start' | 'resume'>('')
const formText = ref('')
const selected = computed(() => runs.value.find((r) => r.id === runId.value) ?? null)

async function loadRuns() {
  if (!enabled.value) return
  try {
    runs.value = await listRuns({ pipeline: props.path, limit: 10 })
    if (!runs.value.some((r) => r.id === runId.value)) runId.value = runs.value[0]?.id ?? ''
    await loadRun()
  } catch (e) {
    runError.value = errorText(e, t, t('pipeline_editor.runs'))
  }
}

async function loadRun() {
  if (!runId.value) {
    run.value = null
    return
  }
  try {
    run.value = runStateOf(await getRun(runId.value))
  } catch {
    run.value = null
  }
}
watch(runId, () => void loadRun())

function parseJSON(text: string): { ok: true; value: unknown } | { ok: false } {
  if (!text.trim()) return { ok: true, value: undefined }
  try {
    return { ok: true, value: JSON.parse(text) }
  } catch (e) {
    runError.value = `${t('pipeline_editor.invalid_json')} ${e instanceof Error ? e.message : String(e)}`
    return { ok: false }
  }
}

async function start() {
  runError.value = null
  const v = parseJSON(formText.value)
  if (!v.ok) return
  try {
    const r = await startRun(props.path, v.value as Record<string, unknown> | undefined)
    form.value = ''
    formText.value = ''
    runId.value = r.id
    await loadRuns()
  } catch (e) {
    runError.value = errorText(e, t, t('pipeline_editor.start'))
  }
}

async function resume() {
  if (!selected.value) return
  runError.value = null
  const v = parseJSON(formText.value)
  if (!v.ok) return
  try {
    await resumeRun(selected.value.id, v.value ?? {})
    form.value = ''
    formText.value = ''
    await loadRuns()
  } catch (e) {
    runError.value = errorText(e, t, t('pipeline_editor.resume'))
  }
}

async function cancel() {
  if (!selected.value) return
  runError.value = null
  try {
    await cancelRun(selected.value.id)
    await loadRuns()
  } catch (e) {
    runError.value = errorText(e, t, t('pipeline_editor.cancel'))
  }
}

function openNote() {
  openWindow({
    type: 'note',
    key: planciaKey('note', props.path),
    title: base(props.path),
    props: { path: props.path },
  })
}

// --- live: the note's changes and the runs' ---

const bareEtag = (e?: string) => (e ?? '').replace(/"/g, '')
const sse = useSSE(['note', 'job'])
function onNoteEvent(p: { path?: string; etag?: string; action?: string }) {
  if (!note.value || (p.path !== props.path && p.action !== 'resync')) return
  if (p.etag && bareEtag(p.etag) === bareEtag(note.value.etag)) return // our own save
  if (dirty.value) remoteChanged.value = true
  else void load()
}
let jobTimer: ReturnType<typeof setTimeout> | null = null
function onJobEvent(p: SSEPayload) {
  if (p.pipeline !== props.path || jobTimer) return
  jobTimer = setTimeout(() => {
    jobTimer = null
    void loadRuns()
  }, 300)
}

function onKey(e: KeyboardEvent) {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
    e.preventDefault()
    void save()
  }
}

const unsubscribe: (() => void)[] = []
onMounted(async () => {
  void load()
  unsubscribe.push(sse.on('note', onNoteEvent), sse.on('job', onJobEvent))
  await loadCatalog()
  void loadRuns()
})
onBeforeUnmount(() => {
  unsubscribe.forEach((u) => u())
  if (checkTimer) clearTimeout(checkTimer)
  if (jobTimer) clearTimeout(jobTimer)
})
</script>

<template>
  <div class="flex h-full flex-col gap-2 overflow-auto p-4" data-pipeline-window @keydown="onKey">
    <div class="flex flex-wrap items-center gap-2 text-sm">
      <span class="font-mono text-xs text-text-muted">{{ path }}</span>
      <span class="flex-1" />
      <span v-if="dirty" class="text-xs text-warning" data-unsaved>{{
        t('pipeline_editor.unsaved')
      }}</span>
      <span v-else-if="savedAt" class="text-xs text-text-muted">{{
        t('pipeline_editor.saved_at', { time: savedAt })
      }}</span>
      <button
        type="button"
        class="rounded border border-border px-2 py-1 hover:bg-surface-hover"
        @click="openNote"
      >
        {{ t('pipeline_editor.open_note') }}
      </button>
      <button
        v-if="writable"
        type="button"
        class="rounded bg-accent px-3 py-1 text-accent-fg disabled:opacity-50"
        :disabled="!dirty || saving"
        data-pipeline-save
        @click="save"
      >
        {{ saving ? t('pipeline_editor.saving') : t('pipeline_editor.save') }}
      </button>
    </div>

    <div
      v-if="conflict"
      class="flex flex-wrap items-center gap-2 rounded border border-warning p-2 text-sm"
      role="alert"
      data-pipeline-conflict
    >
      <span class="text-warning">{{ t('pipeline_editor.conflict') }}</span>
      <button type="button" class="rounded border border-border px-2 py-0.5" @click="load">
        {{ t('pipeline_editor.conflict_reload') }}
      </button>
      <button
        type="button"
        class="rounded border border-border px-2 py-0.5"
        data-pipeline-overwrite
        @click="overwrite"
      >
        {{ t('pipeline_editor.conflict_overwrite') }}
      </button>
    </div>
    <div
      v-else-if="remoteChanged"
      class="flex flex-wrap items-center gap-2 rounded border border-border p-2 text-sm"
      role="status"
    >
      <span>{{ t('pipeline_editor.remote_changed') }}</span>
      <button type="button" class="rounded border border-border px-2 py-0.5" @click="load">
        {{ t('pipeline_editor.reload') }}
      </button>
    </div>
    <ErrorMessage v-if="error" :text="error" class="text-sm" />

    <!-- The runs: where the pipelines run. -->
    <div
      v-if="catalog && !enabled"
      class="rounded border border-border p-2 text-xs text-text-muted"
      data-pipelines-off
    >
      {{ t('pipeline_editor.pipelines_off') }}
    </div>
    <div
      v-else-if="enabled && note && !missing"
      class="flex flex-wrap items-center gap-2 text-xs"
      data-pipeline-runs
    >
      <span class="font-semibold">{{ t('pipeline_editor.runs') }}</span>
      <select
        v-if="runs.length"
        v-model="runId"
        class="h-control-sm rounded border border-border bg-bg-elevated px-2 py-0 text-xs"
        :aria-label="t('pipeline_editor.runs')"
        data-run-select
      >
        <option v-for="r in runs" :key="r.id" :value="r.id">
          {{ t(`pipeline_editor.statuses.${r.status}`) }} · {{ formatDateTime(r.created) }}
        </option>
      </select>
      <span v-else class="text-text-muted">{{ t('pipeline_editor.no_runs') }}</span>
      <span class="flex-1" />
      <template v-if="writable">
        <button
          type="button"
          class="rounded border border-border px-2 py-0.5 hover:bg-surface-hover"
          data-run-start
          @click="form = form === 'start' ? '' : 'start'"
        >
          ▶ {{ t('pipeline_editor.start') }}
        </button>
        <button
          v-if="selected?.status === 'waiting'"
          type="button"
          class="rounded border border-border px-2 py-0.5 hover:bg-surface-hover"
          data-run-resume
          @click="form = form === 'resume' ? '' : 'resume'"
        >
          ↻ {{ t('pipeline_editor.resume') }}
        </button>
        <button
          v-if="selected && !runEnded(selected.status)"
          type="button"
          class="rounded px-2 py-0.5 text-danger hover:bg-surface-hover"
          data-run-cancel
          @click="cancel"
        >
          ■ {{ t('pipeline_editor.cancel') }}
        </button>
      </template>
      <p v-if="selected?.error" class="basis-full text-danger">{{ selected.error }}</p>
    </div>
    <div
      v-if="form"
      class="flex flex-col gap-1 rounded border border-border p-2 text-xs"
      data-run-form
    >
      <p v-if="form === 'start' && dirty" class="text-warning">
        {{ t('pipeline_editor.unsaved_start') }}
      </p>
      <label class="flex flex-col gap-1">
        {{ form === 'start' ? t('pipeline_editor.start_input') : t('pipeline_editor.resume_data') }}
        <textarea
          v-model="formText"
          rows="3"
          class="rounded border border-border bg-bg-elevated p-1 font-mono"
          spellcheck="false"
          data-run-input
        />
      </label>
      <div class="flex gap-2">
        <button
          type="button"
          class="rounded bg-accent px-2 py-0.5 text-accent-fg disabled:opacity-50"
          :disabled="form === 'start' && dirty"
          data-run-go
          @click="form === 'start' ? start() : resume()"
        >
          {{ form === 'start' ? t('pipeline_editor.start_now') : t('pipeline_editor.resume_now') }}
        </button>
        <button type="button" class="rounded border border-border px-2 py-0.5" @click="form = ''">
          {{ t('pipeline_editor.cancel_form') }}
        </button>
      </div>
    </div>
    <ErrorMessage v-if="runError" :text="runError" class="text-sm" />

    <p v-if="loading" class="text-sm text-text-muted">{{ t('pipeline_editor.loading') }}</p>
    <p v-else-if="missing" class="text-sm text-text-muted" data-pipeline-missing>
      {{ t('pipeline_editor.not_found') }}
    </p>
    <template v-else-if="note">
      <PipelineEditor
        v-if="writable"
        v-model:source="draft"
        :catalog="catalog?.nodes ?? []"
        :run="run"
        :problems="problems"
        :labels="labels"
        :height="460"
      />
      <template v-else>
        <p class="text-sm text-text-muted">{{ t('pipeline_editor.read_only') }}</p>
        <PipelineView
          :source="draft"
          :catalog="catalog?.nodes ?? []"
          :run="run"
          :labels="labels"
          :height="460"
        />
      </template>
    </template>
  </div>
</template>
