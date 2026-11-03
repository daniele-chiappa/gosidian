<script setup lang="ts">
/**
 * TreeNode — recursive component that renders one apiTreeNode shape
 * (see internal/api/v1/tree.go). A folder's row is a button that opens
 * and closes it (aria-expanded), its open state persisted in localStorage
 * across reloads; a closed folder draws nothing inside it. The folder's
 * "+" (new note here) sits beside that button, not inside it: a control
 * in a control is announced as neither (IMP-219, F-08).
 *
 * Project roots carry a visibility cue (lock = private, globe = public;
 * internal draws nothing) and the "+" shows only where the account may
 * write — both from the access store, never from the role. A right-click
 * on a row (or the Menu key, or Shift+F10) opens the tree's context menu
 * (IMP-150); the rows do not say they open a menu, as a click opens the
 * note or the folder. A project's IN folder (kind "in", IMP-172) is one
 * row, with what it holds: it opens the IN window, not its files.
 *
 * A note's row shows its title where it has one, its file name on hover
 * (NoteLabel, IMP-219, F-19); the rows stay in file-name order.
 */
import type { TreeNode as TN } from '@/api/tree'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ChevronRight, Dot, Plus, Lock, Globe, Inbox } from 'lucide-vue-next'
import { useRecentlyViewed } from '@/composables/useRecentlyViewed'
import { useOpenFocused } from '@/composables/useOpenFocused'
import { useAccessStore } from '@/stores/access'
import { visibilityHelp } from '@/api/access'
import { planciaKey } from '@/composables/planciaKey'
import { useTreeMenu } from '@/composables/useTreeMenu'
import { formatSize } from '@/api/format'
import NoteLabel from '@/components/domain/NoteLabel.vue'

const props = defineProps<{ node: TN }>()
const { t } = useI18n()
const recents = useRecentlyViewed()
// A gesture's open puts the focus on the window's title (IMP-217, F-03).
const openWindow = useOpenFocused()
const access = useAccessStore()
const menu = useTreeMenu()

const expandedKey = computed(() => `gosidian.tree.open:${props.node.path}`)

const canWriteHere = computed(() => access.canWrite(props.node.path))

/** Visibility of a project root, for the cue next to its name. */
const visibility = computed(() =>
  props.node.is_project_root ? access.visibility(props.node.name) : undefined,
)

function toggleExpanded(open: boolean) {
  isOpen.value = open
  try {
    localStorage.setItem(expandedKey.value, open ? '1' : '0')
  } catch {
    // ignore
  }
}
function persistedOpen(): boolean {
  try {
    return localStorage.getItem(expandedKey.value) === '1'
  } catch {
    return false
  }
}
// The chevron follows this folder's own state: a group-open: class matched
// any open folder above it, and turned the chevrons of closed subfolders.
const isOpen = ref(persistedOpen())

function openNote() {
  if (props.node.is_dir) return
  // The window starts with the title the row shows (F-19).
  const title = props.node.title || props.node.name.replace(/\.md$/, '')
  recents.record(props.node.path, title)
  openWindow({
    type: 'note',
    key: planciaKey('note', props.node.path),
    title,
    props: { path: props.node.path },
  })
}

function openIN() {
  const project = props.node.path.split('/')[0]
  openWindow({
    type: 'in',
    key: planciaKey('in', project),
    title: t('in.window_title', { name: project }),
    props: { project },
  })
}

// The context menu opens at the pointer; from the keyboard, under the row.
// The Menu key can fire both a keydown and a contextmenu (at 0,0): both
// land on the same spot.
function openMenuAt(row: HTMLElement, x?: number, y?: number) {
  if (x === undefined || y === undefined || (x === 0 && y === 0)) {
    const r = row.getBoundingClientRect()
    x = r.left + 8
    y = r.bottom
  }
  menu.open(props.node, x, y, row)
}
function onContextMenu(e: MouseEvent) {
  openMenuAt(e.currentTarget as HTMLElement, e.clientX, e.clientY)
}
function onRowKeydown(e: KeyboardEvent) {
  if (e.key === 'ContextMenu' || (e.shiftKey && e.key === 'F10')) {
    e.preventDefault()
    openMenuAt(e.currentTarget as HTMLElement)
  }
}

