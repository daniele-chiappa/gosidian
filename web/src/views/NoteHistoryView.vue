<script setup lang="ts">
/** NoteHistoryView — git history of a note as a plancia window. `path` comes
 *  from window props; the back link opens the note read window. */
import { useI18n } from 'vue-i18n'
import { onMounted, ref, watch, computed, inject } from 'vue'
import { getHistory, type HistoryEntry } from '@/api/history'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { planciaKey, base } from '@/composables/planciaKey'
import { errorText } from '@/api/errors'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'
import DateTime from '@/components/primitives/DateTime.vue'

const { t } = useI18n()

const props = defineProps<{ path: string }>()

const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))

const path = computed(() => props.path)
const entries = ref<HistoryEntry[]>([])
const loading = ref(false)
const error = ref<string | null>(null)

async function load() {
  if (!path.value) return
  loading.value = true
  error.value = null
  try {
    entries.value = await getHistory(path.value, 100)
  } catch (e) {
    error.value = errorText(e, t, t('history.load_failed'))
    entries.value = []
  } finally {
    loading.value = false
  }
}

function openNote() {
  openWindow({
    type: 'note',
    key: planciaKey('note', path.value),
    title: base(path.value),
    props: { path: path.value },
  })
}

onMounted(load)
watch(path, load)
</script>

<template>
  <div class="p-6 max-w-4xl mx-auto">
    <header class="flex items-center gap-3 mb-4">
      <button
        type="button"
        class="text-sm text-text-muted hover:text-text"
        @click="openNote"
      >{{ t('history.note') }}</button>
      <!-- The window's title bar names it: the heading is for screen readers. -->
      <h1 class="sr-only">{{ t('history.title') }}</h1>
      <span class="font-mono text-sm text-text-muted truncate">{{ path }}</span>
    </header>

    <p v-if="loading" class="text-text-muted">{{ t('common.loading') }}</p>
    <ErrorMessage v-else-if="error" :text="error" />
    <p v-else-if="!entries.length" class="text-text-muted text-sm">
      {{ t('history.empty') }}
    </p>

    <ol v-else class="space-y-2">
      <li
        v-for="e in entries"
        :key="e.sha"
        class="rounded border border-border bg-surface px-4 py-3"
      >
        <div class="flex items-baseline gap-3 text-xs">
          <code class="font-mono text-accent">{{ e.short_sha }}</code>
          <DateTime class="text-text-muted" :value="e.date" />
          <span class="text-text-muted">{{ e.author }}</span>
        </div>
        <p class="text-sm mt-1">{{ e.subject }}</p>
      </li>
    </ol>
  </div>
</template>
