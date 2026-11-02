<script setup lang="ts">
/**
 * ViewBoard — a ```view with `as: board` shown as columns of cards, one per
 * value of its group_by field, in the order the server gives (the options
 * of the schema's select, empty columns included). IMP-127 phase 5.
 *
 * A card the reader may write moves to another column by drag and drop, or
 * with its "Move to…" menu, the way for the keyboard: the move is a PATCH of
 * the group_by field with the column the card was in as `expect`, so a
 * card someone else moved meanwhile is not moved back. The column without a
 * value removes the field. On a refusal the card goes back, with the reason.
 *
 * When the reader may add rows, the "+" of a column opens NewRowForm there,
 * with the view's defaults and the column's value for the group_by field.
 *
 * The board of a ```board block (IMP-216) has the block's columns (lanes),
 * each with its count against its limit, red past it; the closed cards
 * without a column show on request. A card dropped on another goes to its
 * place, in the same column or another, and Alt with the arrows moves it
 * up and down: the PATCH says the place (at), and the server, which keeps
 * the ranks and the limits, answers with a warning when the card took a
 * column past its limit. A new card starts in a column of backlog or
 * submitted, with its state, at its bottom.
 */
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { FieldValue } from '@/api/notes'
import type { PatchFrontmatterBody } from '@/api/notes'
import type { ViewColumn, ViewData, ViewLane, ViewRow } from '@/api/preview'
import { cellValue } from './cellValue'
import { announce } from '@/composables/useAnnounce'
import { saveBody, saveField } from './fieldSave'
import FieldValueView from './FieldValue.vue'
import NewRowForm from './NewRowForm.vue'
import { MoveRight, Plus } from 'lucide-vue-next'
import { noteHref } from '@/composables/planciaKey'

const props = defineProps<{ view: ViewData }>()
const { t } = useI18n()

const rows = ref<ViewRow[]>([])
watch(
  () => props.view,
  (v) => {
    rows.value = (v.rows ?? []).map((r) => ({ ...r, fields: { ...r.fields } }))
  },
  { immediate: true },
)

const group = computed<ViewColumn>(() => props.view.group ?? { name: '' })
const field = computed(() => group.value.name)
// The fields a card shows: the view's columns but the title and group_by.
const cardColumns = computed(() =>
  (props.view.columns ?? []).filter((c) => c.name !== 'title' && c.name !== field.value),
)

// The columns of a ```board block; none for a view's board.
const lanes = computed<ViewLane[]>(() => props.view.lanes ?? [])
const isCards = computed(() => lanes.value.length > 0)
const laneOf = (value: string) => lanes.value.find((l) => l.name === value)
const showClosed = ref(false)

/** The column a row is in: the first value of its group_by field; a card's lane in any case. */
function groupOf(row: ViewRow): string {
  const v = row.fields[field.value]
  const value = (Array.isArray(v) ? v[0] : v) ?? ''
  if (!isCards.value) return value
  return lanes.value.find((l) => l.name.toLowerCase() === value.toLowerCase())?.name ?? value
}

const allColumns = computed(() => {
  const values = [...(props.view.groups ?? [])]
  // A card moved to the no-value column needs it even if the server had none.
  if (!values.includes('') && rows.value.some((r) => groupOf(r) === '')) values.push('')
  return values.map((value) => ({ value, rows: rows.value.filter((r) => groupOf(r) === value) }))
})
// The closed cards of a board block, without a column, wait to be asked for.
const closed = computed(() =>
  isCards.value ? (allColumns.value.find((c) => c.value === '')?.rows.length ?? 0) : 0,
)
const columns = computed(() =>
  allColumns.value.filter((c) => !isCards.value || c.value !== '' || showClosed.value),
)
// The columns a card moves to: a card leaves for no column by a move.
const targets = computed(() => columns.value.filter((c) => !isCards.value || c.value !== ''))

