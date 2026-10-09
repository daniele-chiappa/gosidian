<script setup lang="ts">
/**
 * NewRowForm — the form that adds a row to a database from one of its views
 * (IMP-127 phase 5). It asks for the row's name, suggested by the server
 * from the numbering of the rows and editable, its title, and the required
 * fields that neither the view nor the template give a value; the values
 * the view gives (its filters, or the board column) are shown and sent.
 *
 * The server writes the row from the database's template and checks it
 * against the schema (POST .../rows). A name taken meanwhile comes back as
 * 409 with the next one, which the form proposes; on success the new note
 * opens as a window, and the view shows it when its event comes.
 */
import { computed, inject, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import axios from 'axios'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { createRow, getNewRow, type FieldValue, type NewRow } from '@/api/notes'
import type { ViewColumn } from '@/api/preview'
import { planciaKey, base } from '@/composables/planciaKey'
import { toChange } from './cellValue'

const props = defineProps<{
  database: string
  /** The values the row starts with; null leaves a field out. */
  values?: Record<string, FieldValue>
}>()
const emit = defineEmits<{ done: [path?: string] }>()
const { t } = useI18n()
const openWindow = inject<((spec: OpenSpec) => string) | null>('openWindow', null)

const info = ref<NewRow | null>(null)
const name = ref('')
const title = ref('')
const extra = reactive<Record<string, string | boolean | string[]>>({})
const creating = ref(false)
const message = ref('')

const given = computed(() =>
  Object.entries(props.values ?? {}).filter(
    (e): e is [string, Exclude<FieldValue, null>] => e[1] !== null,
  ),
)

// The required fields the row would lack: not the name and title, nor
// those the view or the template give.
const asked = computed<ViewColumn[]>(() =>
  (info.value?.columns ?? []).filter(
    (c) =>
      c.required &&
      c.name !== 'id' &&
      c.name !== 'title' &&
      !given.value.some(([k]) => k === c.name) &&
      !(info.value?.preset ?? []).includes(c.name),
  ),
)

onMounted(async () => {
  try {
    info.value = await getNewRow(props.database)
    name.value = info.value.name
    for (const c of asked.value) {
      extra[c.name] =
        c.type === 'select'
          ? (c.options?.[0] ?? '')
          : c.type === 'checkbox'
            ? false
            : c.type === 'multi-select'
              ? []
              : ''
    }
  } catch (e) {
    message.value = failure(e)
  }
})

function focusOnMount(el: unknown) {
  if (el instanceof HTMLElement) el.focus()
}

const show = (v: Exclude<FieldValue, null>) => (Array.isArray(v) ? v.join(', ') : String(v))

const inputType = (type?: string) =>
  type === 'date' ? 'date' : type === 'number' ? 'number' : type === 'url' ? 'url' : 'text'

function toggle(field: string, opt: string, on: boolean) {
  const cur = (extra[field] as string[]) ?? []
  extra[field] = on ? [...cur.filter((o) => o !== opt), opt] : cur.filter((o) => o !== opt)
}

function failure(e: unknown): string {
  if (axios.isAxiosError(e) && e.response) {
    const err = (
      e.response.data as { error?: { message?: string; details?: Record<string, unknown> } }
    )?.error
    switch (e.response.status) {
      case 409: {
        const next = String(err?.details?.name ?? '')
        const taken = name.value
        if (next) name.value = next
        return t('views.name_taken', { name: taken, next: next || '—' })
      }
      case 422: {
        const problems = (err?.details?.problems ?? []) as { message?: string }[]
        const reason = problems.length
          ? problems.map((p) => p.message).join('; ')
          : (err?.message ?? '')
        return t('views.create_invalid', { reason })
      }
      case 403:
        return t('views.create_forbidden')
    }
  }
  return t('views.create_failed')
}

function open(path: string) {
  const spec: OpenSpec = {
    type: 'note',
    key: planciaKey('note', path),
    title: base(path),
    props: { path },
  }
  if (openWindow) openWindow(spec)
  else useWindowsStore().open(spec)
}

async function submit() {
  if (creating.value || !info.value || !name.value.trim()) return
  const values: Record<string, FieldValue> = Object.fromEntries(given.value)
  for (const c of asked.value) {
    const change = toChange(c, extra[c.name] ?? '')
    if ('error' in change) {
      message.value = t('views.not_a_number', { field: c.name })
      return
    }
    if ('set' in change) values[c.name] = change.set
  }
  creating.value = true
  message.value = ''
  try {
    const note = await createRow(props.database, {
      name: name.value.trim(),
      title: title.value.trim(),
      values,
    })
    open(note.path)
    emit('done', note.path)
  } catch (e) {
    message.value = failure(e)
  } finally {
    creating.value = false
  }
}
</script>

<template>
  <form
    class="not-prose my-2 flex flex-col gap-2 rounded border border-border bg-bg-elevated p-2 text-sm"
    :aria-label="t('views.new_row')"
    data-new-row
    @submit.prevent="submit"
    @keydown.esc.prevent="emit('done')"
  >
    <label class="flex flex-col gap-0.5">
      <span class="text-xs text-text-muted">{{ t('views.row_name') }}</span>
      <input
        v-model="name"
        type="text"
        class="w-full rounded border-border bg-bg px-1 py-0.5 text-sm text-text"
        autocomplete="off"
        required
        data-row-name
      />
    </label>
    <label class="flex flex-col gap-0.5">
      <span class="text-xs text-text-muted">{{ t('views.row_title') }}</span>
      <input
        :ref="focusOnMount"
        v-model="title"
        type="text"
        class="w-full rounded border-border bg-bg px-1 py-0.5 text-sm text-text"
        autocomplete="off"
        data-row-title
      />
    </label>
    <template v-for="c in asked" :key="c.name">
      <div
        v-if="c.type === 'multi-select'"
        class="flex flex-col gap-0.5"
        role="group"
        :aria-label="c.name"
      >
        <span class="text-xs text-text-muted">{{ c.name }}</span>
        <label v-for="o in c.options ?? []" :key="o" class="flex items-center gap-1">
          <input
            type="checkbox"
            class="rounded-sm border-border bg-bg text-accent focus:ring-focus"
            :checked="(extra[c.name] as string[]).includes(o)"
            @change="toggle(c.name, o, ($event.target as HTMLInputElement).checked)"
          />
          {{ o }}
        </label>
      </div>
      <label v-else-if="c.type === 'checkbox'" class="flex items-center gap-1">
        <input
          v-model="extra[c.name]"
          type="checkbox"
          class="rounded-sm border-border bg-bg text-accent focus:ring-focus"
        />
        <span class="text-xs text-text-muted">{{ c.name }}</span>
      </label>
      <label v-else class="flex flex-col gap-0.5">
        <span class="text-xs text-text-muted">{{ c.name }}</span>
        <select
          v-if="c.type === 'select'"
          v-model="extra[c.name]"
          class="w-full rounded border-border bg-bg py-0.5 text-sm text-text"
          :data-field="c.name"
        >
          <option v-for="o in c.options ?? []" :key="o" :value="o">{{ o }}</option>
        </select>
        <input
          v-else
          v-model="extra[c.name]"
          :type="inputType(c.type)"
          :step="c.type === 'number' ? 'any' : undefined"
          :placeholder="
            c.type === 'list'
              ? t('views.list_hint')
              : c.type === 'relation'
                ? t('views.relation_hint')
                : undefined
          "
          class="w-full rounded border-border bg-bg px-1 py-0.5 text-sm text-text"
          :data-field="c.name"
        />
      </label>
    </template>
    <p v-if="given.length" class="m-0 text-xs text-text-muted" data-row-values>
      {{ t('views.row_with') }}
      <template v-for="([k, v], i) in given" :key="k"
        >{{ i ? ' · ' : ' ' }}<span class="text-text">{{ k }}: {{ show(v) }}</span></template
      >
    </p>
    <div class="flex gap-2">
      <button
        type="submit"
        class="rounded bg-accent px-2 py-0.5 text-sm font-medium text-accent-fg hover:bg-accent-hover disabled:opacity-50"
        :disabled="creating || !info || !name.trim()"
      >
        {{ t('views.create') }}
      </button>
      <button
        type="button"
        class="rounded px-2 py-0.5 text-sm text-text-muted hover:bg-surface-hover hover:text-text"
        @click="emit('done')"
      >
        {{ t('views.cancel') }}
      </button>
    </div>
    <p
      :class="message ? 'm-0 text-sm text-danger' : 'sr-only'"
      role="status"
      aria-live="polite"
      data-view-message
    >
      {{ message }}
    </p>
  </form>
</template>
