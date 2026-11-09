<script setup lang="ts">
/**
 * PluginAppWindow — a note in the app a plugin offers for its type
 * (IMP-226, docs/plugins.md#apps): the page-layout editor, which lays out
 * any note, say. The window finds the app among the accepted
 * manifests' (GET /api/v1/plugins/apps), frames its page with the sandbox
 * every app gets, and talks to it with the bridge of its contract: the
 * editor asks, the window opens and saves the note with the session of
 * whoever is logged in. The editor has no bar of its own there: the
 * window's has its buttons, which reach it over the same bridge.
 */
import { computed, inject, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { CircleHelp, Download, ImagePlus, Save } from 'lucide-vue-next'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { getNote } from '@/api/notes'
import type { NoteTag, ValidVersion } from '@/api/history'
import {
  addPiece,
  editPiece,
  exportLayout,
  listImages,
  listPieces,
  listPluginApps,
  numberPieces,
  openLayout,
  readVaultFile,
  reorderPieces,
  saveLayout,
  vaultFileHref,
  type AttachmentInfo,
  type Piece,
  type PluginApp,
} from '@/api/layouts'
import { fontFiles } from '@/api/fonts'
import { attachFile } from '@/api/attach'
import { errorText } from '@/api/errors'
import { base, planciaKey } from '@/composables/planciaKey'
import { useSSE } from '@/composables/useSSE'
import {
  createLayoutBridge,
  noteTypeOf,
  DRAFT_PDF,
  LAYOUT_BRIDGE,
  type BroughtFrom,
  type LayoutBridgeState,
} from '@/composables/layoutBridge'
import { useAccessStore } from '@/stores/access'
import NoteVersionButton from '@/components/domain/NoteVersionButton.vue'
import PieceEditor from '@/components/domain/PieceEditor.vue'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'
import OverflowMenu, { type OverflowItem } from '@/components/primitives/OverflowMenu.vue'

// version is a tag of the note: the design as that version holds it, to read
// and export (IMP-201). Nothing it does changes the note.
const props = defineProps<{ path: string; version?: string }>()
const emit = defineEmits<{ title: [string]; dirty: [boolean]; close: [] }>()
const { t, locale } = useI18n()
const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))
const sse = useSSE(['note'])

/** Every app is framed the same way: a plugin does not choose these. */
const SANDBOX = 'allow-scripts allow-same-origin allow-downloads allow-popups'
const ALLOW = 'fullscreen; clipboard-read; clipboard-write'
/** The contracts this window speaks. */
const BRIDGES = new Set([LAYOUT_BRIDGE])

const app = ref<PluginApp | null>(null)
const problem = ref('')
const frame = ref<HTMLIFrameElement | null>(null)
// Bumped to reload: a new frame, a new editor, the note as it is now.
const frameKey = ref(0)
const state = reactive<LayoutBridgeState>({
  ready: false,
  engine: '',
  dirty: false,
  saving: false,
  savedAt: 0,
  error: '',
  missing: [],
  exported: '',
  exportedLabel: '',
  edited: [],
})
const project = computed(() => props.path.split('/')[0] ?? '')
const versionLabel = computed(() => props.version?.split('/').pop() ?? '')
// The version readers see (IMP-228): the design here may be the draft of it.
const access = useAccessStore()
const valid = ref<ValidVersion | null>(null)
const readersSee = computed(() =>
  !props.version && valid.value && !valid.value.same && !valid.value.missing ? valid.value.label : '',
)
const refuse = () => Promise.reject(new Error(t('plugin_app.read_only_refused')))

const src = computed(() => {
  if (!app.value) return ''
  const q = new URLSearchParams({
    host: 'gosidian',
    origin: window.location.origin,
    lang: String(locale.value),
  })
  return `${app.value.url}?${q}`
})

const errorMessage = computed(() => {
  switch (state.error) {
    case '':
    case 'conflict':
      return ''
    case 'too_large':
      return t('plugin_app.too_large')
    case 'order_changed':
      return t('plugin_app.order_changed')
    case 'template_unreadable':
      return t('plugin_app.template_unreadable')
    default:
      return t('plugin_app.failed', { error: state.error })
  }
})
const savedTime = computed(() =>
  state.savedAt
    ? new Date(state.savedAt).toLocaleTimeString(String(locale.value), {
        hour: '2-digit',
        minute: '2-digit',
      })
    : '',
)

// The image picker: the bridge waits on it.
const picking = ref(false)
const images = ref<AttachmentInfo[]>([])
const pickError = ref('')
const pickerEl = ref<HTMLElement | null>(null)
let resolvePick: ((v: { name: string; bytes: ArrayBuffer } | null) => void) | null = null
let returnFocus: HTMLElement | null = null
// Who asks for the path alone needs no bytes read.
let pickBytes = true

