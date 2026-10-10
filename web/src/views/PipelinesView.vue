<script setup lang="ts">
/**
 * Runs window (IMP-166): the pipelines of the projects the account reads,
 * their runs, and a run's steps. A writer of the project starts a
 * pipeline, resumes a run that waits (wait.call) and cancels one. The
 * window follows the runs live through the `job` event topic.
 */
import { computed, inject, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { ChevronRight, Play, RefreshCw, RotateCcw, Square } from 'lucide-vue-next'
import {
  cancelRun,
  getRun,
  listPipelines,
  listRuns,
  resumeRun,
  runEnded,
  startRun,
  type PipelineInfo,
  type RunBrief,
  type RunStatus,
  type StepView,
} from '@/api/pipelines'
import { errorText } from '@/api/errors'
import { useAccessStore } from '@/stores/access'
import { useSSE } from '@/composables/useSSE'
import { askText } from '@/composables/useConfirm'
import { base, planciaKey } from '@/composables/planciaKey'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'
import DateTime from '@/components/primitives/DateTime.vue'

const { t } = useI18n()
const access = useAccessStore()
const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))
const sse = useSSE()

const pipelines = ref<PipelineInfo[]>([])
const runs = ref<RunBrief[]>([])
const loading = ref(false)
const loaded = ref(false)
const error = ref<string | null>(null)
const message = ref<string | null>(null)
const project = ref('')
const status = ref<RunStatus | ''>('')
/** The run whose steps are open, and its steps. */
const openRun = ref<string | null>(null)
const steps = ref<StepView[]>([])

const STATUSES: RunStatus[] = ['queued', 'running', 'waiting', 'done', 'failed', 'canceled']

const projects = computed(() => [...new Set(pipelines.value.map((p) => p.project))].sort())
const shownPipelines = computed(() =>
  project.value ? pipelines.value.filter((p) => p.project === project.value) : pipelines.value,
)

async function load() {
  loading.value = true
  error.value = null
  try {
    const [ps, rs] = await Promise.all([
      listPipelines(),
      listRuns({ project: project.value, status: status.value, limit: 100 }),
    ])
    pipelines.value = ps
    runs.value = rs
    loaded.value = true
    if (openRun.value) await loadSteps(openRun.value)
  } catch (e) {
    error.value = errorText(e, t, t('runs.load_failed'))
  } finally {
    loading.value = false
  }
}

async function loadSteps(id: string) {
  try {
    steps.value = (await getRun(id)).steps ?? []
  } catch (e) {
    error.value = errorText(e, t, t('runs.load_failed'))
  }
}

async function toggleRun(id: string) {
  if (openRun.value === id) {
    openRun.value = null
    steps.value = []
    return
  }
  openRun.value = id
  steps.value = []
  await loadSteps(id)
}

/** JSON the user typed, or undefined after telling them it is not. */
function parseJSON(text: string): unknown {
  try {
    return JSON.parse(text.trim() || '{}')
  } catch {
    error.value = t('runs.bad_json')
    return undefined
  }
}

async function start(p: PipelineInfo) {
  const text = await askText(t('runs.input_prompt', { name: p.title }), '{}', {
    title: t('runs.start'),
    confirmLabel: t('runs.start'),
  })
  if (text === null) return
  const input = parseJSON(text)
  if (input === undefined) return
  if (typeof input !== 'object' || input === null || Array.isArray(input)) {
    error.value = t('runs.input_object')
    return
  }
  error.value = null
  try {
    const run = await startRun(p.path, input as Record<string, unknown>)
    message.value = t('runs.started', { name: p.title })
    openRun.value = run.id
    await load()
  } catch (e) {
    error.value = errorText(e, t, t('runs.start_failed'))
  }
}

async function resume(r: RunBrief) {
  const text = await askText(t('runs.resume_prompt'), '{}', {
    title: t('runs.resume'),
    confirmLabel: t('runs.resume'),
  })
  if (text === null) return
  const data = parseJSON(text)
  if (data === undefined) return
  error.value = null
  try {
    await resumeRun(r.id, data)
    message.value = t('runs.resumed')
    await load()
  } catch (e) {
    error.value = errorText(e, t, t('runs.resume_failed'))
  }
}

