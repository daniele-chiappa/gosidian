<script setup lang="ts">
/**
 * NoteLabel — a note's name in a row of the sidebar (the tree, Recent):
 * its title where it has one, else its file name, cut in the middle where
 * it does not fit, so that its end (the words of a date-named note) stays
 * in sight. The file name is in the tooltip either way (IMP-219, F-19).
 *
 * The cut is CSS only: the head truncates with an ellipsis, the last TAIL
 * letters never shrink, and a name that fits reads whole. The two halves
 * are hidden from a screen reader, which reads the whole name once.
 */
import { computed } from 'vue'

const props = defineProps<{ path: string; title?: string }>()

/** Letters of the file name always in sight, past the cut. */
const TAIL = 10

// A letter is a grapheme: an accent written as its own code point stays
// with its letter.
const segmenter = new Intl.Segmenter(undefined, { granularity: 'grapheme' })

const fileName = computed(() => props.path.split('/').pop() || props.path)
const stem = computed(() => fileName.value.replace(/\.md$/, ''))
// A title that is the file name, or the path (a note read without one), is none.
const shownTitle = computed(() =>
  props.title && props.title !== stem.value && props.title !== props.path ? props.title : '',
)
const letters = computed(() => Array.from(segmenter.segment(stem.value), (s) => s.segment))
const head = computed(() => letters.value.slice(0, -TAIL).join(''))
const tail = computed(() => letters.value.slice(-TAIL).join(''))
</script>

<template>
  <span
    class="flex min-w-0"
    :title="fileName"
  >
    <span
      v-if="shownTitle"
      class="truncate"
    >{{ shownTitle }}</span>
    <template v-else-if="head">
      <span class="sr-only">{{ stem }}</span>
      <span
        class="min-w-0 overflow-hidden text-ellipsis whitespace-pre"
        aria-hidden="true"
        data-cut-head
      >{{ head }}</span>
      <span
        class="shrink-0 whitespace-pre"
        aria-hidden="true"
        data-cut-tail
      >{{ tail }}</span>
    </template>
    <span
      v-else
      class="truncate"
    >{{ stem }}</span>
  </span>
</template>
