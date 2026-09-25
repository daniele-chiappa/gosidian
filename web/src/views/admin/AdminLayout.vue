<script setup lang="ts">
/** AdminLayout — admin pages as a single plancia window with internal tabs.
 *  The child routes were retired with the plancia, so the sub-views are
 *  rendered here via <component :is> instead of a nested <RouterView>. The
 *  `section` window prop selects the initial tab. Owner-only. */
import { defineAsyncComponent, markRaw, ref, type Component } from 'vue'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()

const lazy = (loader: () => Promise<unknown>): Component =>
  markRaw(defineAsyncComponent(loader as () => Promise<Component>))

interface Tab { key: string; label: string; comp: Component }
const tabs: Tab[] = [
  { key: 'users', label: 'Users', comp: lazy(() => import('./AdminUsersView.vue')) },
  { key: 'teams', label: 'Teams', comp: lazy(() => import('./AdminTeamsView.vue')) },
  { key: 'tokens', label: 'MCP tokens', comp: lazy(() => import('./AdminTokensView.vue')) },
  { key: 'spa-tokens', label: 'SPA tokens', comp: lazy(() => import('./AdminSpaTokensView.vue')) },
  { key: 'invites', label: 'Invites', comp: lazy(() => import('./AdminInvitesView.vue')) },
  { key: 'audit', label: 'Audit', comp: lazy(() => import('./AdminAuditView.vue')) },
]

const props = defineProps<{ section?: string }>()
const active = ref<string>(tabs.some((t) => t.key === props.section) ? props.section! : 'users')

const current = () => tabs.find((t) => t.key === active.value)?.comp
</script>

<template>
  <div class="p-6 max-w-6xl mx-auto">
    <h1 class="text-2xl font-semibold mb-1">Admin</h1>
    <p v-if="!auth.isOwner" class="text-danger text-sm mb-6">
      You need owner role to access these pages.
    </p>

    <template v-else>
      <nav class="flex gap-1 mb-6 border-b border-border text-sm">
        <button
          v-for="t in tabs"
          :key="t.key"
          type="button"
          class="px-3 py-2 -mb-px border-b-2"
          :class="active === t.key ? 'border-accent text-accent' : 'border-transparent hover:text-text'"
          @click="active = t.key"
        >{{ t.label }}</button>
      </nav>

      <component :is="current()" />
    </template>
  </div>
</template>
