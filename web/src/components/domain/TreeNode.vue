<script setup lang="ts">
/**
 * TreeNode — recursive component that renders one apiTreeNode shape
 * (see internal/api/v1/tree.go). Folders use <details> for native
 * keyboard accessibility + open/close persistence in localStorage
 * across reloads (mirrors the v1.x sidebar-tree.js behaviour).
 *
 * Project roots carry a visibility cue (lock = private, globe = public;
 * internal draws nothing) and the "+" (new note here) shows only where
 * the account may write — both from the access store, never from the role.
 * A right-click on a row (or the Menu key, or Shift+F10) opens the tree's
 * context menu (IMP-150).
 */
import type { TreeNode as TN } from '@/api/tree'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ChevronRight, Dot, Plus, Lock, Globe } from 'lucide-vue-next'
import { useRecentlyViewed } from '@/composables/useRecentlyViewed'
import { useWindowsStore } from 'plancia'
import { useAccessStore } from '@/stores/access'
import { visibilityHelp } from '@/api/access'
import { planciaKey } from '@/composables/planciaKey'
import { useTreeMenu } from '@/composables/useTreeMenu'

const props = defineProps<{ node: TN }>()
const { t } = useI18n()
const recents = useRecentlyViewed()
const windows = useWindowsStore()
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
  const title = props.node.name.replace(/\.md$/, '')
  recents.record(props.node.path, title)
  windows.open({
    type: 'note',
    key: planciaKey('note', props.node.path),
    title,
    props: { path: props.node.path },
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
  windows.open({
    type: 'create',
    key: planciaKey('create', props.node.path),
    title: t('note_create.window_title', { name: props.node.name }),
    props: { path: props.node.path },
  })
}
</script>

<template>
  <li class="text-sm">
    <template v-if="node.is_dir">
      <details
        :open="isOpen"
        @toggle="toggleExpanded(($event.target as HTMLDetailsElement).open)"
      >
        <summary
          class="group/row flex items-center gap-1.5 py-0.5 px-1 rounded cursor-pointer hover:bg-surface-hover select-none"
          aria-haspopup="menu"
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
          <button
            v-if="canWriteHere"
            type="button"
            class="rounded p-0.5 text-text-muted opacity-0 transition-opacity hover:bg-surface-hover hover:text-text group-hover/row:opacity-100 focus-visible:opacity-100"
            :title="t('tree.new_note_here')"
            :aria-label="t('tree.new_note_here')"
            @click.stop.prevent="createHere"
          >
            <Plus class="h-3.5 w-3.5" />
          </button>
          <span
            v-if="node.note_count"
            class="text-xs text-text-muted px-1"
          >{{ node.note_count }}</span>
          <span
            v-if="node.hidden_from_mcp"
            class="text-[10px] uppercase text-warning"
            :title="t('tree.hidden_hint')"
          >hidden</span>
        </summary>
        <ul class="pl-4 border-l border-border ml-1.5">
          <TreeNode
            v-for="child in node.children ?? []"
            :key="child.path"
            :node="child"
          />
        </ul>
      </details>
    </template>

    <template v-else>
      <button
        type="button"
        aria-haspopup="menu"
        @click="openNote"
        @contextmenu.prevent="onContextMenu"
        @keydown="onRowKeydown"
        class="w-full flex items-center gap-1.5 py-0.5 px-2 rounded text-left text-text-muted hover:text-text hover:bg-surface-hover truncate"
      >
        <Dot class="h-3.5 w-3.5 shrink-0 opacity-50" aria-hidden="true" />
        <span class="truncate">{{ node.name.replace(/\.md$/, '') }}</span>
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
