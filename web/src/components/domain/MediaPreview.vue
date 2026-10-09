<script setup lang="ts">
/**
 * MediaPreview — the read view of an image media note (ADR-013): the image
 * itself, rendered prominently from /vault-files/, followed by the note's
 * caption (its markdown body, already rendered to HTML upstream) and a small
 * metadata line. A broken `media:` pointer degrades to a placeholder rather
 * than a missing-image glyph, so the note still reads.
 *
 * The image is served same-origin from /vault-files/, which the app CSP's
 * img-src already allows (markdown-embedded attachments use the same path).
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MediaRef } from '@/api/notes'
import MarkdownPreview from './MarkdownPreview.vue'

const { t } = useI18n()

const props = defineProps<{
  media: MediaRef
  /** Rendered HTML of the note body (the caption), or '' when empty. */
  captionHtml: string
  notePath: string
}>()

const sizeLabel = computed(() => {
  const n = props.media.size
  if (!n) return ''
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
})

// Metadata line under the image: path · mime · size, omitting empty parts.
const metaLabel = computed(() => {
  const parts = [props.media.path]
  if (props.media.mime) parts.push(props.media.mime)
  if (sizeLabel.value) parts.push(sizeLabel.value)
  return parts.join(' · ')
})
</script>

<template>
  <div class="p-6 max-w-3xl mx-auto">
    <figure class="m-0">
      <img
        v-if="!media.broken"
        :src="media.url"
        :alt="notePath"
        class="max-w-full h-auto rounded border border-border bg-bg-elevated"
      >
      <div
        v-else
        class="flex items-center justify-center rounded border border-dashed border-warning/50 bg-warning/5 px-4 py-10 text-sm text-warning"
      >
        {{ t('media.not_found') }}: <span class="ml-1 font-mono">{{ media.path }}</span>
      </div>
      <figcaption class="mt-2 text-xs text-text-muted font-mono">
        {{ metaLabel }}
      </figcaption>
    </figure>

    <div
      v-if="captionHtml"
      class="mt-6"
    >
      <MarkdownPreview :html="captionHtml" :note-path="notePath" />
    </div>
  </div>
</template>