// Open the creation window pre-targeted to this folder (the + on a folder row).
function createHere() {
  openWindow({
    type: 'create',
    key: planciaKey('create', props.node.path),
    title: t('note_create.window_title', { name: props.node.name }),
    props: { path: props.node.path },
  })
}
</script>

<template>
  <li class="text-sm">
    <template v-if="node.kind === 'in'">
      <button
        type="button"
        class="w-full flex items-center gap-1.5 py-0.5 px-1 rounded text-left hover:bg-surface-hover"
        :title="t('in.tree_hint', { files: node.in?.files ?? 0, size: formatSize(node.in?.bytes ?? 0) })"
        data-tree-in
        @click="openIN"
        @contextmenu.prevent="onContextMenu"
        @keydown="onRowKeydown"
      >
        <Inbox class="h-3.5 w-3.5 shrink-0 text-text-muted" aria-hidden="true" />
        <span class="flex-1 truncate font-mono">{{ node.name }}</span>
        <span
          v-if="node.in?.new"
          class="text-xs text-info px-1"
          :aria-label="t('in.count_new', { count: node.in.new })"
        >{{ node.in.new }}</span>
        <span v-if="node.in?.files" class="text-xs text-text-muted px-1">{{ node.in.files }}</span>
      </button>
    </template>
    <template v-else-if="node.is_dir">
      <!-- The row: a toggle and, beside it, the + (F-08). The + is a 24 px
           target (F-18), shown with the pointer on the row, the focus in
           it, or always where nothing hovers. -->
      <div
        class="group/row flex items-center rounded hover:bg-surface-hover"
        data-tree-row
      >
        <button
          type="button"
          class="flex-1 min-w-0 flex items-center gap-1.5 py-0.5 px-1 rounded text-left select-none"
          :aria-expanded="isOpen"
          @click="toggleExpanded(!isOpen)"
          @contextmenu.prevent="onContextMenu"
          @keydown="onRowKeydown"
        >
          <ChevronRight
            class="h-3.5 w-3.5 shrink-0 opacity-60 transition-transform"
            :class="{ 'rotate-90': isOpen }"
            aria-hidden="true"
          />
          <span class="flex-1 truncate">{{ node.name }}</span>
          <Lock
            v-if="visibility === 'private'"
            class="h-3 w-3 shrink-0 text-text-muted"
            :title="visibilityHelp('private')"
            :aria-label="t('tree.private_project')"
          />
          <Globe
            v-else-if="visibility === 'public'"
            class="h-3 w-3 shrink-0 text-success"
            :title="visibilityHelp('public')"
            :aria-label="t('tree.public_project')"
          />
          <span
            v-if="node.note_count"
            class="text-xs text-text-muted px-1"
          >{{ node.note_count }}</span>
          <span
            v-if="node.hidden_from_mcp"
            class="text-[10px] uppercase text-warning"
            :title="t('tree.hidden_hint')"
          >hidden</span>
        </button>
        <button
          v-if="canWriteHere"
          type="button"
          class="h-6 w-6 shrink-0 inline-flex items-center justify-center rounded text-text-muted opacity-0 transition-opacity hover:bg-surface-hover hover:text-text group-hover/row:opacity-100 group-focus-within/row:opacity-100 [@media(hover:none)]:opacity-100"
          :title="t('tree.new_note_here')"
          :aria-label="t('tree.new_note_here')"
          @click="createHere"
        >
          <Plus class="h-3.5 w-3.5" />
        </button>
      </div>
      <ul v-if="isOpen" class="pl-4 border-l border-border ml-1.5">
        <TreeNode
          v-for="child in node.children ?? []"
          :key="child.path"
          :node="child"
        />
      </ul>
    </template>

    <template v-else>
      <button
        type="button"
        @click="openNote"
        @contextmenu.prevent="onContextMenu"
        @keydown="onRowKeydown"
        class="w-full flex items-center gap-1.5 py-0.5 px-2 rounded text-left text-text-muted hover:text-text hover:bg-surface-hover truncate"
      >
        <Dot class="h-3.5 w-3.5 shrink-0 opacity-50" aria-hidden="true" />
        <NoteLabel
          class="flex-1"
          :path="node.path"
          :title="node.title"
        />
        <span
          v-if="node.in_progress"
          class="ml-auto h-1.5 w-1.5 shrink-0 rounded-full bg-info"
          role="img"
          title="status:in-progress"
          :aria-label="t('tree.in_progress')"
        />
      </button>
    </template>
  </li>
</template>
