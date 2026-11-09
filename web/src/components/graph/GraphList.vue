<script setup lang="ts">
/**
 * GraphList — the graph said as text (IMP-224, F-32; WCAG 1.1.1): a table
 * of its nodes, the most linked first, each with the nodes it links to and
 * the ones it is linked from. A node's name opens it, as a click on the
 * drawing does.
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { GraphEdge, GraphNode } from '@/api/graph'
import ScrollRegion from '@/components/primitives/ScrollRegion.vue'

const props = defineProps<{ nodes: GraphNode[]; edges: GraphEdge[] }>()
const emit = defineEmits<{ (e: 'select', id: string): void }>()
const { t } = useI18n()

const rows = computed(() => {
  const label = new Map(props.nodes.map((n) => [n.id, n.label || n.id]))
  const to = new Map<string, string[]>()
  const from = new Map<string, string[]>()
  for (const e of props.edges) {
    to.set(e.source, [...(to.get(e.source) ?? []), label.get(e.target) ?? e.target])
    from.set(e.target, [...(from.get(e.target) ?? []), label.get(e.source) ?? e.source])
  }
  return [...props.nodes]
    .sort((a, b) => b.degree - a.degree || (a.label || a.id).localeCompare(b.label || b.id))
    .map((n) => ({
      id: n.id,
      name: n.label || n.id,
      group: n.project ?? n.group ?? '',
      degree: n.degree,
      to: (to.get(n.id) ?? []).join(', '),
      from: (from.get(n.id) ?? []).join(', '),
    }))
})
</script>

<template>
  <ScrollRegion
    :label="t('graph.view_list')"
    class="h-full overflow-y-auto"
    data-graph-list
  >
    <table class="w-full text-sm">
      <caption class="text-left text-xs text-text-muted px-3 py-2">
        {{ t('graph.list_caption', { nodes: nodes.length, edges: edges.length }) }}
      </caption>
      <thead class="text-text-muted text-xs [&_th]:font-medium">
        <tr>
          <th class="text-left py-2 px-3">
            {{ t('graph.list_name') }}
          </th>
          <th class="text-left py-2 px-3">
            {{ t('graph.list_group') }}
          </th>
          <th class="text-right py-2 px-3">
            {{ t('graph.list_degree') }}
          </th>
          <th class="text-left py-2 px-3">
            {{ t('graph.list_to') }}
          </th>
          <th class="text-left py-2 px-3">
            {{ t('graph.list_from') }}
          </th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="r in rows"
          :key="r.id"
          class="border-t border-border align-top"
        >
          <th
            scope="row"
            class="text-left py-1.5 px-3 font-normal"
          >
            <button
              type="button"
              class="text-left text-accent hover:underline"
              :title="r.id"
              @click="emit('select', r.id)"
            >
              {{ r.name }}
            </button>
          </th>
          <td class="py-1.5 px-3 text-text-muted whitespace-nowrap">
            {{ r.group }}
          </td>
          <td class="py-1.5 px-3 text-right tabular-nums">
            {{ r.degree }}
          </td>
          <td class="py-1.5 px-3">
            {{ r.to }}
          </td>
          <td class="py-1.5 px-3">
            {{ r.from }}
          </td>
        </tr>
      </tbody>
    </table>
  </ScrollRegion>
</template>
