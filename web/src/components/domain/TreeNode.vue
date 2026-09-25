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
 */
import type { TreeNode as TN } from '@/api/tree'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Plus, Lock, Globe } from 'lucide-vue-next'
import { useRecentlyViewed } from '@/composables/useRecentlyViewed'
import { useWindowsStore } from 'plancia'
import { useAccessStore } from '@/stores/access'
import { VISIBILITY_HELP } from '@/api/access'
import { planciaKey } from '@/composables/planciaKey'

const props = defineProps<{ node: TN }>()
const { t } = useI18n()
const recents = useRecentlyViewed()
const windows = useWindowsStore()
const access = useAccessStore()

const expandedKey = computed(() => `gosidian.tree.open:${props.node.path}`)

const canWriteHere = computed(() => access.canWrite(props.node.path))

/** Visibility of a project root, for the cue next to its name. */
const visibility = computed(() =>
  props.node.is_project_root ? access.visibility(props.node.name) : undefined,
)

function toggleExpanded(open: boolean) {
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
        :open="persistedOpen()"
        @toggle="toggleExpanded(($event.target as HTMLDetailsElement).open)"
        class="group"
      >
        <summary
          class="group/row flex items-center gap-1.5 py-0.5 px-1 rounded cursor-pointer hover:bg-surface-hover select-none"
        >
          <span class="opacity-60 group-open:rotate-90 transition-transform">▸</span>
          <span class="flex-1 truncate">{{ node.name }}</span>
          <Lock
            v-if="visibility === 'private'"
            class="h-3 w-3 shrink-0 text-text-muted"
            :title="VISIBILITY_HELP.private"
            aria-label="Private project"
          />
          <Globe
            v-else-if="visibility === 'public'"
            class="h-3 w-3 shrink-0 text-success"
            :title="VISIBILITY_HELP.public"
            aria-label="Public project"
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
            title="Hidden from MCP"
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
        @click="openNote"
        class="w-full flex items-center gap-1.5 py-0.5 px-2 rounded text-left text-text-muted hover:text-text hover:bg-surface-hover truncate"
      >
        <span class="opacity-50 text-xs">·</span>
        <span class="truncate">{{ node.name.replace(/\.md$/, '') }}</span>
        <span
          v-if="node.in_progress"
          class="text-[10px] text-info ml-auto"
          title="status:in-progress"
        >●</span>
      </button>
    </template>
  </li>
</template>
