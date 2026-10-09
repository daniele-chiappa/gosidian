<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { onMounted, reactive, ref } from 'vue'
import { tailAudit, type AuditEntry } from '@/api/admin'
import { errorText } from '@/api/errors'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'
import DateTime from '@/components/primitives/DateTime.vue'
import { formatSize } from '@/api/format'

const { t } = useI18n()

const entries = ref<AuditEntry[]>([])
const loading = ref(false)
const error = ref<string | null>(null)

const filters = reactive<{ actor: string; action: string; source: string; path_prefix: string; limit: number }>({
  actor: '',
  action: '',
  source: '',
  path_prefix: '',
  limit: 100,
})

async function load() {
  loading.value = true
  error.value = null
  try {
    entries.value = await tailAudit({
      actor: filters.actor || undefined,
      action: filters.action || undefined,
      source: filters.source || undefined,
      path_prefix: filters.path_prefix || undefined,
      limit: filters.limit,
    })
  } catch (e) {
    error.value = errorText(e, t, t('admin.audit.load_failed'))
  } finally {
    loading.value = false
  }
}

function reset() {
  filters.actor = ''
  filters.action = ''
  filters.source = ''
  filters.path_prefix = ''
  filters.limit = 100
  void load()
}

onMounted(load)
</script>

<template>
  <form
    class="grid grid-cols-[repeat(auto-fill,minmax(10rem,1fr))] gap-2 mb-4 text-sm"
    @submit.prevent="load"
  >
    <input
      v-model.trim="filters.actor"
      type="text"
      :placeholder="t('admin.audit.actor')"
      class="rounded bg-bg-elevated border border-border px-2 py-1.5"
    />
    <input
      v-model.trim="filters.action"
      type="text"
      :placeholder="t('admin.audit.action_placeholder')"
      class="rounded bg-bg-elevated border border-border px-2 py-1.5"
    />
    <input
      v-model.trim="filters.source"
      type="text"
      :placeholder="t('admin.audit.source_placeholder')"
      class="rounded bg-bg-elevated border border-border px-2 py-1.5"
    />
    <input
      v-model.trim="filters.path_prefix"
      type="text"
      :placeholder="t('admin.audit.path_placeholder')"
      class="rounded bg-bg-elevated border border-border px-2 py-1.5"
    />
    <div class="flex flex-wrap gap-1">
      <input
        v-model.number="filters.limit"
        type="number"
        min="10"
        max="500"
        class="w-20 shrink-0 rounded bg-bg-elevated border border-border px-2 py-1.5"
        :aria-label="t('admin.audit.limit')"
      />
      <button
        type="submit"
        class="px-3 py-1.5 rounded bg-accent text-accent-fg hover:bg-accent-hover text-xs"
      >{{ t('admin.audit.apply') }}</button>
      <button
        type="button"
        class="px-3 py-1.5 rounded border border-border hover:bg-surface-hover text-xs"
        @click="reset"
      >{{ t('graph.reset') }}</button>
    </div>
  </form>

  <p v-if="loading" class="text-text-muted">{{ t('common.loading') }}</p>
  <ErrorMessage v-else-if="error" :text="error" />

  <p v-else-if="!entries.length" class="text-text-muted text-sm">{{ t('admin.audit.empty') }}</p>

  <div v-else class="rounded border border-border overflow-x-auto">
    <table class="w-full text-xs font-mono">
      <thead class="text-text-muted uppercase tracking-wide bg-bg-elevated">
        <tr>
          <th class="text-left py-2 px-2">{{ t('admin.audit.when') }}</th>
          <th class="text-left py-2 px-2">{{ t('admin.audit.source') }}</th>
          <th class="text-left py-2 px-2">{{ t('admin.audit.actor') }}</th>
          <th class="text-left py-2 px-2">{{ t('admin.audit.action') }}</th>
          <th class="text-left py-2 px-2">{{ t('admin.audit.path') }}</th>
          <th class="text-right py-2 px-2">{{ t('admin.audit.size') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="(e, i) in entries"
          :key="i"
          class="border-t border-border"
        >
          <td class="py-1 px-2 whitespace-nowrap"><DateTime :value="e.ts" /></td>
          <td class="py-1 px-2">{{ e.source }}</td>
          <td class="py-1 px-2">{{ e.actor || e.token || '—' }}</td>
          <td class="py-1 px-2">{{ e.action }}</td>
          <td class="py-1 px-2 truncate max-w-[20rem]" :title="e.path">{{ e.path || '—' }}</td>
          <td class="py-1 px-2 text-right whitespace-nowrap">{{ e.size ? formatSize(e.size) : '' }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