async function cancel(r: RunBrief) {
  error.value = null
  try {
    await cancelRun(r.id)
    message.value = t('runs.canceled')
    await load()
  } catch (e) {
    error.value = errorText(e, t, t('runs.cancel_failed'))
  }
}

function openNote(path: string) {
  openWindow({ type: 'note', key: planciaKey('note', path), title: base(path), props: { path } })
}

const STATUS_CLASS: Record<RunStatus, string> = {
  pending: 'text-text-muted border border-border',
  queued: 'text-info bg-info/15',
  running: 'text-info bg-info/15',
  waiting: 'text-warning bg-warning/15',
  done: 'text-success bg-success/15',
  failed: 'text-danger bg-danger/15',
  skipped: 'text-text-muted border border-border',
  canceled: 'text-text-muted border border-border',
}

/** A step's output as text: JSON indented, a cut output as it came. */
function outputText(s: StepView): string {
  if (s.output === undefined || s.output === null) return ''
  return typeof s.output === 'string' ? s.output : JSON.stringify(s.output, null, 2)
}

// Live: a run that changes reloads the list, once per burst of events.
let timer: ReturnType<typeof setTimeout> | null = null
sse.on('job', () => {
  if (timer) return
  timer = setTimeout(() => {
    timer = null
    void load()
  }, 400)
})
onBeforeUnmount(() => {
  if (timer) clearTimeout(timer)
})

onMounted(() => {
  void access.load()
  void load()
})
</script>

