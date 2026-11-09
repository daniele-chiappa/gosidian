<script setup lang="ts">
/**
 * PieceEditor — a piece of a note's text, edited over its design (IMP-228).
 * A text has the note's editor and beside it the piece as it reads, since the
 * frame changes only once it is saved; pasting or dropping a file works as in
 * the note. An image has its picture, its description and the way to replace
 * it. Saving writes the note, and the window has the frames catch up.
 *
 * An image inserted in a text ends the text: the server gives it a piece of
 * its own, to place, as a frame shows a text or an image.
 *
 * A frame of the design may send its words (IMP-230): the editor starts from
 * them when they did not read exactly in the piece's text, and shows them
 * beside the piece when the note changed too, with a way to keep either.
 */
import { computed, defineAsyncComponent, nextTick, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useDebounceFn } from '@vueuse/core'
import axios from 'axios'
import { ImagePlus } from 'lucide-vue-next'
import { vaultFileHref, type Piece } from '@/api/layouts'
import { renderPreviewData } from '@/api/preview'
import { errorText } from '@/api/errors'
import { confirmAction } from '@/composables/useConfirm'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'
import MarkdownPreview from '@/components/domain/MarkdownPreview.vue'

const CodeMirrorEditor = defineAsyncComponent(() => import('@/components/editor/CodeMirrorEditor.vue'))

const props = defineProps<{
  path: string
  project: string
  piece: Piece
  /** A version of the design: its text to read. */
  readonly?: boolean
  /** An image among the project's, its path: null when none was chosen. */
  pick: () => Promise<string | null>
  /** Writes the text in place of the piece. */
  write: (piece: Piece, text: string) => Promise<void>
  /** The piece as it is now. */
  reload: (n: number) => Promise<Piece | null>
  /** The words a frame changed, carried into the piece's text: where the
   *  editor starts. */
  proposed?: string
  /** A frame's words, the piece changed in the note too: to read beside it. */
  design?: string
  /** Keeps the note's text: the frame takes it again. */
  putBack?: () => Promise<void>
}>()
const emit = defineEmits<{ close: []; openNote: [line: number] }>()
const { t } = useI18n()

const root = ref<HTMLElement | null>(null)
const editor = ref<{ focus: () => void } | null>(null)
const current = ref(props.piece)
const text = ref('')
const alt = ref('')
// The image as the note writes it, and where a browser finds it.
const src = ref('')
const shown = ref('')
const saving = ref(false)
const error = ref('')
const changed = ref(false)
const previewHTML = ref('')

