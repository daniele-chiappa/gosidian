<script setup lang="ts">
/**
 * TreeContextMenu — gosidian's own menu on the sidebar tree (IMP-150), in
 * place of the browser's. A note downloads and deletes; a folder downloads
 * as a zip and goes to the trash as one entry; a project root only
 * downloads (its delete stays in the Projects window, which also handles
 * its access). Delete shows only where the account may write, and never on
 * a base or a canvas, which are read-only. Without the trash a note's
 * delete says it is for good, and a folder has none: the server refuses it.
 *
 * Mounted once by the sidebar, opened by TreeNode through useTreeMenu. It
 * closes on Esc (focus back to the row), Tab, a click outside, a scroll, a
 * resize or when the window loses focus.
 */
import { computed, nextTick, onBeforeUnmount, ref, watch, type Component } from 'vue'
import { useI18n } from 'vue-i18n'
import { Download, Trash2 } from 'lucide-vue-next'
import { useWindowsStore } from 'plancia'
import { useTreeMenu } from '@/composables/useTreeMenu'
import { useAccessStore } from '@/stores/access'
import { useTreeStore } from '@/stores/tree'
import { apiErrorMessage } from '@/api/client'
import { downloadErrorMessage } from '@/api/download'
import { deleteFolder, exportFolder } from '@/api/folders'
import { downloadNote, isReadOnlyNotePath } from '@/api/noteDownload'
import { deleteNote } from '@/api/notes'
import { exportProject } from '@/api/projects'
import type { TreeNode } from '@/api/tree'

interface Item {
  key: 'download' | 'delete'
  label: string
  icon: Component
  run: (node: TreeNode) => Promise<void>
}

const { t } = useI18n()
const menu = useTreeMenu()
const access = useAccessStore()
const treeStore = useTreeStore()
const windows = useWindowsStore()

const el = ref<HTMLElement | null>(null)
const pos = ref({ left: 0, top: 0 })
const node = computed(() => menu.state.node)

const items = computed<Item[]>(() => {
  const n = node.value
  if (!n) return []
  const download = (label: string, run: Item['run']): Item => ({ key: 'download', label, icon: Download, run })
  const remove = (run: Item['run']): Item => ({ key: 'delete', label: t('tree.menu.delete'), icon: Trash2, run })
  if (n.is_dir && n.is_project_root) {
    return [download(t('tree.menu.download_zip'), (x) => exportProject(x.name))]
  }
  if (n.is_dir) {
    const out = [download(t('tree.menu.download_zip'), (x) => exportFolder(x.path))]
    if (access.canWrite(n.path) && access.trash !== false) out.push(remove(removeFolder))
    return out
  }
  const out = [download(t('tree.menu.download'), (x) => downloadNote(x.path))]
  if (access.canWrite(n.path) && !isReadOnlyNotePath(n.path)) out.push(remove(removeNote))
  return out
})

async function activate(item: Item) {
  const n = node.value
  if (!n) return
  // Closed first, so the confirmation and the download start without it.
  menu.close(true)
  try {
    await item.run(n)
  } catch (e) {
    window.alert(
      item.key === 'download'
        ? downloadErrorMessage(e, t('tree.menu.download_failed'))
        : apiErrorMessage(e, t('tree.menu.delete_failed')),
    )
  }
}

async function removeNote(n: TreeNode) {
  if (!window.confirm(t(access.deleteNoteKey, { path: n.path }))) return
  await deleteNote(n.path)
  afterDelete([n.path])
}

async function removeFolder(n: TreeNode) {
  const count = n.note_count ?? 0
  if (!window.confirm(t('tree.menu.confirm_delete_folder', { path: n.path, count }, count))) return
  const res = await deleteFolder(n.path)
  afterDelete(res.removed)
}

// The windows of the deleted notes close, unless they hold unsaved edits:
// those stay, and the note window says the note changed.
function afterDelete(paths: string[]) {
  const gone = new Set(paths)
  for (const w of [...windows.windows]) {
    if (w.type === 'note' && !w.dirty && gone.has(String(w.props.path))) windows.close(w.id)
  }
  treeStore.refresh()
}

function menuItems(): HTMLButtonElement[] {
  return [...(el.value?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]') ?? [])]
}

function onKeydown(e: KeyboardEvent) {
  const buttons = menuItems()
  const i = buttons.indexOf(document.activeElement as HTMLButtonElement)
  const focusAt = (j: number) => buttons[(j + buttons.length) % buttons.length]?.focus()
  switch (e.key) {
    case 'Escape':
      e.preventDefault()
      menu.close(true)
      break
    case 'Tab':
      menu.close()
      break
    case 'ArrowDown':
      e.preventDefault()
      focusAt(i + 1)
      break
    case 'ArrowUp':
      e.preventDefault()
      focusAt(i < 0 ? -1 : i - 1)
      break
    case 'Home':
      e.preventDefault()
      focusAt(0)
      break
    case 'End':
      e.preventDefault()
      focusAt(-1)
      break
  }
}

function onPointerDown(e: Event) {
  if (el.value && !el.value.contains(e.target as Node)) menu.close()
}
function dismiss() {
  menu.close()
}
function listen(on: boolean) {
  const fn = on ? 'addEventListener' : 'removeEventListener'
  document[fn]('pointerdown', onPointerDown, true)
  window[fn]('scroll', dismiss, true)
  window[fn]('resize', dismiss)
  window[fn]('blur', dismiss)
}

// Opened at the pointer (or under the row, from the keyboard), then pulled
// back inside the viewport once its size is known.
watch(
  () => [menu.state.node, menu.state.x, menu.state.y] as const,
  async ([n, x, y]) => {
    listen(false)
    if (!n) return
    pos.value = { left: x, top: y }
    await nextTick()
    const r = el.value?.getBoundingClientRect()
    if (!r) return
    const clamp = (v: number, size: number, max: number) => Math.max(4, Math.min(v, max - size - 4))
    pos.value = { left: clamp(x, r.width, window.innerWidth), top: clamp(y, r.height, window.innerHeight) }
    menuItems()[0]?.focus()
    listen(true)
  },
)

onBeforeUnmount(() => listen(false))
</script>

<template>
  <Teleport to="body">
    <div
      v-if="node && items.length"
      ref="el"
      role="menu"
      :aria-label="t('tree.menu.label', { name: node.name })"
      class="fixed z-[60] min-w-[10rem] rounded border border-border bg-surface py-1 text-sm shadow-lg"
      :style="{ left: `${pos.left}px`, top: `${pos.top}px` }"
      @keydown="onKeydown"
      @contextmenu.prevent
    >
      <button
        v-for="item in items"
        :key="item.key"
        type="button"
        role="menuitem"
        tabindex="-1"
        class="flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-surface-hover focus:bg-surface-hover focus:outline-none"
        :class="item.key === 'delete' ? 'text-danger' : 'text-text'"
        @click="activate(item)"
      >
        <component
          :is="item.icon"
          class="h-3.5 w-3.5 shrink-0"
        />
        <span>{{ item.label }}</span>
      </button>
    </div>
  </Teleport>
</template>
