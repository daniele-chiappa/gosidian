<script setup lang="ts">
/**
 * NoteView — a single note as ONE plancia window with an in-place view/edit
 * toggle. Defaults to read (rendered preview); flipping to Edit mounts the
 * editor in the SAME window (no second window). CodeMirror is lazy so a window
 * that's only ever read never loads the editor chunk; the Edit toggle is hidden
 * for read-only users.
 *
 * Concurrency is window-aware: the editor listens for the api client's
 * `note.concurrency-conflict` event filtered to ITS path and resolves the 412
 * inline (reload remote / overwrite).
 *
 * Emits `title`/`dirty`/`close` to the window frame; History opens a sibling
 * window via the injected `openWindow`.
 */
import {
  computed,
  defineAsyncComponent,
  inject,
  nextTick,
  onBeforeUnmount,
  onMounted,
  ref,
  watch,
} from 'vue'
import { useI18n } from 'vue-i18n'
import { useDebounceFn, useElementSize } from '@vueuse/core'
import {
  Printer,
  Download,
  Copy,
  Check,
  GitBranch,
  Camera,
  SquarePen,
  Columns2,
  Rows2,
  Eye,
} from 'lucide-vue-next'
import OverflowMenu, { type OverflowItem } from '@/components/primitives/OverflowMenu.vue'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'
import { getNote, updateNote, deleteNote, createSnapshot, type Note } from '@/api/notes'
import { downloadNote } from '@/api/noteDownload'
import { draftAfterSave } from '@/views/noteDraft'
import { tooLargeToSend } from '@/views/noteSize'
import { renderPreviewData, type ViewData } from '@/api/preview'
import axios from 'axios'
import { isConcurrencyConflict, onApiEvent, type ConcurrencyConflictDetail } from '@/api/client'
import { errorText } from '@/api/errors'
import { formatSize } from '@/api/format'
import { useSSE } from '@/composables/useSSE'
import MarkdownPreview from '@/components/domain/MarkdownPreview.vue'
import { findHeading } from '@/components/domain/headings'
import PropertiesPanel from '@/components/views/PropertiesPanel.vue'
import RowViews from '@/components/views/RowViews.vue'
import HTMLPreview from '@/components/domain/HTMLPreview.vue'
import MediaPreview from '@/components/domain/MediaPreview.vue'
import TablePreview from '@/components/domain/TablePreview.vue'
import CanvasPreview from '@/components/domain/CanvasPreview.vue'
import { useRecentlyViewed } from '@/composables/useRecentlyViewed'
import { planciaKey, base } from '@/composables/planciaKey'
import { useAccessStore } from '@/stores/access'
import { useTreeStore } from '@/stores/tree'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { confirmAction } from '@/composables/useConfirm'

const CodeMirrorEditor = defineAsyncComponent(
  () => import('@/components/editor/CodeMirrorEditor.vue'),
)

type Mode = 'view' | 'edit'
type EditorLayout = 'editor' | 'split' | 'stacked' | 'preview'

// anchor is the heading a link pointed at (IMP-140), anchorAt when it was
// clicked: the window scrolls to the heading, again on every new click.
const props = defineProps<{ path: string; mode?: Mode; anchor?: string; anchorAt?: number }>()
const emit = defineEmits<{ title: [string]; dirty: [boolean]; close: [] }>()

const { t } = useI18n()
const access = useAccessStore()
const recents = useRecentlyViewed()
const treeStore = useTreeStore()
const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))

const rootEl = ref<HTMLElement | null>(null)
const articleEl = ref<HTMLElement | null>(null)
const note = ref<Note | null>(null)
const draft = ref<string>('')
const previewHTML = ref<string>('')
// The note's views as data, for the editors of ViewTable (IMP-127 phase 5).
const previewViews = ref<ViewData[]>([])

