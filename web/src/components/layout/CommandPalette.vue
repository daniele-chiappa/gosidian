<script setup lang="ts">
/**
 * CommandPalette — Cmd+K (or Ctrl+K) overlay. Shows the dataset it has at
 * once and reads it again at each opening, scores entries client-side with
 * a tiny fuzzy matcher, boosts recently-viewed paths, and routes on enter.
 *
 * The matcher is intentionally simple (case-insensitive substring
 * with positional bonus) rather than a full Levenshtein/Smith-
 * Waterman: at <2k vault entries the simple version is faster, has
 * predictable ranking, and survives long unicode strings without
 * extra dependencies.
 *
 * It matches names only. With a text, its last entry looks for it in the
 * notes' text instead, in Search (IMP-218, F-06).
 *
 * To a screen reader it is a named dialog with an ARIA combobox, as
 * SearchSelect (IMP-218, F-05): the field keeps the focus, Tab included,
 * and says through aria-activedescendant which entry the arrows are on.
 * Home and End go to the first and the last entry once the arrows are in
 * the list, and move the caret in the text before.
 */
import { ref, computed, onMounted, onUnmounted, watch, nextTick, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchCommandPalette, type CommandPaletteData } from '@/api/commandPalette'
import { useRecentlyViewed } from '@/composables/useRecentlyViewed'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { Search } from 'lucide-vue-next'
import { useOpenFocused } from '@/composables/useOpenFocused'
import { planciaKey } from '@/composables/planciaKey'
import { paletteRequest } from '@/composables/usePalette'
import { appForType } from '@/composables/pluginApps'

interface PaletteItem {
  kind: 'note' | 'project' | 'tag' | 'action'
  label: string
  detail?: string
  open: OpenSpec
  /** Note path, for recents boosting. */
  path?: string
}

const { t } = useI18n()
// A gesture's open puts the focus on the window's title (IMP-217, F-03).
const openWindow = useOpenFocused()
const windows = useWindowsStore()
const recents = useRecentlyViewed()

const open = ref(false)
const query = ref('')
const dataset = ref<CommandPaletteData | null>(null)
const selected = ref(0)
// The arrows moved in the list since the text last changed.
const browsing = ref(false)
const inputEl = ref<HTMLInputElement | null>(null)
const baseId = `cp-${useId()}`
const titleId = `${baseId}-title`
const listId = `${baseId}-list`
const optionId = (i: number) => `${listId}-${i}`
// Where the palette was opened from: closed without a choice, it gives the
// focus back there (IMP-217, F-04); a choice opens a window, which takes it.
let opener: HTMLElement | null = null

