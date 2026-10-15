<script setup lang="ts">
/**
 * MarkdownPreview — renders the HTML produced by /api/v1/preview inside a
 * sanitized v-html container. Server-side goldmark lets a note's raw HTML
 * through (WithUnsafe): DOMPurify, in sanitizePreviewHtml, is the barrier,
 * not an extra (strips scripts, on*= attrs, javascript: URLs, unknown tags).
 * It keeps data-* attributes, so the renderer's `data-preview-path` on
 * resolved wikilinks survives.
 *
 * Link interception (plancia): internal links open as windows instead of
 * navigating away from the SPA —
 *   - resolved wikilink (`data-preview-path`) → note window
 *   - tag link (`/tags/<tag>`)               → tags window
 *   - in-note anchor (`#heading`)            → scroll within this preview
 *   - a relative or /notes/ link to a note   → note window (noteLinks.ts)
 *   - an attachment, a web page, the rest    → open in a new tab
 *   - mailto:, tel:, an app's own scheme     → the browser's, in place
 * Nothing navigates the SPA away, which lost every window's draft
 * (BUG-117, S7-3).
 * Modified clicks (ctrl/cmd/middle) fall through to the browser (new tab on
 * the canonical deep-link URL).
 *
 * Views (IMP-127 phase 5): given the note's views as data (`views`, from
 * /api/v1/preview), a table view is shown by ViewTable, whose cells edit
 * the rows, and a board view by ViewBoard, whose cards move between columns;
 * a count view shows its number with ViewCount, and a list view keeps the
 * HTML the server rendered. A `=count(…)` value in the text arrives as a
 * <span class="gosidian-count">, styled here. Each view
 * sits in its `<div class="gosidian-view" data-view="N">` placeholder. An
 * embed (`![[note#Heading]]`) arrives as the included text between a
 * `.gosidian-embed-start` head, which links to its origin, and a
 * `.gosidian-embed-end` rule.
 *
 * Diagrams (IMP-169): a top-level ```diagram block arrives as its code and
 * is drawn by DiagramBlock (plancia-diagrams, loaded on first use). A link
 * of a diagram opens like a wikilink: `diagramLinks`, from /api/v1/preview,
 * says the note each [[link]] resolves to; one that resolves to nothing
 * stays closed, as a dangling wikilink does. A diagram's Mermaid twin, the
 * block right after it for Obsidian, is not shown. Another top-level
 * ```mermaid block stays its code, with «Convert to a diagram» for who may
 * change its note: the block becomes a ```diagram one, opened in the editor.
 * A pipeline note's ```pipeline block is drawn (IMP-169, F4), with «Edit»
 * opening the pipeline window.
 */
import { computed, ref } from 'vue'
import { isAxiosError } from 'axios'
import { sanitizePreviewHtml } from './sanitizePreview'
import { planciaKey, base } from '@/composables/planciaKey'
import type { ViewData } from '@/api/preview'
import ViewTable from '@/components/views/ViewTable.vue'
import ViewBoard from '@/components/views/ViewBoard.vue'
import ViewCount from '@/components/views/ViewCount.vue'
import {
  hasDiagrams,
  hasMermaid,
  hasPipeline,
  hasGraph,
  splitViews,
  type DiagramSegment,
  type MermaidSegment,
  type PipelineSegment,
} from '@/components/views/segments'
import { getNote, updateNote } from '@/api/notes'
import { errorText } from '@/api/errors'
import { useAccessStore } from '@/stores/access'
import { i18n } from '@/locales'
import DiagramBlock from './DiagramBlock.vue'
import PipelineBlock from './PipelineBlock.vue'
import GraphBlock from './GraphBlock.vue'
import { usePreviewLinks } from './usePreviewLinks'

// notePath: the note the HTML belongs to, which relative links start from.
// compact: a pane beside or under the editor, set in the smaller type scale
// (IMP-156, M6): a page-sized h1 took seven lines in 280 px.
const props = defineProps<{
  html: string
  views?: ViewData[]
  notePath?: string
  compact?: boolean
  diagramLinks?: Record<string, string>
  /** Diagrams get «Edit» for who may change the note they are written in. */
  diagramsEditable?: boolean
}>()

const root = ref<HTMLElement | null>(null)
const { onClick, openNote, openWindow } = usePreviewLinks(root, () => props.notePath)
const proseClass = computed(
  () =>
    `gosidian-preview ${props.compact ? 'prose prose-sm' : 'prose'} max-w-none prose-pre:bg-bg-elevated prose-pre:border prose-pre:border-border prose-code:before:hidden prose-code:after:hidden`,
)

const sanitized = computed(() => sanitizePreviewHtml(props.html))