/** Renders md for the preview pane: its HTML and its views as data. */
async function renderInto(md: string, notePath: string) {
  const r = await renderPreviewData(md, notePath)
  previewHTML.value = r.html
  previewViews.value = r.views
  previewError.value = null
}
// A preview that failed: the note stays open, with the reason where the
// preview would be (S6-13).
const previewError = ref<string | null>(null)
function previewFailed(e: unknown) {
  previewHTML.value = ''
  previewViews.value = []
  previewError.value = errorText(e, t, t('note.preview_failed'))
}
const loading = ref(false)
const saving = ref(false)
const error = ref<string | null>(null)
// The note is not there (IMP-155, M4): its own state, with the actions that
// help, instead of an error under a working toolbar.
const notFound = ref(false)
const dirty = ref(false)
const lastSavedAt = ref<string | null>(null)
const conflict = ref<ConcurrencyConflictDetail | null>(null)
// The note changed on the server while it had unsaved edits (IMP-127).
const remoteChanged = ref(false)
const sse = useSSE(['note'])
const copied = ref(false)
let copiedTimer: ReturnType<typeof setTimeout> | null = null

// Read by default; honour an explicit edit intent (legacy /notes/:path/edit
// deep-link) only when the user may write.
const mode = ref<Mode>(
  props.mode === 'edit' && access.canWrite(props.path) && !/\.(base|canvas)$/i.test(props.path)
    ? 'edit'
    : 'view',
)

const path = computed(() => props.path)
// HTML notes (.html) render through the sandboxed iframe (HTMLPreview) instead
// of the markdown → /api/preview → MarkdownPreview pipeline.
const isHtml = computed(() => path.value.toLowerCase().endsWith('.html'))
// Image media notes (ADR-013) are plain .md; the backend tags them with
// kind='image' + a resolved media ref when the media_notes feature is on. We
// render the image + caption instead of the bare markdown.
const isMedia = computed(() => note.value?.kind === 'image')
// CSV table notes (ADR-016): same overlay mechanism, kind='table' + a media
// ref pointing at the .csv attachment; rendered as a paginated table.
const isTable = computed(() => note.value?.kind === 'table')
// An Obsidian base (IMP-118): read-only, its views translated by the server
// into view blocks that render like any other; its YAML comes as source.
const isBase = computed(
  () => note.value?.kind === 'base' || path.value.toLowerCase().endsWith('.base'),
)
// An Obsidian canvas (IMP-144): read-only, its cards drawn where the file
// puts them; its JSON comes as source.
const isCanvas = computed(
  () => note.value?.kind === 'canvas' || path.value.toLowerCase().endsWith('.canvas'),
)
// A base or a canvas: shown, never edited here.
const isReadOnlyFile = computed(() => isBase.value || isCanvas.value)
// A note the server would not take back from the web UI (noteSize.ts):
// its text, read-only, with no preview.
const tooLarge = computed(() => !!note.value && !isReadOnlyFile.value && tooLargeToSend(note.value.content))
const canEdit = computed(() => access.canWrite(props.path) && !isReadOnlyFile.value && !tooLarge.value)
const project = computed(() => {
  const parts = path.value.split('/')
  return parts.length > 1 ? parts[0] : undefined
})

const STORAGE_LAYOUT = 'gosidian.editorMode'
const layout = ref<EditorLayout>(loadLayout())
function loadLayout(): EditorLayout {
  try {
    const v = localStorage.getItem(STORAGE_LAYOUT)
    if (v === 'editor' || v === 'split' || v === 'stacked' || v === 'preview') return v
  } catch {
    /* ignore */
  }
  return 'split'
}
watch(layout, (m) => {
  try {
    localStorage.setItem(STORAGE_LAYOUT, m)
  } catch {
    /* ignore */
  }
})

