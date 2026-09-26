<script setup lang="ts">
import { onMounted, computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useTreeStore } from '@/stores/tree'
import { useRecentlyViewed } from '@/composables/useRecentlyViewed'
import { useSSE } from '@/composables/useSSE'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { useAuthStore } from '@/stores/auth'
import { useAccessStore } from '@/stores/access'
import { planciaKey } from '@/composables/planciaKey'
import TreeNode from '@/components/domain/TreeNode.vue'
import {
  RefreshCw,
  ChevronRight,
  Search,
  ListFilter,
  Network,
  Folder,
  Tags,
  Trash2,
  Settings,
  Shield,
} from 'lucide-vue-next'

const { t } = useI18n()

const treeStore = useTreeStore()
const recents = useRecentlyViewed()
const windows = useWindowsStore()
const auth = useAuthStore()
const access = useAccessStore()
const sse = useSSE(['tree', 'sidebar'])

const root = computed(() => treeStore.byProject[''])
const loading = computed(() => Boolean(treeStore.loading['']))
const error = computed(() => treeStore.error[''])

// ── Collapsible menu (req #6): the app menu lives in the sidebar, collapsed by
// default; expanding it pushes the project tree down. Each entry opens a window
// instead of navigating. State persisted across reloads.
const MENU_KEY = 'gosidian.sidebar.menuOpen'
function loadMenuOpen(): boolean {
  try {
    return localStorage.getItem(MENU_KEY) === '1'
  } catch {
    return false
  }
}
const menuOpen = ref(loadMenuOpen())
function toggleMenu() {
  menuOpen.value = !menuOpen.value
  try {
    localStorage.setItem(MENU_KEY, menuOpen.value ? '1' : '0')
  } catch {
    /* ignore */
  }
}

interface MenuItem { key: string; label: string; icon: unknown; spec: OpenSpec }
const menuItems = computed<MenuItem[]>(() => {
  // The menu label doubles as the window title: these views never emit one
  // themselves, and plancia renders an empty tab for a title-less window.
  const item = (key: string, label: string, icon: unknown): MenuItem => ({
    key,
    label,
    icon,
    spec: { type: key, key: planciaKey(key), title: label },
  })
  const items: MenuItem[] = [
    item('search', t('nav.search', 'Search'), Search),
    item('query', t('nav.query', 'Query'), ListFilter),
    item('graph', t('nav.graph'), Network),
    item('projects', t('nav.projects'), Folder),
    item('tags', t('nav.tags'), Tags),
  ]
  if (auth.canWrite) {
    items.push(item('trash', t('nav.trash'), Trash2))
    items.push(item('settings', t('nav.settings'), Settings))
  }
  if (auth.isOwner) {
    items.push(item('admin', t('nav.admin', 'Admin'), Shield))
  }
  return items
})
function openMenu(spec: OpenSpec) {
  windows.open(spec)
}

function openRecent(path: string, title: string) {
  windows.open({ type: 'note', key: planciaKey('note', path), title, props: { path } })
}

onMounted(() => {
  void treeStore.load()
  void access.load()
  sse.on('tree', () => {
    treeStore.invalidate()
    void treeStore.load()
    // A project may have appeared or gone: refresh the per-project levels
    // that gate the + buttons and draw the visibility cues.
    void access.load()
  })
  // Grants and visibility changes publish on the sidebar topic.
  sse.on('sidebar', () => void access.load())
})
</script>

<template>
  <aside class="h-full flex flex-col bg-bg overflow-hidden">
    <!-- Menu (collapsible) -->
    <div class="border-b border-border">
      <button
        type="button"
        class="w-full flex items-center gap-1.5 px-3 py-2 text-xs font-semibold uppercase tracking-wide text-text-muted hover:text-text"
        :aria-expanded="menuOpen"
        @click="toggleMenu"
      >
        <ChevronRight
          class="w-3.5 h-3.5 transition-transform"
          :class="menuOpen ? 'rotate-90' : ''"
        />
        <span>{{ t('nav.menu', 'Menu') }}</span>
      </button>
      <ul v-if="menuOpen" class="pb-2">
        <li v-for="item in menuItems" :key="item.key">
          <button
            type="button"
            class="w-full flex items-center gap-2 px-3 py-1 text-sm text-text-muted hover:text-text hover:bg-surface-hover"
            @click="openMenu(item.spec)"
          >
            <component :is="item.icon" class="w-3.5 h-3.5 shrink-0" />
            <span class="truncate">{{ item.label }}</span>
          </button>
        </li>
      </ul>
    </div>

    <!-- Vault tree -->
    <div class="px-3 py-2 border-b border-border flex items-center justify-between">
      <span class="text-xs font-semibold uppercase tracking-wide text-text-muted">
        {{ t('common.vault') }}
      </span>
      <button
        type="button"
        class="text-text-muted hover:text-text disabled:opacity-50"
        :disabled="loading"
        @click="treeStore.load()"
        :aria-label="'Refresh tree'"
        :title="'Refresh tree'"
      >
        <RefreshCw class="w-3.5 h-3.5" :class="loading ? 'animate-spin' : ''" />
      </button>
    </div>

    <div class="flex-1 overflow-auto px-2 py-2">
      <p v-if="loading && !root" class="text-xs text-text-muted px-1">{{ t('common.loading') }}</p>
      <p v-else-if="error" class="text-xs text-danger px-1">{{ error }}</p>
      <ul v-else-if="root && root.children?.length">
        <TreeNode
          v-for="child in root.children"
          :key="child.path"
          :node="child"
        />
      </ul>
      <p v-else class="text-xs text-text-muted px-1">No notes yet.</p>
    </div>

    <div
      v-if="recents.entries.value.length"
      class="border-t border-border px-3 py-2"
    >
      <p class="text-xs font-semibold uppercase tracking-wide text-text-muted mb-1">
        Recent
      </p>
      <ul class="space-y-0.5 text-sm">
        <li
          v-for="entry in recents.entries.value.slice(0, 5)"
          :key="entry.path"
        >
          <button
            type="button"
            @click="openRecent(entry.path, entry.title)"
            class="block w-full truncate text-left text-text-muted hover:text-text"
          >{{ entry.title }}</button>
        </li>
      </ul>
    </div>
  </aside>
</template>
