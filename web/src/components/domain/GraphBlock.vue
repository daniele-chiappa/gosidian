<script setup lang="ts">
/**
 * GraphBlock — a ```graph block of a note (IMP-183), drawn: the vault's
 * links with the block's filters, or a graph of a plugin exposed in the
 * note's project (the chunks of an archive of vectors and their
 * neighbours). «Open in the Graph» opens the Graph window on the same
 * view, «Code» shows the YAML. The canvas loads on first use; a block that
 * does not read shows its code and why.
 */
import { computed, defineAsyncComponent, inject, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchGraph, type GraphResponse } from '@/api/graph'
import { errorText } from '@/api/errors'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { planciaKey, base } from '@/composables/planciaKey'
import { graphQuery, isPluginSource, parseGraphBlock } from '@/components/graph/graphSpec'

const props = defineProps<{ source: string; notePath?: string }>()
const { t } = useI18n()

const GraphCanvas = defineAsyncComponent(() => import('@/components/graph/GraphCanvas.vue'))
const Graph3DCanvas = defineAsyncComponent(() => import('@/components/graph/Graph3DCanvas.vue'))

const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))

const parsed = computed(() => parseGraphBlock(props.source))
const noteProject = computed(() => props.notePath?.split('/')[0] ?? '')
const data = ref<GraphResponse | null>(null)
const error = ref<string | null>(null)
const loading = ref(false)
const showCode = ref(false)
const selected = ref<{ label: string; text?: string; link?: string } | null>(null)

async function load() {
  const spec = parsed.value.spec
  if (!spec) return
  loading.value = true
  error.value = null
  try {
    data.value = await fetchGraph(graphQuery(spec, noteProject.value))
  } catch (e) {
    error.value = errorText(e, t, t('graph.load_failed'))
  } finally {
    loading.value = false
  }
}
onMounted(() => void load())

function openNote(path: string) {
  openWindow({ type: 'note', key: planciaKey('note', path), title: base(path), props: { path } })
}

function onNode(id: string) {
  const spec = parsed.value.spec
  if (!spec || !isPluginSource(spec.source)) {
    openNote(id)
    return
  }
  const n = data.value?.nodes.find((x) => x.id === id)
  selected.value = n ? { label: n.label, text: n.text, link: n.link } : null
  if (n?.link) openNote(n.link)
}

function openInGraph() {
  const spec = parsed.value.spec
  if (!spec) return
  const q = graphQuery(spec, noteProject.value)
  const props = isPluginSource(spec.source)
    ? { source: spec.source, with: q.with, project: q.project, global: true }
    : { project: q.project, tag: q.tag, focus: q.focus, depth: q.depth, minDegree: q.min_degree, limit: q.limit, global: true }
  openWindow({
    type: 'graph',
    key: planciaKey('graph', `${spec.source}:${q.project ?? ''}:${JSON.stringify(q.with ?? {})}`),
    title: `${t('nav.graph')} · ${spec.source === 'links' ? q.project || '' : spec.source}`,
    props,
  })
}
</script>

<template>
  <div
    class="gosidian-diagram not-prose"
    data-graph-block
  >
    <div class="gosidian-diagram__tools">
      <button
        v-if="parsed.spec"
        type="button"
        class="gosidian-diagram__button"
        data-graph-open
        @click="openInGraph"
      >
        {{ t('graph.open_window') }}
      </button>
      <button
        v-if="parsed.spec"
        type="button"
        class="gosidian-diagram__button"
        :aria-pressed="showCode"
        data-graph-code
        @click="showCode = !showCode"
      >
        {{ showCode ? t('graph.show_drawing') : t('graph.code') }}
      </button>
      <span
        v-if="data"
        class="gosidian-diagram__status"
      >{{
        data.stats.truncated
          ? t('graph.truncated_short', { shown: data.stats.node_count, total: data.stats.total ?? data.stats.node_count })
          : data.stats.node_count
      }} · {{ data.stats.edge_count }}</span>
    </div>
    <p
      v-if="parsed.problem"
      class="gosidian-diagram__status"
      role="alert"
    >
      {{ t('graph.block_problem', { problem: parsed.problem }) }}
    </p>
    <pre
      v-if="showCode || parsed.problem"
      class="gosidian-diagram__code"
    ><code>{{ source }}</code></pre>
    <template v-else>
      <p
        v-if="loading"
        class="gosidian-diagram__status"
      >
        {{ t('common.loading') }}
      </p>
      <p
        v-else-if="error"
        class="gosidian-diagram__status"
        role="alert"
      >
        {{ error }}
      </p>
      <div
        v-else-if="data"
        class="relative h-[420px] rounded border border-border overflow-hidden"
      >
        <Graph3DCanvas
          v-if="parsed.spec?.mode === '3d'"
          :nodes="data.nodes"
          :edges="data.edges"
          z-mode="free"
          @select="onNode"
        />
        <GraphCanvas
          v-else
          :nodes="data.nodes"
          :edges="data.edges"
          @select="onNode"
        />
      </div>
      <div
        v-if="selected"
        class="mt-2 text-xs"
        data-graph-selected
      >
        <p class="font-semibold">
          {{ selected.label }}
        </p>
        <p
          v-if="selected.text"
          class="whitespace-pre-wrap max-h-32 overflow-auto"
        >
          {{ selected.text }}
        </p>
      </div>
    </template>
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
  align-items: center;
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
</style>
