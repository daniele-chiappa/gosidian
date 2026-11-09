<script setup lang="ts">
/**
 * BoardHead — the head of a ```board block's board (IMP-216 phase 2), in a
 * line that wraps: its iteration in progress (goal, days left, cards done)
 * with the switch that leaves only its cards on the board, or the form of
 * a new one; the retrospectives to write; the three measures of its flow,
 * each explained in its title. The words are the board's own catalogue,
 * which ViewBoard loads (useBoardCatalog).
 *
 * A new iteration is a note type: iteration in the project's iterations/
 * folder, with the board, a goal and its first and last day; it opens as a
 * window, and the board shows it once its note is read.
 */
import { computed, inject, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import axios from 'axios'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { createNote } from '@/api/notes'
import type { ViewBoardState } from '@/api/preview'
import { base, noteHref, planciaKey } from '@/composables/planciaKey'

const props = defineProps<{
  state: ViewBoardState
  /** The board's note. */
  board: string
  /** Whether the reader may make an iteration. */
  canCreate: boolean
  onlyIteration: boolean
}>()
const emit = defineEmits<{ 'update:onlyIteration': [on: boolean] }>()
const { t, locale } = useI18n()
const openWindow = inject<((spec: OpenSpec) => string) | null>('openWindow', null)

const decimal = (n: number) =>
  new Intl.NumberFormat(String(locale.value), { maximumFractionDigits: 1 }).format(n)
const cycle = computed(() => {
  const c = props.state.flow.cycle_days
  return c.p50 === undefined
    ? t('board.head.cycle_few')
    : t('board.head.cycle', { p50: decimal(c.p50), p85: decimal(c.p85 ?? c.p50) })
})
const weeks = computed(() => props.state.flow.closed_per_week.join(' · '))
const stale = computed(() => props.state.flow.stale?.length ?? 0)
const iteration = computed(() => props.state.iteration)

// The form of a new iteration: two weeks from today by default.
const day = (d: Date) =>
  `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
const adding = ref(false)
const title = ref('')
const goal = ref('')
const start = ref('')
const end = ref('')
const creating = ref(false)
const message = ref('')
function openForm() {
  const today = new Date()
  start.value = day(today)
  end.value = day(new Date(today.getFullYear(), today.getMonth(), today.getDate() + 13))
  title.value = `${t('board.head.iteration')} ${start.value}`
  goal.value = ''
  message.value = ''
  adding.value = true
}
const project = computed(() => props.board.split('/')[0] ?? '')
const slug = (s: string) =>
  s
    .normalize('NFD')
    .replace(/[̀-ͯ]/g, '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
const yaml = (s: string) => JSON.stringify(s.trim())

async function create() {
  if (creating.value || !title.value.trim() || !start.value || !end.value) return
  const path = `${project.value}/iterations/${slug(title.value) || start.value}.md`
  const lines = [
    '---',
    `title: ${yaml(title.value)}`,
    'type: iteration',
    `board: ${yaml(`[[${props.board.replace(/\.md$/, '')}]]`)}`,
    ...(goal.value.trim() ? [`goal: ${yaml(goal.value)}`] : []),
    `start: ${start.value}`,
    `end: ${end.value}`,
    `tags: [${project.value}]`,
    '---',
    '',
    `# ${title.value.trim()}`,
    '',
  ]
  creating.value = true
  message.value = ''
  try {
    const note = await createNote(path, lines.join('\n'))
    const spec: OpenSpec = { type: 'note', key: planciaKey('note', note.path), title: base(note.path), props: { path: note.path } }
    if (openWindow) openWindow(spec)
    else useWindowsStore().open(spec)
    adding.value = false
  } catch (e) {
    const reason =
      axios.isAxiosError(e) && e.response
        ? String((e.response.data as { error?: { message?: string } })?.error?.message ?? e.response.status)
        : String(e)
    message.value = t('board.head.create_failed', { reason })
  } finally {
    creating.value = false
  }
}
</script>

