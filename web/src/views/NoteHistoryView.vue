<script setup lang="ts">
/** NoteHistoryView — git history of a note as a plancia window. `path` comes
 *  from window props; the back link opens the note read window. A version
 *  opens under its entry (IMP-222, F-01): the diff from it to the note now
 *  first, its text on request, and for who may write the note, the restore
 *  after a confirmation that starts on Cancel. */
import { useI18n } from 'vue-i18n'
import { onMounted, ref, watch, computed, inject, nextTick } from 'vue'
import { getHistory, getVersion, restoreVersion, type HistoryEntry, type Version } from '@/api/history'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { planciaKey, base } from '@/composables/planciaKey'
import { errorText } from '@/api/errors'
import { formatDateTime } from '@/api/format'
import { useAccessStore } from '@/stores/access'
import { confirmAction } from '@/composables/useConfirm'
import { announce } from '@/composables/useAnnounce'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'
import DateTime from '@/components/primitives/DateTime.vue'
import { parseDiff, type DiffLine } from './historyDiff'

const { t } = useI18n()

const props = defineProps<{ path: string }>()

const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))
const access = useAccessStore()

const root = ref<HTMLElement | null>(null)
const path = computed(() => props.path)
const entries = ref<HistoryEntry[]>([])
const loading = ref(false)
const error = ref<string | null>(null)

// The version open, one at a time: its sha, what the server gave, and the
// answer awaited (a click on another entry drops the older one).
const open = ref<string | null>(null)
const version = ref<Version | null>(null)
const versionLoading = ref(false)
const versionError = ref<string | null>(null)
const showText = ref(false)
const restoring = ref(false)
const restoreError = ref<string | null>(null)
const commitNotice = ref<string | null>(null)
let asked = 0

const lines = computed<DiffLine[]>(() => parseDiff(version.value?.diff ?? ''))
const canRestore = computed(
  () => !!version.value && version.value.content !== undefined && !version.value.same && access.canWrite(path.value),
)

