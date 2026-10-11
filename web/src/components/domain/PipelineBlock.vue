<script setup lang="ts">
/**
 * PipelineBlock — the ```pipeline block of a pipeline note, drawn
 * (IMP-169, F4): its trigger and nodes, by plancia-diagrams' PipelineView.
 * «Edit» (for who may change the note) opens the pipeline window, «Code»
 * shows the YAML as written. The drawing loads on first use; when it
 * cannot load, the block shows its code.
 */
import { defineAsyncComponent, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useDiagramLabels } from './diagramLabels'
import { usePipelineCatalog } from '@/composables/pipelineCatalog'

defineProps<{ source: string; editable?: boolean }>()
const emit = defineEmits<{ edit: [] }>()
const { t } = useI18n()

const showCode = ref(false)
const failed = ref(false)
const labels = useDiagramLabels().pipeline
const { catalog, load } = usePipelineCatalog()
onMounted(() => void load())

const PipelineView = defineAsyncComponent({
  loader: () => import('./diagramView').then((m) => m.PipelineView),
  loadingComponent: {
    render: () => h('p', { class: 'gosidian-diagram__status' }, t('pipeline_editor.loading')),
  },
  delay: 150,
  onError(_err, _retry, fail) {
    failed.value = true
    fail()
  },
})
</script>

<template>
  <div class="gosidian-diagram not-prose" data-pipeline-block>
    <template v-if="showCode || failed">
      <div class="gosidian-diagram__tools">
        <button
          v-if="editable"
          type="button"
          class="gosidian-diagram__button"
          data-pipeline-edit
          @click="emit('edit')"
        >
          {{ t('pipeline_editor.edit') }}
        </button>
        <button
          v-if="!failed"
          type="button"
          class="gosidian-diagram__button"
          aria-pressed="true"
          data-pipeline-code
          @click="showCode = false"
        >
          {{ t('pipeline_editor.show_drawing') }}
        </button>
      </div>
      <pre class="gosidian-diagram__code"><code>{{ source }}</code></pre>
    </template>
    <PipelineView
      v-else
      :source="source"
      :catalog="catalog?.nodes ?? []"
      :labels="labels"
      :height="320"
    >
      <template #actions>
        <button
          v-if="editable"
          type="button"
          class="gosidian-diagram__button"
          data-pipeline-edit
          @click="emit('edit')"
        >
          {{ t('pipeline_editor.edit') }}
        </button>
        <button
          type="button"
          class="gosidian-diagram__button"
          aria-pressed="false"
          data-pipeline-code
          @click="showCode = true"
        >
          {{ t('pipeline_editor.show_code') }}
        </button>
      </template>
    </PipelineView>
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
