<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { onMounted, ref } from 'vue'
import { listInvites, createInvite, deleteInvite, type Invite } from '@/api/admin'
import { errorText } from '@/api/errors'
import DateTime from '@/components/primitives/DateTime.vue'
import { formatDateTime } from '@/api/format'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'

const { t } = useI18n()

const invites = ref<Invite[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const fresh = ref<Invite | null>(null)
const ttlHours = ref(24)

async function load() {
  loading.value = true
  error.value = null
  try {
    invites.value = await listInvites()
  } catch (e) {
    error.value = errorText(e, t, t('admin.invites.load_failed'))
  } finally {
    loading.value = false
  }
}

async function create() {
  try {
    fresh.value = await createInvite(ttlHours.value > 0 ? ttlHours.value * 3600 * 1000 : undefined)
    await load()
  } catch (e) {
    error.value = errorText(e, t, t('my_tokens.create_failed'))
  }
}

async function destroy(iv: Invite) {
  if (!confirm(t('admin.invites.confirm_revoke', { id: iv.token.slice(0, 8) }))) return
  try {
    await deleteInvite(iv.token)
    await load()
  } catch (e) {
    error.value = errorText(e, t, t('my_tokens.revoke_failed'))
  }
}

function dismissFresh() {
  fresh.value = null
}

const signupURL = (token: string) => `${window.location.origin}/login?invite=${encodeURIComponent(token)}`

onMounted(load)
</script>

<template>
  <div v-if="fresh" class="rounded border border-success bg-success/10 p-4 mb-6 space-y-2">
    <p class="text-sm font-semibold text-success">{{ t('admin.invites.created') }}</p>
    <code class="block bg-bg-elevated rounded px-3 py-2 font-mono text-sm break-all select-all">{{ fresh.token }}</code>
    <p class="text-xs text-text-muted">{{ t('admin.invites.share') }}</p>
    <code class="block bg-bg-elevated rounded px-3 py-2 font-mono text-xs break-all select-all">{{ signupURL(fresh.token) }}</code>
    <p class="text-xs text-text-muted">{{ t('admin.invites.expires', { when: formatDateTime(fresh.expires_at) }) }}</p>
    <button
      type="button"
      class="text-xs px-2 py-1 rounded border border-border hover:bg-surface-hover"
      @click="dismissFresh"
    >{{ t('common.dismiss') }}</button>
  </div>

  <form
    class="flex flex-wrap items-end gap-3 mb-6"
    @submit.prevent="create"
  >
    <label class="text-sm">
      <span class="block text-text-muted text-xs mb-1">{{ t('admin.invites.ttl') }}</span>
      <input
        v-model.number="ttlHours"
        type="number"
        min="1"
        class="rounded bg-bg-elevated border border-border px-3 py-2 w-32"
      />
    </label>
    <button
      type="submit"
      class="px-3 py-2 rounded bg-accent text-accent-fg hover:bg-accent-hover"
    >{{ t('admin.invites.create') }}</button>
  </form>

  <p v-if="loading" class="text-text-muted">{{ t('common.loading') }}</p>
  <ErrorMessage v-else-if="error" :text="error" />

  <p v-else-if="!invites.length" class="text-text-muted text-sm">{{ t('admin.invites.empty') }}</p>

  <div v-else class="overflow-x-auto">
    <!-- Scrolls inside its box in a narrow window (IMP-156, M7). -->

    <table class="w-full text-sm">
      <thead class="text-text-muted text-xs uppercase tracking-wide">
        <tr>
          <th class="text-left py-2 px-3">{{ t('admin.col.token') }}</th>
          <th class="text-left py-2 px-3">{{ t('admin.col.created_by') }}</th>
          <th class="text-left py-2 px-3">{{ t('admin.col.created') }}</th>
          <th class="text-left py-2 px-3">{{ t('admin.col.expires') }}</th>
          <th class="text-left py-2 px-3">{{ t('admin.col.status') }}</th>
          <th class="text-right py-2 px-3">{{ t('admin.col.actions') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="iv in invites"
          :key="iv.token"
          class="border-t border-border"
        >
          <td class="py-2 px-3 font-mono text-xs">{{ iv.token.slice(0, 16) }}…</td>
          <td class="py-2 px-3 font-mono text-xs">{{ iv.created_by }}</td>
          <td class="py-2 px-3 text-xs whitespace-nowrap"><DateTime :value="iv.created_at" /></td>
          <td class="py-2 px-3 text-xs whitespace-nowrap"><DateTime :value="iv.expires_at" /></td>
          <td class="py-2 px-3">
            <span
              v-if="iv.consumed_at"
              class="text-xs text-text-muted"
            >{{ t('admin.invites.consumed_by', { id: iv.consumed_by }) }}</span>
            <span
              v-else-if="iv.pending"
              class="text-xs text-success"
            >{{ t('admin.invites.pending') }}</span>
            <span
              v-else
              class="text-xs text-warning"
            >{{ t('admin.invites.expired') }}</span>
          </td>
          <td class="py-2 px-3 text-right">
            <button
              v-if="iv.pending"
              type="button"
              class="text-xs px-2 py-1 rounded text-danger hover:bg-surface-hover"
              @click="destroy(iv)"
            >{{ t('common.revoke') }}</button>
          </td>
        </tr>
      </tbody>
    </table>

  </div>
</template>
