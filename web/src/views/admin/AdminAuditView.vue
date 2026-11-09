<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { computed, onMounted, reactive, ref } from 'vue'
import { ArrowDown, ArrowUp } from 'lucide-vue-next'
import { tailAudit, type AuditEntry } from '@/api/admin'
import { errorText } from '@/api/errors'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'
import ScrollRegion from '@/components/primitives/ScrollRegion.vue'
import DateTime from '@/components/primitives/DateTime.vue'
import { formatSize } from '@/api/format'
import { announce } from '@/composables/useAnnounce'
import { useAuditCatalog } from '@/composables/useAuditCatalog'
import { auditArg, auditDefaults, auditFilterOf, type AuditFilter } from './auditFilter'

// The filter, the order and the page the window's address holds; what the
// page shows goes back there (IMP-224, F-30).
const props = defineProps<{ filter?: string }>()
const emit = defineEmits<{ 'update:filter': [arg: string] }>()

const { t, tm, locale } = useI18n()

// The actions said in words, by group (IMP-220, F-25). Their catalogue,
// loaded by this page only, holds the groups; a Go test keeps it in step
// with the actions the server writes. Until it is in, the codes show.
const { ready } = useAuditCatalog()
const actionGroups = computed(() =>
  Object.entries((ready.value && (tm('admin.audit.actions') as Record<string, Record<string, unknown>>)) || {}).map(([group, phrases]) => ({
    group,
    label: t(`admin.audit.action_groups.${group}`),
    actions: Object.keys(phrases),
  })),
)
const groupOf = computed(() => new Map(actionGroups.value.flatMap((g) => g.actions.map((a) => [a, g.group] as const))))
/** An action as a phrase; one this web UI does not know stays its code. */
function actionLabel(code: string): string {
  const group = groupOf.value.get(code)
  return group ? t(`admin.audit.actions.${group}.${code}`) : code
}

const entries = ref<AuditEntry[]>([])
// How many entries match the filter in all.
const total = ref(0)
const loading = ref(false)
const error = ref<string | null>(null)

// What the page shows; the form edits a copy, which Apply shows.
const shown = ref<AuditFilter>(auditFilterOf(props.filter))
const fieldsOf = ({ actor, action, source, path_prefix, limit }: AuditFilter) => ({ actor, action, source, path_prefix, limit })
const filters = reactive(fieldsOf(shown.value))

const offset = computed(() => (shown.value.page - 1) * shown.value.limit)
const pages = computed(() => Math.max(1, Math.ceil(total.value / shown.value.limit)))
const count = (n: number) => new Intl.NumberFormat(locale.value).format(n)
const range = computed(() =>
  t('admin.audit.range', {
    from: count(offset.value + 1),
    to: count(offset.value + entries.value.length),
    total: count(total.value),
  }),
)

// A filter applied says how many entries it keeps (IMP-217, F-10), another
// page which ones it shows, an order which one; the first load says nothing.
type Say = 'count' | 'range' | 'order'
function said(say: Say): string {
  const n = total.value
  if (say === 'count') return n ? t('admin.audit.count', { n }, n) : t('admin.audit.empty')
  if (say === 'range') return range.value
  return t(shown.value.order === 'desc' ? 'admin.audit.newest_first' : 'admin.audit.oldest_first')
}

async function load(say?: Say) {
  loading.value = true
  error.value = null
  const f = shown.value
  let past = false
  try {
    const page = await tailAudit({
      actor: f.actor || undefined,
      action: f.action || undefined,
      source: f.source || undefined,
      path_prefix: f.path_prefix || undefined,
      limit: f.limit,
      order: f.order,
      offset: offset.value,
    })
    entries.value = page.items
    total.value = page.total
    // A page past the end, as an older address may ask: the last one.
    past = !page.items.length && page.total > 0 && f.page > 1
    if (say && !past) announce(said(say))
  } catch (e) {
    error.value = errorText(e, t, t('admin.audit.load_failed'))
  } finally {
    loading.value = false
  }
  if (past) await show({ page: pages.value }, say)
}

/** Shows the audit with change, and puts it in the window's address. */
function show(change: Partial<AuditFilter>, say?: Say) {
  shown.value = { ...shown.value, ...change }
  emit('update:filter', auditArg(shown.value))
  return load(say)
}

function apply() {
  // As the field allows; a number the browser let through goes back in.
  const limit = Math.min(Math.max(Math.round(Number(filters.limit)) || 100, 10), 500)
  filters.limit = limit
  void show({ ...filters, limit, page: 1 }, 'count')
}

function reset() {
  Object.assign(filters, fieldsOf(auditDefaults()))
  void show(auditDefaults(), 'count')
}

const toggleOrder = () => void show({ order: shown.value.order === 'desc' ? 'asc' : 'desc', page: 1 }, 'order')
// aria-disabled at the ends, not disabled: the button keeps the focus.
function turn(step: -1 | 1) {
  const page = shown.value.page + step
  if (loading.value || page < 1 || page > pages.value) return
  void show({ page }, 'range')
}

onMounted(() => load())
</script>

