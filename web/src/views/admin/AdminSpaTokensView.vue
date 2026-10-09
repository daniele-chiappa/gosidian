<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { onMounted, ref } from 'vue'
import { listSpaTokens, revokeSpaToken, type SpaToken } from '@/api/admin'
import { errorText } from '@/api/errors'
import DateTime from '@/components/primitives/DateTime.vue'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'

const { t } = useI18n()

const tokens = ref<SpaToken[]>([])
const loading = ref(false)
const error = ref<string | null>(null)

async function load() {
  loading.value = true
  error.value = null
  try {
    tokens.value = await listSpaTokens()
  } catch (e) {
    error.value = errorText(e, t, t('admin.sessions.load_failed'))
  } finally {
    loading.value = false
  }
}

async function revoke(tok: SpaToken) {
  if (!confirm(t('admin.sessions.confirm_revoke', { id: tok.id.slice(0, 8) }))) return
  try {
    await revokeSpaToken(tok.id)
    await load()
  } catch (e) {
    error.value = errorText(e, t, t('my_tokens.revoke_failed'))
  }
}

onMounted(load)
</script>

<template>
  <p v-if="loading" class="text-text-muted">{{ t('common.loading') }}</p>
  <ErrorMessage v-else-if="error" :text="error" />

  <p v-else-if="!tokens.length" class="text-text-muted text-sm">{{ t('admin.sessions.empty') }}</p>

  <div v-else class="overflow-x-auto">
    <!-- Scrolls inside its box in a narrow window (IMP-156, M7). -->

    <table class="w-full text-sm">
      <thead class="text-text-muted text-xs uppercase tracking-wide">
        <tr>
          <th class="text-left py-2 px-3">{{ t('admin.col.user') }}</th>
          <th class="text-left py-2 px-3">{{ t('admin.col.user_agent') }}</th>
          <th class="text-left py-2 px-3">{{ t('admin.col.issued') }}</th>
          <th class="text-left py-2 px-3">{{ t('admin.col.last_seen') }}</th>
          <th class="text-left py-2 px-3">{{ t('admin.col.expires') }}</th>
          <th class="text-right py-2 px-3">{{ t('admin.col.actions') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="tok in tokens"
          :key="tok.id"
          class="border-t border-border"
        >
          <td class="py-2 px-3 font-mono text-xs">{{ tok.user_id }}</td>
          <td class="py-2 px-3 text-xs truncate max-w-[16rem]" :title="tok.user_agent">
            {{ tok.user_agent || '—' }}
          </td>
          <td class="py-2 px-3 text-xs whitespace-nowrap"><DateTime :value="tok.issued_at" /></td>
          <td class="py-2 px-3 text-xs whitespace-nowrap"><DateTime :value="tok.last_seen_at" /></td>
          <td class="py-2 px-3 text-xs whitespace-nowrap"><DateTime :value="tok.expires_at" /></td>
          <td class="py-2 px-3 text-right">
            <button
              type="button"
              class="text-xs px-2 py-1 rounded text-danger hover:bg-surface-hover"
              @click="revoke(tok)"
            >{{ t('common.revoke') }}</button>
          </td>
        </tr>
      </tbody>
    </table>

  </div>
</template>