<template>
  <div class="p-8 max-w-5xl mx-auto">
    <!-- The window's title bar names the view, the heading is for screen readers. -->
    <h1 class="sr-only">{{ t('runs.title') }}</h1>
    <p class="text-sm text-text-muted mb-6">{{ t('runs.intro') }}</p>

    <div class="flex items-center gap-2 mb-4 flex-wrap">
      <select
        v-model="project"
        class="h-control-sm py-0 text-xs rounded bg-bg-elevated border border-border px-2 focus:outline-none focus:ring-2 focus:ring-focus"
        :aria-label="t('runs.project')"
        @change="load"
      >
        <option value="">{{ t('runs.all_projects') }}</option>
        <option v-for="p in projects" :key="p" :value="p">{{ p }}</option>
      </select>
      <select
        v-model="status"
        class="h-control-sm py-0 text-xs rounded bg-bg-elevated border border-border px-2 focus:outline-none focus:ring-2 focus:ring-focus"
        :aria-label="t('runs.status')"
        @change="load"
      >
        <option value="">{{ t('runs.all_statuses') }}</option>
        <option v-for="s in STATUSES" :key="s" :value="s">{{ t(`runs.state.${s}`) }}</option>
      </select>
      <button
        type="button"
        class="h-control-sm text-xs px-2 rounded border border-border hover:bg-surface-hover inline-flex items-center gap-1"
        :disabled="loading"
        @click="load"
      ><RefreshCw class="w-3 h-3" /> {{ t('runs.refresh') }}</button>
    </div>

    <p v-if="loading && !loaded" class="text-text-muted">{{ t('common.loading') }}</p>
    <ErrorMessage v-if="error" :text="error" class="mb-3" />
    <p v-else-if="message" class="text-success text-sm mb-3">{{ message }}</p>

    <template v-if="loaded">
      <h2 class="text-sm font-semibold mb-2">{{ t('runs.pipelines') }}</h2>
      <p v-if="!shownPipelines.length" class="text-sm text-text-muted mb-6">{{ t('runs.no_pipelines') }}</p>
      <ul v-else class="space-y-2 mb-8" data-pipelines>
        <li
          v-for="p in shownPipelines"
          :key="p.path"
          class="rounded border border-border bg-surface px-4 py-3 flex items-center gap-2 flex-wrap"
        >
          <button
            type="button"
            class="font-mono text-sm truncate hover:underline text-left"
            :title="p.path"
            @click="openNote(p.path)"
          >{{ p.path }}</button>
          <span v-if="p.trigger" class="text-xs px-1.5 rounded border border-border text-text-muted">{{ p.trigger }}</span>
          <span
            v-if="p.last_run"
            class="text-xs px-1.5 rounded"
            :class="STATUS_CLASS[p.last_run.status]"
            :title="t('runs.last_run')"
          >{{ t(`runs.state.${p.last_run.status}`) }}</span>
          <span class="flex-1" />
          <button
            v-if="access.canWrite(p.path) && !p.problem"
            type="button"
            class="h-control-sm text-xs px-2 rounded bg-accent text-accent-fg hover:bg-accent-hover inline-flex items-center gap-1"
            @click="start(p)"
          ><Play class="w-3 h-3" /> {{ t('runs.start') }}</button>
          <p v-if="p.problem" class="basis-full text-xs text-danger">{{ p.problem }}</p>
          <p v-else-if="p.nodes?.length" class="basis-full text-xs text-text-muted font-mono truncate">
            {{ p.nodes.join(' → ') }}
          </p>
        </li>
      </ul>

      <h2 class="text-sm font-semibold mb-2">{{ t('runs.runs') }}</h2>
      <p v-if="!runs.length" class="text-sm text-text-muted">{{ t('runs.no_runs') }}</p>
      <ul v-else class="space-y-2" data-runs>
        <li v-for="r in runs" :key="r.id" class="rounded border border-border bg-surface">
          <div class="px-4 py-2 flex items-center gap-2 flex-wrap">
            <button
              type="button"
              class="inline-flex items-center gap-1 text-sm min-w-0"
              :aria-expanded="openRun === r.id"
              @click="toggleRun(r.id)"
            >
              <ChevronRight class="w-3.5 h-3.5 shrink-0 transition-transform" :class="openRun === r.id ? 'rotate-90' : ''" />
              <span class="font-mono truncate">{{ base(r.pipeline) }}</span>
            </button>
            <span class="text-xs px-1.5 rounded" :class="STATUS_CLASS[r.status]">{{ t(`runs.state.${r.status}`) }}</span>
            <span class="text-xs text-text-muted truncate">{{ t('runs.by', { trigger: r.trigger, actor: r.actor || '—' }) }}</span>
            <span class="flex-1" />
            <DateTime class="text-xs text-text-muted whitespace-nowrap" :value="r.created" />
            <button
              v-if="r.status === 'waiting' && access.canWrite(r.pipeline)"
              type="button"
              class="h-control-sm text-xs px-2 rounded border border-border hover:bg-surface-hover inline-flex items-center gap-1"
              @click="resume(r)"
            ><RotateCcw class="w-3 h-3" /> {{ t('runs.resume') }}</button>
            <button
              v-if="!runEnded(r.status) && access.canWrite(r.pipeline)"
              type="button"
              class="h-control-sm text-xs px-2 rounded text-danger hover:bg-surface-hover inline-flex items-center gap-1"
              @click="cancel(r)"
            ><Square class="w-3 h-3" /> {{ t('runs.cancel') }}</button>
            <p v-if="r.error" class="basis-full text-xs text-danger break-words">{{ r.error }}</p>
          </div>
          <div v-if="openRun === r.id" class="border-t border-border px-4 py-2 space-y-2" data-steps>
            <p class="text-xs text-text-muted font-mono">{{ r.id }} · {{ r.pipeline }}</p>
            <div v-for="s in steps" :key="s.name" class="text-xs">
              <div class="flex items-center gap-2 flex-wrap">
                <span class="font-mono font-medium">{{ s.name }}</span>
                <span v-if="s.use" class="text-text-muted font-mono">{{ s.use }}</span>
                <span class="px-1.5 rounded" :class="STATUS_CLASS[s.status]">{{ t(`runs.state.${s.status}`) }}</span>
                <span v-if="(s.attempt ?? 0) > 1" class="text-text-muted">{{ t('runs.attempt', { n: s.attempt }) }}</span>
                <span v-if="s.wait_until && s.status === 'waiting'" class="text-text-muted">
                  {{ t('runs.waits_until') }} <DateTime :value="s.wait_until" />
                </span>
              </div>
              <p v-if="s.error" class="text-danger break-words mt-0.5">{{ s.error }}</p>
              <pre
                v-if="outputText(s)"
                class="mt-1 max-h-48 overflow-auto rounded bg-bg-elevated border border-border p-2 font-mono whitespace-pre-wrap break-words"
              >{{ outputText(s) }}</pre>
              <p v-if="s.output_truncated" class="text-text-muted mt-0.5">{{ t('runs.truncated') }}</p>
            </div>
          </div>
        </li>
      </ul>
    </template>
  </div>
</template>