/** A column's count, against its limit when it has one. */
function count(col: { value: string; rows: ViewRow[] }): string {
  const wip = laneOf(col.value)?.wip
  return wip ? `${col.rows.length}/${wip}` : `${col.rows.length}`
}
const pastLimit = (col: { value: string; rows: ViewRow[] }) => {
  const wip = laneOf(col.value)?.wip
  return !!wip && col.rows.length > wip
}

const saving = ref<string | null>(null)
const message = ref('')
const dragged = ref<string | null>(null)
const over = ref<string | null>(null)
const moving = ref<string | null>(null) // the card whose "Move to…" menu is open
const addingIn = ref<string | null>(null) // the column whose new-row form is open

const label = (value: string) => value || (isCards.value ? t('views.closed') : t('views.no_value'))
const canMove = (row: ViewRow) => row.writable && !!group.value.type && saving.value === null
// A new card starts in a column of backlog or submitted, as with the task tools.
const canAdd = (value: string) =>
  !!props.view.creatable &&
  !!props.view.database &&
  (!isCards.value || ['backlog', 'submitted'].includes(laneOf(value)?.status ?? ''))

/** The value the group_by field takes in a column. */
function valueFor(column: string): FieldValue {
  if (column === '') return null
  if (group.value.type === 'checkbox') return column === 'true'
  return column
}

/** The values a row made in a column starts with; a card, its column's state and the bottom rank. */
function newRowValues(column: string): Record<string, FieldValue> {
  const values: Record<string, FieldValue> = {
    ...(props.view.defaults ?? {}),
    [field.value]: valueFor(column),
  }
  const lane = laneOf(column)
  if (lane) {
    const ranks = rows.value
      .filter((r) => groupOf(r) === column)
      .map((r) => Number(r.fields.rank) || 0)
    Object.assign(values, { status: lane.status, rank: Math.max(0, ...ranks) + 1 })
  }
  return values
}

// The card moved keeps the focus, in its new column or back in its place
// (IMP-217, F-04): drawn again, it lost it to the page. It comes back only
// from there, on the card's button (its link while it cannot move).
const rootEl = ref<HTMLElement | null>(null)
async function focusCard(path: string) {
  await nextTick()
  const at = document.activeElement
  if (at && at !== document.body) return
  const card = [...(rootEl.value?.querySelectorAll<HTMLElement>('li[data-card]') ?? [])].find(
    (li) => li.dataset.card === path,
  )
  ;(card?.querySelector<HTMLElement>('button') ?? card?.querySelector<HTMLElement>('a'))?.focus()
}

// Where the card went, for who does not see it move (IMP-217, F-10).
function sayMoved(row: ViewRow, to: string) {
  const place = rows.value.filter((r) => groupOf(r) === to).indexOf(row) + 1
  announce(t('views.moved_to', { column: label(to), place }))
}

/**
 * moveCard moves a board block's card to column `to`, before the card at
 * `before`, or at the column's bottom; shown at once, and put back when the
 * server refuses.
 */
async function moveCard(row: ViewRow, to: string, before?: string) {
  const from = groupOf(row)
  if (before === row.path || to === '' || !canMove(row)) return
  const inTo = rows.value.filter((r) => groupOf(r) === to && r.path !== row.path)
  let at = before ? inTo.findIndex((r) => r.path === before) : -1
  if (to === from) {
    if (at < 0) at = inTo.length
    if (rows.value.filter((r) => groupOf(r) === to).indexOf(row) === at) return
  }
  const seen = row.fields[field.value] ?? null
  const body: PatchFrontmatterBody = { expect: { [field.value]: seen } }
  if (to !== from) body.set = { [field.value]: to }
  if (at >= 0) body.at = at

  const order = rows.value.slice()
  rows.value = rows.value.filter((r) => r !== row)
  row.fields[field.value] = to
  // Before the card at its place in the column, or after the column's last.
  const ahead = at >= 0 ? inTo[at] : undefined
  const last = inTo[inTo.length - 1]
  let i = rows.value.length
  if (ahead) i = rows.value.indexOf(ahead)
  else if (last) i = rows.value.indexOf(last) + 1
  rows.value.splice(i, 0, row)
  saving.value = row.path
  const res = await saveBody(row.path, group.value, body, t)
  saving.value = null
  void focusCard(row.path)
  if (res.ok) {
    const wip = laneOf(to)?.wip
    message.value = res.warning && wip ? t('views.wip_over', { column: to, wip }) : ''
    sayMoved(row, to)
    return
  }
  rows.value = order
  const back = res.conflict ? res.current : seen
  if (back === undefined || back === null) delete row.fields[field.value]
  else row.fields[field.value] = back
  message.value = res.message
}

