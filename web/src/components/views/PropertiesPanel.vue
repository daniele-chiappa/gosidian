<script setup lang="ts">
/**
 * PropertiesPanel — the fields of a note that is a row of a database
 * (IMP-127 phase 5), above its body: the fields the schema declares, each
 * with the same editor as a table cell (FieldEditor), and the note's other
 * fields, read-only. Shown only for a row; a row the reader may not write,
 * and the id (the file name), stay read-only.
 *
 * Saving works as in ViewTable: PATCH .../frontmatter with the value the
 * field showed when its editor opened as `expect`, so a change made
 * meanwhile is not overwritten. The panel loads again when the note
 * changes (etag).
 *
 * The tags are a field of every markdown note (IMP-223, F-13): among a
 * row's fields, and a line of their own above any other note's body,
 * which a reader who may not write sees only when there are tags.
 */
import { computed, inject, nextTick, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { getRowFields, type FieldValue, type RowFields } from '@/api/notes'
import type { ViewColumn, ViewRow } from '@/api/preview'
import { planciaKey, noteHref, base } from '@/composables/planciaKey'
import { isEditable } from './cellValue'
import { commitField } from './fieldSave'
import FieldEditor from './FieldEditor.vue'
import FieldValueView from './FieldValue.vue'
import { Pencil } from 'lucide-vue-next'
import TagField from './TagField.vue'
import { useFieldNames } from './fieldNames'

const props = defineProps<{ path: string; etag?: string }>()
const { t } = useI18n()
// Fields and values in the reader's language, the keys in the tooltips
// (IMP-220, F-23).
const { fieldName } = useFieldNames()
const openWindow = inject<((spec: OpenSpec) => string) | null>('openWindow', null)

const data = ref<RowFields | null>(null)
const editing = ref<{ col: string; from: FieldValue } | null>(null)
const saving = ref(false)
const message = ref('')

// Only the answer to the last request is kept: the one for the path or the
// etag before could land after it and show another note's fields.
let seq = 0
async function load() {
  const mine = ++seq
  try {
    const got = await getRowFields(props.path)
    if (mine === seq) data.value = got
  } catch {
    if (mine === seq) data.value = null
  }
}
watch(() => [props.path, props.etag], load, { immediate: true })

// The note as a row, so the cell helpers apply unchanged.
const row = computed<ViewRow>(() => ({
  path: props.path,
  title: '',
  modified: '',
  fields: data.value?.values ?? {},
  links: data.value?.links,
  writable: data.value?.writable ?? false,
}))
// The fields with a value first, the empty ones after (IMP-220, F-23). The
// order is the one the note opened with: a field filled keeps its place.
const emptyAtOpen = ref(new Set<string>())
let orderedFor = ''
watch(data, (d) => {
  if (!d || orderedFor === props.path) return
  orderedFor = props.path
  emptyAtOpen.value = new Set((d.columns ?? []).filter((c) => isEmpty(d.values[c.name])).map((c) => c.name))
})
function isEmpty(v: string | string[] | undefined): boolean {
  return v === undefined || v === '' || (Array.isArray(v) && v.length === 0)
}
const tags = computed(() => (Array.isArray(data.value?.tags) ? data.value.tags : null))
const columns = computed(() => {
  const all = data.value?.columns ?? []
  const empty = emptyAtOpen.value
  return [...all.filter((c) => !empty.has(c.name)), ...all.filter((c) => empty.has(c.name))]
})
// The tags (null here) after the fields with a value, before the empty
// ones: last, a row's long list of empty fields hid them (IMP-223).
const entries = computed<(ViewColumn | null)[]>(() => {
  const cols: (ViewColumn | null)[] = [...columns.value]
  if (!tags.value) return cols
  const at = cols.findIndex((c) => c && emptyAtOpen.value.has(c.name))
  cols.splice(at < 0 ? cols.length : at, 0, null)
  return cols
})

// In the panel the title is a field like the others; in a view's table it
// is the link to the note instead, which the cell helpers assume.
const valueOf = (col: ViewColumn) => row.value.fields[col.name]
const editable = (col: ViewColumn) =>
  col.name === 'title' ? row.value.writable : isEditable(col, row.value)
const seenOf = (col: ViewColumn): FieldValue => {
  const v = valueOf(col)
  return v === undefined || v === '' ? null : v
}
const others = computed<ViewColumn[]>(() => (data.value?.others ?? []).map((name) => ({ name })))

// Who created and last modified the note, from the audit log.
const authors = computed(() =>
  [
    data.value?.created_by ? t('views.created_by', { who: data.value.created_by }) : '',
    data.value?.modified_by ? t('views.modified_by', { who: data.value.modified_by }) : '',
  ]
    .filter(Boolean)
    .join(' · '),
)

const tagsLabelId = `tags-${useId()}`
function tagsSaved(next: string[]) {
  if (data.value) data.value.tags = next
}

const databaseTitle = computed(() =>
  base(data.value?.database ?? ''),
)

function openDatabase(e: MouseEvent) {
  const path = data.value?.database
  if (!path || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return
  e.preventDefault()
  const spec: OpenSpec = {
    type: 'note',
    key: planciaKey('note', path),
    title: databaseTitle.value,
    props: { path },
  }
  if (openWindow) openWindow(spec)
  else useWindowsStore().open(spec)
}

function startEdit(col: ViewColumn) {
  if (!editable(col) || col.type === 'checkbox' || saving.value) return
  editing.value = { col: col.name, from: seenOf(col) }
  message.value = ''
}

function setValue(name: string, v: string | string[] | undefined) {
  if (!data.value) return
  if (v === undefined) delete data.value.values[name]
  else data.value.values[name] = v
}

// The field changed keeps the focus (IMP-217, F-04): its editor goes, and
// the field is disabled while its value is saved, so the focus fell to the
// page. It comes back only from there: a Tab that moved on keeps its place.
const rootEl = ref<HTMLElement | null>(null)
async function refocus(name: string) {
  await nextTick()
  const at = document.activeElement
  if (at && at !== document.body) return
  const field = [...(rootEl.value?.querySelectorAll<HTMLElement>('dd[data-field]') ?? [])].find(
    (dd) => dd.dataset.field === name,
  )
  field?.querySelector<HTMLElement>('button, input')?.focus()
}

function cancelEdit(col: ViewColumn) {
  editing.value = null
  void refocus(col.name)
}

async function commit(col: ViewColumn, input: string | boolean | string[]) {
  let seen = seenOf(col)
  if (col.type !== 'checkbox') {
    if (editing.value?.col !== col.name) return
    seen = editing.value.from
    editing.value = null
  }
  const target = {
    path: props.path,
    col,
    before: row.value.fields[col.name],
    set: (v: string | string[] | undefined) => setValue(col.name, v),
    saving: (on: boolean) => (saving.value = on),
  }
  const msg = await commitField(target, seen, input, t)
  if (msg !== null) message.value = msg
  void refocus(col.name)
}
</script>

<template>
  <section
    v-if="data?.database"
    ref="rootEl"
    class="mb-6 rounded border border-border p-3 text-sm"
    :aria-label="t('views.properties')"
    data-properties
  >
    <header class="mb-2 flex flex-wrap items-baseline gap-x-2 text-xs text-text-muted">
      <span class="font-semibold">{{ t('views.properties') }}</span>
      <span>
        {{ t('views.row_of') }}
        <a
          class="wikilink"
          :href="noteHref(data.database)"
          :data-preview-path="data.database"
          @click="openDatabase"
          >{{ databaseTitle }}</a
        >
      </span>
      <span v-if="!data.writable">· {{ t('views.read_only') }}</span>
    </header>
    <!-- Top-aligned rows of one line height: a value that wraps keeps its
         label on its first line. -->
    <dl class="m-0 grid grid-cols-[minmax(7rem,max-content)_1fr] items-start gap-x-4 gap-y-1">
      <template v-for="c in entries" :key="c?.name ?? '#tags'">
        <template v-if="c === null">
          <dt class="leading-6 text-text-muted" title="tags">{{ t('tags_field.label') }}</dt>
          <dd class="m-0 min-h-[1.5rem] leading-6" data-field="tags">
            <TagField
              :path="path"
              :tags="tags ?? []"
              :writable="data.writable"
              :vocabulary="data.tag_vocabulary"
              @saved="tagsSaved"
            />
          </dd>
        </template>
        <template v-else>
          <dt class="leading-6 text-text-muted" :title="c.name">{{ fieldName(c) }}</dt>
          <dd class="m-0 min-h-[1.5rem] leading-6" :data-field="c.name">
            <input
              v-if="c.type === 'checkbox'"
              type="checkbox"
              class="rounded-sm border-border-input bg-bg-elevated text-accent focus:ring-focus"
              :checked="row.fields[c.name] === 'true'"
              :disabled="!editable(c) || saving"
              :aria-label="t('views.edit', { field: fieldName(c) })"
              @change="commit(c, ($event.target as HTMLInputElement).checked)"
            />
            <FieldEditor
              v-else-if="editing?.col === c.name"
              :column="c"
              :value="valueOf(c)"
              :label="t('views.edit', { field: fieldName(c) })"
              @commit="commit(c, $event)"
              @cancel="cancelEdit(c)"
            />
            <span
              v-else-if="editable(c) && row.links?.[c.name]?.length"
              class="inline-flex items-center gap-1"
            >
              <FieldValueView
                :value="valueOf(c)"
                :links="row.links?.[c.name]"
                :column="c"
              />
              <button
                type="button"
                class="rounded p-0.5 text-text-muted hover:bg-surface-hover hover:text-text focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
                :aria-label="t('views.edit', { field: fieldName(c) })"
                :disabled="saving"
                @click="startEdit(c)"
              >
                <Pencil class="h-3 w-3" aria-hidden="true" />
              </button>
            </span>
            <button
              v-else-if="editable(c)"
              type="button"
              class="-mx-1 w-full cursor-text rounded px-1 text-left hover:bg-surface-hover focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
              :disabled="saving"
              @click="startEdit(c)"
            >
              <!-- Named by what it shows, then what it does (IMP-220, F-22): "review
                   (edit status)". The space is in the expression, as Vue drops a
                   leading one. -->
              <FieldValueView
                v-if="valueOf(c)?.length"
                :value="valueOf(c)"
                :column="c"
              />
              <span v-else class="text-text-muted">
                <span aria-hidden="true">—</span>
                <span class="sr-only">{{ t('views.no_value') }}</span>
              </span>
              <span class="sr-only">{{ ' ' + t('views.edit_value', { field: fieldName(c) }) }}</span>
            </button>
            <FieldValueView
              v-else
              :value="valueOf(c)"
              :links="row.links?.[c.name]"
              :column="c"
            />
          </dd>
        </template>
      </template>
    </dl>
    <p v-if="authors" class="mb-0 mt-2 text-xs text-text-muted" data-authors>{{ authors }}</p>
    <template v-if="others.length">
      <p class="mb-1 mt-3 text-xs text-text-muted">{{ t('views.not_declared') }}</p>
      <dl class="m-0 grid grid-cols-[minmax(7rem,max-content)_1fr] items-start gap-x-4 gap-y-1">
        <template v-for="c in others" :key="c.name">
          <dt class="leading-6 text-text-muted" :title="c.name">{{ fieldName(c) }}</dt>
          <dd class="m-0 leading-6" :data-field="c.name">
            <FieldValueView
              :value="valueOf(c)"
              :links="row.links?.[c.name]"
              :column="c"
            />
          </dd>
        </template>
      </dl>
    </template>
    <p
      :class="message ? 'mb-0 mt-2 text-danger' : 'sr-only'"
      role="status"
      aria-live="polite"
      data-view-message
    >
      {{ message }}
    </p>
  </section>
  <div
    v-else-if="tags && (data?.writable || tags.length)"
    class="mb-4 flex flex-wrap items-center gap-2 text-sm"
    role="group"
    :aria-labelledby="tagsLabelId"
    data-note-tags
  >
    <span :id="tagsLabelId" class="text-xs text-text-muted" title="tags">{{ t('tags_field.label') }}</span>
    <TagField
      :path="path"
      :tags="tags"
      :writable="data?.writable ?? false"
      :vocabulary="data?.tag_vocabulary"
      @saved="tagsSaved"
    />
  </div>
</template>
