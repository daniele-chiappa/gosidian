<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Network, TriangleAlert, X } from 'lucide-vue-next'
import {
  Plancia,
  PlanciaSidebar,
  usePlanciaSync,
  useWindowsStore,
  type PlanciaLabels,
  type SidebarState,
  type WindowInstance,
} from 'plancia'
import TopBar from './TopBar.vue'
import Sidebar from './Sidebar.vue'
import CommandPalette from './CommandPalette.vue'
import ConfirmHost from '@/components/primitives/ConfirmHost.vue'
import AnnounceHost from '@/components/primitives/AnnounceHost.vue'
import TotpEnroll from '@/components/domain/TotpEnroll.vue'
import PasswordChange from '@/components/domain/PasswordChange.vue'
import { windowRegistry } from '@/components/plancia/windowRegistry'
import { windowTone } from '@/components/plancia/windowModules'
import { codec, planciaKey, retitle } from '@/composables/planciaKey'
import { useSidebarResize } from '@/composables/useSidebarResize'
import { NARROW_PX, useNarrowScreen } from '@/composables/useNarrowScreen'
import { useSessionRenewal } from '@/composables/useSessionRenewal'
import { appStale } from '@/composables/staleApp'
import { useShellPageTitle } from '@/composables/usePageTitle'
import { WINDOW_HEADING, provideOpenFocused } from '@/composables/useOpenFocused'
import { useAuthStore } from '@/stores/auth'
import { useUIStore } from '@/stores/ui'

const { t, locale } = useI18n()
const reloadPage = () => window.location.reload()
const { width, min: widthMin, max: widthMax, startDrag, onKeydown: onResizeKey, reset } = useSidebarResize()
const auth = useAuthStore()
// The session renews while the shell is used (BUG-143).
useSessionRenewal()
// The tab says the focused window and its project (IMP-217, F-07).
useShellPageTitle()
const ui = useUIStore()
// On a narrow screen (IMP-221, F-15) one window at a time takes the width.
const narrow = useNarrowScreen()
// plancia layout mode (strip ↔ tabs), persisted in the UI store; the toggle
// lives in the TopBar alone, the plancia's own is off (IMP-229). Two-way via
// v-model, routed through the store action. A narrow screen shows tabs and
// leaves the saved choice as it is.
const viewMode = computed({
  get: () => (narrow.value ? 'tabs' : ui.planciaViewMode),
  set: (m) => {
    if (!narrow.value) ui.setPlanciaViewMode(m)
  },
})
// A plugin's app is an iframe, which the browser reloads once it leaves the
// page: behind another tab it stays in the page, hidden, and keeps its work.
const keepMounted = (win: WindowInstance) => win.type === 'app'
// A window in full screen covers the page (IMP-229): the bar, the sidebar and
// the notices under it are out of reach of the keyboard meanwhile.
const fullscreen = ref(false)

// The sidebar on a narrow screen is the plancia's drawer (IMP-221, F-15),
// closed until the bar's button opens it, and at most DRAWER_PX wide. It
// closes from its header's button, Esc and its backdrop, and once the focus
// goes into a window; widening the screen brings the sidebar back inline.
// On a wide screen the bar's button and Ctrl+\ close it and open it again,
// and the choice is kept as the view's is (IMP-229).
const DRAWER_PX = 280
const sidebarEl = ref<InstanceType<typeof PlanciaSidebar> | null>(null)
const sidebarState = ref<SidebarState>(narrow.value || ui.sidebarClosed ? 'closed' : 'expanded')
const sidebarOpen = computed(() => sidebarState.value !== 'closed')
const sidebarSize = computed(() => (narrow.value ? Math.min(width.value, DRAWER_PX) : width.value))
watch(narrow, (n) => {
  sidebarState.value = n || ui.sidebarClosed ? 'closed' : 'expanded'
})
watch(sidebarState, (s) => {
  if (!narrow.value) ui.setSidebarClosed(s === 'closed')
})

/** The bar's button: the drawer opens with the focus on its first control;
 *  the sidebar of a wide screen leaves the focus where it is. */