async function load() {
  if (!path.value) return
  loading.value = true
  error.value = null
  notFound.value = false
  try {
    const fetched = await getNote(path.value)
    remoteChanged.value = false
    note.value = fetched
    draft.value = fetched.content
    recents.record(fetched.path, fetched.title || fetched.path)
    emit('title', fetched.title || fetched.path)
    // HTML notes bypass the markdown renderer; the iframe shows raw content,
    // and a canvas draws its cards, rendered by the server.
    previewError.value = null
    if (isHtml.value || isCanvas.value || tooLarge.value) {
      previewHTML.value = ''
      previewViews.value = []
      if (tooLarge.value) mode.value = 'view'
    } else {
      try {
        await renderInto(fetched.content, fetched.path)
        void scrollToAnchor()
      } catch (e) {
        previewFailed(e)
      }
    }
    dirty.value = false
  } catch (e) {
    notFound.value = axios.isAxiosError(e) && e.response?.status === 404
    error.value = errorText(e, t, t('note.load_failed'))
    note.value = null
  } finally {
    loading.value = false
  }
}

const refreshPreview = useDebounceFn(async () => {
  try {
    await renderInto(draft.value, path.value)
  } catch {
    /* preview failure shouldn't block editing */
  }
}, 300)

watch(draft, () => {
  if (!note.value) return
  dirty.value = draft.value !== note.value.content
  // HTMLPreview binds the draft directly (reactive); only markdown needs the
  // server round-trip to refresh the preview pane.
  if (!isHtml.value && mode.value === 'edit' && layout.value !== 'editor') void refreshPreview()
})
watch(dirty, (d) => emit('dirty', d))

// ```view blocks are computed on the server when the note is rendered
// (IMP-127): render again when other notes change, so the tables stay live.
const refreshViews = useDebounceFn(async () => {
  if (!note.value || isHtml.value) return
  try {
    const src = mode.value === 'edit' ? draft.value : note.value.content
    await renderInto(src, path.value)
  } catch {
    /* keep the last render */
  }
}, 800)