// The note cut at its views and diagrams, when there are some to show as
// components, and at its Mermaid blocks where they may be converted.
const segments = computed(() =>
  props.views?.length ||
  hasDiagrams(sanitized.value) ||
  hasPipeline(sanitized.value) ||
  hasGraph(sanitized.value) ||
  (props.diagramsEditable && hasMermaid(sanitized.value))
    ? splitViews(sanitized.value)
    : null,
)

/** The view at a placeholder's index, when a component of kind `as` shows it. */
function viewAs(index: number, as: 'table' | 'board' | 'count'): ViewData | undefined {
  const v = props.views?.[index]
  return v && !v.error && v.as === as ? v : undefined
}

// The access store only where diagrams may be edited: a caption or a
// canvas card shows none, and needs no store.
let access: ReturnType<typeof useAccessStore> | null = null

/** Whether a diagram may be edited: its own note, or an embed's that resolved. */
function canEditDiagram(s: DiagramSegment | MermaidSegment | PipelineSegment): boolean {
  if (!props.diagramsEditable) return false
  const path = s.origin === undefined ? props.notePath : s.origin
  access ??= useAccessStore()
  return !!path && access.canWrite(path)
}

/**
 * Opens a diagram in the diagram window: its block found in the text of the
 * note it is written in, by its YAML and how many blocks of the same text
 * come before it, so the window's address holds the block's index.
 */
async function editDiagram(s: DiagramSegment) {
  const path = s.origin === undefined ? props.notePath : s.origin
  if (!path) return
  const [{ findDiagramBlocks }, note] = await Promise.all([import('./diagramView'), getNote(path)])
  const same = (a: string) => a === s.source || a.trimEnd() === s.source.trimEnd()
  const blocks = findDiagramBlocks(note.content)
    .map((b, i) => ({ b, i }))
    .filter((x) => same(x.b.source))
  const hit = blocks[s.occurrence] ?? blocks[0]
  if (!hit) return
  openWindow({
    type: 'diagram',
    key: planciaKey('diagram', `${path}#${hit.i}`),
    title: i18n.global.t('diagram.window_title', { name: base(path) }),
    props: { path, index: hit.i },
  })
}

/** Opens a pipeline in the pipeline window: the note it is written in, its one block. */
function editPipeline(s: PipelineSegment) {
  const path = s.origin === undefined ? props.notePath : s.origin
  if (!path) return
  openWindow({
    type: 'pipeline',
    key: planciaKey('pipeline', path),
    title: i18n.global.t('pipeline_editor.window_title', { name: base(path) }),
    props: { path },
  })
}

const converting = ref<number | null>(null)
const convertError = ref<Record<number, string>>({})

/**
 * Converts a Mermaid block to a diagram: found in its note's text as a
 * diagram is (its text, and how many of the same come before), read by
 * Mermaid's parser, and written in place of the block with the note's
 * etag; then the diagram window opens on it, with what was left out.
 */
async function convertMermaid(s: MermaidSegment) {
  const path = s.origin === undefined ? props.notePath : s.origin
  if (!path || converting.value !== null) return
  const t = i18n.global.t
  converting.value = s.index
  convertError.value = { ...convertError.value, [s.index]: '' }
  try {
    const [lib, note] = await Promise.all([import('./diagramView'), getNote(path)])
    const same = (a: string) => a === s.source || a.trimEnd() === s.source.trimEnd()
    const blocks = lib.findDiagramBlocks(note.content, 'mermaid').filter((b) => same(b.source))
    const hit = blocks[s.occurrence] ?? blocks[0]
    if (!hit) throw new Error(t('diagram.not_found'))
    const r = await lib.fromMermaid(hit.source)
    const content = `${note.content.slice(0, hit.start)}\`\`\`diagram\n${r.source}\`\`\`\n${note.content.slice(hit.end)}`
    await updateNote(path, { content, ifMatch: note.etag })
    const index = lib.findDiagramBlocks(content).filter((b) => b.start < hit.start).length
    openWindow({
      type: 'diagram',
      key: planciaKey('diagram', `${path}#${index}`),
      title: t('diagram.window_title', { name: base(path) }),
      props: { path, index, notes: r.warnings },
    })
  } catch (e) {
    const why = isAxiosError(e)
      ? errorText(e, t, t('diagram.convert_failed'))
      : `${t('diagram.convert_failed')}: ${e instanceof Error ? e.message : String(e)}`
    convertError.value = { ...convertError.value, [s.index]: why }
  } finally {
    converting.value = null
  }
}

/** Opens a link of a diagram: a note it resolves to as a window, an address in a tab. */
function openDiagramLink(l: { target: string; kind: 'note' | 'url' }) {
  if (l.kind === 'url') {
    window.open(l.target, '_blank', 'noopener,noreferrer')
    return
  }
  const path = props.diagramLinks?.[l.target]
  if (!path) return
  const i = l.target.indexOf('#')
  openNote(path, i >= 0 ? l.target.slice(i + 1) || null : null)
}