async function toggleSidebar(): Promise<void> {
  if (sidebarOpen.value) {
    sidebarState.value = 'closed'
    return
  }
  sidebarState.value = 'expanded'
  if (!narrow.value) return
  await nextTick()
  const panel = sidebarEl.value?.$el as HTMLElement | undefined
  panel?.querySelector<HTMLElement>('.plancia-sidebar__body :is(a[href], button, input, summary, [tabindex="0"])')?.focus()
}

// Closed while it held the focus, the sidebar gives it to the bar's button,
// the one that opens it (the plancia's own is hidden, see plancia-bridge.css).
// After the plancia's move to that reveal tab, which comes on the next tick.
watch(sidebarState, (s, prev) => {
  if (s !== 'closed' || prev === 'closed') return
  const panel = sidebarEl.value?.$el as HTMLElement | undefined
  if (!panel?.contains(document.activeElement)) return
  setTimeout(() => document.querySelector<HTMLElement>('[data-sidebar-toggle]')?.focus())
})

/** Ctrl+\ (Cmd+\): the bar's button. Keys typed in a plugin's app, a frame
 *  of its own, do not reach the page. */
function onShortcut(e: KeyboardEvent): void {
  if (e.key !== '\\' || !(e.ctrlKey || e.metaKey) || e.altKey || e.shiftKey) return
  if (gated.value || fullscreen.value) return
  e.preventDefault()
  void toggleSidebar()
}
onMounted(() => window.addEventListener('keydown', onShortcut))
onBeforeUnmount(() => window.removeEventListener('keydown', onShortcut))

function onMainFocus(): void {
  if (narrow.value && sidebarOpen.value) sidebarState.value = 'closed'
}
// URL (`?w=&f=`) ⇆ window-store sync, with localStorage fallback. The gosidian
// token scheme lives in the codec (`@/composables/planciaKey`). Each window
// opened is a step of the browser's history, so Back closes it (IMP-224,
// F-29); Back asks first when the window holds unsaved edits, as its × does.
const plancia = usePlanciaSync(codec, {
  useLocalStorage: true,
  storageKey: 'gosidian.plancia',
  history: 'push',
  confirmClose: () => window.confirm(t('plancia.unsavedClose')),
})

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
  fullscreen: t('plancia.fullscreen'),
  exitFullscreen: t('plancia.exitFullscreen'),
}))

// The open of a gesture, which puts the focus on the window's title: the
// views inject it as 'openWindow', and so do the shell's own actions below
// (IMP-217, F-03). The windows hydrated from the address open without it.
const openWindow = provideOpenFocused()

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
    title: t('graph.links_title', { name: win.title || path }),
    props: { focus: path, depth: 1 },
  })
}

onMounted(() => plancia.hydrate())

// Leaving the page (a reload, a typed address, a link that got past the
// previews) asks first while a window holds unsaved edits: nothing did,
// and the drafts were lost without a word (BUG-117, S7-3).
const windowsStore = useWindowsStore()
function onBeforeUnload(e: BeforeUnloadEvent) {
  if (!windowsStore.windows.some((w) => w.dirty)) return
  e.preventDefault()
  e.returnValue = ''
}
onMounted(() => window.addEventListener('beforeunload', onBeforeUnload))
onBeforeUnmount(() => window.removeEventListener('beforeunload', onBeforeUnload))

// A change of language reaches the windows already open (BUG-155): the
// plancia keeps their titles as text, translated when they opened.
watch(locale, (to, from) => {
  for (const w of windowsStore.windows) {
    const title = retitle(w, from, to)
    if (title && title !== w.title) windowsStore.setTitle(w.id, title)
  }
})

// «Skip to the active window», the first stop of Tab (IMP-217, F-35): past
// the bar and the sidebar to the title of the focused window, or to the
// main while its view has not drawn one.
const mainEl = ref<HTMLElement | null>(null)
function skipToWindow(): void {
  const id = windowsStore.focused?.id
  const heading = id ? document.querySelector<HTMLElement>(`[data-win-id="${id}"] ${WINDOW_HEADING}`) : null
  ;(heading ?? mainEl.value)?.focus()
}

