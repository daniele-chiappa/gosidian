<script setup lang="ts">
/** TagsView — tag browser as a plancia window. The selected tag is local
 *  state (initialised from the `tag` window prop); picking a tag on the left
 *  browses within this window, picking a note opens it as a sibling window. */
import { useI18n } from 'vue-i18n'
import { onMounted, ref, watch, inject } from 'vue'
import { listTags, notesByTag, type TagCount, type NoteSummary } from '@/api/tags'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { planciaKey } from '@/composables/planciaKey'
import { errorText } from '@/api/errors'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'

const { t } = useI18n()

const props = defineProps<{ tag?: string }>()

const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))

const tags = ref<TagCount[]>([])
const notes = ref<NoteSummary[]>([])
const loading = ref(false)
// The list and a tag's notes fail apart: an error on the notes took the
// place of the list for good (S6-12).
const error = ref<string | null>(null)
const notesError = ref<string | null>(null)
const selectedTag = ref<string>(props.tag ?? '')

async function loadTags() {
  loading.value = true
  error.value = null
  try {
    tags.value = await listTags()
  } catch (e) {
    error.value = errorText(e, t, t('tags.load_failed'))
  } finally {
    loading.value = false
  }
}

// Only the answer for the last tag picked is shown.
let notesSeq = 0
async function loadNotes(tag: string) {
  const seq = ++notesSeq
  notesError.value = null
  if (!tag) {
    notes.value = []
    return
  }
  try {
    const got = await notesByTag(tag)
    if (seq === notesSeq) notes.value = got
  } catch (e) {
    if (seq !== notesSeq) return
    notes.value = []
    notesError.value = errorText(e, t, t('tags.notes_failed'))
  }
}

function openNote(n: NoteSummary) {
  openWindow({
    type: 'note',
    key: planciaKey('note', n.path),
    title: n.title || n.path,
    props: { path: n.path },
  })
}

onMounted(async () => {
  await loadTags()
  if (selectedTag.value) await loadNotes(selectedTag.value)
})

watch(selectedTag, (t) => {
  if (t) void loadNotes(t)
})
</script>

<template>
  <div class="flex flex-wrap gap-8 p-6">
    <aside class="w-56 shrink-0">
      <!-- The window's title bar shows the name: the heading is for screen readers. -->
      <h1 class="sr-only">{{ t('tags.title') }}</h1>
      <p v-if="loading" class="text-text-muted text-sm">{{ t('common.loading') }}</p>
      <div v-else-if="error" class="space-y-2">
        <ErrorMessage :text="error" class="text-sm" />
        <button type="button" class="text-sm text-accent hover:underline" @click="loadTags">
          {{ t('common.retry') }}
        </button>
      </div>
      <ul v-else class="space-y-1">
        <li v-for="t in tags" :key="t.tag">
          <button
            type="button"
            class="w-full flex justify-between items-center px-2 py-1 rounded hover:bg-surface-hover text-left"
            :class="selectedTag === t.tag ? 'bg-surface-hover' : ''"
            @click="selectedTag = t.tag"
          >
            <span class="truncate text-sm">#{{ t.tag }}</span>
            <span class="text-xs text-text-muted">{{ t.count }}</span>
          </button>
        </li>
      </ul>
    </aside>

    <section class="min-w-[16rem] flex-1">
      <template v-if="selectedTag">
        <h2 class="text-lg font-semibold mb-3">
          {{ t('tags.notes_tagged') }} <span class="text-accent">#{{ selectedTag }}</span>
        </h2>
        <div v-if="notesError" class="space-y-2">
          <ErrorMessage :text="notesError" class="text-sm" />
          <button type="button" class="text-sm text-accent hover:underline" @click="loadNotes(selectedTag)">
            {{ t('common.retry') }}
          </button>
        </div>
        <ul v-else-if="notes.length" class="space-y-2">
          <li
            v-for="n in notes"
            :key="n.path"
            class="rounded border border-border bg-surface px-3 py-2"
          >
            <button
              type="button"
              class="font-medium hover:text-accent text-left"
              @click="openNote(n)"
            >{{ n.title || n.path }}</button>
            <p class="text-xs text-text-muted font-mono">{{ n.path }}</p>
          </li>
        </ul>
        <p v-else class="text-text-muted text-sm">{{ t('tags.no_notes') }}</p>
      </template>
      <p v-else class="text-text-muted text-sm">
        {{ t('tags.pick') }}
      </p>
    </section>
  </div>
</template>
