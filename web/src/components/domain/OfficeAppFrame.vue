<script setup lang="ts">
/**
 * OfficeAppFrame — a team of agents in the office's map (IMP-233): the app
 * of a plugin with the bridge office/1 (docs/plugins.md), framed the way
 * every app is. The window reads the team with the API and hands it to the
 * page when it says ready, when the team's note or a task of its project
 * changes, when a run changes, and every 10 seconds while it is on screen;
 * it opens the notes the page names. The page's changes are a draft the
 * window keeps (phase C); only this bar's Save writes it.
 */
import { computed, inject, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { CircleHelp, FileText, RotateCw, Save } from 'lucide-vue-next'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { editFleetTeam, getFleetTeam } from '@/api/fleet'
import type { PluginApp } from '@/api/layouts'
import { errorText } from '@/api/errors'
import { base, planciaKey } from '@/composables/planciaKey'
import { useSSE, type SSEPayload } from '@/composables/useSSE'
import { createOfficeBridge, type OfficeDraftState } from '@/composables/officeBridge'
import { useUIStore } from '@/stores/ui'

const props = defineProps<{ app: PluginApp; path: string }>()
const emit = defineEmits<{ dirty: [boolean] }>()
const { t, locale } = useI18n()
const store = useWindowsStore()
const ui = useUIStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))
const sse = useSSE(['note', 'job'])

/** Every app is framed the same way: a plugin does not choose these. */
const SANDBOX = 'allow-scripts allow-same-origin allow-downloads allow-popups'
const ALLOW = 'fullscreen; clipboard-read; clipboard-write'
/** How often the window hands the page a fresh team while on screen. */
const EVERY_MS = 10_000
const DARK = new Set(['catppuccin-mocha', 'tokyo-night', 'custom'])

const root = ref<HTMLElement | null>(null)
const frame = ref<HTMLIFrameElement | null>(null)
const frameKey = ref(0)
const project = computed(() => props.path.split('/')[0] ?? '')
const src = computed(() => {
  const q = new URLSearchParams({ host: 'gosidian', origin: window.location.origin, lang: String(locale.value) })
  return `${props.app.url}?${q}`
})

function openNote(path: string) {
  openWindow({ type: 'note', key: planciaKey('note', path), title: base(path), props: { path } })
}

const draft = reactive<OfficeDraftState>({ ops: 0, saving: false, savedAt: 0, error: '' })
const savedTime = computed(() =>
  draft.savedAt
    ? new Date(draft.savedAt).toLocaleTimeString(String(locale.value), { hour: '2-digit', minute: '2-digit' })
    : '',
)

const bridge = createOfficeBridge({
  path: props.path,
  get origin() {
    return props.app.origin
  },
  frame: () => frame.value?.contentWindow ?? null,
  read: getFleetTeam,
  edit: editFleetTeam,
  open: openNote,
  theme: () => (DARK.has(ui.preset) ? 'dark' : 'light'),
  lang: () => String(locale.value),
  errorText: (e) => errorText(e, t),
  onDraft: (s) => Object.assign(draft, s),
})

// The window holds unsaved changes while the draft has some: plancia asks
// before it closes.
watch(
  () => draft.ops > 0,
  (d) => emit('dirty', d),
)

async function save() {
  await bridge.save()
  frame.value?.focus()
}

function onMessage(e: MessageEvent) {
  bridge.handle({ data: e.data, origin: e.origin, source: e.source })
}

// A burst of changes, a run's steps say, makes one read.
let soon: ReturnType<typeof setTimeout> | null = null
function refreshSoon() {
  if (soon) return
  soon = setTimeout(() => {
    soon = null
    void bridge.refresh()
  }, 400)
}

function onNoteEvent(p: SSEPayload) {
  if (p.path === props.path || p.path?.startsWith(`${project.value}/tasks/`)) refreshSoon()
}

function onJobEvent(p: SSEPayload) {
  const pipeline = typeof p.pipeline === 'string' ? p.pipeline : ''
  if (p.project === project.value || pipeline.startsWith(`${project.value}/`)) refreshSoon()
}

/** On screen: the page visible and the window shown, not a tab behind. */
const onScreen = () => document.visibilityState === 'visible' && !!root.value?.getClientRects().length
let timer: ReturnType<typeof setInterval> | null = null

watch([() => ui.preset, locale], () => void bridge.refresh())

async function reload() {
  bridge.forget()
  frameKey.value++
  await nextTick()
  frame.value?.focus()
}

// The references of the project: a page of the app, in a frame that runs
// nothing.
const about = ref(false)
const aboutEl = ref<HTMLElement | null>(null)
const aboutButton = ref<HTMLElement | null>(null)
const aboutSrc = computed(() => new URL('about', props.app.url).href)

