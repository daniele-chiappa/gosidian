<script setup lang="ts">
/**
 * CodeMirrorEditor — Phase 4 markdown editor wrapping CodeMirror 6.
 *
 * Scope chiuso v2.0 (vedi plan):
 *   - markdown lang + foldGutter + searchKeymap + history + multi-cursor
 *   - wikilink autocomplete: trigger su `[[<prefix>` debounced 200ms
 *     hitting /api/v1/note-titles?q=
 *   - paste/drop file upload via /api/v1/attach → splice markdown
 *     embed at cursor position
 *   - theme legge `--color-bg`, `--color-text`, ecc. dai CSS vars
 *
 * Out of scope: vim mode, LSP, live collab cursors (v2.1+).
 *
 * v-model contract: emette `update:modelValue` su ogni edit. La save
 * (Ctrl+S) e il dirty-tracking restano nel parent, qui esponiamo solo
 * il content. La concurrency conflict UI (412) è gestita dal
 * ConflictDialog root-level — qui ci limitiamo a accettare il nuovo
 * content quando il parent re-passa modelValue.
 */
import { useI18n } from 'vue-i18n'
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { EditorState, Compartment } from '@codemirror/state'
import {
  EditorView,
  keymap,
  highlightActiveLine,
  highlightActiveLineGutter,
  lineNumbers,
  drawSelection,
  rectangularSelection,
  crosshairCursor,
} from '@codemirror/view'
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import { foldGutter, foldKeymap, indentOnInput, bracketMatching } from '@codemirror/language'
import {
  autocompletion,
  closeBrackets,
  closeBracketsKeymap,
  completionKeymap,
  pickedCompletion,
  type Completion,
  type CompletionContext,
  type CompletionResult,
} from '@codemirror/autocomplete'
import { searchKeymap, highlightSelectionMatches } from '@codemirror/search'
import { markdown } from '@codemirror/lang-markdown'
import { suggestNoteTitles } from '@/api/noteTitles'
import { attachFile } from '@/api/attach'
import { wikilinkClosing } from './wikilink'
import { editorThemeSpec } from './theme'
import { errorText } from '@/api/errors'

const { t } = useI18n()

interface Props {
  modelValue: string
  placeholder?: string
  project?: string
}
const props = withDefaults(defineProps<Props>(), {
  placeholder: 'Markdown…',
  project: undefined,
})
const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
}>()

const host = ref<HTMLDivElement | null>(null)
let view: EditorView | null = null
const themeCompartment = new Compartment()

const cmTheme = EditorView.theme(editorThemeSpec, { dark: false })

// Wikilink completion source. CodeMirror gives us a CompletionContext
// from which we slice the `[[<prefix>` opener and ship the prefix to
// /note-titles. The `apply` field controls what the editor inserts —
// we put the full path between [[]] for wiki-style links.
async function wikilinkSource(
  ctx: CompletionContext,
): Promise<CompletionResult | null> {
  // Look back from cursor for the most recent `[[` and ensure no `]]`
  // closes it before the cursor — otherwise we're not inside an open
  // wikilink. matchBefore won't help here because we need a multi-char
  // opener.
  const lineUpto = ctx.state.doc
    .lineAt(ctx.pos)
    .text.slice(0, ctx.pos - ctx.state.doc.lineAt(ctx.pos).from)
  const open = lineUpto.lastIndexOf('[[')
  if (open === -1) return null
  // Reject if a `]]` falls between the opener and the cursor.
  if (lineUpto.slice(open + 2).includes(']]')) return null
  const prefix = lineUpto.slice(open + 2)
  // Allow inside-word triggering only when explicit (Ctrl+Space) or
  // we already have at least 1 character.
  if (!ctx.explicit && prefix.length === 0) return null

  let hits: { title: string; path: string }[]
  try {
    hits = await suggestNoteTitles(prefix, 10)
  } catch {
    return null
  }
  // Position to replace: from start of prefix to cursor. We keep the
  // `[[` outside the replaced range so it survives.
  const fromPos = ctx.pos - prefix.length
  return {
    from: fromPos,
    options: hits.map((h) => ({
      label: h.title || h.path,
      detail: h.path,
      apply: (view: EditorView, completion: Completion, from: number, to: number) => {
        const { insert, skip } = wikilinkClosing(view.state.sliceDoc(to, to + 2))
        const text = stripMdSuffix(h.path) + insert
        view.dispatch({
          changes: { from, to, insert: text },
          selection: { anchor: from + text.length + skip },
          annotations: pickedCompletion.of(completion),
          userEvent: 'input.complete',
        })
      },
      type: 'class',
    })),
  }
}