/** The path of an image chosen, for the text of a piece. */
async function pickPath(): Promise<string | null> {
  return (await pick(false))?.name ?? null
}

async function pick(bytes = true): Promise<{ name: string; bytes: ArrayBuffer } | null> {
  pickBytes = bytes
  pickError.value = ''
  images.value = []
  returnFocus = document.activeElement as HTMLElement | null
  picking.value = true
  try {
    images.value = await listImages(project.value)
  } catch (e) {
    pickError.value = errorText(e, t)
  }
  await nextTick()
  const first =
    pickerEl.value?.querySelector<HTMLElement>('[data-pick-image]') ??
    pickerEl.value?.querySelector<HTMLElement>('[data-pick-cancel]')
  first?.focus()
  return new Promise((resolve) => {
    resolvePick = resolve
  })
}

async function choose(a: AttachmentInfo | null) {
  let chosen: { name: string; bytes: ArrayBuffer } | null = null
  if (a) {
    try {
      chosen = { name: a.path, bytes: pickBytes ? await readVaultFile(a.path) : new ArrayBuffer(0) }
    } catch (e) {
      pickError.value = errorText(e, t)
      return
    }
  }
  picking.value = false
  resolvePick?.(chosen)
  resolvePick = null
  returnFocus?.focus()
}

// A file from this computer, among the project's attachments and chosen.
async function upload(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  pickError.value = ''
  try {
    const { path } = await attachFile(file, project.value)
    await choose({ path, size: file.size, modified: '', image: true })
  } catch (err) {
    pickError.value = errorText(err, t)
  }
}

// The piece open to edit, over the design, and what a frame sent with it.
const editing = ref<Piece | null>(null)
const brought = ref<BroughtFrom | null>(null)
const writeEditing = (piece: Piece, text: string) =>
  brought.value ? bridge.writeBrought(piece, text, brought.value) : bridge.writePiece(piece, text)
const putBackEditing = computed(() =>
  brought.value?.design !== undefined && editing.value
    ? async () => {
        if (editing.value) bridge.putBack(editing.value)
      }
    : undefined,
)
function closeEditing() {
  editing.value = null
  brought.value = null
}
// The pieces changed in the design and not in the note, said once the design
// is saved (IMP-230), until none is left or the notice is closed.
const editedShown = ref(false)
watch(
  () => state.savedAt,
  (at) => {
    if (at && state.edited.length && !props.version) editedShown.value = true
  },
)
watch(
  () => state.edited.length,
  (n) => {
    if (!n) editedShown.value = false
  },
)

/** The note at the line of a piece. A window already open keeps its props
 *  through open, so it hears them from identify. */
function openPiece(line: number) {
  closeEditing()
  const key = planciaKey('note', props.path)
  const at = { path: props.path, mode: 'edit', line, lineAt: Date.now() }
  const id = openWindow({ type: 'note', key, title: base(props.path), props: at })
  store.identify(id, key, at)
}

const bridge = createLayoutBridge({
  path: props.path,
  get origin() {
    return app.value?.origin ?? ''
  },
  frame: () => frame.value?.contentWindow ?? null,
  open: (path) => (props.version ? openLayout(path, props.version) : openLayout(path)),
  save: (path, bytes, etag) => (props.version ? refuse() : saveLayout(path, bytes, etag)),
  // A version's PDF writes the note as it is, beside the draft's (IMP-229).
  exportFile: (path, name, bytes) => exportLayout(path, name, bytes, props.version),
  pick: () => pick(),
  pieces: (path) => (props.version ? listPieces(path, props.version) : listPieces(path)),
  number: (path, n) => (props.version ? refuse() : numberPieces(path, '', n)),
  add: (path, piece) => (props.version ? refuse() : addPiece(path, piece)),
  attach: async (file) => (await attachFile(file, project.value)).path,
  edit: (path, n, hash, text) => (props.version ? refuse() : editPiece(path, n, hash, text)),
  reorder: (path, order) => (props.version ? refuse() : reorderPieces(path, order)),
  showPiece: (piece, from) => {
    editing.value = piece
    brought.value = from ?? null
  },
  readImage: readVaultFile,
  fonts: fontFiles,
  onState: (s) => Object.assign(state, s),
})

async function readValid() {
  valid.value = (await getNote(props.path).catch(() => null))?.valid ?? null
}

