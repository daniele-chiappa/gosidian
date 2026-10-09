<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { onMounted, ref } from 'vue'
import { listTrash, restoreTrash, purgeTrash, type TrashItem } from '@/api/trash'
import { useTreeStore } from '@/stores/tree'
import { Folder, FileText, RotateCcw, Trash2 } from 'lucide-vue-next'
import { errorText } from '@/api/errors'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'
import DateTime from '@/components/primitives/DateTime.vue'
import { confirmAction } from '@/composables/useConfirm'

const { t } = useI18n()

const items = ref<TrashItem[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const message = ref<string | null>(null)
const treeStore = useTreeStore()

async function load() {
  loading.value = true
  error.value = null
  try {
    items.value = await listTrash()
  } catch (e) {
    error.value = errorText(e, t, t('trash.load_failed'))
  } finally {
    loading.value = false
  }
}

async function restore(item: TrashItem) {
  try {
    const res = await restoreTrash(item.id)
    message.value = t('trash.restored', { path: res.restored })
    treeStore.refresh()
    await load()
  } catch (e) {
    // The server says why: a folder or a note recreated meanwhile, a
    // project to restore first.
    error.value = errorText(e, t, t('trash.restore_failed'))
  }
}

async function purge(item: TrashItem) {
  if (!(await confirmAction(t('trash.confirm_purge', { path: item.origin_path }), { confirmLabel: t('common.delete') }))) return
  try {
    await purgeTrash(item.id)
    message.value = t('trash.purged')
    await load()
  } catch (e) {
    error.value = errorText(e, t, t('trash.purge_failed'))
  }
}

onMounted(load)
</script>

<template>
  <div class="p-8 max-w-4xl mx-auto">
    <!-- The window's title bar shows the name: the heading is for screen readers. -->
    <h1 class="sr-only">{{ t('trash.title') }}</h1>
    <p class="text-sm text-text-muted mb-6">
      {{ t('trash.intro') }}
    </p>

    <p v-if="loading" class="text-text-muted">{{ t('common.loading') }}</p>
    <ErrorMessage v-else-if="error" :text="error" />
    <p v-else-if="message" class="text-success text-sm mb-3">{{ message }}</p>

    <p v-if="!loading && !items.length" class="text-text-muted text-sm">{{ t('trash.empty') }}</p>

    <ul v-else class="space-y-2">
      <li
        v-for="item in items"
        :key="item.id"
        class="rounded border border-border bg-surface px-4 py-3 flex items-center gap-3"
      >
        <component
          :is="item.is_dir ? Folder : FileText"
          class="w-4 h-4 text-text-muted shrink-0"
        />
        <span class="flex-1 font-mono text-sm truncate">{{ item.origin_path }}</span>
        <DateTime class="text-xs text-text-muted whitespace-nowrap" :value="item.discarded_at" />
        <button
          type="button"
          class="h-control-sm text-xs px-2 rounded border border-border hover:bg-surface-hover inline-flex items-center gap-1"
          @click="restore(item)"
        ><RotateCcw class="w-3 h-3" /> {{ t('trash.restore') }}</button>
        <button
          type="button"
          class="h-control-sm text-xs px-2 rounded text-danger hover:bg-surface-hover inline-flex items-center gap-1"
          @click="purge(item)"
        ><Trash2 class="w-3 h-3" /> {{ t('trash.purge') }}</button>
      </li>
    </ul>
  </div>
</template>
