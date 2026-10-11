<script setup lang="ts">
/**
 * DiagramBlock — a ```diagram block of a note, drawn (IMP-169, ADR-036).
 * plancia-diagrams reads its YAML, lists what is wrong in it, and draws it;
 * a link of the drawing comes back as `link`, which MarkdownPreview opens
 * as a note window or a tab. «Edit» (for who may change the note) opens
 * the diagram window, «Code» shows the YAML as written, and «Export SVG»
 * saves the drawing with the page's colours: they sit in the head of the
 * diagram, beside its zoom buttons. The drawing loads on first use;
 * when it cannot load, the block shows its code.
 */
import { computed, defineAsyncComponent, h, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { saveBlob } from '@/api/download'
import { useDiagramLabels } from './diagramLabels'

// editable: the reader may change the note the block is written in.
const props = defineProps<{ source: string; editable?: boolean }>()
const emit = defineEmits<{
  link: [link: { target: string; kind: 'note' | 'url'; label: string }]
  edit: []
}>()
const { t } = useI18n()

const showCode = ref(false)
const failed = ref(false)
const view = ref<{ exportSVG: () => string | null } | null>(null)

const DiagramView = defineAsyncComponent({
  loader: () => import('./diagramView').then((m) => m.DiagramView),
  loadingComponent: {
    render: () => h('p', { class: 'gosidian-diagram__status' }, t('diagram.loading')),
  },
  delay: 150,
  onError(_err, _retry, fail) {
    failed.value = true
    fail()
  },
})

const labels = useDiagramLabels().view

/** The file name of the export: the diagram's title, else «diagram». */
const fileName = computed(() => {
  const title = /^title:\s*["']?(.+?)["']?\s*$/m.exec(props.source)?.[1] ?? ''
  const base = title
    .normalize('NFKD')
    .replace(/[^\w\s-]/g, '')
    .trim()
    .replace(/\s+/g, '-')
    .toLowerCase()
  return `${base || 'diagram'}.svg`
})

function exportSvg() {
  const svg = view.value?.exportSVG()
  if (svg) saveBlob(new Blob([svg], { type: 'image/svg+xml' }), fileName.value)
}
</script>

<template>
  <div class="gosidian-diagram not-prose" data-diagram>
    <template v-if="showCode || failed">
      <div class="gosidian-diagram__tools">
        <button
          v-if="!failed"
          type="button"
          class="gosidian-diagram__button"
          aria-pressed="true"
          data-diagram-code
          @click="showCode = false"
        >
          {{ t('diagram.show_drawing') }}
        </button>
      </div>
      <pre class="gosidian-diagram__code"><code>{{ source }}</code></pre>
      <p v-if="failed" class="gosidian-diagram__status" role="alert">
        {{ t('diagram.load_failed') }}
      </p>
    </template>
    <DiagramView v-else ref="view" :source="source" :labels="labels" @link="(l) => emit('link', l)">
      <template #actions="{ drawable }">
        <button
          v-if="editable"
          type="button"
          class="gosidian-diagram__button"
          data-diagram-edit
          @click="emit('edit')"
        >
          {{ t('diagram.edit') }}
        </button>
        <button
          type="button"
          class="gosidian-diagram__button"
          aria-pressed="false"
          data-diagram-code
          @click="showCode = true"
        >
          {{ t('diagram.show_code') }}
        </button>
        <button
          v-if="drawable"
          type="button"
          class="gosidian-diagram__button"
          data-diagram-export
          @click="exportSvg"
        >
          {{ t('diagram.export_svg') }}
        </button>
      </template>
    </DiagramView>
  </div>
</template>

<style scoped>
.gosidian-diagram {
  position: relative;
  margin: 1.25em 0;
}
.gosidian-diagram__tools {
  display: flex;
  justify-content: flex-end;
  gap: 0.25rem;
  margin-bottom: 0.25rem;
}
.gosidian-diagram__button {
  height: 26px;
  padding: 0 0.5rem;
  border: 1px solid rgb(var(--color-border));
  border-radius: 0.25rem;
  background: rgb(var(--color-surface));
  color: rgb(var(--color-text-muted));
  font-size: 0.75rem;
  line-height: 1;
}
.gosidian-diagram__button:hover,
.gosidian-diagram__button:focus-visible {
  color: rgb(var(--color-text));
  border-color: rgb(var(--color-accent));
}
.gosidian-diagram__code {
  margin: 0;
  padding: 0.75rem;
  overflow: auto;
  border: 1px solid rgb(var(--color-border));
  border-radius: 0.375rem;
  background: rgb(var(--color-bg-elevated));
  font-family: ui-monospace, monospace;
  font-size: 0.8125rem;
  white-space: pre;
}
.gosidian-diagram__status {
  margin: 0.25rem 0;
  font-size: 0.75rem;
  color: rgb(var(--color-text-muted));
}
.gosidian-diagram :deep(.plancia-diagram) {
  margin: 0;
}
</style>