// While a gate is up (a password to change, a TOTP to enrol) the server
// refuses every data route: the sidebar and the windows are not mounted, so
// they load once the gate is gone instead of keeping a 403 from behind it.
const gated = computed(() => auth.passwordChangeRequired || auth.enrollmentRequired)

function onEnrolled(codeCount: number) {
  auth.setEnrolled(true)
  auth.setRecoveryCodesRemaining(codeCount)
  auth.clearEnrollment()
}

/** Settings window, same spec the sidebar entry uses. */
function openSettings(): void {
  openWindow({ type: 'settings', key: planciaKey('settings'), title: t('nav.settings') })
}
</script>

<template>
  <div class="h-screen flex flex-col bg-bg text-text">
    <a
      v-if="!gated && windowsStore.focused"
      href="#main"
      class="fixed -top-12 left-2 z-50 rounded bg-accent px-3 py-2 text-sm text-accent-fg focus:top-2"
      :inert="fullscreen || undefined"
      data-skip-to-window
      @click.prevent="skipToWindow"
    >
      {{ t('plancia.skipToWindow') }}
    </a>
    <TopBar
      :narrow="narrow"
      :sidebar-open="sidebarOpen"
      :inert="fullscreen || undefined"
      @toggle-sidebar="toggleSidebar"
    />
    <!-- One-time notice after a login that consumed a recovery code: the
         user should know how many are left and where to regenerate them. -->
    <div
      v-if="auth.recoveryCodeUsed"
      :inert="fullscreen || undefined"
      role="status"
      class="flex items-center gap-3 border-b border-border bg-bg-elevated px-4 py-2 text-sm"
    >
      <TriangleAlert class="h-4 w-4 shrink-0 text-warning" aria-hidden="true" />
      <span class="flex-1">
        {{ t('totp.recovery_used_banner', { n: auth.user?.recovery_codes_remaining ?? 0 }) }}
      </span>
      <button
        type="button"
        class="h-control-sm rounded border border-border px-2 text-xs hover:bg-surface-hover"
        @click="openSettings"
      >
        {{ t('totp.recovery_used_open_settings') }}
      </button>
      <button
        type="button"
        class="h-control-sm rounded px-2 text-xs text-text-muted hover:bg-surface-hover"
        @click="auth.dismissRecoveryNotice()"
      >
        {{ t('totp.recovery_used_dismiss') }}
      </button>
    </div>
    <!-- The page is older than the server: what loads at its first use does
         not come, and its place stays empty without this. -->
    <div
      v-if="appStale"
      :inert="fullscreen || undefined"
      role="alert"
      class="flex items-center gap-3 border-b border-warning/40 bg-warning/10 px-4 py-2 text-sm"
      data-app-stale
    >
      <TriangleAlert class="h-4 w-4 shrink-0 text-warning" aria-hidden="true" />
      <span class="flex-1">{{ t('shell.stale') }}</span>
      <button
        type="button"
        class="h-control-sm rounded border border-border px-2 text-xs hover:bg-surface-hover"
        @click="reloadPage"
      >
        {{ t('shell.stale_reload') }}
      </button>
    </div>
    <div v-if="!gated" class="flex-1 flex overflow-hidden min-h-0">
      <!-- Left sidebar chrome via the library's <PlanciaSidebar> (inline shell).
           gosidian has no collapse/rail/peek, so it stays permanently expanded
           with the toggle suppressed; the host keeps driving the width through
           useSidebarResize (bound to :size) for exact behaviour parity. On a
           narrow screen it is the drawer, closed by its header's button. -->
      <PlanciaSidebar
        ref="sidebarEl"
        v-model:state="sidebarState"
        position="left"
        mode="inline"
        :responsive="NARROW_PX"
        :size="sidebarSize"
        :resizable="false"
        :aria-label="t('common.vault')"
        landmark="complementary"
        class="mr-1"
        :inert="fullscreen || undefined"
      >
        <template #toggle="{ close }">
          <button
            v-if="narrow"
            type="button"
            class="plancia-btn plancia-sidebar__toggle"
            :title="t('sidebar.close')"
            :aria-label="t('sidebar.close')"
            @click="close()"
          >
            <X
              class="h-4 w-4"
              aria-hidden="true"
            />
          </button>
          <span v-else />
        </template>
        <Sidebar />
        <!-- The handle fills the gap right of the sidebar's border, inside
             its landmark (IMP-217, F-35). Its containing block is the aside,
             so the scrolling body neither clips nor scrolls it. Tab reaches
             it, and it says its width, as a window's separator (F-17). -->
        <div
          v-if="!narrow && sidebarOpen"
          class="absolute top-0 -right-[5px] h-full w-1 cursor-col-resize bg-border hover:bg-accent/40 select-none focus-visible:bg-accent/40 focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
          role="separator"
          aria-orientation="vertical"
          tabindex="0"
          :aria-label="t('sidebar.resize')"
          :aria-valuemin="widthMin"
          :aria-valuemax="widthMax"
          :aria-valuenow="width"
          @pointerdown="startDrag"
          @keydown="onResizeKey"
          @dblclick="reset"
        />
      </PlanciaSidebar>

      <main
        id="main"
        ref="mainEl"
        tabindex="-1"
        class="flex-1 min-w-0 overflow-hidden focus:outline-none"
        @focusin="onMainFocus"
      >
        <!-- The focus follows the windows (F-03): onto a window's title,
             and to the main once the last window has gone with it. -->
        <Plancia
          v-model:view-mode="viewMode"
          v-model:fullscreen="fullscreen"
          allow-fullscreen
          :show-view-toggle="false"
          :registry="windowRegistry"
          :labels="planciaLabels"
          :resolve-tone="windowTone"
          :focus-target="WINDOW_HEADING"
          :keep-mounted="keepMounted"
          @focus-out="mainEl?.focus()"
        >
          <!-- Domain action: open the ego-graph ("direct links") of a note
               window. Gated on a note path; lucide icon stays in gosidian. -->
          <!-- The empty desk names itself (IMP-217, F-35): the plancia's own
               two lines, the first as the page's h1. -->
          <template #empty>
            <div>
              <h1>{{ planciaLabels.empty }}</h1>
              <p class="mt-1 text-[0.75rem]">
                {{
                  windowsStore.minimizedList.length
                    ? planciaLabels.minimizedHint(windowsStore.minimizedList.length)
                    : planciaLabels.openHint
                }}
              </p>
            </div>
          </template>
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
    <!-- A password the owner chose (IMP-063): the account sets its own
         first; an enrolment it also owes comes after. -->
    <!-- A gate is the page while it is up, so it is the main (IMP-217). -->
    <main
      v-if="auth.passwordChangeRequired"
      class="fixed inset-0 z-50 flex items-center justify-center bg-bg/95 p-4"
      data-password-interstitial
    >
      <div class="w-full max-w-md rounded-lg bg-surface p-6 ring-1 ring-border shadow">
        <h1 class="text-lg font-semibold mb-1">
          {{ t('password.interstitial_title') }}
        </h1>
        <!-- The owner's password is never set by someone else: a temporary
             one is the password of the first start (IMP-044). -->
        <p class="text-sm text-text-muted mb-4">
          {{
            auth.isOwner
              ? t('password.interstitial_desc_first_start')
              : t('password.interstitial_desc')
          }}
        </p>
        <PasswordChange @done="auth.clearPasswordChange()" />
      </div>
    </main>
    <main
      v-else-if="auth.enrollmentRequired"
      class="fixed inset-0 z-50 flex items-center justify-center bg-bg/95 p-4"
    >
      <div class="w-full max-w-md rounded-lg bg-surface p-6 ring-1 ring-border shadow">
        <h1 class="text-lg font-semibold mb-1">{{ t('totp.interstitial_title') }}</h1>
        <p class="text-sm text-text-muted mb-4">{{ t('totp.interstitial_desc') }}</p>
        <TotpEnroll @done="onEnrolled" />
      </div>
    </main>

    <CommandPalette />
    <ConfirmHost />
    <AnnounceHost />
  </div>
</template>
