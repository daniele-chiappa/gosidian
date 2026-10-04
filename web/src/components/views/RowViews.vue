<script setup lang="ts">
/**
 * RowViews — the row views of a note that is a row of a database (IMP-139),
 * below its body, where Obsidian shows the backlinks: one section per view
 * the schema declares (row_views), computed with the note as `this`. A
 * section folds, and is left out when it lists nothing. Each view shows as
 * the note's own views do, through MarkdownPreview: a table or a board that
 * edits the rows, a list as HTML.
 *
 * The rows are other notes, so the views load again when the note changes
 * and, after a pause, when any other note does.
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useDebounceFn } from '@vueuse/core'
import { ChevronDown, ChevronRight } from 'lucide-vue-next'
import { getRowViews, type RowView, type RowViews } from '@/api/notes'
import { useSSE } from '@/composables/useSSE'
import MarkdownPreview from '@/components/domain/MarkdownPreview.vue'

const props = defineProps<{ path: string; etag?: string }>()
const { t } = useI18n()

const data = ref<RowViews | null>(null)

async function load() {
  try {
    data.value = await getRowViews(props.path)
  } catch {
    data.value = null
  }
}
watch(() => [props.path, props.etag], load, { immediate: true })

const reload = useDebounceFn(load, 800)
useSSE(['note']).on('note', (p: { path?: string }) => {
  if (data.value?.views.length && p.path !== props.path) void reload()
})

// Only the views that list something, or say why they could not.
const shown = computed(() =>
  (data.value?.views ?? []).filter((v) => v.view.error || v.view.total > 0),
)

// The folded sections, by database and title, kept in this browser.
const STORAGE_FOLDED = 'gosidian.rowViews.folded'
const folded = ref<string[]>(loadFolded())
function loadFolded(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(STORAGE_FOLDED) ?? '[]')
    return Array.isArray(v) ? v : []
  } catch {
    return []
  }
}
const foldKey = (v: RowView) => `${data.value?.database ?? ''}#${v.title}`
const isFolded = (v: RowView) => folded.value.includes(foldKey(v))
function toggle(v: RowView) {
  const k = foldKey(v)
  folded.value = isFolded(v) ? folded.value.filter((f) => f !== k) : [...folded.value, k]
  try {
    localStorage.setItem(STORAGE_FOLDED, JSON.stringify(folded.value))
  } catch {
    /* private mode: folding lasts for this page */
  }
}

// The view in a placeholder of its own, so MarkdownPreview shows it with
// the components of a note's views.
const placeholder = (v: RowView) => `<div class="gosidian-view" data-view="0">${v.html}</div>`
</script>

<template>
  <section
    v-if="shown.length"
    class="mt-10 pt-4 border-t border-border space-y-5"
    :aria-label="t('views.row_views')"
  >
    <div v-for="v in shown" :key="v.title">
      <button
        type="button"
        class="flex items-center gap-1 text-sm font-semibold text-text-muted hover:text-text"
        :aria-expanded="!isFolded(v)"
        @click="toggle(v)"
      >
        <ChevronRight v-if="isFolded(v)" class="w-4 h-4" />
        <ChevronDown v-else class="w-4 h-4" />
        {{ v.title }}
        <span v-if="!v.view.error" class="font-normal">({{ v.view.total }})</span>
      </button>
      <MarkdownPreview v-if="!isFolded(v)" class="mt-2" :html="placeholder(v)" :views="[v.view]" />
    </div>
  </section>
</template>