// The text changes while the design is open, here or in the note's window.
function onNoteEvent(p: { path?: string }) {
  if (p.path !== props.path) return
  void readValid()
  if (state.ready) void bridge.refreshPieces()
}

const designTitle = () => t('plugin_app.window_title', { name: base(props.path) })

/** A version of the design, to read, in a window of its own. */
function openVersion(tag: NoteTag) {
  openWindow({
    type: 'app',
    key: planciaKey('app', `${props.path}@${tag.tag}`),
    title: designTitle(),
    props: { path: props.path, version: tag.tag },
  })
}

function openHistory() {
  openWindow({
    type: 'history',
    key: planciaKey('history', props.path),
    title: `${t('history.title')} · ${base(props.path)}`,
    props: { path: props.path },
  })
}

// From a version, the design to work on, in its place. This window closes
// first: with one window on screen, plancia closes the focused one.
function editLatest() {
  emit('close')
  openWindow({
    type: 'app',
    key: planciaKey('app', props.path),
    title: designTitle(),
    props: { path: props.path },
  })
}

function onMessage(e: MessageEvent) {
  void bridge.handle({ data: e.data, origin: e.origin, source: e.source })
}

// What the bar asks goes on in the editor, a dialog of its own among it:
// so does the focus.
function inEditor(ask: () => void) {
  ask()
  frame.value?.focus()
}
const placeImage = () => inEditor(() => bridge.run('app.placeDialog'))

// Every export is a download; «PDF in the vault» keeps the PDF beside the
// note, the version's when the window shows one (IMP-229).
const exportItems = computed<OverflowItem[]>(() => {
  const items: OverflowItem[] = (
    [
      ['app.exportPdf', 'PDF'],
      ['app.exportPng', t('plugin_app.export_png')],
      ['app.exportIdml', 'IDML'],
      ['app.exportEpub', 'EPUB'],
      ['app.exportHtml', 'HTML'],
      ['app.exportPptx', 'PowerPoint'],
    ] as const
  ).map(([id, label]) => ({ key: id, label, run: () => inEditor(() => bridge.run(id)) }))
  if (access.canWrite(props.path))
    items.splice(1, 0, {
      key: 'app.exportPdf.vault',
      label: t('plugin_app.export_pdf_vault'),
      run: () => inEditor(() => bridge.run('app.exportPdf', { toHost: true })),
    })
  return items
})
const exportedName = computed(() =>
  state.exportedLabel === DRAFT_PDF ? t('plugin_app.pdf_draft_name') : state.exportedLabel,
)

// The references of the project: a page of the app, beside its own, in a
// frame that runs nothing.
const about = ref(false)
const aboutEl = ref<HTMLElement | null>(null)
const aboutButton = ref<HTMLElement | null>(null)
const aboutSrc = computed(() => (app.value ? new URL('about', app.value.url).href : ''))

async function showAbout() {
  about.value = true
  await nextTick()
  aboutEl.value?.querySelector<HTMLElement>('[data-about-close]')?.focus()
}

function hideAbout() {
  about.value = false
  aboutButton.value?.focus()
}

// The buttons go with the conflict: the focus goes back to the editor.
function overwrite() {
  bridge.overwrite()
  frame.value?.focus()
}

async function reload() {
  bridge.forget()
  frameKey.value++
  await nextTick()
  frame.value?.focus()
}

// A version has nothing to save: closing it loses nothing.
watch(
  () => state.dirty,
  (d) => emit('dirty', d && !props.version),
)

onMounted(async () => {
  emit('title', t('plugin_app.window_title', { name: base(props.path) }))
  window.addEventListener('message', onMessage)
  sse.on('note', onNoteEvent)
  try {
    const [apps, note] = await Promise.all([listPluginApps(), getNote(props.path)])
    const type = noteTypeOf(note.content)
    valid.value = note.valid ?? null
    // Any markdown note can have a design: the app of the layout bridge
    // lays out those no app claims.
    const found = apps.find((a) => a.note_type === type) ?? apps.find((a) => BRIDGES.has(a.bridge))
    if (!found) problem.value = t('plugin_app.no_app', { type: type || '—' })
    else if (!BRIDGES.has(found.bridge))
      problem.value = t('plugin_app.unknown_bridge', { bridge: found.bridge })
    else if (found.status !== 'ok')
      problem.value = t('plugin_app.not_ok', { plugin: found.plugin, status: found.status })
    else app.value = found
  } catch (e) {
    problem.value = errorText(e, t)
  }
})

onBeforeUnmount(() => {
  window.removeEventListener('message', onMessage)
  resolvePick?.(null)
})
</script>

