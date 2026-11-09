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
  Scissors,
  LayoutTemplate,
} from 'lucide-vue-next'
import OverflowMenu, { type OverflowItem } from '@/components/primitives/OverflowMenu.vue'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'
import { announce } from '@/composables/useAnnounce'
import { getNote, updateNote, deleteNote, createSnapshot, type Note } from '@/api/notes'
import { downloadNote } from '@/api/noteDownload'
import { draftAfterSave, dropDraft, keepDraft, keptDraft, type KeptDraft } from '@/views/noteDraft'
import { tooLargeToSend } from '@/views/noteSize'
import {
  renderPreviewData,
  renderCut,
  listCutCollections,
  type ViewData,
  type CutView,
  type CutCollection,
  type CutRequest,
} from '@/api/preview'
import axios from 'axios'
import { isConcurrencyConflict, onApiEvent, onSessionExpired, type ConcurrencyConflictDetail } from '@/api/client'
import { errorText } from '@/api/errors'
import { formatSize } from '@/api/format'
import { useSSE } from '@/composables/useSSE'
import LayoutCard from '@/components/domain/LayoutCard.vue'
import NoteVersionButton from '@/components/domain/NoteVersionButton.vue'
import MarkdownPreview from '@/components/domain/MarkdownPreview.vue'
import CutPreview from '@/components/domain/CutPreview.vue'
import { listMarks, type MarkSet } from '@/api/marks'
import { findHeading } from '@/components/domain/headings'
import PropertiesPanel from '@/components/views/PropertiesPanel.vue'
import RowViews from '@/components/views/RowViews.vue'
import HTMLPreview from '@/components/domain/HTMLPreview.vue'
import MediaPreview from '@/components/domain/MediaPreview.vue'
import TablePreview from '@/components/domain/TablePreview.vue'
import CanvasPreview from '@/components/domain/CanvasPreview.vue'
import { convertCanvas } from '@/components/domain/canvasConvert'
import { useRecentlyViewed } from '@/composables/useRecentlyViewed'
import { planciaKey, base } from '@/composables/planciaKey'
import { appForType } from '@/composables/pluginApps'
import { noteField, notePdfs, noteTypeOf } from '@/composables/layoutBridge'
import { numberPieces, type PluginApp } from '@/api/layouts'
import type { NoteTag } from '@/api/history'
import { useAccessStore } from '@/stores/access'
import { useAuthStore } from '@/stores/auth'
import { projectOfPath, useTreeStore } from '@/stores/tree'
import { useUIStore } from '@/stores/ui'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { confirmAction } from '@/composables/useConfirm'
import { CUT_STORAGE_PREFIX } from '@/composables/useSessionReset'
import { tagProblems, type TagProblems } from './tagProblems'

const CodeMirrorEditor = defineAsyncComponent(
  () => import('@/components/editor/CodeMirrorEditor.vue'),
)

type Mode = 'view' | 'edit'
type EditorLayout = 'editor' | 'split' | 'stacked' | 'preview'

// line is where the design's panel asked to open the note (IMP-226), lineAt
// when. anchor is the heading a link pointed at (IMP-140), anchorAt when it was
// clicked: the window scrolls to the heading, again on every new click.
// editorFocus: a note just made, whose editor takes the focus (IMP-217, F-04).
const props = defineProps<{ path: string; mode?: Mode; line?: number; lineAt?: number; anchor?: string; anchorAt?: number; editorFocus?: boolean }>()
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
// The version readers must see, when the note has one that differs from the
// note as it is, which is then the draft (IMP-201): what the window shows
// reading, with a way to the draft. Editing is always on the draft.
const versionNote = ref<Note | null>(null)
const showDraft = ref(false)
const valid = computed(() => note.value?.valid)
const hasDraft = computed(() => !!valid.value && !valid.value.same && !valid.value.missing)
const readingValid = computed(() => hasDraft.value && !showDraft.value && mode.value === 'view' && !!versionNote.value)
const readText = computed(() => (readingValid.value ? versionNote.value?.content : note.value?.content) ?? '')
const validBanner = computed(() => {
  const v = valid.value
  if (!v) return ''
  if (v.missing) return t('note.valid_missing', { label: v.label })
  if (mode.value === 'edit') return t('note.valid_editing', { label: v.label })
  if (v.same) return t('note.valid_is', { label: v.label })
  if (showDraft.value) return t('note.valid_seeing_draft', { label: v.label })
  return `${t('note.valid_is', { label: v.label })} · ${t('note.valid_has_draft')}`
})

async function validVersionOf(n: Note): Promise<Note | null> {
  if (!n.valid || n.valid.same || n.valid.missing) return null
  try {
    return await getNote(n.path, { version: 'valid' })
  } catch {
    return null
  }
}
// The editor's name for a screen reader, by the note's title as its heading (IMP-220, F-11).
const editorLabel = computed(() => t('editor.label', { name: note.value?.title || base(props.path) }))
const previewHTML = ref<string>('')
// The note's views as data, for the editors of ViewTable (IMP-127 phase 5).
const previewViews = ref<ViewData[]>([])
// The notes the links of its diagrams resolve to (IMP-169).
const previewDiagramLinks = ref<Record<string, string>>({})

