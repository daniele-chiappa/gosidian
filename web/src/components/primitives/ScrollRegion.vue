<script setup lang="ts">
/**
 * ScrollRegion — a box that scrolls sideways in a narrow window, as a
 * table wider than it (IMP-156, M7): a region with a name, that the Tab
 * reaches, so that the arrows scroll it even when nothing in it takes the
 * focus, as a table of users with the owner alone (IMP-224, F-34; WCAG
 * 2.1.1).
 */
defineProps<{ label: string }>()
</script>

<template>
  <div
    class="overflow-x-auto"
    tabindex="0"
    role="region"
    :aria-label="label"
  >
    <slot />
  </div>
</template>
