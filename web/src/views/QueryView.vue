<script setup lang="ts">
/** QueryView — frontmatter query (IMP-099) as a plancia window: conditions
 *  on fields, the same query as the MCP memory_query tool. The query lives in
 *  the window's props, so the codec keeps it in the URL (queryArg.ts); notes
 *  open as sibling windows. */
import { useI18n } from 'vue-i18n'
import { computed, inject, onMounted, ref } from 'vue'
import { X } from 'lucide-vue-next'
import { runQuery, type QueryCondition, type QueryNote, type QueryOp } from '@/api/query'
import { listProjects } from '@/api/projects'
import { QUERY_OPS } from '@/composables/queryArg'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { planciaKey } from '@/composables/planciaKey'
import { errorText } from '@/api/errors'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'
import DateTime from '@/components/primitives/DateTime.vue'

const { t } = useI18n()

const props = defineProps<{
  project?: string
  where?: QueryCondition[]
  sort?: string
  order?: 'asc' | 'desc'
  fields?: string[]
}>()

const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))

interface Row {
  field: string
  op: QueryOp
  value: string
}

const COMMON_FIELDS = ['type', 'status', 'tags', 'importance', 'updated', 'created', 'description', 'title', 'topic']
const SORT_FIELDS = ['modified', 'path', 'title', 'updated', 'created', 'importance', 'status']
const LIMIT = 200

function toRow(c: QueryCondition): Row {
  let value = ''
  if (Array.isArray(c.value)) value = c.value.join(', ')
  else if (c.value !== undefined) value = String(c.value)
  if (c.op === 'exists' && value === '') value = 'true'
  return { field: c.field, op: c.op, value }
}

const rows = ref<Row[]>(
  props.where?.length ? props.where.map(toRow) : [{ field: 'type', op: 'eq', value: 'plan' }],
)
const project = ref(props.project ?? '')
const sort = ref(props.sort ?? '')
const order = ref<'' | 'asc' | 'desc'>(props.order ?? '')
const fieldsText = ref((props.fields ?? []).join(', '))

const notes = ref<QueryNote[]>([])
const total = ref(0)
const truncated = ref(false)
const loading = ref(false)
const error = ref<string | null>(null)
const ran = ref(false)
const projects = ref<string[]>([])

const splitList = (s: string) =>
  s
    .split(',')
    .map((x) => x.trim())
    .filter(Boolean)

/** Rows → conditions; throws with a message the view shows. */
function conditions(): QueryCondition[] {
  const out: QueryCondition[] = []
  rows.value.forEach((r, i) => {
    const field = r.field.trim()
    if (!field) return
    if (r.op === 'exists') {
      out.push(r.value === 'false' ? { field, op: r.op, value: false } : { field, op: r.op })
      return
    }
    const value = r.value.trim()
    if (!value) throw new Error(t('query.needs_value', { n: i + 1, field }))
    out.push({ field, op: r.op, value: r.op === 'in' ? splitList(value) : value })
  })
  if (!out.length) throw new Error(t('query.needs_condition'))
  return out
}

// Columns in the order of the last run: the fields asked for, else those of
// the conditions and the sort (as the server picks them).
const shownFields = ref<string[]>([])
const columns = computed<string[]>(() => shownFields.value)

function cell(n: QueryNote, k: string): string {
  const v = n.fields?.[k]
  return Array.isArray(v) ? v.join(', ') : String(v ?? '')
}

/** The fields of a sort of one or more keys: "plans desc, id" → plans, id. */
const sortFields = (s: string) =>
  s
    .split(',')
    .map((k) => k.trim().split(/\s+/)[0] ?? '')
    .filter(Boolean)

/** Short values (dates, statuses, numbers) stay on one line. */
const cellClass = (v: string) => (v.length <= 24 ? 'whitespace-nowrap' : '')


// URL persistence, as GraphView does: the window is the `query` singleton,
// so the view finds it and pushes the query back through identify().
function syncWindow(where: QueryCondition[]) {
  const w = store.windows.find((x) => x.type === 'query' && x.key === planciaKey('query'))
  if (!w) return
  const fields = splitList(fieldsText.value)
  store.identify(w.id, w.key, {
    where,
    ...(project.value.trim() ? { project: project.value.trim() } : {}),
    ...(sort.value.trim() ? { sort: sort.value.trim() } : {}),
    ...(order.value ? { order: order.value } : {}),
    ...(fields.length ? { fields } : {}),
  })
}

