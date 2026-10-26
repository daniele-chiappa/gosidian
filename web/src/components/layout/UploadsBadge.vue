<script setup lang="ts">
/**
 * The uploads into IN in the top bar (IMP-172 phase 5): while one is on its
 * way, how far they all are; once done, that they are. A click opens the
 * Uploads window. Closing the tab while one is on its way asks first.
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useEventListener } from '@vueuse/core'
import { CircleAlert, CircleCheck, Upload } from 'lucide-vue-next'
import { useWindowsStore } from 'plancia'
import { planciaKey } from '@/composables/planciaKey'
import { useUploadsStore } from '@/stores/uploads'

const { t } = useI18n()
const uploads = useUploadsStore()
const windows = useWindowsStore()

const percent = computed(() => {
  const { done, total } = uploads.progress
  return total ? Math.floor((done * 100) / total) : 100
})
const attention = computed(() => uploads.jobs.some((j) => j.state === 'asking' || j.state === 'error' || j.state === 'paused'))
const label = computed(() =>
  uploads.active.length
    ? t('uploads.badge_active', { count: uploads.active.length, percent: percent.value }, uploads.active.length)
    : t('uploads.badge_done'),
)

useEventListener(window, 'beforeunload', (e: BeforeUnloadEvent) => {
  if (!uploads.busy) return
  e.preventDefault()
  e.returnValue = ''
})

function open() {
  windows.open({ type: 'uploads', key: planciaKey('uploads'), title: t('uploads.window_title') })
}
</script>

<template>
  <button
    v-if="uploads.jobs.length"
    type="button"
    class="h-control-sm px-2 rounded text-xs border border-border hover:bg-surface-hover inline-flex items-center gap-1"
    :title="label"
    :aria-label="label"
    data-uploads-badge
    @click="open"
  >
    <CircleAlert v-if="attention" class="w-3 h-3 text-warning" aria-hidden="true" />
    <Upload v-else-if="uploads.active.length" class="w-3 h-3 text-accent" aria-hidden="true" />
    <CircleCheck v-else class="w-3 h-3 text-success" aria-hidden="true" />
    <span v-if="uploads.active.length" data-uploads-percent>{{ percent }}%</span>
  </button>
</template>
