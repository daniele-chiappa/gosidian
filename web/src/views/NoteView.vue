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
import { computed, defineAsyncComponent, inject, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useDebounceFn } from '@vueuse/core'
import { Printer, Download, Copy, Check, GitBranch } from 'lucide-vue-next'
import { getNote, updateNote, deleteNote, type Note } from '@/api/notes'
import { renderPreview } from '@/api/preview'
import { onApiEvent, type ConcurrencyConflictDetail } from '@/api/client'
import MarkdownPreview from '@/components/domain/MarkdownPreview.vue'
import HTMLPreview from '@/components/domain/HTMLPreview.vue'
import MediaPreview from '@/components/domain/MediaPreview.vue'
import TablePreview from '@/components/domain/TablePreview.vue'
import { useRecentlyViewed } from '@/composables/useRecentlyViewed'
import { planciaKey } from '@/composables/planciaKey'
import { useAccessStore } from '@/stores/access'
import { useTreeStore } from '@/stores/tree'
import { useWindowsStore, type OpenSpec } from 'plancia'

const CodeMirrorEditor = defineAsyncComponent(
  () => import('@/components/editor/CodeMirrorEditor.vue'),
)

type Mode = 'view' | 'edit'
type EditorLayout = 'editor' | 'split' | 'stacked' | 'preview'

const props = defineProps<{ path: string; mode?: Mode }>()
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
const loading = ref(false)
const saving = ref(false)
const error = ref<string | null>(null)
const dirty = ref(false)
const lastSavedAt = ref<string | null>(null)
const conflict = ref<ConcurrencyConflictDetail | null>(null)
const copied = ref(false)
let copiedTimer: ReturnType<typeof setTimeout> | null = null

// Read by default; honour an explicit edit intent (legacy /notes/:path/edit
// deep-link) only when the user may write.
const mode = ref<Mode>(props.mode === 'edit' && access.canWrite(props.path) ? 'edit' : 'view')

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
  try {
    const fetched = await getNote(path.value)
    note.value = fetched
    draft.value = fetched.content
    recents.record(fetched.path, fetched.title || fetched.path)
    emit('title', fetched.title || fetched.path)
    // HTML notes bypass the markdown renderer; the iframe shows raw content.
    previewHTML.value = isHtml.value ? '' : await renderPreview(fetched.content)
    dirty.value = false
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load note'
    note.value = null
  } finally {
    loading.value = false
  }
}

