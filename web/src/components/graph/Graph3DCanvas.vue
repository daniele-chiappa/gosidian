<script setup lang="ts">
/**
 * Graph3DCanvas — 3d-force-graph (three.js WebGL + d3-force-3d).
 * Optional 3D twin of GraphCanvas: same props/emits contract, same
 * shared adapter/theme modules, orbit camera instead of pan/zoom.
 * The third dimension is free (it emerges from the simulation), not
 * semantic. three.js is heavy, so the whole runtime loads as a lazy
 * chunk only when the user switches a graph window to 3D.
 *
 * Labels: no persistent 3D text sprites (they'd need an extra
 * dependency and clutter fast) — the node tooltip carries the title
 * and hover lights up the 1-hop neighbourhood like the 2D canvas.
 */
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { ForceGraph3DInstance } from '3d-force-graph'
import type { GraphEdge, GraphNode } from '@/api/graph'
import {
  buildAdjacency,
  endpointId,
  NODE_REL_SIZE,
  nodeRadius,
  toForceGraph,
  nodeVal,
  type FGLink,
  type FGNode,
  type ZMode,
} from './adapter'
import { makeGroupColor, resolveTheme, withAlpha } from './theme'

interface Props {
  nodes: GraphNode[]
  edges: GraphEdge[]
  zMode?: ZMode
}
const props = defineProps<Props>()
const emit = defineEmits<{
  (e: 'select', path: string): void
}>()

const host = ref<HTMLDivElement | null>(null)
let graph: ForceGraph3DInstance<FGNode, FGLink> | null = null
let presetObserver: MutationObserver | null = null
let resizeObserver: ResizeObserver | null = null
let resizeRaf = 0

let hoverNode: FGNode | null = null
let highlightIds = new Set<string>()
let adjacency = new Map<string, Set<string>>()

let theme = resolveTheme()
let groupColor = makeGroupColor(theme)

