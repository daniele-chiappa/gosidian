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

const { t } = useI18n()

const props = defineProps<{ q?: string; project?: string }>()

const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))

const query = ref<string>(props.q ?? '')
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
    return
  }
  loading.value = true
  error.value = null
  try {
    const found = await search({ q, project: p || undefined, limit: 50 })
    if (mine !== gen) return
    hits.value = found
    lastSubmitted.value = q
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
    <h1 class="sr-only">{{ t('search.title') }}</h1>

    <div class="flex gap-2 mb-6">
      <input
        v-model="query"
        type="search"
        autofocus
        :placeholder="t('search.placeholder')"
        class="h-control py-0 flex-1 rounded bg-bg-elevated border border-border px-3 focus:outline-none focus:ring-2 focus:ring-focus"
      />
      <input
        v-model="project"
        type="text"
        :placeholder="t('search.project_placeholder')"
        class="h-control py-0 w-48 min-w-0 shrink rounded bg-bg-elevated border border-border px-3 text-sm focus:outline-none focus:ring-2 focus:ring-focus"
      />
    </div>

    <p v-if="loading" class="text-text-muted text-sm">{{ t('search.searching') }}</p>
    <ErrorMessage v-else-if="error" :text="error" class="text-sm" />
    <p
      v-else-if="lastSubmitted && hits.length === 0"
      class="text-text-muted text-sm"
    >
      <i18n-t keypath="search.no_matches" scope="global">
        <template #query><strong class="font-mono">{{ lastSubmitted }}</strong></template>
      </i18n-t>
    </p>

    <ul v-if="hits.length" class="space-y-3">
      <li
        v-for="hit in hits"
        :key="hit.path"
        class="rounded border border-border bg-surface px-4 py-3"
      >
        <button
          type="button"
          class="font-medium hover:text-accent text-left"
          @click="openHit(hit)"
        >{{ hit.title || hit.path }}</button>
        <p class="text-xs text-text-muted font-mono mt-0.5">{{ hit.path }}</p>
        <p
          v-if="hit.snippet"
          class="text-sm text-text-muted mt-2 line-clamp-2"
        >{{ plainSnippet(hit.snippet) }}</p>
      </li>
    </ul>
  </div>
</template>