// A new page layout goes where the work is: the folder of the note in the
// focused window, the note or its design (IMP-232), else of the last note
// read, else the first project. Offered only when an app opens it (IMP-226).
const canLayout = ref(false)
const layoutFolder = computed(() => {
  const win = windows.focused
  const path = win?.props?.path
  const open = (win?.type === 'note' || win?.type === 'app') && typeof path === 'string' ? path : ''
  const note = open || recents.entries.value[0]?.path || ''
  return note.slice(0, Math.max(0, note.lastIndexOf('/'))) || dataset.value?.projects[0]?.name || ''
})

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
      detail: t('projects.notes', { n: p.noteCount }, p.noteCount),
      open: { type: 'projects', key: planciaKey('projects'), title: t('nav.projects'), props: { project: p.name } },
    })
  }
  for (const tag of dataset.value.tags) {
    items.push({
      kind: 'tag',
      label: '#' + tag.tag,
      detail: t('projects.notes', { n: tag.count }, tag.count),
      open: { type: 'tags', key: planciaKey('tags', tag.tag), title: '#' + tag.tag, props: { tag: tag.tag } },
    })
  }
  if (canLayout.value && layoutFolder.value) {
    items.push({
      kind: 'action',
      label: t('plugin_app.palette_new'),
      detail: layoutFolder.value,
      open: {
        type: 'create',
        key: planciaKey('create', layoutFolder.value),
        title: t('note_create.window_title', { name: layoutFolder.value }),
        props: { path: layoutFolder.value, startKind: 'layout' },
      },
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

// With a text, the last entry searches the notes' text for it: no name
// may have it, and the Search button, which showed Ctrl+K, opened Search,
// not this palette (IMP-218, F-06).
const searchItem = computed<PaletteItem | null>(() => {
  const q = query.value.trim()
  if (!q) return null
  return {
    kind: 'action',
    label: t('palette.search_text', { query: q }),
    open: { type: 'search', key: planciaKey('search'), title: t('nav.search'), props: { q } },
  }
})
// What the keyboard moves through and Enter picks.
const entries = computed<PaletteItem[]>(() => (searchItem.value ? [...filtered.value, searchItem.value] : filtered.value))

// The answer to the last opening wins; an older one is kept only while there
// is no list at all, so a newer request that fails leaves something to show.
let fetchSeq = 0
const itemKey = (i: PaletteItem) => i.kind + ':' + (i.path ?? i.label)
// The kind shows only when the list mixes kinds: a badge "note" on every
// row of a list of notes said nothing.
const mixedKinds = computed(() => new Set(filtered.value.map((i) => i.kind)).size > 1)
async function refreshDataset() {
  const seq = ++fetchSeq
  try {
    const d = await fetchCommandPalette()
    if (seq !== fetchSeq && dataset.value) return
    // The entry the keyboard is on stays selected, wherever the new list
    // puts it: Enter opened whatever came to sit at its old place.
    const kept = entries.value[selected.value]
    dataset.value = d
    const i = kept ? entries.value.findIndex((x) => itemKey(x) === itemKey(kept)) : -1
    selected.value = i >= 0 ? i : 0
  } catch {
    // Not fatal: the palette keeps the list it has, or none, and the next
    // opening asks again.
  }
}

async function show(q = '') {
  opener = document.activeElement instanceof HTMLElement ? document.activeElement : null
  open.value = true
  void appForType('layout').then((a) => (canLayout.value = a !== null))
  query.value = q
  selected.value = 0
  browsing.value = false
  // The list was read once a session: a note created later never showed
  // until a reload. The one it has shows at once, and the server's comes
  // in behind it.
  if (dataset.value) void refreshDataset()
  else await refreshDataset()
  await nextTick()
  inputEl.value?.focus()
}
function hide(giveBack = true) {
  open.value = false
  if (giveBack && opener?.isConnected) opener.focus()
  opener = null
}

// A Search or Projects already open only takes the focus: what was asked
// goes to it too, with when it was asked, so the same asked again acts
// again (IMP-218, F-06; BUG-160).
const askedAt: Record<string, string> = { search: 'qAt', projects: 'projectAt' }

function pick(item: PaletteItem | undefined) {
  if (!item) return
  hide(false)
  const id = openWindow(item.open)
  const at = askedAt[item.open.type]
  if (at && id) windows.identify(id, planciaKey(item.open.type), { ...item.open.props, [at]: Date.now() })
}

// A move of the keyboard keeps the entry in view: a mouse is already on it.
function moveTo(i: number) {
  if (!entries.value.length) return
  browsing.value = true
  selected.value = Math.max(0, Math.min(i, entries.value.length - 1))
  void nextTick(() => document.getElementById(optionId(selected.value))?.scrollIntoView?.({ block: 'nearest' }))
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
    moveTo(selected.value + 1)
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    moveTo(selected.value - 1)
  } else if ((e.key === 'Home' || e.key === 'End') && browsing.value) {
    e.preventDefault()
    moveTo(e.key === 'Home' ? 0 : entries.value.length - 1)
  } else if (e.key === 'Tab') {
    // The field is the dialog's one control: Tab left it for the page
    // under the dialog.
    e.preventDefault()
    inputEl.value?.focus()
  } else if (e.key === 'Enter') {
    e.preventDefault()
    pick(entries.value[selected.value])
  }
}

// Another view opens it with a text (IMP-218, F-26).
watch(paletteRequest, (r) => {
  if (r) void show(r.q)
})

watch(query, () => {
  selected.value = 0
  browsing.value = false
})
// A list read again while the palette is open can be shorter.
watch(entries, (items) => {
  if (selected.value >= items.length) selected.value = Math.max(items.length - 1, 0)
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
      :aria-labelledby="titleId"
      @click.self="hide()"
    >
      <div
        class="w-[min(640px,90vw)] rounded-lg bg-surface ring-1 ring-border shadow-lg overflow-hidden"
      >
        <h2
          :id="titleId"
          class="sr-only"
        >
          {{ t('palette.title') }}
        </h2>
        <input
          ref="inputEl"
          v-model="query"
          type="text"
          autocomplete="off"
          role="combobox"
          aria-autocomplete="list"
          :aria-labelledby="titleId"
          :aria-expanded="entries.length > 0"
          :aria-controls="listId"
          :aria-activedescendant="entries.length ? optionId(selected) : undefined"
          :placeholder="t('palette.placeholder')"
          class="w-full px-4 py-3 bg-bg-elevated border-b border-border-input focus:outline-none"
        />
        <p
          v-if="!filtered.length"
          class="px-4 py-3 text-sm text-text-muted"
        >
          <i18n-t keypath="palette.no_matches" scope="global">
            <template #key><kbd>Esc</kbd></template>
          </i18n-t>
        </p>
        <ul
          v-if="entries.length"
          :id="listId"
          role="listbox"
          :aria-labelledby="titleId"
          class="max-h-[50vh] overflow-auto"
        >
          <li
            v-for="(item, idx) in filtered"
            :id="optionId(idx)"
            :key="itemKey(item)"
            role="option"
            :aria-selected="idx === selected"
            :class="[
              'flex items-center gap-3 px-4 py-2 cursor-pointer',
              idx === selected ? 'bg-surface-hover' : 'hover:bg-surface-hover',
            ]"
            @click="pick(item)"
            @mouseenter="selected = idx"
          >
            <span
              v-if="mixedKinds"
              class="text-xs px-1.5 py-0.5 rounded bg-bg-elevated text-text-muted"
            >{{ t(`palette.category_${item.kind}`) }}</span>
            <span class="flex-1 truncate">{{ item.label }}</span>
            <span
              v-if="item.detail"
              class="text-xs text-text-muted truncate max-w-[40%]"
            >{{ item.detail }}</span>
          </li>
          <li
            v-if="searchItem"
            :id="optionId(filtered.length)"
            role="option"
            :aria-selected="selected === filtered.length"
            :class="[
              'flex items-center gap-3 px-4 py-2 cursor-pointer border-t border-border',
              selected === filtered.length ? 'bg-surface-hover' : 'hover:bg-surface-hover',
            ]"
            @click="pick(searchItem)"
            @mouseenter="selected = filtered.length"
          >
            <Search
              class="w-4 h-4 shrink-0 text-text-muted"
              aria-hidden="true"
            />
            <span class="flex-1 truncate">{{ searchItem.label }}</span>
          </li>
        </ul>
      </div>
    </div>
  </Teleport>
</template>