const bareEtag = (e?: string) => (e ?? '').replace(/"/g, '')

function onNoteEvent(p: { path?: string; etag?: string; action?: string }) {
  if (!note.value) return
  if (p.action === 'resync') {
    void checkRemote()
    return
  }
  if (p.path === path.value) {
    if (p.etag && bareEtag(p.etag) === bareEtag(note.value.etag)) return // our own save
    if (dirty.value) {
      remoteChanged.value = true // keep the draft; the banner offers a reload
      return
    }
    void load()
    return
  }
  if (previewHTML.value.includes('gosidian-view') || previewHTML.value.includes('gosidian-count'))
    void refreshViews()
}

// The event stream was down a while (BUG-117, S7-8): the note may have
// changed meanwhile. A clean note reloads, a draft keeps its edits and
// gets the banner; the same etag only refreshes the views.
async function checkRemote() {
  if (!note.value) return
  const at = path.value
  try {
    const fresh = await getNote(at)
    if (!note.value || path.value !== at) return
    if (bareEtag(fresh.etag) === bareEtag(note.value.etag)) {
      if (previewHTML.value.includes('gosidian-view') || previewHTML.value.includes('gosidian-count'))
        void refreshViews()
      return
    }
    if (dirty.value) remoteChanged.value = true
    else void load()
  } catch {
    /* the next event, or a reload, tells */
  }
}

/** Scrolls to the heading the window was opened at, once it is rendered. */
async function scrollToAnchor() {
  const anchor = props.anchor
  if (!anchor) return
  // The preview renders on the next tick, its views a frame or two later.
  for (let i = 0; i < 10; i++) {
    await nextTick()
    const el = articleEl.value ? findHeading(articleEl.value, anchor) : null
    if (el) {
      el.scrollIntoView({ block: 'start' })
      return
    }
    await new Promise((r) => requestAnimationFrame(() => r(null)))
  }
}
watch(
  () => [props.anchor, props.anchorAt],
  () => {
    if (note.value && mode.value === 'view') void scrollToAnchor()
  },
)

function enterEdit() {
  if (!canEdit.value) return
  mode.value = 'edit'
}
async function enterView() {
  mode.value = 'view'
  // View shows the saved content; the draft stays in memory for re-editing.
  if (!note.value || isHtml.value || tooLarge.value) return
  try {
    await renderInto(note.value.content, note.value.path)
  } catch (e) {
    previewFailed(e)
  }
}

async function save() {
  if (!note.value || !dirty.value || saving.value) return
  error.value = null
  // The server would refuse it with a bare "body too large".
  if (tooLargeToSend(draft.value)) {
    error.value = t('note.too_large_to_save')
    return
  }
  saving.value = true
  try {
    const sent = draft.value
    const updated = await updateNote(note.value.path, {
      content: sent,
      ifMatch: note.value.etag,
    })
    note.value = updated
    const after = draftAfterSave(sent, draft.value, updated.content)
    draft.value = after.draft
    dirty.value = after.dirty
    lastSavedAt.value = new Date().toLocaleTimeString()
    remoteChanged.value = false
    // Our own save's event may arrive before this reply: not a remote change.
    remoteChanged.value = false
  } catch (e) {
    // The conflict banner owns a 412: an error pane would hide the draft.
    if (!isConcurrencyConflict(e)) error.value = errorText(e, t, t('note.save_failed'))
  } finally {
    saving.value = false
  }
}

async function reloadRemote() {
  conflict.value = null
  await load()
}
async function forceOverwrite() {
  if (!note.value) return
  saving.value = true
  error.value = null
  try {
    const sent = draft.value
    const updated = await updateNote(note.value.path, { content: sent })
    note.value = updated
    const after = draftAfterSave(sent, draft.value, updated.content)
    draft.value = after.draft
    dirty.value = after.dirty
    lastSavedAt.value = new Date().toLocaleTimeString()
    conflict.value = null
  } catch (e) {
    error.value = errorText(e, t, t('note.overwrite_failed'))
  } finally {
    saving.value = false
  }
}

async function destroy() {
  if (!note.value) return
  // Without the trash the delete is for good, and the question says so
  // (BUG-116, S6-3).
  if (!(await confirmAction(t(access.deleteNoteKey, { path: note.value.path }), { confirmLabel: t('common.delete') }))) return
  try {
    await deleteNote(note.value.path)
    treeStore.refresh()
    emit('close')
  } catch (e) {
    error.value = errorText(e, t, t('note.delete_failed'))
  }
}

// Copy the raw note source the user sees in the editor (markdown or HTML).
// `draft` always holds the current source: the saved content in view mode and
// the live edits in edit mode.
async function copySource() {
  const text = isReadOnlyFile.value ? (note.value?.source ?? '') : draft.value
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    // Fallback for non-secure contexts / clipboard API unavailable.
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    try {
      document.execCommand('copy')
    } catch {
      /* ignore */
    }
    document.body.removeChild(ta)
  }
  copied.value = true
  if (copiedTimer) clearTimeout(copiedTimer)
  copiedTimer = setTimeout(() => {
    copied.value = false
  }, 1500)
}

// Print the rendered markdown note as a single document — the basis for the
// browser's "Save as PDF". A print stylesheet (@media print) shows only this
// note's <article> and hides the rest of the plancia, so ONLY this note reaches
// the page. Markdown only for now: HTML notes live in a sandboxed iframe the
// browser clips to one page and we can't reach to print in full (IMP-053).
function printNote() {
  const el = articleEl.value
  if (!el) return
  el.classList.add('gosidian-print-target')
  document.body.classList.add('gosidian-printing')
  const cleanup = () => {
    el.classList.remove('gosidian-print-target')
    document.body.classList.remove('gosidian-printing')
    window.removeEventListener('afterprint', cleanup)
  }
  window.addEventListener('afterprint', cleanup)
  window.print()
}

// Download the note as a file (shared with the tree's context menu, IMP-150).
async function downloadOriginal() {
  if (!note.value) return
  try {
    await downloadNote(note.value.path, note.value)
  } catch (e) {
    error.value = errorText(e, t, t('note.download_failed'))
  }
}

