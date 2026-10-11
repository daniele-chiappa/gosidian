<script setup lang="ts">
/**
 * DiagramWindow — a diagram of a note in the editor (IMP-169, F2): the
 * index-th ```diagram block of the note at `path`, as findDiagramBlocks
 * counts them in its text (the server's FindFenced, fences inside fences
 * left out). The window is opened by «Edit» on a drawn diagram, and its
 * address keeps path and index, so a reload opens it again.
 *
 * plancia-diagrams' DiagramEditor edits the YAML; saving puts it back in
 * place of the block and nothing else, with the note's etag. A note that
 * changed meanwhile gets the conflict banner: reload (the draft goes), or
 * put this diagram in the new version of the note, whose other changes
 * stay. A change from elsewhere reloads a clean window and warns a dirty
 * one. Ctrl+S saves.
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
import { search } from '@/api/search'
import { isConcurrencyConflict } from '@/api/client'
import { errorText } from '@/api/errors'
import { useAccessStore } from '@/stores/access'
import { useSSE } from '@/composables/useSSE'
import { planciaKey, base } from '@/composables/planciaKey'
import { useDiagramLabels } from '@/components/domain/diagramLabels'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'

type Lib = typeof import('@/components/domain/diagramView')

const props = defineProps<{ path: string; index: number }>()
const emit = defineEmits<{ title: [string]; dirty: [boolean]; close: [] }>()

const { t } = useI18n()
const access = useAccessStore()
const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))
const labels = useDiagramLabels()

const DiagramEditor = defineAsyncComponent(() =>
  import('@/components/domain/diagramView').then((m) => m.DiagramEditor),
)
const DiagramView = defineAsyncComponent(() =>
  import('@/components/domain/diagramView').then((m) => m.DiagramView),
)

const lib = shallowRef<Lib | null>(null)
const note = ref<Note | null>(null)
// The block as the note has it, and the draft the editor changes.
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

/** The block at index in a note's text, or null. */
function blockOf(content: string) {
  return lib.value?.findDiagramBlocks(content)[props.index] ?? null
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
    emit('title', t('diagram.window_title', { name: base(props.path) }))
  } catch (e) {
    error.value = errorText(e, t, t('note.load_failed'))
  } finally {
    loading.value = false
  }
}

/** Writes the draft in place of the block of `into`, a version of the note. */
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
    else error.value = errorText(e, t, t('diagram.save_failed'))
  } finally {
    saving.value = false
  }
}

/** The draft into the note as it is now: the note's other changes stay. */
async function overwrite() {
  saving.value = true
  error.value = null
  try {
    const fresh = await getNote(props.path)
    await write(fresh, fresh.etag)
  } catch (e) {
    if (isConcurrencyConflict(e)) conflict.value = true
    else error.value = errorText(e, t, t('diagram.save_failed'))
  } finally {
    saving.value = false
  }
}

async function linkSuggest(q: string) {
  const hits = await search({ q, limit: 8 })
  return hits.map((h) => ({
    label: h.title || base(h.path),
    link: `[[${h.path.replace(/\.md$/i, '')}]]`,
  }))
}

function openNote() {
  openWindow({
    type: 'note',
    key: planciaKey('note', props.path),
    title: base(props.path),
    props: { path: props.path },
  })
}

const bareEtag = (e?: string) => (e ?? '').replace(/"/g, '')
const sse = useSSE(['note'])
function onNoteEvent(p: { path?: string; etag?: string; action?: string }) {
  if (!note.value || (p.path !== props.path && p.action !== 'resync')) return
  if (p.etag && bareEtag(p.etag) === bareEtag(note.value.etag)) return // our own save
  if (dirty.value) remoteChanged.value = true
  else void load()
}

function onKey(e: KeyboardEvent) {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
    e.preventDefault()
    void save()
  }
}

let unsubscribe: (() => void) | null = null
onMounted(() => {
  void load()
  unsubscribe = sse.on('note', onNoteEvent)
})
onBeforeUnmount(() => unsubscribe?.())
</script>

<template>
  <div class="flex h-full flex-col gap-2 overflow-auto p-4" data-diagram-window @keydown="onKey">
    <div class="flex flex-wrap items-center gap-2 text-sm">
      <span class="font-mono text-xs text-text-muted">{{ path }}</span>
      <span class="flex-1" />
      <span v-if="dirty" class="text-xs text-warning" data-unsaved>{{ t('diagram.unsaved') }}</span>
      <span v-else-if="savedAt" class="text-xs text-text-muted">{{
        t('diagram.saved_at', { time: savedAt })
      }}</span>
      <button
        type="button"
        class="rounded border border-border px-2 py-1 hover:bg-surface-hover"
        @click="openNote"
      >
        {{ t('diagram.open_note') }}
      </button>
      <button
        v-if="writable"
        type="button"
        class="rounded bg-accent px-3 py-1 text-accent-fg disabled:opacity-50"
        :disabled="!dirty || saving"
        data-diagram-save
        @click="save"
      >
        {{ saving ? t('diagram.saving') : t('diagram.save') }}
      </button>
    </div>

    <div
      v-if="conflict"
      class="flex flex-wrap items-center gap-2 rounded border border-warning p-2 text-sm"
      role="alert"
      data-diagram-conflict
    >
      <span class="text-warning">{{ t('diagram.conflict') }}</span>
      <button type="button" class="rounded border border-border px-2 py-0.5" @click="load">
        {{ t('diagram.conflict_reload') }}
      </button>
      <button
        type="button"
        class="rounded border border-border px-2 py-0.5"
        data-diagram-overwrite
        @click="overwrite"
      >
        {{ t('diagram.conflict_overwrite') }}
      </button>
    </div>
    <div
      v-else-if="remoteChanged"
      class="flex flex-wrap items-center gap-2 rounded border border-border p-2 text-sm"
      role="status"
    >
      <span>{{ t('diagram.remote_changed') }}</span>
      <button type="button" class="rounded border border-border px-2 py-0.5" @click="load">
        {{ t('diagram.reload') }}
      </button>
    </div>

    <ErrorMessage v-if="error" :text="error" class="text-sm" />
    <p v-if="loading" class="text-sm text-text-muted">{{ t('diagram.loading') }}</p>
    <p v-else-if="missing" class="text-sm text-text-muted" data-diagram-missing>
      {{ t('diagram.not_found') }}
    </p>
    <template v-else-if="note">
      <DiagramEditor
        v-if="writable"
        v-model:source="draft"
        :labels="labels.editor.value"
        :link-suggest="linkSuggest"
        :height="460"
      />
      <template v-else>
        <p class="text-sm text-text-muted">{{ t('diagram.read_only') }}</p>
        <DiagramView :source="draft" :labels="labels.view.value" />
      </template>
    </template>
  </div>
</template>
