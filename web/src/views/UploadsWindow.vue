<script setup lang="ts">
/**
 * UploadsWindow — the uploads into IN of this tab (IMP-172 phase 5), on
 * their way and done, whatever window started them. An upload a reload
 * cut short is resumed from its project's IN window, by choosing the same
 * files again.
 */
import { inject, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { planciaKey } from '@/composables/planciaKey'
import { useUploadsStore } from '@/stores/uploads'
import UploadJobCard from '@/components/domain/UploadJobCard.vue'

const emit = defineEmits<{ title: [string] }>()
const { t } = useI18n()
const uploads = useUploadsStore()
const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))

function openIn(project: string) {
  openWindow({ type: 'in', key: planciaKey('in', project), title: t('in.window_title', { name: project }), props: { project } })
}

onMounted(() => emit('title', t('uploads.window_title')))
</script>

<template>
  <div class="p-6 max-w-4xl mx-auto text-sm space-y-3" data-uploads-window>
    <div class="flex items-center gap-3">
      <h1 class="text-base font-semibold">{{ t('uploads.window_title') }}</h1>
      <span class="flex-1" />
      <button
        v-if="uploads.jobs.some((j) => j.state === 'done')"
        type="button"
        class="h-control-sm text-xs px-2 rounded border border-border hover:bg-surface-hover"
        data-uploads-clear
        @click="uploads.clearDone()"
      >{{ t('uploads.clear_done') }}</button>
    </div>
    <p v-if="!uploads.jobs.length" class="text-text-muted" data-uploads-empty>{{ t('uploads.empty') }}</p>
    <UploadJobCard v-for="job in uploads.jobs" :key="job.key" :job="job" @open-in="openIn" />
  </div>
</template>
