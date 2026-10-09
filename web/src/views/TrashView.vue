<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { listTrash, restoreTrash, purgeTrash, type TrashItem } from '@/api/trash'
import { apiErrorMessage } from '@/api/client'
import { useTreeStore } from '@/stores/tree'
import { Folder, FileText, RotateCcw, Trash2 } from 'lucide-vue-next'

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
    error.value = e instanceof Error ? e.message : 'Failed to load'
  } finally {
    loading.value = false
  }
}

async function restore(item: TrashItem) {
  try {
    const res = await restoreTrash(item.id)
    message.value = `Restored to ${res.restored}`
    treeStore.refresh()
    await load()
  } catch (e) {
    // The server says why: a folder or a note recreated meanwhile, a
    // project to restore first.
    error.value = apiErrorMessage(e, 'Restore failed')
  }
}

async function purge(item: TrashItem) {
  if (!confirm(`Permanently delete "${item.origin_path}"? This cannot be undone.`)) return
  try {
    await purgeTrash(item.id)
    message.value = 'Purged.'
    await load()
  } catch (e) {
    error.value = apiErrorMessage(e, 'Purge failed')
  }
}

onMounted(load)
</script>

<template>
  <div class="p-8 max-w-4xl mx-auto">
    <h1 class="text-2xl font-semibold mb-1">Trash</h1>
    <p class="text-sm text-text-muted mb-6">
      Soft-deleted notes and folders. Restore puts them back at their original path; purge wipes them for good.
    </p>

    <p v-if="loading" class="text-text-muted">Loading…</p>
    <p v-else-if="error" class="text-danger">{{ error }}</p>
    <p v-else-if="message" class="text-success text-sm mb-3">{{ message }}</p>

    <p v-if="!loading && !items.length" class="text-text-muted text-sm">Trash is empty.</p>

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
        <span class="text-xs text-text-muted whitespace-nowrap">{{ item.discarded_at }}</span>
        <button
          type="button"
          class="text-xs px-2 py-1 rounded border border-border hover:bg-surface-hover inline-flex items-center gap-1"
          @click="restore(item)"
        ><RotateCcw class="w-3 h-3" /> Restore</button>
        <button
          type="button"
          class="text-xs px-2 py-1 rounded text-danger hover:bg-surface-hover inline-flex items-center gap-1"
          @click="purge(item)"
        ><Trash2 class="w-3 h-3" /> Purge</button>
      </li>
    </ul>
  </div>
</template>