// A snapshot of the note as it reads now, opened in a window of its own
// (IMP-127 iteration 3): its views, counts and embeds frozen.
const snapshotting = ref(false)
async function snapshot() {
  if (!note.value || snapshotting.value) return
  snapshotting.value = true
  error.value = null
  try {
    const snap = await createSnapshot(note.value.path)
    openWindow({
      type: 'note',
      key: planciaKey('note', snap.path),
      title: base(snap.path),
      props: { path: snap.path },
    })
  } catch (e) {
    error.value = errorText(e, t, t('note.snapshot_failed'))
  } finally {
    snapshotting.value = false
  }
}

// Not found: "create it here" opens the creation window on the note's
// folder, with its name, for whoever may write there.
// A markdown note only: a missing image or base is not one to write here.
const canCreateHere = computed(() => {
  const name = path.value.split('/').pop() ?? ''
  return access.canWrite(path.value) && (/\.md$/i.test(name) || !/\.[^.]+$/.test(name))
})
function createHere() {
  const i = path.value.lastIndexOf('/')
  const folder = i >= 0 ? path.value.slice(0, i) : ''
  const name = (i >= 0 ? path.value.slice(i + 1) : path.value).replace(/\.md$/i, '')
  // Keyed by the note, not the folder: an open creation window of the
  // folder would take the focus with a name of its own.
  openWindow({
    type: 'create',
    key: planciaKey('create', `${folder}/${name}`),
    title: t('note_create.window_title', { name: folder.split('/').pop() || '/' }),
    props: { path: folder, name },
  })
  emit('close')
}

function openHistory() {
  if (!note.value) return
  openWindow({
    type: 'history',
    key: planciaKey('history', note.value.path),
    title: `${t('history.title')} · ${note.value.title || note.value.path}`,
    props: { path: note.value.path },
  })
}

// The window's toolbar (IMP-156, M5): below these widths the secondary
// actions go into the ⋯ menu. Editing adds the layouts, Save and Delete;
// with the layouts as icons it all fits a default window of ~575 px.
const COMPACT_BELOW = { edit: 560, view: 360 }
const headerEl = ref<HTMLElement | null>(null)
const { width: headerWidth } = useElementSize(headerEl)
const compact = computed(() => headerWidth.value > 0 && headerWidth.value < COMPACT_BELOW[mode.value])

const LAYOUTS: { key: EditorLayout; icon: typeof Eye }[] = [
  { key: 'editor', icon: SquarePen },
  { key: 'split', icon: Columns2 },
  { key: 'stacked', icon: Rows2 },
  { key: 'preview', icon: Eye },
]

const actions = computed<OverflowItem[]>(() => {
  const out: OverflowItem[] = []
  if (note.value && mode.value === 'view' && !isHtml.value && !isMedia.value && !isCanvas.value && !tooLarge.value)
    out.push({ key: 'print', label: t('note.print'), icon: Printer, run: printNote })
  out.push({ key: 'download', label: t('note.download'), icon: Download, disabled: !note.value, run: downloadOriginal })
  out.push({
    key: 'copy',
    label: copied.value ? t('note.copied') : t('note.copy'),
    icon: copied.value ? Check : Copy,
    disabled: !note.value,
    run: copySource,
  })
  if (note.value && !isHtml.value && !isMedia.value && !isReadOnlyFile.value && access.canWrite(props.path))
    out.push({ key: 'snapshot', label: t('note.snapshot'), icon: Camera, disabled: snapshotting.value, run: snapshot })
  out.push({ key: 'history', label: t('note.history'), icon: GitBranch, run: openHistory })
  return out
})

function onKeydown(e: KeyboardEvent) {
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
    if (mode.value !== 'edit' || !rootEl.value?.contains(document.activeElement)) return
    e.preventDefault()
    void save()
  }
}

let unsub: (() => void) | null = null
onMounted(() => {
  void load()
  window.addEventListener('keydown', onKeydown)
  unsub = onApiEvent('note.concurrency-conflict', (detail) => {
    if (detail.path === path.value) conflict.value = detail
  })
  sse.on('note', onNoteEvent)
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
  unsub?.()
  if (copiedTimer) clearTimeout(copiedTimer)
})
watch(path, load)
</script>