const refreshPreview = useDebounceFn(async () => {
  try {
    previewHTML.value = await renderPreview(draft.value)
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

function enterEdit() {
  if (!access.canWrite(props.path)) return
  mode.value = 'edit'
}
async function enterView() {
  mode.value = 'view'
  // View shows the saved content; the draft stays in memory for re-editing.
  if (note.value && !isHtml.value) previewHTML.value = await renderPreview(note.value.content)
}

async function save() {
  if (!note.value || !dirty.value || saving.value) return
  saving.value = true
  error.value = null
  try {
    const updated = await updateNote(note.value.path, {
      content: draft.value,
      ifMatch: note.value.etag,
    })
    note.value = updated
    draft.value = updated.content
    dirty.value = false
    lastSavedAt.value = new Date().toLocaleTimeString()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Save failed'
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
    const updated = await updateNote(note.value.path, { content: draft.value })
    note.value = updated
    draft.value = updated.content
    dirty.value = false
    lastSavedAt.value = new Date().toLocaleTimeString()
    conflict.value = null
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Overwrite failed'
  } finally {
    saving.value = false
  }
}

async function destroy() {
  if (!note.value) return
  if (!confirm(`Delete ${note.value.path}? This moves it to the trash.`)) return
  try {
    await deleteNote(note.value.path)
    treeStore.invalidateAll()
    emit('close')
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Delete failed'
  }
}

// Copy the raw note source the user sees in the editor (markdown or HTML).
// `draft` always holds the current source: the saved content in view mode and
// the live edits in edit mode.
async function copySource() {
  const text = draft.value
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

// Download the note's original source file (the raw .md / .html as stored in
// the vault) as-is. Purely client-side: the saved content is already in
// memory, so we wrap it in a Blob and synthesise an <a download>.
async function downloadOriginal() {
  if (!note.value) return
  const filename = note.value.path.split('/').pop() || note.value.title || 'note'
  const mime = filename.toLowerCase().endsWith('.html') ? 'text/html' : 'text/markdown'
  // Download a SELF-CONTAINED copy: image references inlined as data: URIs
  // (server ?inline). The stored note keeps the lightweight reference for MCP
  // reads/editing. Falls back to the in-memory content if the fetch fails.
  let content = note.value.content
  try {
    content = (await getNote(note.value.path, { inline: true })).content
  } catch {
    /* keep the raw content already loaded */
  }
  const blob = new Blob([content], { type: `${mime};charset=utf-8` })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

function openHistory() {
  if (!note.value) return
  openWindow({
    type: 'history',
    key: planciaKey('history', note.value.path),
    title: `⏱ ${note.value.title || note.value.path}`,
    props: { path: note.value.path },
  })
}

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
    <header class="flex items-center gap-2 px-4 py-2 border-b border-border bg-bg-elevated">
      <span class="font-semibold truncate">{{ note?.title || note?.path || path }}</span>
      <span v-if="dirty" class="text-xs text-warning" :title="t('note.unsaved')">●</span>
      <span v-else-if="lastSavedAt" class="text-xs text-success">saved {{ lastSavedAt }}</span>

      <div class="flex-1" />

      <!-- View / Edit toggle (Edit hidden for read-only users) -->
      <div class="inline-flex rounded border border-border overflow-hidden text-xs">
        <button
          type="button"
          class="px-2 py-1"
          :class="mode === 'view' ? 'bg-accent text-accent-fg' : 'hover:bg-surface-hover'"
          @click="enterView"
        >View</button>
        <button
          v-if="access.canWrite(props.path)"
          type="button"
          class="px-2 py-1"
          :class="mode === 'edit' ? 'bg-accent text-accent-fg' : 'hover:bg-surface-hover'"
          @click="enterEdit"
        >Edit</button>
      </div>

      <!-- Edit-only controls -->
      <template v-if="mode === 'edit'">
        <div class="inline-flex rounded border border-border overflow-hidden text-xs">
          <button
            v-for="m in (['editor', 'split', 'stacked', 'preview'] as EditorLayout[])"
            :key="m"
            type="button"
            class="px-2 py-1"
            :class="layout === m ? 'bg-accent text-accent-fg' : 'hover:bg-surface-hover'"
            @click="layout = m"
          >{{ m }}</button>
        </div>
        <button
          type="button"
          class="text-xs px-2 py-1 rounded bg-accent text-accent-fg hover:bg-accent-hover disabled:opacity-50"
          :disabled="!dirty || saving"
          @click="save"
        >{{ saving ? 'Saving…' : 'Save' }}</button>
        <button
          type="button"
          class="text-xs px-2 py-1 rounded text-danger hover:bg-surface-hover"
          @click="destroy"
        >Delete</button>
      </template>

      <button
        v-if="note && mode === 'view' && !isHtml && !isMedia"
        type="button"
        class="rounded p-1 text-text-muted hover:bg-surface-hover hover:text-text"
        :title="t('note.print')"
        :aria-label="t('note.print')"
        @click="printNote"
      >
        <Printer class="h-3.5 w-3.5" />
      </button>

      <button
        type="button"
        class="rounded p-1 text-text-muted hover:bg-surface-hover hover:text-text disabled:opacity-50"
        :disabled="!note"
        :title="t('note.download')"
        :aria-label="t('note.download')"
        @click="downloadOriginal"
      >
        <Download class="h-3.5 w-3.5" />
      </button>

      <button
        type="button"
        class="rounded p-1 text-text-muted hover:bg-surface-hover hover:text-text disabled:opacity-50"
        :class="{ 'text-success': copied }"
        :disabled="!note"
        :title="copied ? t('note.copied') : t('note.copy')"
        :aria-label="copied ? t('note.copied') : t('note.copy')"
        @click="copySource"
      >
        <Check
          v-if="copied"
          class="h-3.5 w-3.5"
        />
        <Copy
          v-else
          class="h-3.5 w-3.5"
        />
      </button>

      <button
        type="button"
        class="rounded p-1 text-text-muted hover:bg-surface-hover hover:text-text"
        :title="t('note.history')"
        :aria-label="t('note.history')"
        @click="openHistory"
      >
        <GitBranch class="h-3.5 w-3.5" />
      </button>
    </header>

    <div
      v-if="conflict"
      class="flex flex-wrap items-center gap-2 border-b border-warning/40 bg-warning/10 px-4 py-2 text-xs"
    >
      <span class="text-warning">Modified externally since you opened it.</span>
      <div class="flex-1" />
      <button type="button" class="rounded px-2 py-1 hover:bg-surface-hover" @click="reloadRemote">
        Reload remote
      </button>
      <button
        type="button"
        class="rounded bg-accent px-2 py-1 text-accent-fg hover:bg-accent-hover"
        @click="forceOverwrite"
      >
        Overwrite
      </button>
    </div>

    <p v-if="loading" class="p-6 text-text-muted">Loading…</p>
    <p v-else-if="error" class="p-3 text-danger text-sm">{{ error }}</p>

    <!-- View mode: rendered preview -->
    <div v-else-if="mode === 'view'" class="flex-1 overflow-auto">
      <!-- HTML note: full-bleed sandboxed iframe -->
      <template v-if="isHtml && note">
        <p class="px-4 pt-2 text-xs text-text-muted font-mono">
          {{ note.path }} · html · etag {{ note.etag.slice(0, 12) }} · {{ note.size }} bytes
        </p>
        <HTMLPreview :html="note.content" />
      </template>
      <!-- Image media note (ADR-013): image + rendered caption -->
      <MediaPreview
        v-else-if="isMedia && note && note.media"
        :media="note.media"
        :caption-html="previewHTML"
        :note-path="note.path"
      />
      <!-- CSV table note (ADR-016): paginated table + rendered caption -->
      <TablePreview
        v-else-if="isTable && note && note.media"
        :media="note.media"
        :caption-html="previewHTML"
        :note-path="note.path"
      />
      <!-- Markdown note: prose-rendered preview -->
      <article v-else ref="articleEl" class="p-6 max-w-3xl mx-auto">
        <p v-if="note" class="text-xs text-text-muted font-mono mb-6">
          {{ note.path }} · etag {{ note.etag.slice(0, 12) }} · {{ note.size }} bytes
        </p>
        <MarkdownPreview :html="previewHTML" />
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
        <HTMLPreview v-if="isHtml" :html="draft" />
        <MarkdownPreview v-else :html="previewHTML" />
      </div>
    </div>
  </div>
</template>