/** Renders md for the preview pane: its HTML and its views as data. */
async function renderInto(md: string, notePath: string) {
  const r = await renderPreviewData(md, notePath)
  previewHTML.value = r.html
  previewViews.value = r.views
  previewDiagramLinks.value = r.diagramLinks
  previewError.value = null
}
// A preview that failed: the note stays open, with the reason where the
// preview would be (S6-13).
const previewError = ref<string | null>(null)
function previewFailed(e: unknown) {
  previewHTML.value = ''
  previewViews.value = []
  previewDiagramLinks.value = {}
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
// Its banner comes with its text, which a screen reader does not read:
// the shell's region says it (IMP-217, F-10).
watch(
  () => remoteChanged.value && !conflict.value,
  (on) => {
    if (on) announce(t('note.changed_elsewhere'))
  },
)
// The etags of this note's events while a save is on its way (BUG-144):
// the event of the save itself may come before its reply, so they wait
// for the etag the reply brings.
let heldEtags: string[] = []
// The draft kept in this browser (BUG-142), so that a session that ends, a
// reload or a crash does not take it: written a moment after an edit and
// before each save, dropped once saved. A note that opens with one offers
// it back (offeredDraft) until it is restored or discarded.
const auth = useAuthStore()
const offeredDraft = ref<KeptDraft | null>(null)
// The etag a restored draft was written on: its save asks for that version,
// so a note changed since answers with the conflict instead of losing the
// other writer's change.
let draftBase: string | null = null
// For a session stored before its length was kept: the default maximum.
const DRAFT_MAX_AGE = 168 * 3600_000
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

// A canvas made a diagram note beside it (IMP-169, F3), for who may write there.
const canWriteBeside = computed(() => access.canWrite(props.path))
const convertingCanvas = ref(false)
const canvasError = ref('')
async function toDiagram() {
  if (!note.value?.canvas || convertingCanvas.value) return
  convertingCanvas.value = true
  canvasError.value = ''
  try {
    const made = await convertCanvas(note.value.path, note.value.canvas)
    openWindow({
      type: 'diagram',
      key: planciaKey('diagram', `${made.path}#0`),
      title: t('diagram.window_title', { name: base(made.path) }),
      props: { path: made.path, index: 0, notes: made.warnings },
    })
  } catch (e) {
    canvasError.value = errorText(e, t, t('diagram.convert_failed'))
  } finally {
    convertingCanvas.value = false
  }
}
// A note the server would not take back from the web UI (noteSize.ts):
// its text, read-only, with no preview.
const tooLarge = computed(() => !!note.value && !isReadOnlyFile.value && tooLargeToSend(note.value.content))

// The tags of the frontmatter checked as they are written (IMP-223, F-13):
// a comma out of place saved a wrong tag unseen. A pause in the typing
// reads them again; nothing stops the save.
const TAG_CHECK_MS = 300
const tagCheck = ref<TagProblems | null>(null)
let tagTimer: ReturnType<typeof setTimeout> | undefined
watch([draft, mode], () => {
  clearTimeout(tagTimer)
  if (mode.value !== 'edit' || isHtml.value) {
    tagCheck.value = null
    return
  }
  tagTimer = setTimeout(() => (tagCheck.value = tagProblems(draft.value)), TAG_CHECK_MS)
})
const tagWarning = computed(() => {
  const p = tagCheck.value
  if (mode.value !== 'edit' || !p || (!p.empty && !p.spaced.length)) return ''
  const problems = [
    ...(p.empty ? [t('note.tags_empty', { n: p.empty }, p.empty)] : []),
    ...p.spaced.map((tag) => t('note.tags_spaced', { tag })),
  ]
  return t('note.tags_check', { problems: problems.join('; ') })
})
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
    if (v === 'editor' || v === 'split' || v === 'stacked') return v
  } catch {
    /* ignore */
  }
  return 'split'
}
// The preview is for a look, not to be remembered: the next Edit would show
// no text to edit.
watch(layout, (m) => {
  if (m === 'preview') return
  try {
    localStorage.setItem(STORAGE_LAYOUT, m)
  } catch {
    /* ignore */
  }
})

