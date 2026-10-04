<script setup lang="ts">
/**
 * ViewCount — a ```view with `as: count` (IMP-127 iteration 2): the number
 * of notes its filters select, large, and with `group_by` one chip per
 * value, in the order of the select's options (the notes without a value
 * last), as the server computed them.
 */
import { useI18n } from 'vue-i18n'
import type { ViewData } from '@/api/preview'

defineProps<{ view: ViewData }>()
const { t } = useI18n()
</script>

<template>
  <div class="not-prose flex flex-wrap items-baseline gap-x-3 gap-y-2 my-4">
    <span class="text-3xl font-semibold tabular-nums text-text">{{ view.total }}</span>
    <span class="text-text-muted">{{ t('views.count_notes', view.total) }}</span>
    <span
      v-for="c in view.counts ?? []"
      :key="c.value"
      class="text-sm px-2 py-0.5 rounded border border-border bg-bg-elevated text-text-muted"
    >
      {{ c.value || t('views.no_value') }}
      <span class="font-semibold tabular-nums text-text">{{ c.count }}</span>
    </span>
  </div>
</template>
