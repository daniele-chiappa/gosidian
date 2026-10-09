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
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { FieldValue } from '@/api/notes'
import type { ViewColumn, ViewData, ViewRow } from '@/api/preview'
import { cellValue } from './cellValue'
import { saveField } from './fieldSave'
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

/** The column a row is in: the first value of its group_by field. */
function groupOf(row: ViewRow): string {
  const v = row.fields[field.value]
  return (Array.isArray(v) ? v[0] : v) ?? ''
}

const columns = computed(() => {
  const values = [...(props.view.groups ?? [])]
  // A card moved to the no-value column needs it even if the server had none.
  if (!values.includes('') && rows.value.some((r) => groupOf(r) === '')) values.push('')
  return values.map((value) => ({ value, rows: rows.value.filter((r) => groupOf(r) === value) }))
})

const saving = ref<string | null>(null)
const message = ref('')
const dragged = ref<string | null>(null)
const over = ref<string | null>(null)
const moving = ref<string | null>(null) // the card whose "Move to…" menu is open
const addingIn = ref<string | null>(null) // the column whose new-row form is open

const label = (value: string) => value || t('views.no_value')
const canMove = (row: ViewRow) => row.writable && !!group.value.type && saving.value === null

/** The value the group_by field takes in a column. */
function valueFor(column: string): FieldValue {
  if (column === '') return null
  if (group.value.type === 'checkbox') return column === 'true'
  return column
}

/** The values a row made in a column starts with. */
function newRowValues(column: string): Record<string, FieldValue> {
  return { ...(props.view.defaults ?? {}), [field.value]: valueFor(column) }
}

async function move(row: ViewRow, to: string) {
  moving.value = null
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
  if (res.ok) {
    message.value = ''
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

function onDrop(e: DragEvent, column: string) {
  e.preventDefault()
  const path = dragged.value ?? e.dataTransfer?.getData('text/plain')
  dragged.value = null
  over.value = null
  const row = rows.value.find((r) => r.path === path)
  if (row) void move(row, column)
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
  <div class="not-prose">
    <div class="flex gap-3 overflow-x-auto pb-2" role="list" :aria-label="field">
      <section
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
          <span class="ml-auto text-xs text-text-muted">{{ col.rows.length }}</span>
          <button
            v-if="view.creatable && view.database && addingIn !== col.value"
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
            @dragstart="onDragStart($event, row)"
            @dragend="onDragEnd"
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
              <option v-for="c in columns" :key="c.value" :value="c.value">
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
      </section>
    </div>
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
