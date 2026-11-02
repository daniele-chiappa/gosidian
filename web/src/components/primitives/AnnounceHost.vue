<script setup lang="ts">
/** The shell's polite region, where announce() speaks (composables/useAnnounce). */
import { announcement } from '@/composables/useAnnounce'
</script>

<template>
  <div
    role="status"
    aria-live="polite"
    class="sr-only"
    data-announce
  >
    {{ announcement }}
  </div>
</template>
