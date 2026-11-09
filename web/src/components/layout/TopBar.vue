<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useAccessStore } from '@/stores/access'
import { useUIStore } from '@/stores/ui'
import { useOpenFocused } from '@/composables/useOpenFocused'
import { planciaKey } from '@/composables/planciaKey'
import { roleLabel } from '@/api/access'
import { resetSessionState } from '@/composables/useSessionReset'
import { Search, LogOut, LogIn, Columns2, SquareStack, PanelLeft } from 'lucide-vue-next'
import InsightsBadge from '@/components/layout/InsightsBadge.vue'
import UploadsBadge from '@/components/layout/UploadsBadge.vue'
import OverflowMenu, { type OverflowItem } from '@/components/primitives/OverflowMenu.vue'

// The bar's button opens and closes the sidebar: inline on a wide screen,
// with Ctrl+\ too (IMP-229). On a narrow screen (IMP-221, F-15) it opens the
// sidebar's drawer, the bar shows no choice of view (AppShell shows one
// window at a time), and keeps the account's name, its role and Sign out in
// a menu.
defineProps<{ narrow?: boolean; sidebarOpen?: boolean }>()
const emit = defineEmits<{ 'toggle-sidebar': [] }>()

const { t } = useI18n()

const router = useRouter()
const auth = useAuthStore()
const access = useAccessStore()
const ui = useUIStore()
// A gesture's open puts the focus on the window's title (IMP-217, F-03).
const openWindow = useOpenFocused()

function openSearch() {
  openWindow({ type: 'search', key: planciaKey('search'), title: t('nav.search') })
}

async function handleLogout() {
  await auth.logout()
  // Drop the tree, access map, recents and window layout of the account
  // that just left, so the next one never glimpses them while its own
  // requests are in flight.
  resetSessionState()
  await router.push('/login')
}

const accountLabel = computed(() =>
  auth.username ? t('topbar.menu', { name: auth.username, role: roleLabel(auth.user?.role) }) : t('topbar.menu_anonymous'),
)
const accountItems = computed<OverflowItem[]>(() => [
  auth.isAnonymous
    ? { key: 'login', label: t('common.login'), icon: LogIn, run: () => void router.push('/login') }
    : { key: 'logout', label: t('common.logout'), icon: LogOut, run: () => void handleLogout() },
])
</script>

<template>
  <header
    class="flex items-center justify-between px-4 py-2 border-b border-border bg-bg-elevated"
  >
    <div class="flex items-center gap-3">
      <button
        type="button"
        class="h-control-sm px-1.5 rounded border border-border hover:bg-surface-hover inline-flex items-center"
        :title="narrow ? t('sidebar.toggle') : `${t('sidebar.toggle')} (Ctrl+\\)`"
        :aria-label="t('sidebar.toggle')"
        :aria-expanded="!!sidebarOpen"
        :aria-keyshortcuts="narrow ? undefined : 'Control+Backslash'"
        data-sidebar-toggle
        @click="emit('toggle-sidebar')"
      >
        <PanelLeft
          class="w-4 h-4"
          aria-hidden="true"
        />
      </button>
      <span class="font-semibold tracking-tight">gosidian</span>
      <button
        type="button"
        class="h-control-sm text-xs text-text-muted hover:text-text px-2 rounded border border-border ml-3 inline-flex items-center gap-1"
        @click="openSearch"
      >
        <Search class="w-3 h-3" />
        <span>{{ t('nav.search') }}</span>
      </button>

      <!-- plancia layout toggle: niri strip ↔ tabs (persisted in the UI store) -->
      <div
        v-if="!narrow"
        class="inline-flex items-center rounded border border-border ml-1 overflow-hidden"
      >
        <button
          type="button"
          :class="[
            'h-control-sm px-1.5 inline-flex items-center',
            ui.planciaViewMode === 'strip'
              ? 'bg-accent/10 text-accent'
              : 'text-text-muted hover:text-text hover:bg-surface-hover',
          ]"
          :title="t('plancia.viewStrip')"
          :aria-label="t('plancia.viewStrip')"
          :aria-pressed="ui.planciaViewMode === 'strip'"
          @click="ui.setPlanciaViewMode('strip')"
        >
          <Columns2 class="w-3.5 h-3.5" />
        </button>
        <button
          type="button"
          :class="[
            'h-control-sm px-1.5 inline-flex items-center border-l border-border',
            ui.planciaViewMode === 'tabs'
              ? 'bg-accent/10 text-accent'
              : 'text-text-muted hover:text-text hover:bg-surface-hover',
          ]"
          :title="t('plancia.viewTabs')"
          :aria-label="t('plancia.viewTabs')"
          :aria-pressed="ui.planciaViewMode === 'tabs'"
          @click="ui.setPlanciaViewMode('tabs')"
        >
          <SquareStack class="w-3.5 h-3.5" />
        </button>
      </div>
    </div>
    <div class="flex items-center gap-3 text-sm">
      <UploadsBadge />
      <InsightsBadge v-if="auth.isOwner" />
      <OverflowMenu
        v-if="narrow"
        :items="accountItems"
        :label="accountLabel"
      />
      <template v-else>
        <span class="text-text-muted hidden md:inline">{{ auth.username }}</span>
        <span
          v-if="auth.isOwner"
          class="px-2 py-0.5 rounded text-xs bg-accent/10 text-accent"
        >{{ roleLabel('owner') }}</span>
        <span
          v-else-if="auth.user?.role === 'member'"
          class="px-2 py-0.5 rounded text-xs border border-border text-text-muted"
          :title="t(access.restricted ? 'topbar.member_restricted' : 'topbar.member', { role: roleLabel('member'), r: access.readableCount, w: access.writableCount })"
        >{{ roleLabel('member') }}</span>
        <span
          v-else-if="auth.isGuest"
          class="px-2 py-0.5 rounded text-xs border border-border text-text-muted"
          :title="t('topbar.guest')"
        >{{ roleLabel('guest') }}</span>
        <button
          v-if="auth.isAnonymous"
          type="button"
          class="h-control-sm px-2 rounded text-xs hover:bg-surface-hover inline-flex items-center gap-1"
          @click="router.push('/login')"
        >
          <LogIn class="w-3 h-3" />
          {{ t('common.login') }}
        </button>
        <button
          v-else
          type="button"
          class="h-control-sm px-2 rounded text-xs hover:bg-surface-hover inline-flex items-center gap-1"
          @click="handleLogout"
        >
          <LogOut class="w-3 h-3" />
          {{ t('common.logout') }}
        </button>
      </template>
    </div>
  </header>
</template>
