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
 */
import { computed, inject, ref } from 'vue'
import { sanitizePreviewHtml } from './sanitizePreview'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { planciaKey } from '@/composables/planciaKey'
import type { ViewData } from '@/api/preview'
import ViewTable from '@/components/views/ViewTable.vue'
import ViewBoard from '@/components/views/ViewBoard.vue'
import ViewCount from '@/components/views/ViewCount.vue'
import { splitViews } from '@/components/views/segments'
import { findHeading } from './headings'
import { linkTarget } from './noteLinks'

// notePath: the note the HTML belongs to, which relative links start from.
const props = defineProps<{ html: string; views?: ViewData[]; notePath?: string }>()

const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))
const root = ref<HTMLElement | null>(null)
const proseClass =
  'gosidian-preview prose max-w-none prose-pre:bg-bg-elevated prose-pre:border prose-pre:border-border prose-code:before:hidden prose-code:after:hidden'

const sanitized = computed(() => sanitizePreviewHtml(props.html))

// The note cut at its views, when there are views to show as components.
const segments = computed(() => (props.views?.length ? splitViews(sanitized.value) : null))

/** The view at a placeholder's index, when a component of kind `as` shows it. */
function viewAs(index: number, as: 'table' | 'board' | 'count'): ViewData | undefined {
  const v = props.views?.[index]
  return v && !v.error && v.as === as ? v : undefined
}

/** The decoded fragment of an href, without the #; null when there is none. */
function fragment(href: string): string | null {
  const i = href.indexOf('#')
  if (i < 0 || i === href.length - 1) return null
  try {
    return decodeURIComponent(href.slice(i + 1))
  } catch {
    return href.slice(i + 1)
  }
}

function onClick(e: MouseEvent) {
  if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return
  const a = (e.target as HTMLElement | null)?.closest('a')
  if (!a) return

  const previewPath = a.getAttribute('data-preview-path')
  const href = a.getAttribute('href') ?? ''

  if (previewPath) {
    e.preventDefault()
    // A link to a heading opens the note there (IMP-140): the heading as
    // written, else the fragment of the href.
    openNote(previewPath, a.getAttribute('data-heading') ?? fragment(href))
    return
  }
  if (href.startsWith('/tags/')) {
    e.preventDefault()
    const tag = decodeURIComponent(href.slice('/tags/'.length).split('#')[0] ?? '')
    if (tag)
      openWindow({ type: 'tags', key: planciaKey('tags', tag), title: `#${tag}`, props: { tag } })
    return
  }
  if (href.startsWith('#')) {
    e.preventDefault()
    const anchor = a.getAttribute('data-heading') ?? fragment(href) ?? ''
    if (root.value) findHeading(root.value, anchor)?.scrollIntoView({ behavior: 'smooth' })
    return
  }
  if (href.startsWith('/notes/new')) {
    // Unresolved wikilink → nothing to open yet; swallow to stay in the SPA.
    e.preventDefault()
    return
  }
  if (a.hasAttribute('download')) return
  const target = linkTarget(href, props.notePath)
  if (target.kind === 'browser') return // mailto:, tel:, an app's scheme: the browser's
  e.preventDefault()
  if (target.kind === 'note') openNote(target.path, target.anchor ?? null)
  else if (target.kind === 'tab') window.open(target.url, '_blank', 'noopener,noreferrer')
}

/** Opens a note window; a window already open on the note gets the anchor too, and scrolls. */
function openNote(path: string, anchor: string | null) {
  const key = planciaKey('note', path)
  const props = anchor ? { path, anchor, anchorAt: Date.now() } : { path }
  const id = openWindow({
    type: 'note',
    key,
    title: (path.split('/').pop() ?? path).replace(/\.(md|html)$/, ''),
    props,
  })
  if (anchor && id) store.identify(id, key, props)
}
</script>

<template>
  <div v-if="!segments" ref="root" :class="proseClass" v-html="sanitized" @click="onClick" />
  <div v-else ref="root" :class="proseClass" @click="onClick">
    <template v-for="(s, i) in segments" :key="s.kind === 'view' ? `view-${s.index}` : `html-${i}`">
      <div v-if="s.kind === 'html'" class="contents" v-html="s.html" />
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
/* The note's first and last blocks sit inside the segment wrappers, out of
   reach of the prose rules that drop their outer margins. */
.contents:first-child > :deep(:first-child) {
  margin-top: 0;
}
.contents:last-child > :deep(:last-child) {
  margin-bottom: 0;
}
</style>
