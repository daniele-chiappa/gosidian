<script setup lang="ts">
/**
 * LayoutCard — a note with a design (IMP-226): its first page and the way to
 * its editor. The design is a file of its own, out of the note's text. The
 * PDF kept beside the note is the one of what readers see: the pinned
 * version's, or the draft's; who can edit sees the draft's too (IMP-229).
 */
import { computed, inject, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { LayoutTemplate } from 'lucide-vue-next'
import { appForType } from '@/composables/pluginApps'
import { planciaKey, base } from '@/composables/planciaKey'
import { vaultFileHref, type PluginApp } from '@/api/layouts'
import { DRAFT_PDF, type NotePdf } from '@/composables/layoutBridge'

const props = defineProps<{
  path: string
  preview: string
  version?: string
  /** The PDFs the note lists, as it is now. */
  pdfs?: NotePdf[]
  /** The pinned version's label, '' when readers see the latest. */
  pinned?: string
  canEdit?: boolean
}>()
const { t } = useI18n()
const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))

const app = ref<PluginApp | null>(null)

onMounted(async () => {
  app.value = await appForType('layout')
})

const name = (label: string) => (label === DRAFT_PDF ? t('plugin_app.pdf_draft_name') : label)
const links = computed(() => {
  const pdfs = props.pdfs ?? []
  const readers = pdfs.find((p) => p.label === (props.pinned || DRAFT_PDF))
  const draft = props.canEdit && props.pinned ? pdfs.find((p) => p.label === DRAFT_PDF) : undefined
  return [readers, draft]
    .filter((p): p is NotePdf => !!p)
    .map((p) => ({ ...p, text: t('plugin_app.pdf_link', { label: name(p.label) }), file: `${base(props.path)}-${name(p.label)}.pdf` }))
})

function open() {
  openWindow({
    type: 'app',
    key: planciaKey('app', props.version ? `${props.path}@${props.version}` : props.path),
    title: t('plugin_app.window_title', { name: base(props.path) }),
    props: props.version ? { path: props.path, version: props.version } : { path: props.path },
  })
}
</script>

<template>
  <div
    class="my-4 rounded border border-border p-3 text-sm"
    data-layout-card
  >
    <img
      v-if="preview"
      :src="vaultFileHref(preview)"
      :alt="t('plugin_app.block_preview')"
      class="mb-3 max-h-64 rounded border border-border"
      loading="lazy"
      data-layout-preview
    >
    <div class="flex flex-wrap items-center gap-2">
      <LayoutTemplate
        class="h-4 w-4 text-text-muted"
        aria-hidden="true"
      />
      <span class="font-semibold">{{ t('plugin_app.block_title') }}</span>
      <span class="flex-1" />
      <a
        v-for="pdf in links"
        :key="pdf.label"
        :href="vaultFileHref(pdf.path)"
        :download="pdf.file"
        class="rounded border border-border px-2 py-1 hover:bg-surface-hover"
        data-layout-pdf
      >{{ pdf.text }}</a>
      <button
        v-if="app"
        type="button"
        class="rounded border border-border px-2 py-1 hover:bg-surface-hover"
        data-layout-open
        @click="open"
      >
        {{ t('plugin_app.open_in_editor') }}
      </button>
    </div>
    <p
      v-if="!app"
      class="mt-2 text-text-muted"
      data-layout-no-app
    >
      {{ t('plugin_app.block_no_app') }}
    </p>
  </div>
</template>
