<script setup lang="ts">
/**
 * CommandPalette — Cmd+K (or Ctrl+K) overlay. Fetches the dataset
 * once per session, scores entries client-side with a tiny fuzzy
 * matcher, boosts recently-viewed paths, and routes on enter.
 *
 * The matcher is intentionally simple (case-insensitive substring
 * with positional bonus) rather than a full Levenshtein/Smith-
 * Waterman: at <2k vault entries the simple version is faster, has
 * predictable ranking, and survives long unicode strings without
 * extra dependencies.
 */
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchCommandPalette, type CommandPaletteData } from '@/api/commandPalette'
import { useRecentlyViewed } from '@/composables/useRecentlyViewed'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { planciaKey } from '@/composables/planciaKey'

interface PaletteItem {
  kind: 'note' | 'project' | 'tag' | 'action'
  label: string
  detail?: string
  open: OpenSpec
  /** Note path, for recents boosting. */
  path?: string
}

const { t } = useI18n()
const windows = useWindowsStore()
const recents = useRecentlyViewed()

const open = ref(false)
const query = ref('')
const dataset = ref<CommandPaletteData | null>(null)
const selected = ref(0)
const inputEl = ref<HTMLInputElement | null>(null)

const baseItems = computed<PaletteItem[]>(() => {
  const items: PaletteItem[] = []
  if (!dataset.value) return items
  for (const n of dataset.value.notes) {
    items.push({
      kind: 'note',
      label: n.title || n.path,
      detail: n.path,
      path: n.path,
      open: {
        type: 'note',
        key: planciaKey('note', n.path),
        title: n.title || n.path,
        props: { path: n.path },
      },
    })
  }
  for (const p of dataset.value.projects) {
    items.push({
      kind: 'project',
      label: p.name,
      detail: `${p.noteCount} notes`,
      open: { type: 'projects', key: planciaKey('projects'), title: t('nav.projects'), props: { project: p.name } },
    })
  }
  for (const t of dataset.value.tags) {
    items.push({
      kind: 'tag',
      label: '#' + t.tag,
      detail: `${t.count} notes`,
      open: { type: 'tags', key: planciaKey('tags', t.tag), title: '#' + t.tag, props: { tag: t.tag } },
    })
  }
  return items
})

function score(item: PaletteItem, q: string): number {
  if (!q) {
    // Boost recents when the query is empty.
    const isRecent = !!item.path && recents.entries.value.some((e) => e.path === item.path)
    return isRecent ? 100 : 1
  }
  const haystack = (item.label + ' ' + (item.detail ?? '')).toLowerCase()
  const needle = q.toLowerCase()
  const idx = haystack.indexOf(needle)
  if (idx < 0) return 0
  // Earlier match wins, label match outweighs detail match.
  let s = 100 - Math.min(idx, 90)
  if (item.label.toLowerCase().startsWith(needle)) s += 50
  return s
}

const filtered = computed<PaletteItem[]>(() => {
  const q = query.value.trim()
  return baseItems.value
    .map((item) => ({ item, s: score(item, q) }))
    .filter((x) => x.s > 0)
    .sort((a, b) => b.s - a.s)
    .slice(0, 30)
    .map((x) => x.item)
})

async function show() {
  open.value = true
  query.value = ''
  selected.value = 0
  if (!dataset.value) {
    try {
      dataset.value = await fetchCommandPalette()
    } catch {
      // Dataset fetch failure isn't fatal — the palette stays open
      // with whatever data we have. The user sees an empty list and
      // can close + retry.
    }
  }
  await nextTick()
  inputEl.value?.focus()
}
function hide() {
  open.value = false
}

function pick(item: PaletteItem | undefined) {
  if (!item) return
  hide()
  windows.open(item.open)
}

function onKey(e: KeyboardEvent) {
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
    e.preventDefault()
    if (open.value) hide()
    else void show()
    return
  }
  if (!open.value) return
  if (e.key === 'Escape') {
    e.preventDefault()
    hide()
  } else if (e.key === 'ArrowDown') {
    e.preventDefault()
    selected.value = Math.min(selected.value + 1, filtered.value.length - 1)
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    selected.value = Math.max(selected.value - 1, 0)
  } else if (e.key === 'Enter') {
    e.preventDefault()
    pick(filtered.value[selected.value])
  }
}

watch(query, () => {
  selected.value = 0
})

onMounted(() => {
  window.addEventListener('keydown', onKey)
})
onUnmounted(() => {
  window.removeEventListener('keydown', onKey)
})
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="fixed inset-0 z-40 flex items-start justify-center pt-24 bg-overlay/60"
      role="dialog"
      aria-modal="true"
      @click.self="hide"
    >
      <div
        class="w-[min(640px,90vw)] rounded-lg bg-surface ring-1 ring-border shadow-lg overflow-hidden"
      >
        <input
          ref="inputEl"
          v-model="query"
          type="text"
          placeholder="Jump to a note, project, or tag…"
          class="w-full px-4 py-3 bg-bg-elevated border-b border-border focus:outline-none"
        />
        <ul class="max-h-[50vh] overflow-auto">
          <li
            v-for="(item, idx) in filtered"
            :key="item.kind + ':' + (item.path ?? item.label)"
            :class="[
              'flex items-center gap-3 px-4 py-2 cursor-pointer',
              idx === selected ? 'bg-surface-hover' : 'hover:bg-surface-hover',
            ]"
            @click="pick(item)"
            @mouseenter="selected = idx"
          >
            <span
              class="text-[10px] uppercase tracking-wider px-1.5 py-0.5 rounded bg-bg-elevated text-text-muted"
            >{{ item.kind }}</span>
            <span class="flex-1 truncate">{{ item.label }}</span>
            <span
              v-if="item.detail"
              class="text-xs text-text-muted truncate max-w-[40%]"
            >{{ item.detail }}</span>
          </li>
          <li
            v-if="!filtered.length"
            class="px-4 py-3 text-sm text-text-muted"
          >
            No matches. Press <kbd>Esc</kbd> to close.
          </li>
        </ul>
      </div>
    </div>
  </Teleport>
</template>