async function showAbout() {
  about.value = true
  await nextTick()
  aboutEl.value?.querySelector<HTMLElement>('[data-about-close]')?.focus()
}

function hideAbout() {
  about.value = false
  aboutButton.value?.focus()
}

const unsubscribe: (() => void)[] = []
onMounted(() => {
  window.addEventListener('message', onMessage)
  unsubscribe.push(sse.on('note', onNoteEvent), sse.on('job', onJobEvent))
  timer = setInterval(() => {
    if (onScreen()) void bridge.refresh()
  }, EVERY_MS)
})

onBeforeUnmount(() => {
  window.removeEventListener('message', onMessage)
  unsubscribe.forEach((u) => u())
  if (timer) clearInterval(timer)
  if (soon) clearTimeout(soon)
})
</script>

<template>
  <div
    ref="root"
    class="relative flex h-full flex-col"
    data-plugin-app-window
    data-office-app
  >
    <h1
      class="sr-only"
      tabindex="-1"
      data-window-heading
    >
      {{ t('plugin_app.window_title', { name: base(path) }) }}
    </h1>
    <div
      class="flex items-center gap-2 border-b border-border px-3 py-1 text-xs"
      data-app-bar
    >
      <span class="min-w-0 flex-1 truncate text-text-muted">
        <span class="font-mono">{{ path }}</span> · {{ app.title }}
      </span>
      <span
        v-if="draft.saving"
        class="shrink-0 text-text-muted"
        role="status"
        data-office-saving
      >{{ t('plugin_app.saving') }}</span>
      <span
        v-else-if="draft.ops > 0"
        class="shrink-0 text-warning"
        role="status"
        data-unsaved
      >{{ t('plugin_app.unsaved') }}</span>
      <span
        v-else-if="savedTime"
        class="shrink-0 text-text-muted"
        role="status"
        data-office-saved
      >{{ t('plugin_app.saved_at', { time: savedTime }) }}</span>
      <button
        v-if="draft.ops > 0"
        type="button"
        class="flex shrink-0 items-center gap-1 rounded border border-border px-2 py-0.5 hover:bg-surface-hover disabled:opacity-50"
        :disabled="draft.saving"
        data-office-save
        @click="save"
      >
        <Save class="h-3.5 w-3.5" />
        {{ t('plugin_app.save') }}
      </button>
      <div class="flex shrink-0 items-center gap-1">
        <button
          type="button"
          class="rounded p-1 text-text-muted hover:bg-surface-hover hover:text-text"
          :title="t('graph.open_note')"
          :aria-label="t('graph.open_note')"
          data-office-open-note
          @click="openNote(path)"
        >
          <FileText class="h-3.5 w-3.5" />
        </button>
        <button
          type="button"
          class="rounded p-1 text-text-muted hover:bg-surface-hover hover:text-text"
          :title="t('diagram.reload')"
          :aria-label="t('diagram.reload')"
          data-office-reload
          @click="reload"
        >
          <RotateCw class="h-3.5 w-3.5" />
        </button>
        <button
          ref="aboutButton"
          type="button"
          class="rounded p-1 text-text-muted hover:bg-surface-hover hover:text-text"
          :title="t('plugin_app.references')"
          :aria-label="t('plugin_app.references')"
          data-app-references
          @click="showAbout"
        >
          <CircleHelp class="h-3.5 w-3.5" />
        </button>
      </div>
    </div>
    <iframe
      :key="frameKey"
      ref="frame"
      :src="src"
      :title="t('plugin_app.frame_title', { app: app.title, name: base(path) })"
      :sandbox="SANDBOX"
      :allow="ALLOW"
      class="min-h-0 w-full flex-1 border-0"
      data-app-frame
    />
    <div
      v-if="about"
      ref="aboutEl"
      class="absolute inset-0 z-10 flex flex-col gap-3 bg-surface p-4"
      role="dialog"
      aria-modal="true"
      :aria-label="t('plugin_app.references')"
      data-app-about
      @keydown.esc.prevent="hideAbout"
    >
      <div class="flex items-center gap-2">
        <h2 class="text-sm font-semibold">
          {{ t('plugin_app.references') }}
        </h2>
        <span class="flex-1" />
        <button
          type="button"
          class="rounded border border-border px-2 py-1 text-sm hover:bg-surface-hover"
          data-about-close
          @click="hideAbout"
        >
          {{ t('common.dismiss') }}
        </button>
      </div>
      <iframe
        :src="aboutSrc"
        :title="t('plugin_app.references')"
        sandbox="allow-popups allow-popups-to-escape-sandbox"
        class="min-h-0 w-full flex-1 border-0"
      />
    </div>
  </div>
</template>
