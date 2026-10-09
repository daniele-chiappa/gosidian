<script setup lang="ts">
/**
 * ErrorMessage — an error as errorText writes it (IMP-155, M3): the
 * sentence, and the server's detail on a smaller line under it. Announced
 * to screen readers as an alert.
 */
import { computed } from 'vue'

const props = defineProps<{ text: string }>()
const lines = computed(() => {
  const i = props.text.indexOf('\n')
  return i < 0 ? { head: props.text, detail: '' } : { head: props.text.slice(0, i), detail: props.text.slice(i + 1) }
})
</script>

<template>
  <p class="text-danger" role="alert">
    {{ lines.head }}
    <span v-if="lines.detail" class="mt-0.5 block text-xs opacity-80">{{ lines.detail }}</span>
  </p>
</template>