/** nudge moves a card one place up (-1) or down (+1) its column. */
function nudge(row: ViewRow, by: -1 | 1) {
  if (!isCards.value) return
  const col = groupOf(row)
  const inCol = rows.value.filter((r) => groupOf(r) === col)
  const i = inCol.indexOf(row)
  if (i + by < 0 || i + by >= inCol.length) return
  void moveCard(row, col, by < 0 ? inCol[i - 1]?.path : inCol[i + 2]?.path)
}

async function move(row: ViewRow, to: string) {
  moving.value = null
  if (isCards.value) return moveCard(row, to)
  const from = groupOf(row)
  if (to === from || !canMove(row)) return
  const before = row.fields[field.value]
  const seen: FieldValue = from === '' ? null : (before ?? null)
  const target = valueFor(to)
  const change = target === null ? ({ unset: true } as const) : { set: target }

  if (to === '') delete row.fields[field.value]
  else row.fields[field.value] = to
  saving.value = row.path
  const res = await saveField(row.path, group.value, seen, change, t)
  saving.value = null
  void focusCard(row.path)
  if (res.ok) {
    message.value = ''
    sayMoved(row, to)
    return
  }
  const back = res.conflict ? res.current : before
  if (back === undefined) delete row.fields[field.value]
  else row.fields[field.value] = back
  message.value = res.message
}

function onDragStart(e: DragEvent, row: ViewRow) {
  if (!canMove(row)) {
    e.preventDefault()
    return
  }
  dragged.value = row.path
  e.dataTransfer?.setData('text/plain', row.path)
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'
}

function onDragOver(e: DragEvent, column: string) {
  if (!dragged.value) return
  e.preventDefault()
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
  over.value = column
}

/** A card dropped on a column, or on the card at `before` (a board block's: its place). */
function onDrop(e: DragEvent, column: string, before?: string) {
  e.preventDefault()
  const path = dragged.value ?? e.dataTransfer?.getData('text/plain')
  dragged.value = null
  over.value = null
  const row = rows.value.find((r) => r.path === path)
  if (!row) return
  if (isCards.value) void moveCard(row, column, before)
  else void move(row, column)
}

function onDragEnd() {
  dragged.value = null
  over.value = null
}

function focusOnMount(el: unknown) {
  if (el instanceof HTMLElement) el.focus()
}
</script>