<template>
  <div class="mb-2 flex flex-col gap-1 text-xs" role="group" :aria-label="t('board.head.label')" data-board-head>
    <div class="flex flex-wrap items-center gap-x-4 gap-y-1">
      <span v-if="iteration" class="flex flex-wrap items-center gap-x-2" data-iteration>
        <span class="text-text-muted">{{ t('board.head.iteration') }}:</span>
        <a class="wikilink font-medium" :href="noteHref(iteration.path)" :data-preview-path="iteration.path">{{
          iteration.goal || iteration.title || base(iteration.path)
        }}</a>
        <span>{{ t('board.head.days_left', { n: iteration.days_left }, iteration.days_left) }}</span>
        <span>{{ t('board.head.cards_done', { done: iteration.cards.done, total: iteration.cards.total }) }}</span>
        <button
          type="button"
          class="rounded border border-border px-1 hover:bg-surface-hover focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
          :class="onlyIteration ? 'bg-surface-hover font-semibold' : ''"
          :aria-pressed="onlyIteration"
          data-only-iteration
          @click="emit('update:onlyIteration', !onlyIteration)"
        >
          {{ t('board.head.only_iteration') }}
        </button>
      </span>
      <button
        v-else-if="canCreate && !adding"
        type="button"
        class="rounded border border-border px-1 hover:bg-surface-hover focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
        data-new-iteration
        @click="openForm"
      >
        {{ t('board.head.new_iteration') }}
      </button>
      <span v-for="r in state.retro ?? []" :key="r.path" class="text-warning" data-retro>
        {{ t('board.head.retro_pending') }}:
        <a class="wikilink" :href="noteHref(r.path)" :data-preview-path="r.path">{{ r.title || base(r.path) }}</a>
      </span>
      <span :title="t('board.head.cycle_title')" data-cycle>{{ cycle }}</span>
      <span :title="t('board.head.closed_title')" data-closed>{{ t('board.head.closed', { weeks }) }}</span>
      <span
        :class="stale ? 'font-semibold text-warning' : ''"
        :title="t('board.head.stale_title', { days: state.stale_days })"
        data-stale-count
        >{{ t('board.head.stale', { n: stale }) }}</span
      >
    </div>
    <form
      v-if="adding"
      class="flex flex-wrap items-end gap-2 rounded border border-border bg-bg-elevated p-2"
      :aria-label="t('board.head.new_iteration')"
      data-iteration-form
      @submit.prevent="create"
      @keydown.esc.prevent="adding = false"
    >
      <label class="flex flex-col gap-0.5">
        <span class="text-text-muted">{{ t('views.row_title') }}</span>
        <input v-model="title" type="text" required class="rounded border-border-input bg-bg px-1 py-0.5 text-sm text-text" />
      </label>
      <label class="flex flex-col gap-0.5">
        <span class="text-text-muted">{{ t('board.head.goal') }}</span>
        <input v-model="goal" type="text" class="rounded border-border-input bg-bg px-1 py-0.5 text-sm text-text" data-goal />
      </label>
      <label class="flex flex-col gap-0.5">
        <span class="text-text-muted">{{ t('board.head.start') }}</span>
        <input v-model="start" type="date" required class="rounded border-border-input bg-bg px-1 py-0.5 text-sm text-text" />
      </label>
      <label class="flex flex-col gap-0.5">
        <span class="text-text-muted">{{ t('board.head.end') }}</span>
        <input v-model="end" type="date" required :min="start" class="rounded border-border-input bg-bg px-1 py-0.5 text-sm text-text" />
      </label>
      <button
        type="submit"
        :disabled="creating"
        class="rounded bg-accent px-2 py-0.5 text-sm font-medium text-accent-fg hover:bg-accent-hover disabled:opacity-50"
      >
        {{ t('views.create') }}
      </button>
      <button
        type="button"
        class="rounded px-2 py-0.5 text-sm text-text-muted hover:bg-surface-hover hover:text-text"
        @click="adding = false"
      >
        {{ t('views.cancel') }}
      </button>
      <p v-if="message" class="m-0 w-full text-danger" role="alert">{{ message }}</p>
    </form>
  </div>
</template>