const image = computed(() => (current.value.kind === 'image' ? current.value.image : undefined))
const embed = computed(() => /^!\[\[/.test(current.value.text) && src.value === image.value?.src)
const title = computed(() =>
  current.value.n ? t('plugin_app.piece_title', { n: current.value.n }) : t('plugin_app.piece_no_number'),
)

function fill(p: Piece) {
  current.value = p
  text.value = p.text
  alt.value = p.image?.alt ?? ''
  src.value = p.image?.src ?? ''
  shown.value = p.image?.path ? vaultFileHref(p.image.path) : (p.image?.src ?? '')
}
fill(props.piece)
if (props.proposed !== undefined) text.value = props.proposed

const written = computed(() => {
  if (!image.value) return text.value
  const description = alt.value.replace(/[[\]\n]/g, ' ').trim()
  if (embed.value) return description ? `![[${src.value}|${description}]]` : `![[${src.value}]]`
  return `![${description}](${src.value})`
})
const dirty = computed(() => written.value !== current.value.text)
const canSave = computed(() => !props.readonly && dirty.value && !saving.value && written.value.trim() !== '')

const renderPreview = useDebounceFn(async () => {
  if (image.value) return
  try {
    previewHTML.value = (await renderPreviewData(text.value, props.path)).html
  } catch {
    /* the text is what counts: the preview waits for the next change */
  }
}, 300)
watch(text, () => void renderPreview(), { immediate: true })

async function insertImage() {
  const path = await props.pick()
  if (path) text.value = `${text.value.trimEnd()}\n\n![](${vaultFileHref(path)})\n`
}

async function replaceImage() {
  const path = await props.pick()
  if (!path) return
  src.value = vaultFileHref(path)
  shown.value = src.value
}

async function save() {
  if (!canSave.value) return
  saving.value = true
  error.value = ''
  changed.value = false
  try {
    await props.write(current.value, written.value)
    emit('close')
  } catch (e) {
    changed.value = axios.isAxiosError(e) && e.response?.status === 409
    error.value = changed.value ? t('plugin_app.piece_changed') : errorText(e, t, t('plugin_app.piece_failed'))
  } finally {
    saving.value = false
  }
}

/** What the piece holds now, in place of what was written here. */
async function reloadPiece() {
  const p = await props.reload(current.value.n)
  if (!p) {
    error.value = t('plugin_app.piece_gone')
    changed.value = false
    return
  }
  fill(p)
  error.value = ''
  changed.value = false
}

/** The piece as the note has it stays, and the frame takes it again. */
async function keepNote() {
  if (!props.putBack) return
  saving.value = true
  try {
    await props.putBack()
    emit('close')
  } catch (e) {
    error.value = errorText(e, t, t('plugin_app.piece_failed'))
  } finally {
    saving.value = false
  }
}

async function close() {
  if (dirty.value && !props.readonly && !(await confirmAction(t('plugin_app.piece_discard'), { confirmLabel: t('note.draft_discard') })))
    return
  emit('close')
}

onMounted(async () => {
  await nextTick()
  if (image.value) root.value?.querySelector<HTMLElement>('[data-piece-alt]')?.focus()
  else editor.value?.focus()
})
watch(editor, (e) => e?.focus())
</script>

<template>
  <div
    class="absolute inset-0 z-10 flex items-center justify-center bg-black/40 p-4"
    data-piece-overlay
  >
    <div
      ref="root"
      class="flex max-h-full w-full max-w-4xl flex-col gap-3 overflow-auto rounded border border-border bg-bg-elevated p-4 text-sm shadow-lg"
      role="dialog"
      aria-modal="true"
      :aria-label="title"
      data-piece-editor
      @keydown.esc.prevent="close"
    >
      <div class="flex items-center gap-2">
        <h2 class="text-sm font-semibold">
          {{ title }}
        </h2>
        <span class="flex-1" />
        <button
          v-if="!readonly && !image"
          type="button"
          class="inline-flex items-center gap-1 rounded border border-border px-2 py-1 text-xs hover:bg-surface-hover"
          data-piece-image
          @click="insertImage"
        >
          <ImagePlus
            class="h-3.5 w-3.5"
            aria-hidden="true"
          />
          {{ t('plugin_app.piece_insert_image') }}
        </button>
        <button
          type="button"
          class="rounded border border-border px-2 py-1 text-xs hover:bg-surface-hover"
          data-piece-note
          @click="emit('openNote', current.line)"
        >
          {{ t('plugin_app.piece_open_note') }}
        </button>
      </div>

      <p
        v-if="proposed !== undefined"
        class="text-xs text-text-muted"
        data-piece-from-design
      >
        {{ t('plugin_app.piece_from_design') }}
      </p>
      <div
        v-if="design !== undefined"
        class="flex flex-col gap-2 rounded border border-border p-3"
        data-piece-design
      >
        <p class="text-xs text-text-muted">
          {{ t('plugin_app.piece_both_sides') }}
        </p>
        <h3 class="text-xs font-semibold">
          {{ t('plugin_app.piece_in_design') }}
        </h3>
        <pre class="max-h-40 overflow-auto whitespace-pre-wrap text-xs">{{ design }}</pre>
        <div class="flex flex-wrap gap-2">
          <button
            type="button"
            class="rounded border border-border px-2 py-1 text-xs hover:bg-surface-hover"
            data-piece-use-design
            @click="text = design"
          >
            {{ t('plugin_app.piece_use_design') }}
          </button>
          <button
            v-if="putBack"
            type="button"
            class="rounded border border-border px-2 py-1 text-xs hover:bg-surface-hover disabled:opacity-60"
            :disabled="saving"
            data-piece-keep-note
            @click="keepNote"
          >
            {{ t('plugin_app.piece_keep_note') }}
          </button>
        </div>
      </div>

      <template v-if="image">
        <img
          :src="shown"
          alt=""
          class="max-h-64 self-center rounded border border-border object-contain"
          data-piece-picture
        >
        <label class="flex flex-col gap-1 text-xs text-text-muted">
          {{ t('plugin_app.piece_description') }}
          <input
            v-model="alt"
            class="rounded border border-border-input bg-bg px-2 py-1 text-sm text-text"
            :readonly="readonly"
            data-piece-alt
          >
        </label>
        <p
          v-if="image.described"
          class="text-xs text-text-muted"
          data-piece-described
        >
          {{ image.described }}
        </p>
        <button
          v-if="!readonly"
          type="button"
          class="self-start rounded border border-border px-2 py-1 text-xs hover:bg-surface-hover"
          data-piece-replace
          @click="replaceImage"
        >
          {{ t('plugin_app.piece_replace_image') }}
        </button>
      </template>
      <div
        v-else
        class="grid min-h-0 flex-1 grid-cols-[repeat(auto-fit,minmax(18rem,1fr))] gap-3"
      >
        <div class="h-72 overflow-hidden rounded border border-border">
          <CodeMirrorEditor
            ref="editor"
            v-model="text"
            :project="project"
            :label="title"
            :readonly="readonly"
          />
        </div>
        <div
          class="h-72 overflow-auto rounded border border-border p-3"
          data-piece-preview
        >
          <MarkdownPreview
            :html="previewHTML"
            :views="[]"
            :diagram-links="{}"
            :note-path="path"
            compact
          />
        </div>
      </div>

      <ErrorMessage
        v-if="error"
        :text="error"
        class="text-xs"
        data-piece-error
      />
      <div class="flex items-center gap-2">
        <button
          v-if="changed"
          type="button"
          class="rounded border border-border px-2 py-1 text-xs hover:bg-surface-hover"
          data-piece-reload
          @click="reloadPiece"
        >
          {{ t('plugin_app.piece_reload') }}
        </button>
        <span class="flex-1" />
        <button
          type="button"
          class="rounded border border-border px-3 py-1 text-xs hover:bg-surface-hover"
          data-piece-cancel
          @click="close"
        >
          {{ readonly ? t('common.dismiss') : t('common.cancel') }}
        </button>
        <button
          v-if="!readonly"
          type="button"
          class="rounded bg-accent px-3 py-1 text-xs text-accent-fg hover:bg-accent-hover disabled:opacity-60"
          :disabled="!canSave"
          :aria-busy="saving ? 'true' : undefined"
          data-piece-save
          @click="save"
        >
          {{ t('common.save') }}
        </button>
      </div>
    </div>
  </div>
</template>