/** Opens a note window; a window already open on the note gets the anchor too, and scrolls. */
</script>

<template>
  <div v-if="!segments" ref="root" :class="proseClass" v-html="sanitized" @click="onClick" />
  <div v-else ref="root" :class="proseClass" @click="onClick">
    <template
      v-for="(s, i) in segments"
      :key="s.kind === 'html' ? `html-${i}` : `${s.kind}-${s.index}`"
    >
      <div v-if="s.kind === 'html'" class="contents" v-html="s.html" />
      <DiagramBlock
        v-else-if="s.kind === 'diagram'"
        :source="s.source"
        :editable="canEditDiagram(s)"
        @link="openDiagramLink"
        @edit="editDiagram(s)"
      />
      <PipelineBlock
        v-else-if="s.kind === 'pipeline'"
        :source="s.source"
        :editable="canEditDiagram(s)"
        @edit="editPipeline(s)"
      />
      <GraphBlock v-else-if="s.kind === 'graph'" :source="s.source" :note-path="notePath" />
      <div v-else-if="s.kind === 'mermaid'" class="gosidian-mermaid" data-mermaid>
        <div class="contents" v-html="s.html" />
        <div v-if="canEditDiagram(s)" class="gosidian-mermaid__tools not-prose">
          <button
            type="button"
            class="gosidian-mermaid__button"
            :title="i18n.global.t('diagram.convert_mermaid_hint')"
            :disabled="converting !== null"
            data-mermaid-convert
            @click="convertMermaid(s)"
          >
            {{ i18n.global.t('diagram.convert_mermaid') }}
          </button>
          <span v-if="convertError[s.index]" class="gosidian-mermaid__error" role="alert">{{
            convertError[s.index]
          }}</span>
        </div>
      </div>
      <div v-else-if="viewAs(s.index, 'table')" class="gosidian-view" :data-view="s.index">
        <ViewTable :view="viewAs(s.index, 'table')!" />
      </div>
      <div v-else-if="viewAs(s.index, 'board')" class="gosidian-view" :data-view="s.index">
        <ViewBoard :view="viewAs(s.index, 'board')!" />
      </div>
      <div v-else-if="viewAs(s.index, 'count')" class="gosidian-view" :data-view="s.index">
        <ViewCount :view="viewAs(s.index, 'count')!" />
      </div>
      <div v-else class="gosidian-view" :data-view="s.index" v-html="s.html" />
    </template>
  </div>
</template>

<style scoped>
/* A second fence behind the sanitizer, which keeps no positioning style and
   none of the app's classes (BUG-117, S7-4): the preview is the containing
   block of what is positioned in it, `fixed` included, and a stacking
   context of its own. */
.gosidian-preview {
  contain: layout;
}
/* A `=count(…)` value in the text: the number, its expression on hover. */
:deep(.gosidian-count) {
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  cursor: help;
  border-bottom: 1px dotted currentColor;
}
:deep(.gosidian-count-error) {
  font-weight: 400;
  color: rgb(var(--color-danger));
}
/* An embed (![[note#Heading]]): the included text sits between a head that
   links to its origin and a closing rule, siblings of the text so the views
   it includes stay editable components. */
:deep(.gosidian-embed-start) {
  margin-top: 1.25em;
  padding-top: 0.25em;
  border-top: 1px dashed rgb(var(--color-text-muted) / 0.5);
  font-size: 0.75rem;
  color: rgb(var(--color-text-muted));
}
:deep(.gosidian-embed-start)::before {
  content: '↪ ';
}
:deep(.gosidian-embed-end) {
  margin-bottom: 1.25em;
  border-top: 1px dashed rgb(var(--color-text-muted) / 0.5);
}
/* A Mermaid block that may become a diagram: its code, the button under it. */
.gosidian-mermaid__tools {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  margin: -0.75em 0 1.25em;
  font-size: 0.75rem;
}
.gosidian-mermaid__button {
  height: 26px;
  padding: 0 0.5rem;
  border: 1px solid rgb(var(--color-border));
  border-radius: 0.25rem;
  background: rgb(var(--color-surface));
  color: rgb(var(--color-text-muted));
}
.gosidian-mermaid__button:hover,
.gosidian-mermaid__button:focus-visible {
  color: rgb(var(--color-text));
  border-color: rgb(var(--color-accent));
}
.gosidian-mermaid__error {
  color: rgb(var(--color-danger));
}
/* The note's first and last blocks sit inside the segment wrappers, out of
   reach of the prose rules that drop their outer margins. */
.contents:first-child > :deep(:first-child) {
  margin-top: 0;
}
.contents:last-child > :deep(:last-child) {
  margin-bottom: 0;
}
</style>
