<script setup lang="ts">
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useUIStore } from '@/stores/ui'
import { useWindowsStore } from 'plancia'
import { planciaKey } from '@/composables/planciaKey'
import { Search, LogOut, LogIn, Columns2, SquareStack } from 'lucide-vue-next'
import InsightsBadge from '@/components/layout/InsightsBadge.vue'

const { t } = useI18n()

const router = useRouter()
const auth = useAuthStore()
const ui = useUIStore()
const windows = useWindowsStore()

function openSearch() {
  windows.open({ type: 'search', key: planciaKey('search'), title: t('nav.search', 'Search') })
}

async function handleLogout() {
  await auth.logout()
  await router.push('/login')
}
</script>

<template>
  <header
    class="flex items-center justify-between px-4 py-2 border-b border-border bg-bg-elevated"
  >
    <div class="flex items-center gap-3">
      <span class="font-semibold tracking-tight">gosidian</span>
      <button
        type="button"
        class="text-xs text-text-muted hover:text-text px-2 py-0.5 rounded border border-border ml-3 inline-flex items-center gap-1"
        @click="openSearch"
      >
        <Search class="w-3 h-3" />
        <span>{{ t('nav.search', 'Search') }}</span>
        <kbd class="opacity-60">⌘K</kbd>
      </button>

      <!-- plancia layout toggle: niri strip ↔ tabs (persisted in the UI store) -->
      <div class="inline-flex items-center rounded border border-border ml-1 overflow-hidden">
        <button
          type="button"
          :class="[
            'px-1.5 py-1 inline-flex items-center',
            ui.planciaViewMode === 'strip'
              ? 'bg-accent/20 text-accent'
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
            'px-1.5 py-1 inline-flex items-center border-l border-border',
            ui.planciaViewMode === 'tabs'
              ? 'bg-accent/20 text-accent'
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
      <InsightsBadge v-if="auth.isOwner" />
      <span class="text-text-muted hidden md:inline">{{ auth.username }}</span>
      <span
        v-if="auth.isOwner"
        class="px-2 py-0.5 rounded text-xs bg-accent/20 text-accent"
      >owner</span>
      <span
        v-else-if="auth.isGuest"
        class="px-2 py-0.5 rounded text-xs border border-border text-text-muted"
        title="Read-only guest — public projects only"
      >guest</span>
      <button
        v-if="auth.isAnonymous"
        type="button"
        class="px-2 py-1 rounded text-xs hover:bg-surface-hover inline-flex items-center gap-1"
        @click="router.push('/login')"
      >
        <LogIn class="w-3 h-3" />
        {{ t('common.login') }}
      </button>
      <button
        v-else
        type="button"
        class="px-2 py-1 rounded text-xs hover:bg-surface-hover inline-flex items-center gap-1"
        @click="handleLogout"
      >
        <LogOut class="w-3 h-3" />
        {{ t('common.logout') }}
      </button>
    </div>
  </header>
</template>
