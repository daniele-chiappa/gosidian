<script setup lang="ts">
/**
 * ViewTable — a ```view shown as a table whose cells edit the rows' fields
 * (IMP-127 phase 5). It looks like the table the server renders; on a row
 * the reader may write, a cell of a field the database's schema declares
 * turns into its editor (FieldEditor) on click or Enter, and a checkbox
 * toggles in place.
 *
 * A change goes to PATCH /api/v1/notes/{path}/frontmatter with the value
 * the cell showed when it opened as `expect`. When someone changed that
 * field meanwhile the server refuses (409): the cell shows the current
 * value and says so, and nothing is overwritten. A value the schema refuses
 * (422) or a row the reader may not write (403) leaves the cell as it was,
 * with the reason.
 *
 * Under the table, "New row" opens NewRowForm when the reader may add rows
 * to the database, with the values the view's filters ask for.
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { FieldValue } from '@/api/notes'
import type { ViewColumn, ViewData, ViewRow } from '@/api/preview'
import { cellValue, expectValue, isEditable, shownValue, toChange } from './cellValue'
import { saveField } from './fieldSave'
import FieldEditor from './FieldEditor.vue'
import FieldValueView from './FieldValue.vue'
import NewRowForm from './NewRowForm.vue'
import { Pencil, Plus } from 'lucide-vue-next'

const props = defineProps<{ view: ViewData }>()
const { t } = useI18n()

// A copy of the rows, so a cell shows its new value while the save is in
// flight; the note's views are computed again when the row's event comes.
const rows = ref<ViewRow[]>([])
watch(
  () => props.view,
  (v) => {
    rows.value = (v.rows ?? []).map((r) => ({ ...r, fields: { ...r.fields } }))
  },
  { immediate: true },
)
const columns = computed(() => props.view.columns ?? [])
const adding = ref(false)

// The open editor, with the value the cell showed when it opened: what
// `expect` checks, even if the view is computed again meanwhile.
const editing = ref<{ path: string; col: string; from: FieldValue } | null>(null)
const saving = ref<string | null>(null)
const message = ref('')

const cellKey = (row: ViewRow, col: ViewColumn) => `${row.path}\u0000${col.name}`
const isEditing = (row: ViewRow, col: ViewColumn) =>
  editing.value?.path === row.path && editing.value?.col === col.name

function noteHref(path: string): string {
  return '/notes/' + path.split('/').map(encodeURIComponent).join('/')
}

function startEdit(row: ViewRow, col: ViewColumn) {
  if (!isEditable(col, row) || col.type === 'checkbox' || saving.value) return
  editing.value = { path: row.path, col: col.name, from: expectValue(row, col) }
  message.value = ''
}

function cancel() {
  editing.value = null
}

function setField(row: ViewRow, name: string, v: string | string[] | undefined) {
  if (v === undefined) delete row.fields[name]
  else row.fields[name] = v
}

const same = (a: unknown, b: unknown) => JSON.stringify(a ?? null) === JSON.stringify(b ?? null)

async function commit(row: ViewRow, col: ViewColumn, input: string | boolean | string[]) {
  // A checkbox commits without an editor; any other cell only while open.
  let seen = expectValue(row, col)
  if (col.type !== 'checkbox') {
    if (!isEditing(row, col) || !editing.value) return
    seen = editing.value.from
    editing.value = null
  }
  const before = row.fields[col.name]
  const change = toChange(col, input, before)
  if ('error' in change) {
    message.value = t('views.not_a_number', { field: col.name })
    return
  }
  const shown = shownValue(change)
  if (same(shown, seen)) return

  setField(row, col.name, shown)
  saving.value = cellKey(row, col)
  const res = await saveField(row.path, col, seen, change, t)
  saving.value = null
  if (res.ok) {
    message.value = ''
    return
  }
  setField(row, col.name, res.conflict ? res.current : before)
  message.value = res.message
}
</script>

<template>
  <p v-if="!rows.length">
    <em>{{ t('views.no_rows') }}</em>
  </p>
  <table v-else>
    <thead>
      <tr>
        <th v-for="c in columns" :key="c.name">{{ c.name }}</th>
      </tr>
    </thead>
    <tbody>
      <tr v-for="row in rows" :key="row.path">
        <td
          v-for="c in columns"
          :key="c.name"
          :class="{ 'opacity-60': saving === cellKey(row, c) }"
          :data-field="c.name"
        >
          <a
            v-if="c.name === 'title'"
            class="wikilink"
            :href="noteHref(row.path)"
            :data-preview-path="row.path"
            >{{ row.title }}</a
          >
          <input
            v-else-if="c.type === 'checkbox'"
            type="checkbox"
            class="rounded-sm border-border bg-bg-elevated text-accent focus:ring-accent"
            :checked="row.fields[c.name] === 'true'"
            :disabled="!isEditable(c, row) || saving !== null"
            :aria-label="t('views.edit', { field: c.name })"
            @change="commit(row, c, ($event.target as HTMLInputElement).checked)"
          />
          <FieldEditor
            v-else-if="isEditing(row, c)"
            :column="c"
            :value="cellValue(row, c)"
            :label="t('views.edit', { field: c.name })"
            @commit="commit(row, c, $event)"
            @cancel="cancel"
          />
          <span
            v-else-if="isEditable(c, row) && row.links?.[c.name]?.length"
            class="inline-flex items-center gap-1"
          >
            <FieldValueView :value="cellValue(row, c)" :links="row.links?.[c.name]" />
            <button
              type="button"
              class="rounded p-0.5 text-text-muted hover:bg-surface-hover hover:text-text focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
              :aria-label="t('views.edit', { field: c.name })"
              :disabled="saving !== null"
              @click="startEdit(row, c)"
            >
              <Pencil class="h-3 w-3" aria-hidden="true" />
            </button>
          </span>
          <button
            v-else-if="isEditable(c, row)"
            type="button"
            class="-mx-1 w-full cursor-text rounded px-1 text-left hover:bg-surface-hover focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
            :aria-label="t('views.edit', { field: c.name })"
            :disabled="saving !== null"
            @click="startEdit(row, c)"
          >
            <FieldValueView v-if="cellValue(row, c)?.length" :value="cellValue(row, c)" />
            <span v-else class="text-text-muted">—</span>
          </button>
          <FieldValueView v-else :value="cellValue(row, c)" :links="row.links?.[c.name]" />
        </td>
      </tr>
    </tbody>
  </table>
  <p v-if="view.total > rows.length">
    <em>{{ t('views.showing', { shown: rows.length, total: view.total }) }}</em>
  </p>
  <template v-if="view.creatable && view.database">
    <NewRowForm
      v-if="adding"
      :database="view.database"
      :values="view.defaults"
      @done="adding = false"
    />
    <button
      v-else
      type="button"
      class="not-prose inline-flex items-center gap-1 rounded px-1 py-0.5 text-sm text-text-muted hover:bg-surface-hover hover:text-text focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
      data-new-row-button
      @click="adding = true"
    >
      <Plus class="h-3.5 w-3.5" aria-hidden="true" />
      {{ t('views.new_row') }}
    </button>
  </template>
  <p
    :class="message ? 'text-sm text-danger' : 'sr-only'"
    role="status"
    aria-live="polite"
    data-view-message
  >
    {{ message }}
  </p>
</template>