<template>
  <div
    ref="rootEl"
    class="not-prose"
  >
    <div class="flex gap-3 overflow-x-auto pb-2" role="list" :aria-label="field">
      <div
        v-for="col in columns"
        :key="col.value"
        role="listitem"
        class="flex w-56 shrink-0 flex-col rounded border bg-bg-elevated p-2 text-sm"
        :class="over === col.value ? 'border-accent' : 'border-border'"
        :data-group="col.value"
        :aria-label="label(col.value)"
        @dragover="onDragOver($event, col.value)"
        @dragleave="over === col.value && (over = null)"
        @drop="onDrop($event, col.value)"
      >
        <header class="mb-2 flex items-baseline gap-2 px-1">
          <span class="font-semibold" :class="{ 'text-text-muted': !col.value }">{{
            label(col.value)
          }}</span>
          <span
            class="ml-auto text-xs"
            :class="pastLimit(col) ? 'font-semibold text-danger' : 'text-text-muted'"
            data-wip
            >{{ count(col) }}</span
          >
          <button
            v-if="canAdd(col.value) && addingIn !== col.value"
            type="button"
            class="self-center rounded p-0.5 text-text-muted hover:bg-surface-hover hover:text-text focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
            :aria-label="t('views.new_row_in', { column: label(col.value) })"
            data-new-row-button
            @click="addingIn = col.value"
          >
            <Plus class="h-3.5 w-3.5" aria-hidden="true" />
          </button>
        </header>
        <NewRowForm
          v-if="addingIn === col.value && view.database"
          class="mt-0"
          :database="view.database"
          :values="newRowValues(col.value)"
          @done="addingIn = null"
        />
        <ul class="m-0 flex min-h-[2.5rem] list-none flex-col gap-2 p-0">
          <li
            v-for="row in col.rows"
            :key="row.path"
            class="rounded border border-border bg-bg p-2"
            :class="{
              'cursor-grab': canMove(row),
              'opacity-60': saving === row.path || dragged === row.path,
            }"
            :draggable="canMove(row)"
            :data-card="row.path"
            :aria-keyshortcuts="isCards && canMove(row) ? 'Alt+ArrowUp Alt+ArrowDown' : undefined"
            @dragstart="onDragStart($event, row)"
            @dragend="onDragEnd"
            @dragover="onDragOver($event, col.value)"
            @drop.stop="onDrop($event, col.value, isCards ? row.path : undefined)"
            @keydown.alt.up.prevent="nudge(row, -1)"
            @keydown.alt.down.prevent="nudge(row, 1)"
          >
            <div class="flex items-start justify-between gap-1">
              <a
                class="wikilink font-medium"
                :href="noteHref(row.path)"
                :data-preview-path="row.path"
                >{{ row.title }}</a
              >
              <button
                v-if="canMove(row) && moving !== row.path"
                type="button"
                class="rounded p-0.5 text-text-muted hover:bg-surface-hover hover:text-text focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
                :aria-label="t('views.move_to', { title: row.title })"
                @click="moving = row.path"
              >
                <MoveRight class="h-3.5 w-3.5" aria-hidden="true" />
              </button>
            </div>
            <select
              v-if="moving === row.path"
              :ref="focusOnMount"
              class="mt-1 w-full rounded border-border bg-bg-elevated py-0.5 text-xs text-text"
              :aria-label="t('views.move_to', { title: row.title })"
              :value="col.value"
              @change="move(row, ($event.target as HTMLSelectElement).value)"
              @blur="moving = null"
              @keydown.esc.prevent="moving = null"
            >
              <option v-for="c in targets" :key="c.value" :value="c.value">
                {{ label(c.value) }}
              </option>
            </select>
            <dl v-if="cardColumns.length" class="m-0 mt-1 text-xs">
              <template v-for="c in cardColumns" :key="c.name">
                <div v-if="cellValue(row, c)?.length" class="flex gap-1">
                  <dt class="text-text-muted">{{ c.name }}:</dt>
                  <dd class="m-0">
                    <FieldValueView :value="cellValue(row, c)" :links="row.links?.[c.name]" />
                  </dd>
                </div>
              </template>
            </dl>
          </li>
        </ul>
        <p v-if="!col.rows.length" class="m-0 px-1 text-xs text-text-muted">
          {{ t('views.column_empty') }}
        </p>
      </div>
    </div>
    <button
      v-if="closed"
      type="button"
      class="text-sm text-text-muted underline hover:text-text focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
      :aria-expanded="showClosed"
      data-show-closed
      @click="showClosed = !showClosed"
    >
      {{ t('views.closed') }} ({{ closed }})
    </button>
    <p v-if="view.total > rows.length" class="text-sm">
      <em>{{ t('views.showing', { shown: rows.length, total: view.total }) }}</em>
    </p>
    <p
      :class="message ? 'text-sm text-danger' : 'sr-only'"
      role="status"
      aria-live="polite"
      data-view-message
    >
      {{ message }}
    </p>
  </div>
</template>