<template>
  <div
    class="relative flex h-full flex-col"
    data-plugin-app-window
  >
    <h1
      class="sr-only"
      tabindex="-1"
      data-window-heading
    >
      {{ t('plugin_app.window_title', { name: base(path) }) }}
    </h1>
    <!-- One line: the buttons stay at the right end, so Export's menu opens
         inside the window, and the path gives way to them. -->
    <div
      class="flex items-center gap-2 border-b border-border px-3 py-1 text-xs"
      data-app-bar
    >
      <span class="min-w-0 flex-1 truncate text-text-muted">
        <span class="font-mono">{{ path }}</span>
        <template v-if="app"> · {{ app.title }}<template v-if="state.engine"> ({{ state.engine }})</template></template>
      </span>
      <span
        v-if="readersSee"
        class="shrink-0 text-text-muted"
        data-readers-see
      >{{
        t('plugin_app.readers_see', { label: readersSee })
      }}</span>
      <span
        v-if="version"
        class="shrink-0 text-text-muted"
        data-read-only
      >{{
        t('plugin_app.read_only', { label: versionLabel })
      }}</span>
      <span
        v-else-if="state.saving"
        class="shrink-0 text-text-muted"
        data-saving
      >{{
        t('plugin_app.saving')
      }}</span>
      <span
        v-else-if="state.dirty"
        class="shrink-0 text-warning"
        data-unsaved
      >{{
        t('plugin_app.unsaved')
      }}</span>
      <span
        v-else-if="savedTime"
        class="shrink-0 text-text-muted"
        data-saved
      >{{
        t('plugin_app.saved_at', { time: savedTime })
      }}</span>
      <div
        v-if="state.ready"
        class="flex shrink-0 items-center gap-1"
        data-app-actions
      >
        <button
          v-if="version && access.canWrite(path)"
          type="button"
          class="rounded border border-border px-2 py-0.5 hover:bg-surface-hover"
          data-app-edit-latest
          @click="editLatest"
        >
          {{ t('plugin_app.edit_latest') }}
        </button>
        <NoteVersionButton
          :path="path"
          :valid="valid"
          :dirty="state.dirty && !version"
          :save="bridge.saveNow"
          @changed="readValid"
          @open="openVersion"
          @history="openHistory"
        />
        <button
          v-if="!version"
          type="button"
          class="rounded p-1 text-text-muted hover:bg-surface-hover hover:text-text disabled:opacity-50"
          :title="t('plugin_app.save')"
          :aria-label="t('plugin_app.save')"
          :disabled="state.saving"
          data-app-save
          @click="inEditor(bridge.requestSave)"
        >
          <Save class="h-3.5 w-3.5" />
        </button>
        <button
          v-if="!version"
          type="button"
          class="rounded p-1 text-text-muted hover:bg-surface-hover hover:text-text"
          :title="t('plugin_app.place_image')"
          :aria-label="t('plugin_app.place_image')"
          data-app-place
          @click="placeImage"
        >
          <ImagePlus class="h-3.5 w-3.5" />
        </button>
        <OverflowMenu
          :items="exportItems"
          :label="t('plugin_app.export')"
          data-app-export
        >
          <Download class="h-3.5 w-3.5" />
        </OverflowMenu>
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
    <ErrorMessage
      v-if="problem"
      :text="problem"
      class="m-4 text-sm"
      data-app-problem
    />
    <ErrorMessage
      v-if="errorMessage"
      :text="errorMessage"
      class="mx-3 mt-2 text-sm"
      data-app-error
    />
    <div
      v-if="state.error === 'conflict'"
      class="mx-3 mt-2 flex flex-wrap items-center gap-2 rounded border border-warning p-2 text-sm"
      role="alert"
      data-app-conflict
    >
      <span class="text-warning">{{ t('plugin_app.conflict') }}</span>
      <button
        type="button"
        class="rounded border border-border px-2 py-0.5 hover:bg-surface-hover"
        data-app-reload
        @click="reload"
      >
        {{ t('plugin_app.conflict_reload') }}
      </button>
      <button
        type="button"
        class="rounded border border-border px-2 py-0.5 hover:bg-surface-hover"
        data-app-overwrite
        @click="overwrite"
      >
        {{ t('plugin_app.conflict_overwrite') }}
      </button>
    </div>
    <p
      v-if="state.missing.length"
      class="mx-3 mt-2 text-sm text-warning"
      role="status"
      data-app-missing
    >
      {{ t('plugin_app.missing', { count: state.missing.length }) }}
    </p>
    <div
      v-if="editedShown && state.edited.length"
      class="mx-3 mt-2 flex flex-col gap-1 rounded border border-warning p-2 text-sm"
      role="status"
      data-app-edited
    >
      <span class="text-warning">{{ t('plugin_app.edited_notice', { count: state.edited.length }, state.edited.length) }}</span>
      <ul class="ml-4 list-disc text-text-muted">
        <li
          v-for="p in state.edited.slice(0, 5)"
          :key="p.n"
        >
          {{ p.n }} — {{ p.preview }}
        </li>
        <li v-if="state.edited.length > 5">
          {{ t('plugin_app.edited_more', { count: state.edited.length - 5 }) }}
        </li>
      </ul>
      <div class="flex flex-wrap gap-2">
        <button
          type="button"
          class="rounded border border-border px-2 py-0.5 hover:bg-surface-hover"
          data-app-bring-all
          @click="bridge.bringAll()"
        >
          {{ t('plugin_app.edited_bring_all') }}
        </button>
        <button
          type="button"
          class="rounded border border-border px-2 py-0.5 hover:bg-surface-hover"
          data-app-edited-close
          @click="editedShown = false"
        >
          {{ t('common.dismiss') }}
        </button>
      </div>
    </div>
    <p
      v-if="state.exported"
      class="mx-3 mt-2 text-sm text-text-muted"
      role="status"
      data-app-exported
    >
      {{ t('plugin_app.exported', { label: exportedName }) }}
      <a
        :href="vaultFileHref(state.exported)"
        target="_blank"
        rel="noopener"
        class="underline"
      >{{ t('plugin_app.exported_open') }}</a>
    </p>
    <iframe
      v-if="app"
      :key="frameKey"
      ref="frame"
      :src="src"
      :title="t('plugin_app.frame_title', { app: app.title, name: base(path) })"
      :sandbox="SANDBOX"
      :allow="ALLOW"
      class="min-h-0 w-full flex-1 border-0"
      data-app-frame
    />
    <p
      v-else-if="!problem"
      class="m-4 text-sm text-text-muted"
    >
      {{ t('plugin_app.loading') }}
    </p>

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

    <PieceEditor
      v-if="editing"
      :key="`${editing.n}:${editing.line}:${brought?.frame ?? ''}`"
      :path="path"
      :project="project"
      :piece="editing"
      :readonly="!!version"
      :pick="pickPath"
      :write="writeEditing"
      :reload="bridge.pieceNow"
      :proposed="brought?.proposed"
      :design="brought?.design"
      :put-back="putBackEditing"
      @close="closeEditing"
      @open-note="openPiece"
    />

    <div
      v-if="picking"
      ref="pickerEl"
      class="absolute inset-0 z-20 flex flex-col gap-3 overflow-auto bg-surface p-4"
      role="dialog"
      aria-modal="true"
      :aria-label="t('plugin_app.pick_title')"
      data-app-picker
      @keydown.esc.prevent="choose(null)"
    >
      <div class="flex items-center gap-2">
        <h2 class="text-sm font-semibold">
          {{ t('plugin_app.pick_title') }}
        </h2>
        <span class="flex-1" />
        <label class="cursor-pointer rounded border border-border px-2 py-1 text-sm hover:bg-surface-hover focus-within:outline focus-within:outline-2 focus-within:outline-accent">
          {{ t('plugin_app.pick_upload') }}
          <input
            type="file"
            accept="image/*"
            class="sr-only"
            data-pick-upload
            @change="upload"
          >
        </label>
        <button
          type="button"
          class="rounded border border-border px-2 py-1 text-sm hover:bg-surface-hover"
          data-pick-cancel
          @click="choose(null)"
        >
          {{ t('plugin_app.pick_cancel') }}
        </button>
      </div>
      <ErrorMessage
        v-if="pickError"
        :text="pickError"
        class="text-sm"
      />
      <p
        v-else-if="!images.length"
        class="text-sm text-text-muted"
      >
        {{ t('plugin_app.pick_empty') }}
      </p>
      <ul class="grid grid-cols-[repeat(auto-fill,minmax(8rem,1fr))] gap-3">
        <li
          v-for="a in images"
          :key="a.path"
        >
          <button
            type="button"
            class="flex w-full flex-col items-center gap-1 rounded border border-border p-2 text-xs hover:bg-surface-hover"
            data-pick-image
            :data-path="a.path"
            @click="choose(a)"
          >
            <img
              :src="vaultFileHref(a.path)"
              alt=""
              class="h-24 w-full object-contain"
              loading="lazy"
            >
            <span class="w-full truncate font-mono">{{ a.path.split('/').pop() }}</span>
          </button>
        </li>
      </ul>
    </div>
  </div>
</template>