// The cut view (IMP-170): the note as note.sections cuts it for an archive
// of vectors, compared with a collection. Its rules must be the pipeline's,
// or no block matches: they are kept per project, in the browser.
interface CutSettings {
  collection: string
  icon: string
  image: string
  context: 'heading' | 'path' | 'none'
  max_chars: number
  inline_max_side: number
  media_paragraphs: 'apart' | 'join'
  icon_descriptions: 'none' | 'first' | 'all'
}
// The number fields: their default (the node's) and the range the server
// takes, which an emptied or out-of-range field falls back into.
type CutNumber = 'max_chars' | 'inline_max_side'
const CUT_NUMBERS: Record<CutNumber, { def: number; min: number; max: number }> = {
  max_chars: { def: 6000, min: 200, max: 1 << 20 },
  inline_max_side: { def: 128, min: 0, max: 10000 },
}
const ui = useUIStore()
const cutOn = ref(false)
const cutView = ref<CutView | null>(null)
const cutError = ref<string | null>(null)
const cutLoading = ref(false)
const cutCollections = ref<CutCollection[]>([])
// Why an archive lists no collection, or the list itself failed: without
// it the select shows only "None" and the reader cannot tell why.
const cutProblems = ref<string[]>([])
const cutCollectionsError = ref<string | null>(null)
const cutCollectionsLoading = ref(false)
const cutSettings = ref<CutSettings>(defaultCutSettings())
const canCut = computed(
  () =>
    ui.pipelinesEnabled &&
    !!note.value &&
    path.value.toLowerCase().endsWith('.md') &&
    !isMedia.value &&
    !isTable.value &&
    !isBase.value &&
    !tooLarge.value,
)
function defaultCutSettings(): CutSettings {
  return {
    collection: '',
    icon: '',
    image: '',
    context: 'heading',
    max_chars: CUT_NUMBERS.max_chars.def,
    inline_max_side: CUT_NUMBERS.inline_max_side.def,
    media_paragraphs: 'apart',
    icon_descriptions: 'none',
  }
}
/** A whole number in the field's range; an empty or broken one is the default. */
function cutNumber(key: CutNumber, v: unknown): number {
  const r = CUT_NUMBERS[key]
  const n = typeof v === 'number' ? v : typeof v === 'string' && v.trim() !== '' ? Number(v) : NaN
  return Number.isFinite(n) ? Math.min(r.max, Math.max(r.min, Math.round(n))) : r.def
}
// On change, not on input: clamped while typing, 1500 could never be typed.
function setCutNumber(key: CutNumber, e: Event) {
  const input = e.target as HTMLInputElement
  const n = cutNumber(key, input.value)
  cutSettings.value[key] = n
  // An emptied field back to the value it had leaves the model as it was,
  // and so the field empty: it shows the number used all the same.
  input.value = String(n)
}
const cutStorageKey = () => `${CUT_STORAGE_PREFIX}${project.value ?? ''}`
// The saved rules as written by an older version or by hand are checked
// field by field: a '' sent as max_chars is a 400 on every cut.
function loadCutSettings(): CutSettings {
  const d = defaultCutSettings()
  let saved: Record<string, unknown> = {}
  try {
    const v: unknown = JSON.parse(localStorage.getItem(cutStorageKey()) ?? '{}')
    if (v && typeof v === 'object') saved = v as Record<string, unknown>
  } catch {
    /* ignore */
  }
  const str = (v: unknown, def: string) => (typeof v === 'string' ? v : def)
  return {
    collection: str(saved.collection, d.collection),
    icon: str(saved.icon, d.icon),
    image: str(saved.image, d.image),
    context: saved.context === 'path' || saved.context === 'none' ? saved.context : d.context,
    max_chars: cutNumber('max_chars', saved.max_chars),
    inline_max_side: cutNumber('inline_max_side', saved.inline_max_side),
    media_paragraphs: saved.media_paragraphs === 'join' ? 'join' : d.media_paragraphs,
    icon_descriptions:
      saved.icon_descriptions === 'first' || saved.icon_descriptions === 'all'
        ? saved.icon_descriptions
        : d.icon_descriptions,
  }
}
// The rules as last loaded or saved: the watcher below acts on a change of
// them, not on their reload, which would cut the note a second time.
let cutSettingsSaved = ''
function applyCutSettings(s: CutSettings) {
  cutSettingsSaved = JSON.stringify(s)
  cutSettings.value = s
}
function cutRequest(): CutRequest {
  const s = cutSettings.value
  const req: CutRequest = { context: s.context, max_chars: s.max_chars, inline_max_side: s.inline_max_side }
  if (s.collection) req.collection = s.collection
  if (s.media_paragraphs === 'join') req.media_paragraphs = 'join'
  if (s.icon_descriptions !== 'none') req.icon_descriptions = s.icon_descriptions
  if (s.icon.trim() || s.image.trim())
    req.media_words = { icon: s.icon.trim() || 'icon', image: s.image.trim() || 'image' }
  return req
}
// Only the last request of each kind draws: a slow answer (an archive's
// plugin takes up to 30 s) to older rules, an older text or another note
// replaced the one asked after (as GraphView's, BUG-116).
let cutGen = 0
let marksGen = 0
let collectionsGen = 0
// The text the shown cut was made from: after an edit it is not the note's.
let cutContent: string | null = null
async function refreshCut() {
  if (!cutOn.value || !note.value) return
  const mine = ++cutGen
  const content = note.value.content
  cutLoading.value = true
  try {
    const v = await renderCut(content, note.value.path, cutRequest())
    if (mine !== cutGen) return
    cutView.value = v
    cutContent = content
    cutError.value = null
  } catch (e) {
    if (mine !== cutGen) return
    cutView.value = null
    cutError.value = errorText(e, t, t('cut.failed'))
  } finally {
    if (mine === cutGen) cutLoading.value = false
  }
}
const refreshCutSoon = useDebounceFn(refreshCut, 400)
watch(
  cutSettings,
  (s) => {
    const json = JSON.stringify(s)
    if (json === cutSettingsSaved) return
    cutSettingsSaved = json
    try {
      localStorage.setItem(cutStorageKey(), json)
    } catch {
      /* ignore */
    }
    void refreshCutSoon()
  },
  { deep: true },
)
async function loadCollections() {
  const mine = ++collectionsGen
  const p = project.value
  cutCollectionsError.value = null
  if (!p) {
    cutCollections.value = []
    cutProblems.value = []
    cutCollectionsLoading.value = false
    return
  }
  cutCollectionsLoading.value = true
  try {
    const r = await listCutCollections(p)
    if (mine !== collectionsGen) return
    cutCollections.value = r.collections
    cutProblems.value = r.problems
  } catch (e) {
    if (mine !== collectionsGen) return
    cutCollections.value = []
    cutProblems.value = []
    cutCollectionsError.value = errorText(e, t, t('cut.collections_failed'))
  } finally {
    if (mine === collectionsGen) cutCollectionsLoading.value = false
  }
}
// The saved collection though the archive does not list it now (its
// plugin down): the select shows what the cut is compared with.
const cutCollectionUnlisted = computed(
  () => !!cutSettings.value.collection && !cutCollections.value.some((c) => c.source === cutSettings.value.collection),
)
// The marks the pipelines put on the note (IMP-188), shown in the cut view.
const markSets = ref<MarkSet[]>([])
async function loadMarks() {
  const mine = ++marksGen
  if (!ui.pipelinesEnabled || !path.value.toLowerCase().endsWith('.md')) {
    markSets.value = []
    return
  }
  try {
    const sets = await listMarks(path.value)
    if (mine === marksGen) markSets.value = sets
  } catch {
    if (mine === marksGen) markSets.value = []
  }
}

// One cut request per toggle: the settings are applied without firing the
// watcher, and the cut does not wait for the archive's list.
function toggleCut() {
  cutOn.value = !cutOn.value
  if (!cutOn.value) {
    // What is still on its way is no longer wanted, and the next toggle
    // must not show this cut as the note's while it asks again.
    cutGen++
    collectionsGen++
    cutLoading.value = false
    cutCollectionsLoading.value = false
    cutView.value = null
    cutError.value = null
    return
  }
  applyCutSettings(loadCutSettings())
  void loadCollections()
  void loadMarks()
  void refreshCut()
}

// Another note in this window: the answers on their way are for the
// previous one, and another project has rules of its own.
watch(path, () => {
  cutGen++
  marksGen++
  cutView.value = null
  cutContent = null
  markSets.value = []
  void load()
})
watch(project, () => {
  if (!cutOn.value) return
  applyCutSettings(loadCutSettings())
  void loadCollections()
})

