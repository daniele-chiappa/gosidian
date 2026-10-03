<script setup lang="ts">
/**
 * MarkdownPreview — renders the HTML produced by /api/v1/preview inside a
 * sanitized v-html container. Server-side goldmark already escapes raw HTML;
 * DOMPurify is defense-in-depth (strips on*= attrs, javascript: URLs, unknown
 * tags). DOMPurify keeps data-* attributes by default, so the renderer's
 * `data-preview-path` on resolved wikilinks survives.
 *
 * Link interception (plancia): internal links open as windows instead of
 * navigating away from the SPA —
 *   - resolved wikilink (`data-preview-path`) → note window
 *   - tag link (`/tags/<tag>`)               → tags window
 *   - in-note anchor (`#heading`)            → scroll within this preview
 *   - external link                          → open in a new tab
 * Modified clicks (ctrl/cmd/middle) fall through to the browser (new tab on
 * the canonical deep-link URL).
 *
 * Views (IMP-127 phase 5): given the note's views as data (`views`, from
 * /api/v1/preview), a table view is shown by ViewTable, whose cells edit
 * the rows, and a board view by ViewBoard, whose cards move between columns;
 * a list view keeps the HTML the server rendered. Each view
 * sits in its `<div class="gosidian-view" data-view="N">` placeholder.
 */
import { computed, inject, ref } from 'vue'
import DOMPurify from 'dompurify'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { planciaKey } from '@/composables/planciaKey'
import type { ViewData } from '@/api/preview'
import ViewTable from '@/components/views/ViewTable.vue'
import ViewBoard from '@/components/views/ViewBoard.vue'
import { splitViews } from '@/components/views/segments'

const props = defineProps<{ html: string; views?: ViewData[] }>()

const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))
const root = ref<HTMLElement | null>(null)
const proseClass =
  'prose prose-invert max-w-none prose-pre:bg-bg-elevated prose-pre:border prose-pre:border-border prose-code:before:hidden prose-code:after:hidden'

const sanitized = computed(() =>
  DOMPurify.sanitize(props.html, {
    ADD_TAGS: ['math', 'mfrac', 'mrow', 'msup', 'mn', 'mi'],
    ADD_ATTR: ['class', 'data-preview-path', 'data-view'],
  }),
)

// The note cut at its views, when there are views to show as components.
const segments = computed(() => (props.views?.length ? splitViews(sanitized.value) : null))

/** The view at a placeholder's index, when a component of kind `as` shows it. */
function viewAs(index: number, as: 'table' | 'board'): ViewData | undefined {
  const v = props.views?.[index]
  return v && !v.error && v.as === as ? v : undefined
}

function onClick(e: MouseEvent) {
  if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return
  const a = (e.target as HTMLElement | null)?.closest('a')
  if (!a) return

  const previewPath = a.getAttribute('data-preview-path')
  const href = a.getAttribute('href') ?? ''

  if (previewPath) {
    e.preventDefault()
    openWindow({
      type: 'note',
      key: planciaKey('note', previewPath),
      title: (previewPath.split('/').pop() ?? previewPath).replace(/\.md$/, ''),
      props: { path: previewPath },
    })
    return
  }
  if (href.startsWith('/tags/')) {
    e.preventDefault()
    const tag = decodeURIComponent(href.slice('/tags/'.length).split('#')[0] ?? '')
    if (tag) openWindow({ type: 'tags', key: planciaKey('tags', tag), title: `#${tag}`, props: { tag } })
    return
  }
  if (href.startsWith('#')) {
    e.preventDefault()
    const id = href.slice(1)
    root.value?.querySelector(`#${CSS.escape(id)}`)?.scrollIntoView({ behavior: 'smooth' })
    return
  }
  if (href.startsWith('/notes/new')) {
    // Unresolved wikilink → nothing to open yet; swallow to stay in the SPA.
    e.preventDefault()
    return
  }
  if (/^https?:\/\//i.test(href)) {
    e.preventDefault()
    window.open(href, '_blank', 'noopener,noreferrer')
  }
}
</script>

<template>
  <div
    v-if="!segments"
    ref="root"
    :class="proseClass"
    v-html="sanitized"
    @click="onClick"
  />
  <div v-else ref="root" :class="proseClass" @click="onClick">
    <template v-for="(s, i) in segments" :key="s.kind === 'view' ? `view-${s.index}` : `html-${i}`">
      <div v-if="s.kind === 'html'" class="contents" v-html="s.html" />
      <div v-else-if="viewAs(s.index, 'table')" class="gosidian-view" :data-view="s.index">
        <ViewTable :view="viewAs(s.index, 'table')!" />
      </div>
      <div v-else-if="viewAs(s.index, 'board')" class="gosidian-view" :data-view="s.index">
        <ViewBoard :view="viewAs(s.index, 'board')!" />
      </div>
      <div v-else class="gosidian-view" :data-view="s.index" v-html="s.html" />
    </template>
  </div>
</template>

<style scoped>
/* The note's first and last blocks sit inside the segment wrappers, out of
   reach of the prose rules that drop their outer margins. */
.contents:first-child > :deep(:first-child) {
  margin-top: 0;
}
.contents:last-child > :deep(:last-child) {
  margin-bottom: 0;
}
</style>
