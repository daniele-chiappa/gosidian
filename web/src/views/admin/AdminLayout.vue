<script setup lang="ts">
/** AdminLayout — admin pages as a single plancia window with internal tabs.
 *  The child routes were retired with the plancia, so the sub-views are
 *  rendered here via <component :is> instead of a nested <RouterView>. The
 *  `section` window prop selects the initial tab, and follows the tab
 *  chosen; `audit` holds the audit's filter. Owner-only. */
import { useI18n } from 'vue-i18n'
import { defineAsyncComponent, markRaw, ref, type Component } from 'vue'
import { useWindowsStore } from 'plancia'
import { planciaKey } from '@/composables/planciaKey'
import { useAuthStore } from '@/stores/auth'
import { exportVault } from '@/api/admin'
import { errorText } from '@/api/errors'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'

const { t } = useI18n()

const auth = useAuthStore()

const lazy = (loader: () => Promise<unknown>): Component =>
  markRaw(defineAsyncComponent(loader as () => Promise<Component>))

interface Tab { key: string; comp: Component }
const tabs: Tab[] = [
  { key: 'users', comp: lazy(() => import('./AdminUsersView.vue')) },
  { key: 'teams', comp: lazy(() => import('./AdminTeamsView.vue')) },
  { key: 'tokens', comp: lazy(() => import('./AdminTokensView.vue')) },
  { key: 'spa-tokens', comp: lazy(() => import('./AdminSpaTokensView.vue')) },
  { key: 'invites', comp: lazy(() => import('./AdminInvitesView.vue')) },
  { key: 'fonts', comp: lazy(() => import('./AdminFontsView.vue')) },
  { key: 'audit', comp: lazy(() => import('./AdminAuditView.vue')) },
]

const props = defineProps<{ section?: string; audit?: string }>()
const active = ref<string>(tabs.some((tab) => tab.key === props.section) ? props.section! : 'users')
// The audit's filter, kept while another tab shows (IMP-224, F-30).
const audit = ref(props.audit ?? '')

const current = () => tabs.find((tab) => tab.key === active.value)?.comp

// The window is the admin's one (planciaKey): the tab shown, and the
// audit's filter while it shows, go to its address and its title, as
// QueryView does with its query. identify merges the props it is given:
// audit goes as undefined to leave the address.
const store = useWindowsStore()
function syncWindow() {
  const w = store.windows.find((x) => x.type === 'admin' && x.key === planciaKey('admin'))
  if (!w) return
  store.identify(w.id, w.key, {
    section: active.value,
    audit: active.value === 'audit' && audit.value ? audit.value : undefined,
  })
  store.setTitle(w.id, `${t('nav.admin')} · ${t(`admin.tab.${active.value}`)}`)
}
function select(key: string) {
  active.value = key
  syncWindow()
}
function onAudit(arg: string) {
  audit.value = arg
  syncWindow()
}

const exporting = ref(false)
const exportError = ref<string | null>(null)

async function exportZip() {
  exportError.value = null
  exporting.value = true
  try {
    await exportVault()
  } catch (e) {
    exportError.value = errorText(e, t, t('admin.export_failed'))
  } finally {
    exporting.value = false
  }
}
</script>

<template>
  <div class="p-6 max-w-6xl mx-auto">
    <!-- The window's title bar shows the name: the heading is for screen readers. -->
    <h1
      class="sr-only"
      tabindex="-1"
      data-window-heading
    >
      {{ t('admin.title') }}
    </h1>
    <ErrorMessage v-if="exportError" :text="exportError" class="text-sm mb-3" />
    <p v-if="!auth.isOwner" class="text-danger text-sm mb-6">
      {{ t('admin.owner_only') }}
    </p>

    <template v-else>
      <nav class="flex flex-wrap gap-1 mb-6 border-b border-border text-sm" data-admin-sections>
        <button
          v-for="tab in tabs"
          :key="tab.key"
          type="button"
          :data-section="tab.key"
          class="px-3 py-2 -mb-px border-b-2"
          :class="active === tab.key ? 'border-accent text-accent' : 'border-transparent hover:text-text'"
          :aria-current="active === tab.key ? 'page' : undefined"
          @click="select(tab.key)"
        >{{ t(`admin.tab.${tab.key}`) }}</button>
        <button
          type="button"
          class="ml-auto mb-1 h-control-sm self-center rounded border border-border px-3 hover:bg-surface-hover disabled:opacity-50"
          :title="t('admin.export_hint')"
          :disabled="exporting"
          @click="exportZip"
        >{{ exporting ? t('projects.exporting') : t('admin.export') }}</button>
      </nav>

      <!-- The tab's name, shown on its button: the tabs' headings are h3. -->
      <h2 class="sr-only">
        {{ t(`admin.tab.${active}`) }}
      </h2>
      <component
        :is="current()"
        v-if="active === 'audit'"
        :filter="audit"
        @update:filter="onAudit"
      />
      <component :is="current()" v-else />
    </template>
  </div>
</template>
