<script setup lang="ts">
/**
 * DateTime — a date in the user's language and time zone, the ISO form in
 * the tooltip (IMP-155, m1).
 */
import { computed } from 'vue'
import { formatDateTime } from '@/api/format'

const props = defineProps<{ value?: string | null }>()
const shown = computed(() => formatDateTime(props.value))
</script>

<template>
  <time v-if="value" :datetime="value" :title="value">{{ shown }}</time>
</template>
