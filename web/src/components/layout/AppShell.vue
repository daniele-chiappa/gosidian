<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Network } from 'lucide-vue-next'
import {
  Plancia,
  PlanciaSidebar,
  usePlanciaSync,
  useOpenWindow,
  type PlanciaLabels,
  type WindowInstance,
} from 'plancia'
import TopBar from './TopBar.vue'
import Sidebar from './Sidebar.vue'
import CommandPalette from './CommandPalette.vue'
import TotpEnroll from '@/components/domain/TotpEnroll.vue'
import { windowRegistry } from '@/components/plancia/windowRegistry'
import { windowTone } from '@/components/plancia/windowModules'
import { codec, planciaKey } from '@/composables/planciaKey'
import { useSidebarResize } from '@/composables/useSidebarResize'
import { useAuthStore } from '@/stores/auth'
import { useUIStore } from '@/stores/ui'

const { t } = useI18n()
const { width, startDrag, reset } = useSidebarResize()
const auth = useAuthStore()
const ui = useUIStore()
// plancia layout mode (strip ↔ tabs), persisted in the UI store; the toggle
// lives in the TopBar. Two-way via v-model, routed through the store action.
const viewMode = computed({
  get: () => ui.planciaViewMode,
  set: (m) => ui.setPlanciaViewMode(m),
})
// URL (`?w=&f=`) ⇆ window-store sync, with localStorage fallback. The gosidian
// token scheme lives in the codec (`@/composables/planciaKey`).
const plancia = usePlanciaSync(codec, { useLocalStorage: true, storageKey: 'gosidian.plancia' })

// Map gosidian's plancia.* i18n keys onto plancia's PlanciaLabels. A computed
// (not a plain object) so the labels track locale changes reactively. Note the
// key delta: plancia's `openHint` ⇆ gosidian's `plancia.openFromSidebar`.
const planciaLabels = computed<PlanciaLabels>(() => ({
  empty: t('plancia.empty'),
  openHint: t('plancia.openFromSidebar'),
  minimizedHint: (n: number) => t('plancia.minimizedHint', { n }),
  minimizedLabel: t('plancia.minimizedLabel'),
  unknownType: (type: string) => t('plancia.unknownType', { type }),
  resize: t('plancia.resize'),
  resizeWidth: t('plancia.resizeWidth'),
  minimize: t('plancia.minimize'),
  close: t('plancia.close'),
  restore: t('plancia.restore'),
  dirty: t('plancia.dirty'),
  unsavedClose: t('plancia.unsavedClose'),
  openTag: t('plancia.openTag'),
  viewStrip: t('plancia.viewStrip'),
  viewTabs: t('plancia.viewTabs'),
  viewToggle: t('plancia.viewToggle'),
}))

// Cross-window open for the ego-graph action below (plancia provides it through
// the enclosing <Plancia>).
const openWindow = useOpenWindow()

/** Note path carried by note-shaped windows; gates the "direct links" button. */
function notePath(win: WindowInstance): string | null {
  return typeof win.props?.path === 'string' ? (win.props.path as string) : null
}

/** Open the ego-graph (direct links, depth 1) of a window's note. */
function openLinks(win: WindowInstance): void {
  const path = notePath(win)
  if (!path) return
  openWindow({
    type: 'graph',
    key: `graph:${path}`,
    title: `↳ ${win.title || path}`,
    props: { focus: path, depth: 1 },
  })
}

onMounted(() => plancia.hydrate())

function onEnrolled(codeCount: number) {
  auth.setEnrolled(true)
  auth.setRecoveryCodesRemaining(codeCount)
  auth.clearEnrollment()
}

/** Settings window, same spec the sidebar entry uses. */
function openSettings(): void {
  openWindow({ type: 'settings', key: planciaKey('settings') })
}
</script>

<template>
  <div class="h-screen flex flex-col bg-bg text-text">
    <TopBar />
    <!-- One-time notice after a login that consumed a recovery code: the
         user should know how many are left and where to regenerate them. -->
    <div
      v-if="auth.recoveryCodeUsed"
      role="status"
      class="flex items-center gap-3 border-b border-border bg-bg-elevated px-4 py-2 text-sm"
    >
      <span class="text-warning" aria-hidden="true">●</span>
      <span class="flex-1">
        {{ t('totp.recovery_used_banner', { n: auth.user?.recovery_codes_remaining ?? 0 }) }}
      </span>
      <button
        type="button"
        class="rounded border border-border px-2 py-1 text-xs hover:bg-surface-hover"
        @click="openSettings"
      >
        {{ t('totp.recovery_used_open_settings') }}
      </button>
      <button
        type="button"
        class="rounded px-2 py-1 text-xs text-text-muted hover:bg-surface-hover"
        @click="auth.dismissRecoveryNotice()"
      >
        {{ t('totp.recovery_used_dismiss') }}
      </button>
    </div>
    <div class="flex-1 flex overflow-hidden min-h-0">
      <!-- Left sidebar chrome via the library's <PlanciaSidebar> (inline shell).
           gosidian has no collapse/rail/peek, so it stays permanently expanded
           with the toggle suppressed; the host keeps driving the width through
           useSidebarResize (bound to :size) for exact behaviour parity. -->
      <PlanciaSidebar
        position="left"
        mode="inline"
        :size="width"
        :resizable="false"
        :aria-label="t('common.vault')"
        landmark="complementary"
      >
        <template #toggle><span /></template>
        <Sidebar />
      </PlanciaSidebar>

      <div
        class="w-1 cursor-col-resize bg-border hover:bg-accent/40 select-none flex-shrink-0"
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize sidebar"
        @pointerdown="startDrag"
        @dblclick="reset"
      />

      <main class="flex-1 min-w-0 overflow-hidden">
        <Plancia
          v-model:view-mode="viewMode"
          :registry="windowRegistry"
          :labels="planciaLabels"
          :resolve-tone="windowTone"
        >
          <!-- Domain action: open the ego-graph ("direct links") of a note
               window. Gated on a note path; lucide icon stays in gosidian. -->
          <template #window-actions="{ win }">
            <button
              v-if="notePath(win)"
              type="button"
              class="rounded p-1 text-text-muted hover:bg-surface-hover hover:text-text"
              :title="t('plancia.links')"
              :aria-label="t('plancia.links')"
              @click="openLinks(win)"
            >
              <Network class="h-3.5 w-3.5" />
            </button>
          </template>
        </Plancia>
      </main>
    </div>

    <!-- Forced TOTP enrolment interstitial: blocks the app when the user's
         effective policy requires two-factor but no secret is enrolled. -->
    <div
      v-if="auth.enrollmentRequired"
      class="fixed inset-0 z-50 flex items-center justify-center bg-bg/95 p-4"
    >
      <div class="w-full max-w-md rounded-lg bg-surface p-6 ring-1 ring-border shadow">
        <h2 class="text-lg font-semibold mb-1">{{ t('totp.interstitial_title') }}</h2>
        <p class="text-sm text-text-muted mb-4">{{ t('totp.interstitial_desc') }}</p>
        <TotpEnroll @done="onEnrolled" />
      </div>
    </div>

    <CommandPalette />
  </div>
</template>