async function load() {
  if (!path.value) return
  // Read again after a restore, the list stays until the new one comes.
  loading.value = !entries.value.length
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

async function loadVersion(sha: string) {
  const n = ++asked
  versionLoading.value = true
  versionError.value = null
  try {
    const v = await getVersion(path.value, sha)
    if (n === asked) version.value = v
  } catch (e) {
    if (n === asked) versionError.value = errorText(e, t, t('history.version_failed'))
  } finally {
    if (n === asked) versionLoading.value = false
  }
}

function toggle(e: HistoryEntry) {
  restoreError.value = null
  commitNotice.value = null
  showText.value = false
  version.value = null
  if (open.value === e.sha) {
    open.value = null
    asked++
    return
  }
  open.value = e.sha
  void loadVersion(e.sha)
}

async function restore() {
  const v = version.value
  if (!v) return
  const ok = await confirmAction(t('history.confirm_restore', { date: formatDateTime(v.date) }), {
    confirmLabel: t('history.restore'),
  })
  if (!ok) return
  restoring.value = true
  restoreError.value = null
  try {
    const res = await restoreVersion(path.value, v.sha, v.etag)
    announce(t('history.restored'))
    open.value = null
    version.value = null
    await load()
    commitNotice.value = res.commit_error ? t('history.commit_failed', { error: res.commit_error }) : null
    // The new version is the top of the list: the focus goes there.
    await nextTick()
    const top = res.commit?.sha ?? entries.value[0]?.sha
    if (top) root.value?.querySelector<HTMLElement>(`button[data-sha="${top}"]`)?.focus()
  } catch (e) {
    restoreError.value = errorText(e, t, t('history.restore_failed'))
    // The note changed meanwhile, or the version: the diff as it is now.
    await loadVersion(v.sha)
  } finally {
    restoring.value = false
  }
}

/** "daniele <gosidian@localhost>" shows as daniele, the whole in the tooltip. */
function authorName(author: string): string {
  return author.replace(/\s*<[^>]*>\s*$/, '') || author
}

const omittedText = computed(() => {
  switch (version.value?.omitted) {
    case 'deleted':
      return t('history.omitted_deleted')
    case 'too_large':
      return t('history.omitted_too_large')
    case 'not_text':
      return t('history.omitted_not_text')
  }
  return ''
})

const LINE_CLASS: Record<DiffLine['kind'], string> = {
  hunk: 'text-text-muted mt-1 first:mt-0',
  same: '',
  removed: 'bg-danger/15',
  added: 'bg-success/15',
  note: 'text-text-muted italic',
}
// The marks in the text's colour, bold: the line's tint says red or green,
// and on it Latte's red and green read under 4.5:1 (the green 2.55).
const MARK: Record<DiffLine['kind'], string> = { hunk: '', same: ' ', removed: '−', added: '+', note: '' }

function openNote() {
  openWindow({
    type: 'note',
    key: planciaKey('note', path.value),
    title: base(path.value),
    props: { path: path.value },
  })
}

onMounted(load)
watch(path, () => {
  open.value = null
  version.value = null
  entries.value = []
  void load()
})
</script>

<template>
  <div ref="root" class="p-6 max-w-4xl mx-auto">
    <header class="flex items-center gap-3 mb-4">
      <button
        type="button"
        class="text-sm text-text-muted hover:text-text"
        @click="openNote"
      >{{ t('history.note') }}</button>
      <!-- The window's title bar names it: the heading is for screen readers. -->
      <h1
        class="sr-only"
        tabindex="-1"
        data-window-heading
      >
        {{ t('history.title') }}
      </h1>
      <span class="font-mono text-sm text-text-muted truncate">{{ path }}</span>
    </header>

    <p v-if="commitNotice" class="mb-3 text-sm text-warning">{{ commitNotice }}</p>

    <p v-if="loading" class="text-text-muted">{{ t('common.loading') }}</p>
    <ErrorMessage v-else-if="error" :text="error" />
    <p v-else-if="!entries.length" class="text-text-muted text-sm">
      {{ t('history.empty') }}
    </p>

    <ol v-else class="space-y-2">
      <li
        v-for="e in entries"
        :key="e.sha"
        class="rounded border border-border bg-surface"
      >
        <button
          type="button"
          class="w-full text-left px-4 py-3 rounded hover:bg-surface-hover focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
          :data-sha="e.sha"
          :aria-expanded="open === e.sha ? 'true' : 'false'"
          :aria-controls="`version-${e.sha}`"
          @click="toggle(e)"
        >
          <span class="flex items-baseline gap-3 text-xs">
            <code class="font-mono text-accent">{{ e.short_sha }}</code>
            <DateTime class="text-text-muted" :value="e.date" />
            <span class="text-text-muted" :title="e.author">{{ authorName(e.author) }}</span>
          </span>
          <span class="block text-sm mt-1">{{ e.subject }}</span>
        </button>

        <div
          v-if="open === e.sha"
          :id="`version-${e.sha}`"
          class="border-t border-border px-4 py-3 space-y-3"
        >
          <p v-if="versionLoading" class="text-text-muted text-sm">{{ t('common.loading') }}</p>
          <ErrorMessage v-else-if="versionError" :text="versionError" />
          <template v-else-if="version">
            <p v-if="version.path !== path" class="text-xs text-text-muted">
              {{ t('history.then_named', { path: version.path }) }}
            </p>
            <p v-if="version.omitted" class="text-sm text-text-muted">{{ omittedText }}</p>
            <template v-else>
              <p v-if="version.same" class="text-sm text-text-muted">{{ t('history.same') }}</p>
              <section v-else :aria-labelledby="`diff-${e.sha}`">
                <h2 :id="`diff-${e.sha}`" class="text-xs font-semibold text-text-muted mb-1">
                  {{ t('history.diff_title') }}
                </h2>
                <div class="font-mono text-xs leading-5 overflow-x-auto rounded border border-border bg-bg p-2">
                  <div
                    v-for="(l, i) in lines"
                    :key="i"
                    :data-line="l.kind"
                    class="whitespace-pre-wrap break-words"
                    :class="LINE_CLASS[l.kind]"
                    :aria-hidden="l.kind === 'hunk' ? 'true' : undefined"
                  ><span
                    v-if="l.kind === 'removed'"
                    class="sr-only"
                  >{{ t('history.removed') }}: </span><span
                    v-else-if="l.kind === 'added'"
                    class="sr-only"
                  >{{ t('history.added') }}: </span><span
                    v-if="MARK[l.kind]"
                    aria-hidden="true"
                    class="inline-block w-4 select-none font-bold"
                  >{{ MARK[l.kind] }}</span>{{ l.text }}</div>
                </div>
              </section>
              <ErrorMessage v-if="restoreError" :text="restoreError" />
              <div class="flex flex-wrap gap-2">
                <button
                  type="button"
                  class="text-sm rounded border border-border px-3 py-1 hover:bg-surface-hover"
                  :aria-expanded="showText ? 'true' : 'false'"
                  :aria-controls="`text-${e.sha}`"
                  @click="showText = !showText"
                >{{ t('history.show_text') }}</button>
                <button
                  v-if="canRestore"
                  type="button"
                  class="text-sm rounded bg-accent text-accent-fg px-3 py-1 hover:bg-accent-hover disabled:opacity-60"
                  :disabled="restoring"
                  :aria-busy="restoring ? 'true' : undefined"
                  @click="restore"
                >{{ t('history.restore') }}</button>
              </div>
              <pre
                v-if="showText"
                :id="`text-${e.sha}`"
                data-version-text
                class="font-mono text-xs whitespace-pre-wrap break-words rounded border border-border bg-bg p-2 max-h-96 overflow-auto"
              >{{ version.content }}</pre>
            </template>
          </template>
        </div>
      </li>
    </ol>
  </div>
</template>