async function load() {
  if (!path.value) return
  // A reload of the note shown (an event of its own, a field saved from its
  // panel) keeps the window until the new version is there: blanked, it
  // lost the focus and the place (IMP-217, F-04).
  loading.value = note.value?.path !== path.value
  error.value = null
  notFound.value = false
  if (note.value?.path !== path.value) showDraft.value = false
  try {
    const fetched = await getNote(path.value)
    versionNote.value = await validVersionOf(fetched)
    remoteChanged.value = false
    note.value = fetched
    draft.value = fetched.content
    draftBase = null
    offerDraft(fetched)
    recents.record(fetched.path, fetched.title || fetched.path)
    emit('title', fetched.title || fetched.path)
    // HTML notes bypass the markdown renderer; the iframe shows raw content,
    // and a canvas draws its cards, rendered by the server.
    previewError.value = null
    if (isHtml.value || isCanvas.value || tooLarge.value) {
      previewHTML.value = ''
      previewViews.value = []
      previewDiagramLinks.value = {}
      if (tooLarge.value) mode.value = 'view'
    } else {
      try {
        await renderInto(readText.value, fetched.path)
        void scrollToAnchor()
      } catch (e) {
        previewFailed(e)
      }
      if (cutOn.value) void refreshCut()
      void loadMarks()
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
  void persistDraftSoon()
  // HTMLPreview binds the draft directly (reactive); only markdown needs the
  // server round-trip to refresh the preview pane.
  if (!isHtml.value && mode.value === 'edit' && layout.value !== 'editor') void refreshPreview()
})
watch(dirty, (d) => emit('dirty', d))

// ```view blocks are computed on the server when the note is rendered
// (IMP-127): render again when other notes change, so the tables stay live.
// Events closer than the pause it waits for held it back for good, with
// many writers in the vault: while they keep coming it still runs every
// 2 s (BUG-146).
const refreshViews = useDebounceFn(
  async () => {
    if (!note.value || isHtml.value) return
    try {
      const src = mode.value === 'edit' ? draft.value : readText.value
      await renderInto(src, path.value)
    } catch {
      /* keep the last render */
    }
  },
  800,
  { maxWait: 2000 },
)

const bareEtag = (e?: string) => (e ?? '').replace(/"/g, '')

function onNoteEvent(p: { path?: string; etag?: string; action?: string }) {
  if (!note.value) return
  if (p.action === 'resync') {
    void checkRemote()
    return
  }
  if (p.path === path.value) {
    if (p.etag && bareEtag(p.etag) === bareEtag(note.value.etag)) return // our own save
    if (saving.value) {
      heldEtags.push(bareEtag(p.etag))
      return
    }
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

// The events held during a save, once it is over: an etag other than the
// note's own now is another writer's (after ours: one before it makes the
// save a 412).
function releaseHeld() {
  const others = note.value && heldEtags.some((e) => e !== bareEtag(note.value!.etag))
  heldEtags = []
  if (!others) return
  if (dirty.value) remoteChanged.value = true
  else void load()
}

/** Keeps the draft as it is now, or drops it once it is the saved text; an
 *  offered draft stays until it is answered. */
function persistDraft() {
  const user = auth.user?.id
  if (!user || !note.value || !canEdit.value) return
  if (dirty.value)
    keepDraft(user, note.value.path, { text: draft.value, etag: draftBase ?? note.value.etag, at: Date.now() })
  else if (!offeredDraft.value) dropDraft(user, note.value.path)
}
const persistDraftSoon = useDebounceFn(persistDraft, 500)

// A draft kept from before is offered, unless it is the saved text.
function offerDraft(fetched: Note) {
  offeredDraft.value = null
  const user = auth.user?.id
  if (!user || !canEdit.value) return
  const kept = keptDraft(user, fetched.path, auth.maxSessionMs || DRAFT_MAX_AGE)
  if (kept && kept.text === fetched.content) dropDraft(user, fetched.path)
  else offeredDraft.value = kept
}
const draftOfferText = computed(() => {
  if (!offeredDraft.value) return ''
  const at = new Date(offeredDraft.value.at)
  return at.toDateString() === new Date().toDateString()
    ? t('note.draft_kept_at', { time: at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) })
    : t('note.draft_kept_on', { date: at.toLocaleString([], { dateStyle: 'short', timeStyle: 'short' }) })
})
// The banner's buttons go with it: the focus moves on to the editor, or to
// the toolbar, so that typing and Ctrl+S carry on in this window.
const editorEl = ref<{ focus: () => void; goToLine: (line: number) => void } | null>(null)
let focusEditor = false
watch(
  editorEl,
  (ed) => {
    if (!ed || !focusEditor) return
    focusEditor = false
    ed.focus()
  },
  { flush: 'post' },
)
function keepFocus() {
  if (mode.value !== 'edit' || layout.value === 'preview') {
    headerEl.value?.querySelector<HTMLElement>('button:not([disabled])')?.focus()
    return
  }
  // The editor loads at its first use: it takes the focus once it is there.
  if (editorEl.value) editorEl.value.focus()
  else focusEditor = true
}
function restoreDraft() {
  const kept = offeredDraft.value
  if (!kept || !note.value) return
  offeredDraft.value = null
  draftBase = kept.etag
  enterEdit()
  draft.value = kept.text
  // Written on a version the note no longer is: the banner says so before
  // the save does.
  if (bareEtag(kept.etag) !== bareEtag(note.value.etag)) remoteChanged.value = true
  void nextTick(keepFocus)
}
function discardDraft() {
  offeredDraft.value = null
  // What was typed since is the draft now; nothing typed, none.
  persistDraft()
  void nextTick(keepFocus)
}
function forgetDraft() {
  offeredDraft.value = null
  draftBase = null
  const user = auth.user?.id
  if (user && note.value) dropDraft(user, note.value.path)
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
  const shown = readText.value
  mode.value = 'edit'
  // Whoever edits sees the text, and beside it the draft: reading, the pane
  // held the valid version, or the note as saved.
  if (layout.value === 'preview') layout.value = loadLayout()
  if (!isHtml.value && shown !== draft.value) void refreshPreview()
}

// The editor loads at its first use: the line waits for it, once. Asked again
// when the editor comes back, it would take the reader out of View.
let lineToShow = 0
function showLine() {
  if (!lineToShow || !editorEl.value) return
  editorEl.value.goToLine(lineToShow)
  lineToShow = 0
}
watch(
  [() => props.line, () => props.lineAt],
  ([line]) => {
    if (!line) return
    lineToShow = line
    enterEdit()
    showLine()
  },
  { immediate: true },
)
watch(editorEl, showLine)
async function enterView() {
  mode.value = 'view'
  // View shows the saved content; the draft stays in memory for re-editing.
  if (!note.value || isHtml.value || tooLarge.value) return
  // The cut and the marks were read before the edit, and a save since
  // changed the text: a cut of the old text is not shown as this one's.
  if (cutOn.value) {
    if (cutContent !== note.value.content) cutView.value = null
    void refreshCut()
  }
  void loadMarks()
  versionNote.value = await validVersionOf(note.value)
  try {
    await renderInto(readText.value, note.value.path)
  } catch (e) {
    previewFailed(e)
  }
}

async function toggleDraft() {
  showDraft.value = !showDraft.value
  if (!note.value) return
  try {
    await renderInto(readText.value, note.value.path)
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
  // Kept before the request: a session that ended answers 401, and the
  // page goes to the login.
  persistDraft()
  try {
    const sent = draft.value
    const updated = await updateNote(note.value.path, {
      content: sent,
      ifMatch: draftBase ?? note.value.etag,
    })
    note.value = updated
    draftBase = null
    const after = draftAfterSave(sent, draft.value, updated.content)
    draft.value = after.draft
    dirty.value = after.dirty
    persistDraft()
    markSaved()
    remoteChanged.value = false
  } catch (e) {
    // The conflict banner owns a 412: an error pane would hide the draft.
    if (!isConcurrencyConflict(e)) error.value = errorText(e, t, t('note.save_failed'))
  } finally {
    saving.value = false
    releaseHeld()
  }
}

// The toolbar shows the save, and the shell's region says it: in a narrow
// window the toolbar has room for its time only (IMP-217, F-10).
function markSaved() {
  lastSavedAt.value = new Date().toLocaleTimeString()
  announce(t('note.saved_at', { time: lastSavedAt.value }))
}

async function reloadRemote() {
  forgetDraft()
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
    draftBase = null
    const after = draftAfterSave(sent, draft.value, updated.content)
    draft.value = after.draft
    dirty.value = after.dirty
    persistDraft()
    markSaved()
    conflict.value = null
    // The change the banner told of is under the draft now.
    remoteChanged.value = false
  } catch (e) {
    error.value = errorText(e, t, t('note.overwrite_failed'))
  } finally {
    saving.value = false
    releaseHeld()
  }
}

async function destroy() {
  if (!note.value) return
  // Without the trash the delete is for good, and the question says so
  // (BUG-116, S6-3).
  if (!(await confirmAction(t(access.deleteNoteKey, { path: note.value.path }), { confirmLabel: t('common.delete') }))) return
  try {
    await deleteNote(note.value.path)
    forgetDraft()
    treeStore.refresh(projectOfPath(note.value.path))
    emit('close')
  } catch (e) {
    error.value = errorText(e, t, t('note.delete_failed'))
  }
}

// Copy the raw note source the user sees in the editor (markdown or HTML).
// `draft` always holds the current source: the saved content in view mode and
// the live edits in edit mode.
async function copySource() {
  const text = isReadOnlyFile.value ? (note.value?.source ?? '') : readingValid.value ? readText.value : draft.value
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
    if (readingValid.value && versionNote.value) await downloadNote(note.value.path, versionNote.value, 'valid')
    else await downloadNote(note.value.path, note.value)
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
  // This window closes first: with one window on screen, plancia closes
  // the focused window, which would be the creation window.
  emit('close')
  // Keyed by the note, not the folder: an open creation window of the
  // folder would take the focus with a name of its own.
  openWindow({
    type: 'create',
    key: planciaKey('create', `${folder}/${name}`),
    title: t('note_create.window_title', { name: folder.split('/').pop() || '/' }),
    props: { path: folder, name },
  })
}

// The app a plugin offers for the note's type (IMP-226), and the page-layout
// editor, which lays out any markdown note. The frontmatter says when a note
// has its design.
const noteApp = ref<PluginApp | null>(null)
const layoutApp = ref<PluginApp | null>(null)
const designFile = computed(() => (note.value ? noteField(readText.value, 'layout_file') : ''))
const designPreview = computed(() => (note.value ? noteField(readText.value, 'layout_preview') : ''))
// The PDFs are listed by the note as it is: one of a version is kept after
// the version was named (IMP-229).
const designPdfs = computed(() => (note.value ? notePdfs(note.value.content) : []))
// Reading the valid version, the design is that version's, to read. Who can
// edit works on the draft, as Edit does (IMP-228): the panel of the versions
// opens any version.
const designVersion = computed(() => (readingValid.value && !canEdit.value ? valid.value?.tag : undefined))
const canLayOut = computed(
  () => /\.md$/i.test(props.path) && canEdit.value && !isHtml.value && !isMedia.value && !isTable.value,
)
watch(
  () => (note.value ? noteTypeOf(note.value.content) : null),
  async (type) => {
    if (type === null) return
    noteApp.value = await appForType(type)
    layoutApp.value = await appForType('layout')
  },
)

function openInApp() {
  openDesign(designVersion.value)
}

function openDesign(version?: string) {
  if (!note.value) return
  openWindow({
    type: 'app',
    key: planciaKey('app', version ? `${note.value.path}@${version}` : note.value.path),
    title: t('plugin_app.window_title', { name: base(note.value.path) }),
    props: version ? { path: note.value.path, version } : { path: note.value.path },
  })
}

// The first numbering of the note's pieces, each block its own, then the
// editor: the panel there places them by number.
async function layOut() {
  if (!note.value) return
  previewError.value = null
  try {
    await numberPieces(note.value.path, note.value.etag)
  } catch (e) {
    previewError.value = errorText(e, t)
    return
  }
  openInApp()
}

function openHistory(sha?: string) {
  if (!note.value) return
  const key = planciaKey('history', note.value.path)
  const at = sha ? { path: note.value.path, sha, shaAt: Date.now() } : { path: note.value.path }
  const id = openWindow({
    type: 'history',
    key,
    title: `${t('history.title')} · ${note.value.title || note.value.path}`,
    props: at,
  })
  if (sha) store.identify(id, key, at)
}

// The versions (IMP-228): a markdown note has them, whatever else it is not.
const canVersion = computed(
  () => /\.md$/i.test(props.path) && !isMedia.value && !isTable.value && !isBase.value && !isCanvas.value,
)
// Whatever is unsaved goes first, so that what is pinned is what shows.
async function saveForVersion() {
  await save()
  return !dirty.value
}
/** A version asked from the panel: its design when the note has one, or its text. */
function openVersion(tag: NoteTag) {
  if (designFile.value) openDesign(tag.tag)
  else openHistory(tag.sha)
}

// The window's toolbar (IMP-156, M5): below these widths the secondary
// actions go into the ⋯ menu. Editing adds the layouts, Save and Delete;
// with the layouts as icons it all fits a default window of ~575 px.
const COMPACT_BELOW = { edit: 560, view: 460 }
// From these widths each action says its name next to its icon (IMP-220,
// F-25). Measured in the lab, the Italian bar needs 851 px reading and 926
// editing, with the time of the last save and the button of the versions; the
// rest is for languages with longer words, German's Speichern and Bearbeiten.
const LABELLED_FROM = { edit: 1000, view: 920 }
const headerEl = ref<HTMLElement | null>(null)
const { width: headerWidth } = useElementSize(headerEl)
const compact = computed(() => headerWidth.value > 0 && headerWidth.value < COMPACT_BELOW[mode.value])
const labelled = computed(() => headerWidth.value >= LABELLED_FROM[mode.value])

const LAYOUTS: { key: EditorLayout; icon: typeof Eye }[] = [
  { key: 'editor', icon: SquarePen },
  { key: 'split', icon: Columns2 },
  { key: 'stacked', icon: Rows2 },
  { key: 'preview', icon: Eye },
]

const actions = computed<OverflowItem[]>(() => {
  const out: OverflowItem[] = []
  if (note.value && mode.value === 'view' && !isHtml.value && !isMedia.value && !isCanvas.value && !tooLarge.value)
    out.push({ key: 'print', label: t('note.print'), short: t('note.print_short'), icon: Printer, run: printNote })
  out.push({
    key: 'download',
    label: t('note.download'),
    short: t('note.download_short'),
    icon: Download,
    disabled: !note.value,
    run: downloadOriginal,
  })
  out.push({
    key: 'copy',
    label: copied.value ? t('note.copied') : t('note.copy'),
    short: copied.value ? t('note.copied') : t('note.copy_short'),
    icon: copied.value ? Check : Copy,
    disabled: !note.value,
    run: copySource,
  })
  if (note.value && !isHtml.value && !isMedia.value && !isReadOnlyFile.value && access.canWrite(props.path))
    out.push({
      key: 'snapshot',
      label: t('note.snapshot'),
      short: t('note.snapshot_short'),
      icon: Camera,
      disabled: snapshotting.value,
      run: snapshot,
    })
  out.push({ key: 'history', label: t('note.history'), short: t('note.history'), icon: GitBranch, run: () => openHistory() })
  if (note.value && (noteApp.value || (layoutApp.value && designFile.value)))
    out.unshift({
      key: 'app',
      label: t('plugin_app.open_in_editor'),
      short: t('plugin_app.open_in_editor_short'),
      icon: LayoutTemplate,
      run: openInApp,
    })
  else if (note.value && layoutApp.value && canLayOut.value)
    out.unshift({
      key: 'app',
      label: t('plugin_app.lay_out'),
      short: t('plugin_app.lay_out_short'),
      icon: LayoutTemplate,
      run: layOut,
    })
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
let unsubSession: (() => void) | null = null
onMounted(() => {
  void load()
  if (props.editorFocus && mode.value === 'edit') keepFocus()
  window.addEventListener('keydown', onKeydown)
  unsub = onApiEvent('note.concurrency-conflict', (detail) => {
    if (detail.path === path.value) conflict.value = detail
  })
  // The draft written a moment ago may not be kept yet.
  unsubSession = onSessionExpired(persistDraft)
  sse.on('note', onNoteEvent)
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
  unsub?.()
  unsubSession?.()
  if (copiedTimer) clearTimeout(copiedTimer)
  clearTimeout(tagTimer)
})
</script>

<template>
  <div ref="rootEl" class="flex flex-col h-full">
    <!-- The window's name, where an open puts the focus (IMP-217, F-03);
         the title bar shows it. -->
    <h1
      class="sr-only"
      tabindex="-1"
      data-window-heading
    >
      {{ note?.title || base(path) }}
    </h1>
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
      <span
        v-else-if="lastSavedAt && compact"
        class="inline-flex shrink-0 items-center gap-0.5 text-xs text-success"
        :title="t('note.saved_at', { time: lastSavedAt })"
        data-saved-at
      >
        <Check
          class="h-3 w-3"
          aria-hidden="true"
        />
        <span
          aria-hidden="true"
          data-saved-time
        >{{ lastSavedAt }}</span>
        <span class="sr-only">{{ t('note.saved_at', { time: lastSavedAt }) }}</span>
      </span>
      <span
        v-else-if="lastSavedAt"
        class="shrink-0 text-xs text-success"
        data-saved-at
      >
        {{ t('note.saved_at', { time: lastSavedAt }) }}
      </span>
      <span class="flex-1" />

      <!-- What readers see, and the way to change it (IMP-228) -->
      <NoteVersionButton
        v-if="note && canVersion"
        :path="note.path"
        :valid="note.valid"
        :dirty="dirty"
        :save="saveForVersion"
        :compact="compact"
        @changed="load"
        @open="openVersion"
        @history="openHistory()"
      />

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

      <!-- The cut view (IMP-170): the note as an archive of vectors gets it -->
      <button
        v-if="canCut && mode === 'view'"
        type="button"
        class="inline-flex h-control-sm shrink-0 items-center gap-1 rounded border border-border px-2 text-xs"
        :class="cutOn ? 'bg-accent text-accent-fg' : 'hover:bg-surface-hover'"
        :aria-pressed="cutOn"
        :title="t('cut.toggle_hint')"
        data-cut-toggle
        @click="toggleCut"
      >
        <Scissors class="h-3.5 w-3.5" aria-hidden="true" />
        <!-- Narrow, the label stays for screen readers: the button's name -->
        <span :class="{ 'sr-only': compact }">{{ t('cut.toggle') }}</span>
        <!-- The dot is a colour only: its words are read with the button -->
        <template v-if="markSets.length">
          <span
            class="h-1.5 w-1.5 rounded-full bg-warning"
            :title="t('marks.has', { n: markSets.length }, markSets.length)"
            aria-hidden="true"
            data-cut-marks-dot
          />
          <span class="sr-only" data-cut-marks-text>{{ t('marks.has', { n: markSets.length }, markSets.length) }}</span>
        </template>
      </button>

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
        <!-- aria-disabled, not disabled: with nothing to save it stays in the
             Tab order, and save() does nothing (IMP-217, F-04). -->
        <button
          type="button"
          class="h-control-sm shrink-0 rounded bg-accent px-2 text-xs text-accent-fg hover:bg-accent-hover aria-disabled:opacity-50 aria-disabled:hover:bg-accent"
          :aria-disabled="!dirty || saving ? 'true' : undefined"
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
        <!-- Wide, the word shown is the name read, the tooltip the whole;
             icons alone are named by the whole. -->
        <button
          v-for="a in actions"
          :key="a.key"
          type="button"
          class="inline-flex shrink-0 items-center gap-1 rounded text-text-muted hover:bg-surface-hover hover:text-text disabled:opacity-50"
          :class="[labelled ? 'px-1.5 py-1 text-xs' : 'p-1', { 'text-success': a.key === 'copy' && copied }]"
          :disabled="a.disabled"
          :title="a.label"
          :aria-label="labelled ? undefined : a.label"
          :data-snapshot="a.key === 'snapshot' ? '' : undefined"
          @click="a.run"
        >
          <component
            :is="a.icon"
            class="h-3.5 w-3.5"
            aria-hidden="true"
          />
          <span v-if="labelled">{{ a.short }}</span>
        </button>
      </template>
      <OverflowMenu v-else-if="note" class="shrink-0" :items="actions" :label="t('note.more_actions')" />
    </header>

    <!-- A draft kept in this browser from before (BUG-142): restored, it
         saves on the version it was written on. -->
    <div
      v-if="offeredDraft && note"
      class="flex flex-wrap items-center gap-2 border-b border-warning/40 bg-warning/10 px-4 py-2 text-xs"
      role="alert"
      data-draft-offer
    >
      <span class="text-warning">{{ draftOfferText }}</span>
      <div class="flex-1" />
      <button type="button" class="h-control-sm rounded px-2 hover:bg-surface-hover" @click="discardDraft">
        {{ t('note.draft_discard') }}
      </button>
      <button
        type="button"
        class="h-control-sm rounded bg-accent px-2 text-accent-fg hover:bg-accent-hover"
        @click="restoreDraft"
      >
        {{ t('note.draft_restore') }}
      </button>
    </div>

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
      data-changed-elsewhere
    >
      <span class="text-warning">{{ t('note.changed_elsewhere') }}</span>
      <div class="flex-1" />
      <button type="button" class="h-control-sm rounded px-2 hover:bg-surface-hover" @click="reloadRemote">
        {{ t('note.reload_remote') }}
      </button>
    </div>

    <div
      v-if="validBanner"
      class="flex flex-wrap items-center gap-2 border-b px-4 py-2 text-xs"
      :class="valid?.missing ? 'border-warning/40 bg-warning/10 text-warning' : 'border-border bg-surface'"
      role="status"
      data-valid-version
    >
      <span>{{ validBanner }}</span>
      <div class="flex-1" />
      <button
        v-if="hasDraft && mode === 'view'"
        type="button"
        class="h-control-sm rounded px-2 hover:bg-surface-hover"
        data-valid-toggle
        @click="toggleDraft"
      >
        {{ showDraft ? t('note.valid_back') : t('note.valid_see_draft') }}
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
    <div
      v-if="tagWarning"
      class="border-b border-warning/40 bg-warning/10 px-4 py-2 text-xs text-warning"
      role="status"
      data-tag-warning
    >
      {{ tagWarning }}
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
        <p class="flex flex-wrap items-center gap-2 px-4 py-1 text-xs text-text-muted">
          <span class="font-mono">
            <span :title="`etag ${note.etag}`">{{ note.path }} · {{ formatSize(note.size) }}</span> ·
            {{ t('note.canvas_readonly') }}
          </span>
          <button
            v-if="note.canvas && canWriteBeside"
            type="button"
            class="rounded border border-border px-2 py-0.5 hover:bg-surface-hover disabled:opacity-50"
            :title="t('diagram.convert_canvas_hint')"
            :disabled="convertingCanvas"
            data-canvas-convert
            @click="toDiagram"
          >
            {{ t('diagram.convert_canvas') }}
          </button>
          <span v-if="canvasError" class="text-danger" role="alert">{{ canvasError }}</span>
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
        <CodeMirrorEditor
          :model-value="note.content"
          :project="project"
          :label="editorLabel"
          readonly
        />
      </div>
      <!-- Markdown note: prose-rendered preview -->
      <article v-else ref="articleEl" class="p-6 max-w-3xl mx-auto">
        <p v-if="note" class="text-xs text-text-muted font-mono mb-6">
          <span :title="`etag ${note.etag}`">{{ note.path }} · {{ formatSize(note.size) }}</span>
          <template v-if="isBase"> · {{ t('note.base_readonly') }}</template>
        </p>
        <template v-if="cutOn && canCut">
          <details class="mb-4 rounded border border-border p-2 text-xs" data-cut-settings>
            <summary class="cursor-pointer text-text-muted">{{ t('cut.settings') }}</summary>
            <div class="mt-2 grid gap-2 sm:grid-cols-2">
              <div class="flex flex-col gap-1">
                <label class="flex flex-col gap-1">
                  {{ t('cut.collection') }}
                  <select v-model="cutSettings.collection" class="h-control-sm rounded border border-border-input bg-bg px-1" data-cut-collection>
                    <option value="">{{ t('cut.no_collection') }}</option>
                    <option v-if="cutCollectionUnlisted" :value="cutSettings.collection">
                      {{ cutSettings.collection }}
                    </option>
                    <option v-for="c in cutCollections" :key="c.source" :value="c.source">
                      {{ c.source }} · {{ c.count }}
                    </option>
                  </select>
                </label>
                <span v-if="cutCollectionsLoading" class="text-text-muted" data-cut-collections-loading>
                  {{ t('cut.collections_loading') }}
                </span>
                <ErrorMessage v-if="cutCollectionsError" :text="cutCollectionsError" data-cut-collections-failed />
                <p v-if="cutProblems.length" class="text-warning" data-cut-collections-problems>
                  {{ t('cut.collections_problems', { problems: cutProblems.join('; ') }) }}
                </p>
              </div>
              <label class="flex flex-col gap-1">
                {{ t('cut.context') }}
                <select v-model="cutSettings.context" class="h-control-sm rounded border border-border-input bg-bg px-1">
                  <option value="heading">{{ t('cut.context_heading') }}</option>
                  <option value="path">{{ t('cut.context_path') }}</option>
                  <option value="none">{{ t('cut.context_none') }}</option>
                </select>
              </label>
              <label class="flex flex-col gap-1">
                {{ t('cut.word_icon') }}
                <input v-model="cutSettings.icon" class="h-control-sm rounded border border-border-input bg-bg px-1" placeholder="icon" />
              </label>
              <label class="flex flex-col gap-1">
                {{ t('cut.word_image') }}
                <input v-model="cutSettings.image" class="h-control-sm rounded border border-border-input bg-bg px-1" placeholder="image" />
              </label>
              <label class="flex flex-col gap-1" :title="t('cut.media_paragraphs_hint')">
                {{ t('cut.media_paragraphs') }}
                <select
                  v-model="cutSettings.media_paragraphs"
                  class="h-control-sm rounded border border-border-input bg-bg px-1"
                  data-cut-media-paragraphs
                >
                  <option value="apart">{{ t('cut.media_paragraphs_apart') }}</option>
                  <option value="join">{{ t('cut.media_paragraphs_join') }}</option>
                </select>
              </label>
              <label class="flex flex-col gap-1" :title="t('cut.icon_descriptions_hint')">
                {{ t('cut.icon_descriptions') }}
                <select
                  v-model="cutSettings.icon_descriptions"
                  class="h-control-sm rounded border border-border-input bg-bg px-1"
                  data-cut-icon-descriptions
                >
                  <option value="none">{{ t('cut.icon_descriptions_none') }}</option>
                  <option value="first">{{ t('cut.icon_descriptions_first') }}</option>
                  <option value="all">{{ t('cut.icon_descriptions_all') }}</option>
                </select>
              </label>
              <label class="flex flex-col gap-1">
                {{ t('cut.max_chars') }}
                <input
                  :value="cutSettings.max_chars"
                  type="number"
                  :min="CUT_NUMBERS.max_chars.min"
                  :max="CUT_NUMBERS.max_chars.max"
                  step="100"
                  class="h-control-sm rounded border border-border-input bg-bg px-1"
                  data-cut-max-chars
                  @change="setCutNumber('max_chars', $event)"
                />
              </label>
              <label class="flex flex-col gap-1" :title="t('cut.inline_max_side_hint')">
                {{ t('cut.inline_max_side') }}
                <input
                  :value="cutSettings.inline_max_side"
                  type="number"
                  :min="CUT_NUMBERS.inline_max_side.min"
                  :max="CUT_NUMBERS.inline_max_side.max"
                  step="16"
                  class="h-control-sm rounded border border-border-input bg-bg px-1"
                  data-cut-inline-max-side
                  @change="setCutNumber('inline_max_side', $event)"
                />
              </label>
            </div>
            <p class="mt-2 text-text-muted">{{ t('cut.settings_note') }}</p>
          </details>
          <ErrorMessage v-if="cutError" :text="cutError" class="mb-4 text-sm" data-cut-failed />
          <p v-else-if="cutLoading && !cutView" class="text-xs text-text-muted">{{ t('cut.loading') }}</p>
          <CutPreview v-if="cutView && !cutError" :view="cutView" :marks="markSets" :note-path="note?.path" compact />
        </template>
        <template v-else>
          <!-- A row of a database: its fields, editable (IMP-127 phase 5);
               any markdown note: its tags (IMP-223) -->
          <PropertiesPanel v-if="note && !isBase" :path="note.path" :etag="note.etag" />
          <ErrorMessage v-if="previewError" :text="previewError" class="mb-4 text-sm" data-preview-error />
          <LayoutCard
            v-if="note && designFile"
            :path="note.path"
            :preview="designPreview"
            :version="designVersion"
            :pdfs="designPdfs"
            :pinned="valid && !valid.missing ? valid.label : ''"
            :can-edit="canEdit"
          />
          <MarkdownPreview
            :html="previewHTML"
            :views="previewViews"
            :diagram-links="previewDiagramLinks"
            :diagrams-editable="canEdit"
            :note-path="note?.path"
          />
          <!-- A row of a database: the views its schema declares (IMP-139) -->
          <RowViews v-if="note && !isBase" :path="note.path" :etag="note.etag" />
        </template>
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
        <CodeMirrorEditor
          ref="editorEl"
          v-model="draft"
          :project="project"
          :label="editorLabel"
          placeholder="Markdown…"
        />
      </div>
      <div v-if="layout !== 'editor'" class="overflow-auto p-4 max-w-none">
        <HTMLPreview v-if="isHtml" :html="draft" :path="note.path" />
        <ErrorMessage v-else-if="previewError" :text="previewError" class="text-sm" data-preview-error />
        <MarkdownPreview
          v-else
          :html="previewHTML"
          :views="previewViews"
          :diagram-links="previewDiagramLinks"
          :note-path="note.path"
          :compact="layout !== 'preview'"
        />
      </div>
    </div>
  </div>
</template>
