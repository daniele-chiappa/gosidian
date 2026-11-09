<script setup lang="ts">
/** SearchView — full-text search as a plancia window. Local query state (the
 *  plancia owns the URL); hits open as note windows. */
import { useI18n } from 'vue-i18n'
import { ref, watch, onMounted, inject } from 'vue'
import { useDebounceFn } from '@vueuse/core'
import { search, type SearchHit } from '@/api/search'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { planciaKey } from '@/composables/planciaKey'
import { errorText } from '@/api/errors'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'
import { plainSnippet } from '@/views/searchSnippet'
import { announce } from '@/composables/useAnnounce'
import { openPalette } from '@/composables/usePalette'
import { shortcutLabel } from '@/composables/platform'

const { t } = useI18n()

// q is a text to search, qAt when it was asked: the palette hands one to a
// Search already open (IMP-218, F-06).
const props = defineProps<{ q?: string; qAt?: number; project?: string }>()

const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))
// The field takes the focus at mount only in the active window, as /search
// opens it: a reload that brought Search back beside the focused window put
// the focus in Search, away from the window the plancia showed as active.
const autofocus = store.focused?.type === 'search'

const query = ref<string>(props.q ?? '')
const queryEl = ref<HTMLInputElement | null>(null)
const project = ref<string>(props.project ?? '')
const hits = ref<SearchHit[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const lastSubmitted = ref('')
// Only the last search fills the list: a slow answer to an older query
// replaced the one typed after (BUG-116, S6-10).
let gen = 0

async function run(q: string, p: string) {
  const mine = ++gen
  if (!q.trim()) {
    hits.value = []
    error.value = null
    loading.value = false
    // A field cleared said there was nothing for the text it held.
    lastSubmitted.value = ''
    return
  }
  loading.value = true
  error.value = null
  try {
    const found = await search({ q, project: p || undefined, limit: 50 })
    if (mine !== gen) return
    hits.value = found
    lastSubmitted.value = q
    // The list changes under the field: a screen reader hears how (IMP-217, F-10).
    announce(found.length ? t('search.count', { n: found.length }, found.length) : t('search.no_matches', { query: q }))
  } catch (e) {
    if (mine !== gen) return
    error.value = errorText(e, t, t('search.failed'))
    hits.value = []
  } finally {
    if (mine === gen) loading.value = false
  }
}

const debounced = useDebounceFn(() => void run(query.value, project.value), 300)
watch([query, project], debounced)
// The text asked replaces the field, even the same text over what was typed.
// The text the field already holds searches again: the field's own watch
// does not see it, and a note written since may hold it (BUG-159).
watch(
  () => [props.q, props.qAt],
  () => {
    const q = props.q ?? ''
    if (query.value === q) void debounced()
    else query.value = q
  },
)

// What a search that found nothing offers (IMP-218, F-26): the same text
// in the names, in the palette that Ctrl+K opens, and without the filter.
const paletteKeys = shortcutLabel('K')
function clearProject() {
  project.value = ''
  // The button goes with the filter: the focus goes on to the text.
  queryEl.value?.focus()
}

// Two notes titled alike are told apart by their project, shown with
// every hit so it stays in one place from a search to the next.
const projectOf = (path: string) => (path.includes('/') ? path.slice(0, path.indexOf('/')) : '')

function openHit(hit: SearchHit) {
  openWindow({
    type: 'note',
    key: planciaKey('note', hit.path),
    title: hit.title || hit.path,
    props: { path: hit.path },
  })
}

onMounted(() => {
  if (query.value) void run(query.value, project.value)
})
</script>

<template>
  <div class="p-6 max-w-3xl mx-auto">
    <!-- The window's title bar shows the name: the heading is for screen readers. -->
    <h1
      class="sr-only"
      tabindex="-1"
      data-window-heading
    >
      {{ t('search.title') }}
    </h1>

    <div class="flex gap-2 mb-6">
      <input
        ref="queryEl"
        v-model="query"
        type="search"
        :autofocus="autofocus"
        :placeholder="t('search.placeholder')"
        :aria-label="t('search.query_label')"
        class="h-control py-0 flex-1 rounded bg-bg-elevated border border-border-input px-3 focus:outline-none focus:ring-2 focus:ring-focus"
      />
      <input
        v-model="project"
        type="text"
        :placeholder="t('search.project_placeholder')"
        :aria-label="t('search.project_label')"
        class="h-control py-0 w-48 min-w-0 shrink rounded bg-bg-elevated border border-border-input px-3 text-sm focus:outline-none focus:ring-2 focus:ring-focus"
      />
    </div>

    <p v-if="loading" class="text-text-muted text-sm">{{ t('search.searching') }}</p>
    <ErrorMessage v-else-if="error" :text="error" class="text-sm" />
    <div
      v-else-if="lastSubmitted && hits.length === 0"
      class="text-text-muted text-sm space-y-2"
    >
      <p>
        <i18n-t keypath="search.no_matches" scope="global">
          <template #query><strong class="font-mono">{{ lastSubmitted }}</strong></template>
        </i18n-t>
      </p>
      <p>{{ t('search.tip_spelling') }}</p>
      <div class="flex flex-wrap gap-2">
        <button
          v-if="project.trim()"
          type="button"
          data-testid="search-clear-project"
          class="h-control-sm text-xs px-2 rounded border border-border hover:bg-surface-hover"
          @click="clearProject"
        >
          {{ t('search.clear_project') }}
        </button>
        <button
          type="button"
          data-testid="search-in-titles"
          class="h-control-sm text-xs px-2 rounded border border-border hover:bg-surface-hover inline-flex items-center gap-1"
          @click="openPalette(lastSubmitted)"
        >
          {{ t('search.in_titles') }}
          <kbd class="text-text-muted">{{ paletteKeys }}</kbd>
        </button>
      </div>
    </div>
    <p
      v-else-if="hits.length"
      data-testid="search-count"
      class="text-text-muted text-sm mb-3"
    >
      {{ t('search.count', { n: hits.length }, hits.length) }}
    </p>

    <ul v-if="hits.length" class="space-y-3">
      <li
        v-for="hit in hits"
        :key="hit.path"
        class="rounded border border-border bg-surface px-4 py-3"
      >
        <div class="flex items-baseline gap-2">
          <button
            type="button"
            class="font-medium hover:text-accent text-left min-w-0"
            @click="openHit(hit)"
          >{{ hit.title || hit.path }}</button>
          <span
            v-if="projectOf(hit.path)"
            data-testid="search-project"
            class="shrink-0 text-xs px-1.5 rounded border border-border text-text-muted"
          >{{ projectOf(hit.path) }}</span>
        </div>
        <p class="text-xs text-text-muted font-mono mt-0.5">{{ hit.path }}</p>
        <p
          v-if="hit.missing?.length"
          class="text-xs text-text-muted mt-0.5"
          data-testid="search-missing"
        >{{ t('search.missing', { words: hit.missing.join(', ') }) }}</p>
        <p
          v-if="hit.snippet"
          class="text-sm text-text-muted mt-2 line-clamp-2"
        >{{ plainSnippet(hit.snippet) }}</p>
      </li>
    </ul>
  </div>
</template>