function stripMdSuffix(p: string): string {
  return p.endsWith('.md') ? p.slice(0, -3) : p
}

function buildState(initial: string): EditorState {
  return EditorState.create({
    doc: initial,
    extensions: [
      lineNumbers(),
      highlightActiveLine(),
      highlightActiveLineGutter(),
      foldGutter(),
      drawSelection(),
      rectangularSelection(),
      crosshairCursor(),
      indentOnInput(),
      bracketMatching(),
      closeBrackets(),
      history(),
      highlightSelectionMatches(),
      autocompletion({ override: [wikilinkSource] }),
      markdown({ codeLanguages: [] }),
      EditorView.lineWrapping,
      EditorView.domEventHandlers({
        paste: handlePaste,
        drop: handleDrop,
        dragover: (event) => {
          event.preventDefault()
        },
      }),
      EditorView.updateListener.of((u) => {
        if (u.docChanged) {
          const next = u.state.doc.toString()
          if (next !== props.modelValue) emit('update:modelValue', next)
        }
      }),
      keymap.of([
        ...closeBracketsKeymap,
        ...defaultKeymap,
        ...searchKeymap,
        ...historyKeymap,
        ...foldKeymap,
        ...completionKeymap,
        indentWithTab,
      ]),
      themeCompartment.of(cmTheme),
    ],
  })
}

function handlePaste(event: ClipboardEvent, _view: EditorView): boolean {
  const items = event.clipboardData?.items
  if (!items) return false
  for (const item of items) {
    if (item.kind === 'file') {
      const file = item.getAsFile()
      if (file) {
        event.preventDefault()
        void uploadAndInsert(file)
        return true
      }
    }
  }
  return false
}

function handleDrop(event: DragEvent, _view: EditorView): boolean {
  const files = event.dataTransfer?.files
  if (!files || files.length === 0) return false
  event.preventDefault()
  for (const file of Array.from(files)) {
    void uploadAndInsert(file)
  }
  return true
}

async function uploadAndInsert(file: File) {
  if (!view) return
  // Insert a placeholder while uploading, replace with the real
  // markdown when /attach returns.
  const placeholder = `![uploading ${file.name}…]()`
  const cursor = view.state.selection.main.head
  view.dispatch({
    changes: { from: cursor, insert: placeholder },
    selection: { anchor: cursor + placeholder.length },
  })
  try {
    const res = await attachFile(file, props.project)
    if (!view) return
    // Find the placeholder in the current doc; its position may have
    // shifted if the user kept typing during the upload — search
    // backward from cursor for safety.
    const doc = view.state.doc.toString()
    const idx = doc.lastIndexOf(placeholder)
    if (idx === -1) return
    view.dispatch({
      changes: { from: idx, to: idx + placeholder.length, insert: res.markdown },
      selection: { anchor: idx + res.markdown.length },
    })
  } catch (e) {
    if (!view) return
    const doc = view.state.doc.toString()
    const idx = doc.lastIndexOf(placeholder)
    if (idx === -1) return
    // In the note itself, on one line.
    const replacement = `[${errorText(e, t, t('editor.upload_failed')).replace(/\n/g, ' — ')}]`
    view.dispatch({
      changes: { from: idx, to: idx + placeholder.length, insert: replacement },
    })
  }
}

onMounted(() => {
  if (!host.value) return
  view = new EditorView({
    state: buildState(props.modelValue),
    parent: host.value,
  })
})

onBeforeUnmount(() => {
  view?.destroy()
  view = null
})

// External update: parent rehydrates content (e.g. after save → fresh
// note from server, or load() on path change). Avoid rebuilding the
// state when the parent is just echoing what we emitted.
watch(
  () => props.modelValue,
  (next) => {
    if (!view) return
    const current = view.state.doc.toString()
    if (current === next) return
    view.dispatch({
      changes: { from: 0, to: current.length, insert: next },
    })
  },
)

defineExpose({
  focus: () => view?.focus(),
})
</script>

<template>
  <div ref="host" class="cm-host h-full w-full overflow-hidden bg-bg-elevated" />
</template>

<style scoped>
.cm-host :deep(.cm-editor) {
  height: 100%;
  outline: none;
}
.cm-host :deep(.cm-scroller) {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
</style>
