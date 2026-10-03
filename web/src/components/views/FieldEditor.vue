<script setup lang="ts">
/**
 * FieldEditor — the editor of one field of a database row, typed by the
 * schema: the options of a select, checkboxes for a multi-select, a date or
 * number input, text for the rest (lists comma-separated). A relation
 * suggests notes by title as you type and writes the chosen one as a
 * [[wikilink]]. Used by the cells of ViewTable and by the property panel.
 *
 * It emits `commit` with what it holds (the text, or the options picked)
 * on change, Enter or blur, and `cancel` on Escape. Checkboxes are not
 * edited here: they toggle in place.
 */
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useDebounceFn } from '@vueuse/core'
import type { ViewColumn } from '@/api/preview'
import { suggestNoteTitles, type NoteTitleHit } from '@/api/noteTitles'
import { isListType } from './cellValue'

const props = defineProps<{ column: ViewColumn; value?: string | string[]; label: string }>()
const emit = defineEmits<{ commit: [input: string | string[]]; cancel: [] }>()
const { t } = useI18n()

const v = props.value
const draft = ref<string | string[]>(
  isListType(props.column)
    ? Array.isArray(v)
      ? [...v]
      : v
        ? [v]
        : []
    : Array.isArray(v)
      ? v.join(', ')
      : (v ?? ''),
)

function focusOnMount(el: unknown) {
  if (el instanceof HTMLElement) el.focus()
}

let relationInput: HTMLInputElement | null = null
function relationRef(el: unknown) {
  if (el instanceof HTMLInputElement) {
    if (!relationInput) el.focus()
    relationInput = el
  }
}

const inputValue = (e: Event) => (e.target as HTMLInputElement | HTMLSelectElement).value

function toggleOption(opt: string, on: boolean) {
  const cur = Array.isArray(draft.value) ? draft.value : []
  draft.value = on ? [...cur.filter((o) => o !== opt), opt] : cur.filter((o) => o !== opt)
}

// Leaving the group (or the input and its suggestions) commits; moving
// inside it does not.
function onFocusOut(e: FocusEvent) {
  const box = e.currentTarget as HTMLElement
  if (e.relatedTarget instanceof Node && box.contains(e.relatedTarget)) return
  emit('commit', draft.value)
}

// Relation: suggestions for the text after the last comma.
const suggestions = ref<NoteTitleHit[]>([])
const lastSegment = (s: string) => (s.split(',').pop() ?? '').trim()
const suggest = useDebounceFn(async (q: string) => {
  if (!q || q.startsWith('[[')) {
    suggestions.value = []
    return
  }
  try {
    suggestions.value = await suggestNoteTitles(q, 8)
  } catch {
    suggestions.value = []
  }
}, 200)

function onRelationInput(e: Event) {
  draft.value = inputValue(e)
  void suggest(lastSegment(String(draft.value)))
}

function pick(hit: NoteTitleHit) {
  const link = `[[${hit.path.replace(/\.md$/, '')}]]`
  const segments = String(draft.value).split(',')
  segments[segments.length - 1] = (segments.length > 1 ? ' ' : '') + link
  draft.value = segments.join(',')
  suggestions.value = []
  // Back to the input, so picking a note does not count as leaving it.
  relationInput?.focus()
}

const inputType = (type?: string) =>
  type === 'date' ? 'date' : type === 'number' ? 'number' : type === 'url' ? 'url' : 'text'
</script>

<template>
  <select
    v-if="column.type === 'select'"
    :ref="focusOnMount"
    class="w-full rounded border-border bg-bg-elevated py-0.5 text-sm text-text"
    :value="draft"
    :aria-label="label"
    @change="emit('commit', inputValue($event))"
    @blur="emit('cancel')"
    @keydown.esc.prevent="emit('cancel')"
  >
    <option v-if="!column.required" value="">—</option>
    <option v-for="o in column.options ?? []" :key="o" :value="o">{{ o }}</option>
  </select>
  <div
    v-else-if="column.type === 'multi-select'"
    class="flex flex-col gap-0.5 text-sm"
    role="group"
    :aria-label="label"
    @focusout="onFocusOut"
    @keydown.esc.prevent="emit('cancel')"
    @keydown.enter.prevent="emit('commit', draft)"
  >
    <label v-for="(o, i) in column.options ?? []" :key="o" class="flex items-center gap-1">
      <input
        :ref="i === 0 ? focusOnMount : undefined"
        type="checkbox"
        class="rounded-sm border-border bg-bg-elevated text-accent focus:ring-accent"
        :checked="Array.isArray(draft) && draft.includes(o)"
        @change="toggleOption(o, ($event.target as HTMLInputElement).checked)"
      />
      {{ o }}
    </label>
  </div>
  <div
    v-else-if="column.type === 'relation'"
    class="relative"
    @focusout="onFocusOut"
    @keydown.esc.prevent="emit('cancel')"
  >
    <input
      :ref="relationRef"
      type="text"
      class="w-full rounded border-border bg-bg-elevated px-1 py-0.5 text-sm text-text"
      :value="draft"
      :placeholder="t('views.relation_hint')"
      :aria-label="label"
      autocomplete="off"
      @input="onRelationInput"
      @keydown.enter.prevent="emit('commit', draft)"
    />
    <ul
      v-if="suggestions.length"
      class="absolute z-10 mt-1 max-h-48 w-full overflow-auto rounded border border-border bg-bg-elevated p-0 text-sm shadow-lg"
      :aria-label="t('views.suggestions')"
    >
      <li v-for="h in suggestions" :key="h.path" class="m-0 list-none p-0">
        <button
          type="button"
          class="w-full px-2 py-1 text-left hover:bg-surface-hover focus-visible:bg-surface-hover"
          @click="pick(h)"
        >
          {{ h.title }} <span class="text-xs text-text-muted">{{ h.path }}</span>
        </button>
      </li>
    </ul>
  </div>
  <input
    v-else
    :ref="focusOnMount"
    :type="inputType(column.type)"
    :step="column.type === 'number' ? 'any' : undefined"
    class="w-full rounded border-border bg-bg-elevated px-1 py-0.5 text-sm text-text"
    :value="draft"
    :placeholder="column.type === 'list' ? t('views.list_hint') : undefined"
    :aria-label="label"
    @change="column.type === 'date' ? emit('commit', inputValue($event)) : undefined"
    @keydown.enter.prevent="emit('commit', inputValue($event))"
    @keydown.esc.prevent="emit('cancel')"
    @blur="emit('commit', inputValue($event))"
  />
</template>
