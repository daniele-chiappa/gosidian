<script setup lang="ts">
/**
 * OverflowMenu — a ⋯ button that opens a short list of actions, where a
 * window toolbar puts what does not fit (IMP-156). It closes on a pick, on
 * Escape (focus back to the button), on a click outside, or when the focus
 * leaves it for another control; the arrows move between the entries.
 */
import { nextTick, ref, type Component } from 'vue'
import { onClickOutside } from '@vueuse/core'
import { Ellipsis } from 'lucide-vue-next'

export interface OverflowItem {
  key: string
  label: string
  icon?: Component
  disabled?: boolean
  run: () => void
}

defineProps<{ items: OverflowItem[]; label: string }>()

const open = ref(false)
const root = ref<HTMLElement | null>(null)
const button = ref<HTMLButtonElement | null>(null)

const entries = () => [...(root.value?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not(:disabled)') ?? [])]

onClickOutside(root, () => close())

async function show() {
  open.value = true
  await nextTick()
  entries()[0]?.focus()
}

function close(refocus = false) {
  if (!open.value) return
  open.value = false
  if (refocus) button.value?.focus()
}

function pick(item: OverflowItem) {
  if (item.disabled) return
  close(true)
  item.run()
}

function onKeydown(e: KeyboardEvent) {
  if (!open.value) return
  if (e.key === 'Escape') {
    e.preventDefault()
    e.stopPropagation()
    close(true)
    return
  }
  if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return
  e.preventDefault()
  const list = entries()
  const i = list.indexOf(document.activeElement as HTMLButtonElement)
  const down = e.key === 'ArrowDown'
  // From outside the entries (the ⋯ button), the first or the last one.
  const next = i < 0 ? (down ? 0 : list.length - 1) : down ? i + 1 : i - 1
  list[(next + list.length) % list.length]?.focus()
}

// Tabbing away closes it. A blur to nowhere does not: Safari, and Firefox
// on macOS, give a clicked button no focus, so the entry lost it to null
// before its click (onClickOutside handles a click elsewhere).
function onFocusOut(e: FocusEvent) {
  if (!(e.relatedTarget instanceof Node) || root.value?.contains(e.relatedTarget)) return
  close()
}
</script>

<template>
  <div ref="root" class="relative" @keydown="onKeydown" @focusout="onFocusOut">
    <button
      ref="button"
      type="button"
      class="rounded p-1 text-text-muted hover:bg-surface-hover hover:text-text"
      :title="label"
      :aria-label="label"
      aria-haspopup="menu"
      :aria-expanded="open"
      data-overflow-button
      @click="open ? close() : show()"
    >
      <Ellipsis class="h-3.5 w-3.5" />
    </button>
    <ul
      v-if="open"
      role="menu"
      :aria-label="label"
      class="absolute right-0 top-full z-[60] mt-1 min-w-44 rounded border border-border bg-bg-elevated py-1 text-xs shadow-lg"
    >
      <li v-for="item in items" :key="item.key" role="none">
        <button
          type="button"
          role="menuitem"
          :disabled="item.disabled"
          class="flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-surface-hover focus-visible:bg-surface-hover disabled:opacity-50"
          :data-overflow-item="item.key"
          @mousedown.prevent
          @click="pick(item)"
        >
          <component :is="item.icon" v-if="item.icon" class="h-3.5 w-3.5 shrink-0" />
          {{ item.label }}
        </button>
      </li>
    </ul>
  </div>
</template>
