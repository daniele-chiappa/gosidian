<script setup lang="ts">
/** SearchView — full-text search as a plancia window. Local query state (the
 *  plancia owns the URL); hits open as note windows. */
import { ref, watch, onMounted, inject } from 'vue'
import { useDebounceFn } from '@vueuse/core'
import { search, type SearchHit } from '@/api/search'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { planciaKey } from '@/composables/planciaKey'

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
    error.value = e instanceof Error ? e.message : 'Search failed'
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
    <h1 class="text-xl font-semibold mb-4">Search</h1>

    <div class="flex gap-2 mb-6">
      <input
        v-model="query"
        type="search"
        autofocus
        placeholder="Full-text search…"
        class="flex-1 rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-accent"
      />
      <input
        v-model="project"
        type="text"
        placeholder="project filter (optional)"
        class="w-48 rounded bg-bg-elevated border border-border px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-accent"
      />
    </div>

    <p v-if="loading" class="text-text-muted text-sm">Searching…</p>
    <p v-else-if="error" class="text-danger text-sm">{{ error }}</p>
    <p
      v-else-if="lastSubmitted && hits.length === 0"
      class="text-text-muted text-sm"
    >
      No matches for <strong class="font-mono">{{ lastSubmitted }}</strong>.
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
        >{{ hit.snippet }}</p>
      </li>
    </ul>
  </div>
</template>