// The tooltip accessor is rendered as HTML by the library; note
// titles are user content, so escape them.
function escapeHTML(s: string): string {
  return s.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`)
}

// Accessors are only re-evaluated when a prop setter fires; re-set
// nodeColor with itself to refresh hover/theme styling.
function refresh() {
  graph?.nodeColor(graph.nodeColor())
}

function reapplyTheme() {
  theme = resolveTheme()
  groupColor = makeGroupColor(theme)
  graph?.backgroundColor(theme.bg)
  refresh()
}

function linkTouchesHover(l: FGLink): boolean {
  if (!hoverNode) return false
  return endpointId(l.source) === hoverNode.id || endpointId(l.target) === hoverNode.id
}

/**
 * Semantic z-axis: pin (fz) or free the third coordinate per mode.
 *  - groups: one plane per color group (spread symmetrically around 0) —
 *    projects/folders become readable layers;
 *  - recency: newer notes float up, older sink (mtime normalized to a
 *    fixed band; unknown mtime sits mid-band);
 *  - free: the simulation owns z (default).
 */
const Z_BAND = 160
function applyZMode() {
  if (!graph) return
  const { nodes } = graph.graphData()
  if (props.zMode === 'groups') {
    const groups = [...new Set(nodes.map((n) => n.group))].sort()
    const gap = groups.length > 1 ? (Z_BAND * 2) / (groups.length - 1) : 0
    const layer = new Map(groups.map((g, i) => [g, -Z_BAND + i * gap]))
    for (const n of nodes) n.fz = layer.get(n.group) ?? 0
  } else if (props.zMode === 'recency') {
    const known = nodes.filter((n) => n.mtime > 0).map((n) => n.mtime)
    const min = Math.min(...known)
    const max = Math.max(...known)
    const span = max - min
    for (const n of nodes) {
      n.fz = n.mtime > 0 && span > 0 ? -Z_BAND + ((n.mtime - min) / span) * Z_BAND * 2 : 0
    }
  } else {
    for (const n of nodes) n.fz = undefined
  }
  graph.d3ReheatSimulation()
}

watch(
  () => props.zMode,
  () => applyZMode(),
)

function applyData(nodes: GraphNode[], edges: GraphEdge[]) {
  if (!graph) return
  const prev = new Map<string, { x: number; y: number; z?: number }>()
  for (const n of graph.graphData().nodes) {
    if (n.x !== undefined && n.y !== undefined) prev.set(n.id, { x: n.x, y: n.y, z: n.z })
  }
  const data = toForceGraph(nodes, edges, prev)
  adjacency = buildAdjacency(data)
  hoverNode = null
  highlightIds = new Set()
  pendingFit = true
  graph.graphData(data)
  if (props.zMode && props.zMode !== 'free') applyZMode()
}

let pendingFit = false

async function mount() {
  if (!host.value) return
  const [{ default: ForceGraph3D }, { forceCollide }] = await Promise.all([
    import('3d-force-graph'),
    import('d3-force-3d'),
  ])
  if (!host.value) return // unmounted while the chunk loaded

  const box = host.value.getBoundingClientRect()
  // The shipped d.ts binds the exported const to the *default* node/link
  // generics (they sit on the interface, not on the `new` signature), so
  // retype the constructor to get FGNode/FGLink-aware accessors.
  const Ctor = ForceGraph3D as unknown as new (
    element: HTMLElement,
  ) => ForceGraph3DInstance<FGNode, FGLink>
  const g = new Ctor(host.value)
    .width(box.width)
    .height(box.height)
    .backgroundColor(theme.bg)
    .showNavInfo(false)
    .nodeRelSize(NODE_REL_SIZE)
    .nodeVal(nodeVal)
    .nodeLabel((n) => escapeHTML(n.label))
    .nodeColor((n) => {
      if (hoverNode && hoverNode.id === n.id) return theme.warning
      const base = groupColor(n.group)
      return hoverNode && !highlightIds.has(n.id) ? withAlpha(theme.textMuted, 0.15) : base
    })
    .linkColor((l) => {
      if (linkTouchesHover(l)) return theme.accent
      const base = l.cross ? 0.35 : 0.7
      return withAlpha(theme.textMuted, hoverNode ? base * 0.25 : base)
    })
    // Width 0 renders cheap 1px lines; highlighted links get a thin
    // cylinder so the hovered neighbourhood pops.
    .linkWidth((l) => (linkTouchesHover(l) ? 1.2 : 0))
    .linkOpacity(0.45)
    .warmupTicks(60)
    .cooldownTime(6000)
    .d3AlphaMin(0.015)
    // The interaction callbacks are typed against the base NodeObject
    // (the generics don't flow through them in the shipped d.ts), so
    // narrow back to FGNode here.
    .onNodeClick((node) => emit('select', (node as FGNode).id))
    .onNodeHover((n) => {
      const node = (n as FGNode | null) ?? null
      hoverNode = node
      if (node) {
        highlightIds = new Set(adjacency.get(node.id) ?? [])
        highlightIds.add(node.id)
      } else {
        highlightIds = new Set()
      }
      refresh()
    })
    .onEngineStop(() => {
      if (!pendingFit || !graph) return
      pendingFit = false
      graph.zoomToFit(400, 60)
    })

  g.d3Force('charge')?.strength?.(-70)
  g.d3Force('charge')?.distanceMax?.(400)
  g.d3Force('link')?.distance?.(40)
  g.d3Force('collide', forceCollide<FGNode>((n) => nodeRadius(n) + 5))
  graph = g

  applyData(props.nodes, props.edges)

  presetObserver = new MutationObserver(reapplyTheme)
  presetObserver.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['data-preset'],
  })

  resizeObserver = new ResizeObserver(() => {
    if (resizeRaf) cancelAnimationFrame(resizeRaf)
    resizeRaf = requestAnimationFrame(() => {
      if (!graph || !host.value) return
      const b = host.value.getBoundingClientRect()
      graph.width(b.width).height(b.height)
    })
  })
  resizeObserver.observe(host.value)
}

onMounted(mount)
onBeforeUnmount(() => {
  presetObserver?.disconnect()
  presetObserver = null
  resizeObserver?.disconnect()
  resizeObserver = null
  if (resizeRaf) cancelAnimationFrame(resizeRaf)
  graph?._destructor()
  graph = null
})

watch(
  () => [props.nodes, props.edges] as const,
  ([nodes, edges]) => applyData(nodes, edges),
  { deep: false },
)
</script>

<template>
  <div
    ref="host"
    class="w-full h-full bg-bg overflow-hidden"
  />
</template>