<template>
  <form
    class="grid grid-cols-[repeat(auto-fill,minmax(10rem,1fr))] gap-2 mb-4 text-sm"
    @submit.prevent="apply"
  >
    <label class="flex flex-col gap-1">
      <span class="text-xs text-text-muted">{{ t('admin.audit.actor_column') }}</span>
      <input
        v-model.trim="filters.actor"
        type="text"
        :placeholder="t('admin.audit.actor_placeholder')"
        class="h-control py-0 rounded bg-bg-elevated border border-border-input px-2"
      />
    </label>
    <label class="flex flex-col gap-1">
      <span class="text-xs text-text-muted">{{ t('admin.audit.action') }}</span>
      <select
        v-model="filters.action"
        class="h-control py-0 rounded bg-bg-elevated border border-border-input px-2"
        data-audit-action
      >
        <option value="">{{ t('admin.audit.all_actions') }}</option>
        <optgroup
          v-for="g in actionGroups"
          :key="g.group"
          :label="g.label"
        >
          <option
            v-for="a in g.actions"
            :key="a"
            :value="a"
            :title="a"
          >{{ actionLabel(a) }}</option>
        </optgroup>
      </select>
    </label>
    <label class="flex flex-col gap-1">
      <span class="text-xs text-text-muted">{{ t('admin.audit.source') }}</span>
      <input
        v-model.trim="filters.source"
        type="text"
        :placeholder="t('admin.audit.source_placeholder')"
        class="h-control py-0 rounded bg-bg-elevated border border-border-input px-2"
      />
    </label>
    <label class="flex flex-col gap-1">
      <span class="text-xs text-text-muted">{{ t('admin.audit.path') }}</span>
      <input
        v-model.trim="filters.path_prefix"
        type="text"
        :placeholder="t('admin.audit.path_placeholder')"
        class="h-control py-0 rounded bg-bg-elevated border border-border-input px-2"
      />
    </label>
    <div class="flex flex-wrap items-end gap-1 self-end">
      <label class="flex flex-col gap-1">
        <span class="text-xs text-text-muted">{{ t('admin.audit.limit') }}</span>
        <input
          v-model.number="filters.limit"
          type="number"
          min="10"
          max="500"
          class="h-control py-0 w-20 shrink-0 rounded bg-bg-elevated border border-border-input px-2"
        />
      </label>
      <button
        type="submit"
        class="h-control px-3 rounded bg-accent text-accent-fg hover:bg-accent-hover text-xs"
      >{{ t('admin.audit.apply') }}</button>
      <button
        type="button"
        class="h-control px-3 rounded border border-border hover:bg-surface-hover text-xs"
        @click="reset"
      >{{ t('graph.reset') }}</button>
    </div>
  </form>

  <!-- Another page keeps the table and its buttons in place: the focus
       stays where it was. -->
  <p v-if="loading && !entries.length" class="text-text-muted">{{ t('common.loading') }}</p>
  <ErrorMessage v-else-if="error" :text="error" />

  <p v-else-if="!entries.length" class="text-text-muted text-sm">{{ t('admin.audit.empty') }}</p>

  <template v-else>
    <ScrollRegion
      class="rounded border border-border"
      :label="ready ? t('admin.audit.table') : t('admin.tab.audit')"
      :aria-busy="loading"
      data-audit-table
    >
      <table class="w-full text-xs font-mono">
        <thead class="text-text-muted [&_th]:font-medium bg-bg-elevated">
          <tr>
            <th
              class="text-left py-2 px-2"
              :aria-sort="shown.order === 'desc' ? 'descending' : 'ascending'"
            >
              <button
                type="button"
                class="inline-flex items-center gap-1 font-medium hover:text-text"
                :title="ready ? t(shown.order === 'desc' ? 'admin.audit.newest_first' : 'admin.audit.oldest_first') : undefined"
                data-audit-order
                @click="toggleOrder"
              >
                {{ t('admin.audit.when') }}
                <ArrowDown v-if="shown.order === 'desc'" class="h-3 w-3" aria-hidden="true" />
                <ArrowUp v-else class="h-3 w-3" aria-hidden="true" />
              </button>
            </th>
            <th class="text-left py-2 px-2">{{ t('admin.audit.source') }}</th>
            <th class="text-left py-2 px-2">{{ t('admin.audit.actor_column') }}</th>
            <th class="text-left py-2 px-2">{{ t('admin.audit.action') }}</th>
            <th class="text-left py-2 px-2">{{ t('admin.audit.path') }}</th>
            <th class="text-right py-2 px-2">{{ t('admin.audit.size') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="(e, i) in entries"
            :key="i"
            class="border-t border-border"
          >
            <td class="py-1 px-2 whitespace-nowrap"><DateTime :value="e.ts" /></td>
            <td class="py-1 px-2">{{ e.source }}</td>
            <td class="py-1 px-2">{{ e.actor || e.token || '—' }}</td>
            <td class="py-1 px-2 font-sans" :title="e.action">{{ actionLabel(e.action) }}</td>
            <td class="py-1 px-2 truncate max-w-[20rem]" :title="e.path">{{ e.path || '—' }}</td>
            <td class="py-1 px-2 text-right whitespace-nowrap">{{ e.size ? formatSize(e.size) : '' }}</td>
          </tr>
        </tbody>
      </table>
    </ScrollRegion>
    <nav
      v-if="ready"
      class="mt-2 flex flex-wrap items-center gap-2 text-xs"
      :aria-label="t('admin.audit.pages')"
      data-audit-pages
    >
      <span class="text-text-muted">{{ range }}</span>
      <button
        type="button"
        class="h-control-sm px-3 rounded border border-border hover:bg-surface-hover aria-disabled:opacity-50 aria-disabled:hover:bg-transparent"
        :aria-disabled="shown.page <= 1 ? 'true' : undefined"
        data-audit-previous
        @click="turn(-1)"
      >{{ t('admin.audit.previous') }}</button>
      <button
        type="button"
        class="h-control-sm px-3 rounded border border-border hover:bg-surface-hover aria-disabled:opacity-50 aria-disabled:hover:bg-transparent"
        :aria-disabled="shown.page >= pages ? 'true' : undefined"
        data-audit-next
        @click="turn(1)"
      >{{ t('admin.audit.next') }}</button>
    </nav>
  </template>
</template>
