<script setup lang="ts">
/** AdminLayout — admin pages as a single plancia window with internal tabs.
 *  The child routes were retired with the plancia, so the sub-views are
 *  rendered here via <component :is> instead of a nested <RouterView>. The
 *  `section` window prop selects the initial tab. Owner-only. */
import { useI18n } from 'vue-i18n'
import { defineAsyncComponent, markRaw, ref, type Component } from 'vue'
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
  { key: 'audit', comp: lazy(() => import('./AdminAuditView.vue')) },
]

const props = defineProps<{ section?: string }>()
const active = ref<string>(tabs.some((tab) => tab.key === props.section) ? props.section! : 'users')

const current = () => tabs.find((tab) => tab.key === active.value)?.comp

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
    <h1 class="sr-only">{{ t('admin.title') }}</h1>
    <ErrorMessage v-if="exportError" :text="exportError" class="text-sm mb-3" />
    <p v-if="!auth.isOwner" class="text-danger text-sm mb-6">
      {{ t('admin.owner_only') }}
    </p>

    <template v-else>
      <nav class="flex flex-wrap gap-1 mb-6 border-b border-border text-sm">
        <button
          v-for="tab in tabs"
          :key="tab.key"
          type="button"
          class="px-3 py-2 -mb-px border-b-2"
          :class="active === tab.key ? 'border-accent text-accent' : 'border-transparent hover:text-text'"
          @click="active = tab.key"
        >{{ t(`admin.tab.${tab.key}`) }}</button>
        <button
          type="button"
          class="ml-auto mb-1 h-control-sm self-center rounded border border-border px-3 hover:bg-surface-hover disabled:opacity-50"
          :title="t('admin.export_hint')"
          :disabled="exporting"
          @click="exportZip"
        >{{ exporting ? t('projects.exporting') : t('admin.export') }}</button>
      </nav>

      <component :is="current()" />
    </template>
  </div>
</template>