<template>
  <div ref="rootEl" class="flex flex-col h-full">
    <header
      ref="headerEl"
      class="flex items-center gap-2 border-b border-border bg-bg-elevated px-4 py-2"
    >
      <!-- Only the toolbar: the window's title bar names the note, and its
           content opens with the heading. The save state comes first. -->
      <span
        v-if="dirty"
        class="h-2 w-2 shrink-0 rounded-full bg-warning"
        role="img"
        :title="t('note.unsaved')"
        :aria-label="t('note.unsaved')"
      />
      <span v-else-if="lastSavedAt && !compact" class="shrink-0 text-xs text-success">
        {{ t('note.saved_at', { time: lastSavedAt }) }}
      </span>
      <span class="flex-1" />

      <!-- View / Edit toggle, only where the note can be edited: alone, the
           View button of a canvas, a base or a reader's note chose nothing. -->
      <div
        v-if="note && access.canWrite(props.path) && !isReadOnlyFile"
        class="inline-flex h-control-sm shrink-0 overflow-hidden rounded border border-border text-xs"
      >
        <button
          type="button"
          class="px-2"
          :class="mode === 'view' ? 'bg-accent text-accent-fg' : 'hover:bg-surface-hover'"
          :aria-pressed="mode === 'view'"
          @click="enterView"
        >
          {{ t('note.view') }}
        </button>
        <button
          type="button"
          class="px-2 disabled:cursor-not-allowed disabled:opacity-50"
          :class="mode === 'edit' ? 'bg-accent text-accent-fg' : 'hover:bg-surface-hover'"
          :aria-pressed="mode === 'edit'"
          :disabled="tooLarge"
          :title="tooLarge ? t('note.too_large') : undefined"
          @click="enterEdit"
        >
          {{ t('note.edit') }}
        </button>
      </div>

      <!-- Edit-only controls: the layouts as icons (IMP-156, M5) -->
      <template v-if="mode === 'edit' && note">
        <div
          class="inline-flex h-control-sm shrink-0 overflow-hidden rounded border border-border"
          role="group"
          :aria-label="t('note.layout.label')"
        >
          <button
            v-for="l in LAYOUTS"
            :key="l.key"
            type="button"
            class="px-1.5"
            :class="layout === l.key ? 'bg-accent text-accent-fg' : 'text-text-muted hover:bg-surface-hover hover:text-text'"
            :title="t(`note.layout.${l.key}`)"
            :aria-label="t(`note.layout.${l.key}`)"
            :aria-pressed="layout === l.key"
            :data-layout="l.key"
            @click="layout = l.key"
          >
            <component :is="l.icon" class="h-3.5 w-3.5" />
          </button>
        </div>
        <button
          type="button"
          class="h-control-sm shrink-0 rounded bg-accent px-2 text-xs text-accent-fg hover:bg-accent-hover disabled:opacity-50"
          :disabled="!dirty || saving"
          @click="save"
        >
          {{ saving ? t('note.saving') : t('common.save') }}
        </button>
        <button
          type="button"
          class="h-control-sm shrink-0 rounded px-2 text-xs text-danger hover:bg-surface-hover"
          @click="destroy"
        >
          {{ t('common.delete') }}
        </button>
      </template>

      <!-- The secondary actions: in a row, or in the ⋯ menu when the window
           is narrow (IMP-156, M5). -->
      <template v-if="note && !compact">
        <button
          v-for="a in actions"
          :key="a.key"
          type="button"
          class="shrink-0 rounded p-1 text-text-muted hover:bg-surface-hover hover:text-text disabled:opacity-50"
          :class="{ 'text-success': a.key === 'copy' && copied }"
          :disabled="a.disabled"
          :title="a.label"
          :aria-label="a.label"
          :data-snapshot="a.key === 'snapshot' ? '' : undefined"
          @click="a.run"
        >
          <component :is="a.icon" class="h-3.5 w-3.5" />
        </button>
      </template>
      <OverflowMenu v-else-if="note" class="shrink-0" :items="actions" :label="t('note.more_actions')" />
    </header>

    <!-- Announced to screen readers (S6-14): the save was refused. -->
    <div
      v-if="conflict"
      class="flex flex-wrap items-center gap-2 border-b border-warning/40 bg-warning/10 px-4 py-2 text-xs"
      role="alert"
    >
      <span class="text-warning">{{ t('note.conflict_banner') }}</span>
      <div class="flex-1" />
      <button type="button" class="h-control-sm rounded px-2 hover:bg-surface-hover" @click="reloadRemote">
        {{ t('note.reload_remote') }}
      </button>
      <button
        type="button"
        class="h-control-sm rounded bg-accent px-2 text-accent-fg hover:bg-accent-hover"
        @click="forceOverwrite"
      >
        {{ t('note.overwrite') }}
      </button>
    </div>

    <div
      v-if="remoteChanged && !conflict"
      class="flex flex-wrap items-center gap-2 border-b border-warning/40 bg-warning/10 px-4 py-2 text-xs"
      role="status"
    >
      <span class="text-warning">{{ t('note.changed_elsewhere') }}</span>
      <div class="flex-1" />
      <button type="button" class="h-control-sm rounded px-2 hover:bg-surface-hover" @click="reloadRemote">
        {{ t('note.reload_remote') }}
      </button>
    </div>

    <div
      v-if="tooLarge"
      class="border-b border-warning/40 bg-warning/10 px-4 py-2 text-xs text-warning"
      role="status"
      data-note-too-large
    >
      {{ t('note.too_large') }}
    </div>

    <!-- A save, delete or download that failed: a banner above the editor,
         which keeps the draft and its undo history (BUG-116, S6-7). Only a
         note that did not load takes the pane. -->
    <div
      v-if="error && note"
      class="flex items-center gap-2 border-b border-danger/40 bg-danger/10 px-4 py-2 text-xs"
      data-note-error
    >
      <ErrorMessage :text="error" class="min-w-0" />
      <div class="flex-1" />
      <button type="button" class="h-control-sm rounded px-2 hover:bg-surface-hover" @click="error = null">
        {{ t('common.dismiss') }}
      </button>
    </div>

    <p v-if="loading" class="p-6 text-text-muted">{{ t('common.loading') }}</p>
    <div v-else-if="notFound" class="mx-auto max-w-md p-8 text-sm" data-note-missing>
      <p class="font-medium">{{ t('note.not_found') }}</p>
      <p class="mt-1 font-mono text-xs text-text-muted break-all">{{ path }}</p>
      <p class="mt-3 text-text-muted">{{ t('note.not_found_hint') }}</p>
      <div class="mt-4 flex gap-2">
        <button
          v-if="canCreateHere"
          type="button"
          class="h-control rounded bg-accent px-3 text-accent-fg hover:bg-accent-hover"
          @click="createHere"
        >
          {{ t('note.create_here') }}
        </button>
        <button
          type="button"
          class="h-control rounded border border-border px-3 hover:bg-surface-hover"
          @click="emit('close')"
        >
          {{ t('note.close') }}
        </button>
      </div>
    </div>
    <ErrorMessage v-else-if="error && !note" :text="error" class="p-3 text-sm" />

    <!-- View mode: rendered preview -->
    <div v-else-if="mode === 'view'" class="flex-1 overflow-auto">
      <!-- HTML note: full-bleed sandboxed iframe -->
      <template v-if="isHtml && note">
        <p class="px-4 pt-2 text-xs text-text-muted font-mono">
          <span :title="`etag ${note.etag}`">{{ note.path }} · html · {{ formatSize(note.size) }}</span>
        </p>
        <HTMLPreview :html="note.content" :path="note.path" />
      </template>
      <!-- A media or table note whose caption could not be rendered -->
      <ErrorMessage
        v-if="previewError && (isMedia || isTable)"
        :text="previewError"
        class="px-6 pt-4 text-sm"
        data-preview-error
      />
      <!-- Image media note (ADR-013): image + rendered caption -->
      <MediaPreview
        v-else-if="isMedia && note && note.media"
        :media="note.media"
        :caption-html="previewHTML"
        :note-path="note.path"
      />
      <!-- An Obsidian canvas (IMP-144): its cards on a plane, full-bleed -->
      <div v-else-if="isCanvas && note" class="flex h-full flex-col">
        <p class="px-4 py-1 text-xs text-text-muted font-mono">
          <span :title="`etag ${note.etag}`">{{ note.path }} · {{ formatSize(note.size) }}</span> ·
          {{ t('note.canvas_readonly') }}
        </p>
        <CanvasPreview v-if="note.canvas" class="flex-1" :canvas="note.canvas" :path="note.path" />
      </div>
      <!-- CSV table note (ADR-016): paginated table + rendered caption -->
      <TablePreview
        v-else-if="isTable && note && note.media"
        :media="note.media"
        :caption-html="previewHTML"
        :note-path="note.path"
      />
      <!-- A note too large to preview or save here: its text, read-only -->
      <div v-else-if="tooLarge && note" class="h-full min-h-0" data-note-text>
        <CodeMirrorEditor :model-value="note.content" :project="project" readonly />
      </div>
      <!-- Markdown note: prose-rendered preview -->
      <article v-else ref="articleEl" class="p-6 max-w-3xl mx-auto">
        <p v-if="note" class="text-xs text-text-muted font-mono mb-6">
          <span :title="`etag ${note.etag}`">{{ note.path }} · {{ formatSize(note.size) }}</span>
          <template v-if="isBase"> · {{ t('note.base_readonly') }}</template>
        </p>
        <!-- A row of a database: its fields, editable (IMP-127 phase 5) -->
        <PropertiesPanel v-if="note && !isBase" :path="note.path" :etag="note.etag" />
        <ErrorMessage v-if="previewError" :text="previewError" class="mb-4 text-sm" data-preview-error />
        <MarkdownPreview :html="previewHTML" :views="previewViews" :note-path="note?.path" />
        <!-- A row of a database: the views its schema declares (IMP-139) -->
        <RowViews v-if="note && !isBase" :path="note.path" :etag="note.etag" />
        <!-- An Obsidian base: the YAML its views were translated from -->
        <details v-if="isBase && note?.source" class="mt-8 text-xs" data-base-source>
          <summary class="cursor-pointer text-text-muted">{{ t('note.base_source') }}</summary>
          <pre class="mt-2 overflow-auto rounded bg-surface-hover p-3 font-mono">{{
            note.source
          }}</pre>
        </details>
      </article>
    </div>

    <!-- Edit mode: editor + preview -->
    <div
      v-else-if="note"
      class="flex-1 grid min-h-0"
      :class="{
        'grid-cols-1': layout !== 'split',
        'grid-cols-2': layout === 'split',
        'grid-rows-2': layout === 'stacked',
      }"
    >
      <div
        v-if="layout !== 'preview'"
        class="h-full overflow-hidden"
        :class="{
          'border-r border-border': layout === 'split',
          'border-b border-border': layout === 'stacked',
        }"
      >
        <CodeMirrorEditor v-model="draft" :project="project" placeholder="Markdown…" />
      </div>
      <div v-if="layout !== 'editor'" class="overflow-auto p-4 max-w-none">
        <HTMLPreview v-if="isHtml" :html="draft" :path="note.path" />
        <ErrorMessage v-else-if="previewError" :text="previewError" class="text-sm" data-preview-error />
        <MarkdownPreview
          v-else
          :html="previewHTML"
          :views="previewViews"
          :note-path="note.path"
          :compact="layout !== 'preview'"
        />
      </div>
    </div>
  </div>
</template>