async function run() {
  let where: QueryCondition[]
  try {
    where = conditions()
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
    return
  }
  loading.value = true
  error.value = null
  try {
    const fields = splitList(fieldsText.value)
    const res = await runQuery({
      where,
      project: project.value.trim() || undefined,
      sort: sort.value.trim() || undefined,
      order: order.value || undefined,
      fields: fields.length ? fields : undefined,
      limit: LIMIT,
    })
    const asked = fields.length ? fields : [...where.map((c) => c.field), ...sortFields(sort.value)]
    shownFields.value = asked.filter(
      (f, i) => f && !['modified', 'path', 'title'].includes(f) && asked.indexOf(f) === i,
    )
    notes.value = res.notes
    total.value = res.total
    truncated.value = res.truncated
    ran.value = true
    syncWindow(where)
  } catch (e) {
    error.value = errorText(e, t, t('query.failed'))
  } finally {
    loading.value = false
  }
}

function addRow() {
  rows.value.push({ field: '', op: 'eq', value: '' })
}

function removeRow(i: number) {
  rows.value.splice(i, 1)
  if (!rows.value.length) addRow()
}

function openNote(n: QueryNote) {
  openWindow({ type: 'note', key: planciaKey('note', n.path), title: n.title || n.path, props: { path: n.path } })
}

onMounted(async () => {
  if (props.where?.length) void run()
  try {
    projects.value = (await listProjects()).map((p) => p.name)
  } catch {
    // the project field stays free text
  }
})
</script>

