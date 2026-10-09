<script setup lang="ts" generic="T">
/**
 * SearchSelect — combobox primitive with a free-text input + a
 * dropdown of pre-fetched, pre-sorted items. Used by GraphView's
 * Project / Tag / Focus filters; can be reused anywhere a "type to
 * search a known list, fall back to free text" picker is the right
 * affordance.
 *
 * The parent owns:
 *   - `items`: the full list, in the desired display order. No
 *     sorting happens here.
 *   - `valueKey(item)`: extracts the string the parent commits when
 *     the user picks an item (e.g. project name, tag name, note
 *     path).
 *   - `label(item)` / `secondary(item)?`: the two columns in each
 *     dropdown row.
 *   - `modelValue`: the committed string. Two-way bound; the parent
 *     persists it to URL / Pinia / wherever.
 *   - `placeholder`, `limit`: cosmetic.
 *
 * Behaviour:
 *   - Focus opens the dropdown.
 *   - Type filters by case-insensitive substring against label +
 *     secondary; commits to v-model on every keystroke so free-form
 *     filters work without a confirm step.
 *   - Click an entry → commit valueKey(entry) verbatim (overwrites
 *     the typed query so the visible value matches what's applied).
 *   - Esc / Enter / blur outside → close.
 *   - × button clears the value and reopens the dropdown.
 */
import { useI18n } from 'vue-i18n'
import { computed, ref, useTemplateRef, watch } from 'vue'
import { onClickOutside } from '@vueuse/core'

const { t } = useI18n()

interface Props<U> {
  modelValue: string
  items: U[]
  valueKey: (item: U) => string
  label: (item: U) => string
  secondary?: (item: U) => string
  placeholder?: string
  limit?: number
}
const props = withDefaults(defineProps<Props<T>>(), {
  secondary: undefined,
  placeholder: '',
  limit: 15,
})
const emit = defineEmits<{
  (e: 'update:modelValue', v: string): void
}>()

const open = ref(false)
const query = ref<string>(props.modelValue)
const root = useTemplateRef<HTMLElement>('root')
onClickOutside(root, () => {
  open.value = false
})

// Keep the input in sync if the parent resets the model (e.g. Reset
// button on GraphView). Skips writes the user just made themselves.
watch(
  () => props.modelValue,
  (v) => {
    if (v !== query.value) query.value = v
  },
)

const filtered = computed<T[]>(() => {
  const q = query.value.trim().toLowerCase()
  const list = q
    ? props.items.filter((it) => {
        const l = props.label(it).toLowerCase()
        if (l.includes(q)) return true
        const s = props.secondary?.(it).toLowerCase() ?? ''
        return s.includes(q)
      })
    : props.items
  return list.slice(0, props.limit)
})

function onInput() {
  // Free-typing both filters the dropdown AND commits the literal
  // string, so a user who types something not in the list still
  // applies it as a filter on Enter/blur.
  open.value = true
  emit('update:modelValue', query.value)
}
function pick(item: T) {
  const v = props.valueKey(item)
  query.value = v
  emit('update:modelValue', v)
  open.value = false
}
function clear() {
  query.value = ''
  emit('update:modelValue', '')
  open.value = true
}
function commitAndClose() {
  open.value = false
}
</script>

<template>
  <div ref="root" class="relative">
    <div class="relative">
      <input
        v-model="query"
        type="text"
        :placeholder="placeholder"
        autocomplete="off"
        class="w-full rounded bg-bg border border-border pl-2 pr-7 py-1.5 text-sm"
        @focus="open = true"
        @input="onInput"
        @keydown.escape="commitAndClose"
        @keydown.enter.prevent="commitAndClose"
      />
      <button
        v-if="query"
        type="button"
        class="absolute right-1 top-1/2 -translate-y-1/2 text-text-muted hover:text-text px-1 text-xs"
        :title="t('select.clear')"
        @click="clear"
      >×</button>
    </div>
    <ul
      v-if="open && filtered.length"
      class="absolute z-20 left-0 right-0 mt-1 max-h-72 overflow-auto rounded border border-border bg-bg-elevated shadow-lg text-sm"
    >
      <li
        v-for="(item, i) in filtered"
        :key="valueKey(item) || String(i)"
        class="px-2 py-1.5 cursor-pointer hover:bg-surface-hover flex items-center gap-2"
        :class="valueKey(item) === modelValue ? 'bg-surface-hover' : ''"
        @mousedown.prevent="pick(item)"
      >
        <span class="flex-1 truncate">{{ label(item) }}</span>
        <span v-if="secondary" class="text-xs text-text-muted shrink-0">{{ secondary(item) }}</span>
      </li>
    </ul>
    <p
      v-else-if="open && !items.length"
      class="absolute z-20 left-0 right-0 mt-1 px-2 py-1.5 rounded border border-border bg-bg-elevated text-xs text-text-muted"
    >{{ t('select.no_options') }}</p>
    <p
      v-else-if="open && query && !filtered.length"
      class="absolute z-20 left-0 right-0 mt-1 px-2 py-1.5 rounded border border-border bg-bg-elevated text-xs text-text-muted"
    >{{ t('select.free_filter') }}</p>
  </div>
</template>