<template>
  <div class="p-6 max-w-5xl mx-auto">
    <!-- The window's title bar shows the name: the heading is for screen readers. -->
    <h1 class="sr-only">{{ t('query.title') }}</h1>
    <p class="text-sm text-text-muted mb-4">
      {{ t('query.intro') }}
    </p>

    <form
      class="space-y-3"
      @submit.prevent="run"
    >
      <datalist id="query-fields">
        <option
          v-for="f in COMMON_FIELDS"
          :key="f"
          :value="f"
        />
      </datalist>
      <datalist id="query-sort">
        <option
          v-for="f in SORT_FIELDS"
          :key="f"
          :value="f"
        />
      </datalist>
      <datalist id="query-projects">
        <option
          v-for="p in projects"
          :key="p"
          :value="p"
        />
      </datalist>

      <div class="flex flex-wrap gap-2 items-center">
        <label
          class="text-sm text-text-muted"
          for="query-project"
        >{{ t('query.project') }}</label>
        <input
          id="query-project"
          v-model="project"
          list="query-projects"
          :placeholder="t('query.project_placeholder')"
          class="h-control py-0 w-56 rounded bg-bg-elevated border border-border px-2 text-sm focus:outline-none focus:ring-2 focus:ring-focus"
        >
      </div>

      <div
        v-for="(r, i) in rows"
        :key="i"
        class="flex flex-wrap gap-2 items-center"
        data-test="query-row"
      >
        <input
          v-model="r.field"
          list="query-fields"
          :placeholder="t('query.field')"
          :aria-label="t('query.field')"
          class="h-control py-0 w-44 rounded bg-bg-elevated border border-border px-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-focus"
        >
        <select
          v-model="r.op"
          :aria-label="t('query.operator')"
          class="h-control py-0 rounded bg-bg-elevated border border-border px-2 text-sm focus:outline-none focus:ring-2 focus:ring-focus"
        >
          <option
            v-for="op in QUERY_OPS"
            :key="op"
            :value="op"
          >
            {{ op }}
          </option>
        </select>
        <select
          v-if="r.op === 'exists'"
          v-model="r.value"
          :aria-label="t('query.value')"
          class="h-control py-0 rounded bg-bg-elevated border border-border px-2 text-sm focus:outline-none focus:ring-2 focus:ring-focus"
        >
          <option value="true">
            true
          </option>
          <option value="false">
            false
          </option>
        </select>
        <input
          v-else
          v-model="r.value"
          :placeholder="r.op === 'in' ? 'a, b, c' : t('query.value')"
          :aria-label="t('query.value')"
          class="h-control py-0 flex-1 min-w-40 rounded bg-bg-elevated border border-border px-2 text-sm focus:outline-none focus:ring-2 focus:ring-focus"
        >
        <button
          type="button"
          class="h-control px-2 text-sm rounded hover:bg-surface-hover text-text-muted"
          :aria-label="t('query.remove_condition', { n: i + 1 })"
          @click="removeRow(i)"
        >
          <X class="h-4 w-4" aria-hidden="true" />
        </button>
      </div>

      <div class="flex flex-wrap gap-2 items-center">
        <button
          type="button"
          class="h-control px-2 text-sm rounded border border-border hover:bg-surface-hover"
          @click="addRow"
        >
          {{ t('query.add_condition') }}
        </button>
        <span class="inline-flex gap-2 items-center ml-4">
          <label
            class="text-sm text-text-muted"
            for="query-sort-field"
          >{{ t('query.sort') }}</label>
          <input
            id="query-sort-field"
            v-model="sort"
            list="query-sort"
            :placeholder="t('query.sort_placeholder')"
            :title="t('query.sort_hint')"
            class="h-control py-0 w-56 rounded bg-bg-elevated border border-border px-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-focus"
          >
          <select
            v-model="order"
            :aria-label="t('query.order')"
            class="h-control py-0 rounded bg-bg-elevated border border-border px-2 text-sm focus:outline-none focus:ring-2 focus:ring-focus"
          >
            <option value="">
              {{ t('query.order_default') }}
            </option>
            <option value="desc">
              desc
            </option>
            <option value="asc">
              asc
            </option>
          </select>
        </span>
        <span class="inline-flex gap-2 items-center ml-4">
          <label
            class="text-sm text-text-muted"
            for="query-fields-input"
          >{{ t('query.fields') }}</label>
          <input
            id="query-fields-input"
            v-model="fieldsText"
            :placeholder="t('query.fields_placeholder')"
            class="h-control py-0 w-56 rounded bg-bg-elevated border border-border px-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-focus"
          >
        </span>
        <button
          type="submit"
          class="h-control ml-auto px-3 text-sm rounded bg-accent text-accent-fg hover:bg-accent-hover disabled:opacity-60"
          :disabled="loading"
        >
          {{ t('query.run') }}
        </button>
      </div>
    </form>

    <p
      v-if="loading"
      class="mt-6 text-text-muted text-sm"
    >
      {{ t('query.running') }}
    </p>
    <ErrorMessage
      v-else-if="error"
      :text="error"
      class="mt-6 text-sm"
      data-test="query-error"
    />
    <template v-else-if="ran">
      <p
        class="mt-6 mb-2 text-sm text-text-muted"
        data-test="query-count"
      >
        {{ t('query.count', { n: notes.length, total }) }}<span v-if="truncated"> — {{ t('query.truncated') }}</span>
      </p>
      <div
        v-if="notes.length"
        class="overflow-x-auto"
      >
        <table class="w-full text-sm">
          <thead>
            <tr class="text-left text-text-muted border-b border-border">
              <th class="py-1 pr-3 font-medium">
                {{ t('query.col_note') }}
              </th>
              <th
                v-for="c in columns"
                :key="c"
                class="py-1 pr-3 font-medium font-mono"
              >
                {{ c }}
              </th>
              <th class="py-1 font-medium">
                {{ t('query.col_modified') }}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="n in notes"
              :key="n.path"
              class="border-b border-border/50 align-top"
            >
              <td class="py-1 pr-3">
                <button
                  type="button"
                  class="font-medium hover:text-accent text-left"
                  @click="openNote(n)"
                >
                  {{ n.title || n.path }}
                </button>
                <p class="text-xs text-text-muted font-mono">
                  {{ n.path }}
                </p>
              </td>
              <td
                v-for="c in columns"
                :key="c"
                class="py-1 pr-3"
                :class="cellClass(cell(n, c))"
              >
                {{ cell(n, c) }}
              </td>
              <td class="py-1 whitespace-nowrap text-text-muted">
                <DateTime :value="n.modified" />
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p
        v-else
        class="text-text-muted text-sm"
      >
        {{ t('query.no_match') }}
      </p>
    </template>
  </div>
</template>
