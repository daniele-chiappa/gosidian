<script setup lang="ts">
/**
 * GraphCanvas — force-graph (canvas 2D + live d3-force simulation).
 * Replaces the former Cytoscape+fcose static layout: nodes are now
 * draggable, collision-separated, colored per group and labelled with
 * zoom-level LOD, which is what keeps a dense vault readable. The
 * runtime is dynamically imported so routes that never open a graph
 * window don't pay for it.
 *
 * The component is presentational: it receives `nodes` + `edges` and
 * emits `select(path)` when the user clicks a node. Filter state
 * lives in the parent (GraphView) so the URL stays sharable.
 *
 * Layout stability: on every data change the current node positions
 * are fed back as seeds (see adapter.toForceGraph), so tweaking a
 * filter refines the picture instead of re-rolling it.
 *
 * Theme: canvas colors can't reference CSS `var(--color-x)`, so the
 * tokens are resolved up front via getComputedStyle and re-resolved
 * when the `data-preset` attribute on <html> changes (Mocha → Latte
 * etc. without remounting). Group colors are HSL-generated from the
 * group name, with lightness picked for the current bg luminance.
 */
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type ForceGraph from 'force-graph'
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
} from './adapter'
import { makeGroupColor, resolveTheme, withAlpha } from './theme'

interface Props {
  nodes: GraphNode[]
  edges: GraphEdge[]
}
const props = defineProps<Props>()
const emit = defineEmits<{
  (e: 'select', path: string): void
}>()

const host = ref<HTMLDivElement | null>(null)
let graph: ForceGraph<FGNode, FGLink> | null = null
let presetObserver: MutationObserver | null = null
let resizeObserver: ResizeObserver | null = null
let resizeRaf = 0
let fitTimer = 0

// Labels fade in between these zoom levels; below the range the
// canvas shows shape only (hover always reveals the full title).
const LABEL_FADE_START = 1.0
const LABEL_FADE_RANGE = 0.5
const LABEL_MAX_CHARS = 28

// Hover state, read by the per-frame paint accessors. force-graph
// redraws on pointer interaction, so mutating these in onNodeHover is
// enough — no explicit invalidation needed.
let hoverNode: FGNode | null = null
let highlightIds = new Set<string>()
let adjacency = new Map<string, Set<string>>()

let theme = resolveTheme()
let groupColor = makeGroupColor(theme)

function reapplyTheme() {
  theme = resolveTheme()
  groupColor = makeGroupColor(theme)
  // Any prop setter triggers a redraw; the accessors re-read `theme`.
  graph?.nodeRelSize(NODE_REL_SIZE)
}

function trimLabel(label: string): string {
  return label.length > LABEL_MAX_CHARS ? label.slice(0, LABEL_MAX_CHARS - 1) + '…' : label
}

function drawNode(node: FGNode, ctx: CanvasRenderingContext2D, scale: number) {
  const r = nodeRadius(node)
  const hovered = hoverNode !== null && hoverNode.id === node.id
  const lit = highlightIds.has(node.id)
  const dimmed = hoverNode !== null && !lit
  const x = node.x ?? 0
  const y = node.y ?? 0

  ctx.globalAlpha = dimmed ? 0.15 : 1
  ctx.beginPath()
  ctx.arc(x, y, r, 0, 2 * Math.PI)
  ctx.fillStyle = hovered ? theme.warning : groupColor(node.group)
  ctx.fill()
  if (hovered || lit) {
    ctx.lineWidth = Math.max(1.5 / scale, 0.75)
    ctx.strokeStyle = hovered ? theme.warning : theme.accent
    ctx.stroke()
  }

  // Label LOD: fade in with zoom; hover shows the neighbourhood's
  // full titles regardless of zoom level.
  const zoomAlpha = Math.min(Math.max((scale - LABEL_FADE_START) / LABEL_FADE_RANGE, 0), 1)
  const labelAlpha = hovered || (lit && hoverNode !== null) ? 1 : zoomAlpha
  if (labelAlpha > 0 && !dimmed) {
    const fontSize = (hovered ? 13 : 11) / scale
    ctx.font = `${hovered ? 600 : 400} ${fontSize}px ui-sans-serif, system-ui, sans-serif`
    ctx.textAlign = 'center'
    ctx.textBaseline = 'top'
    const label = hovered ? node.label : trimLabel(node.label)
    const ty = y + r + 2 / scale
    ctx.globalAlpha = labelAlpha
    ctx.lineWidth = 3 / scale
    ctx.strokeStyle = theme.bg
    ctx.strokeText(label, x, ty)
    ctx.fillStyle = theme.text
    ctx.fillText(label, x, ty)
  }
  ctx.globalAlpha = 1
}

function linkTouchesHover(l: FGLink): boolean {
  if (!hoverNode) return false
  return endpointId(l.source) === hoverNode.id || endpointId(l.target) === hoverNode.id
}

function applyData(nodes: GraphNode[], edges: GraphEdge[]) {
  if (!graph) return
  const prev = new Map<string, { x: number; y: number }>()
  for (const n of graph.graphData().nodes) {
    if (n.x !== undefined && n.y !== undefined) prev.set(n.id, { x: n.x, y: n.y })
  }
  const data = toForceGraph(nodes, edges, prev)
  adjacency = buildAdjacency(data)
  hoverNode = null
  highlightIds = new Set()
  pendingFit = true
  graph.graphData(data)
}

let pendingFit = false

async function mount() {
  if (!host.value) return
  const [{ default: ForceGraphCtor }, { forceCollide, forceX, forceY }] = await Promise.all([
    import('force-graph'),
    import('d3-force-3d'),
  ])
  if (!host.value) return // unmounted while the chunk loaded

  const box = host.value.getBoundingClientRect()
  graph = new ForceGraphCtor<FGNode, FGLink>(host.value)
    .width(box.width)
    .height(box.height)
    .minZoom(0.2)
    .maxZoom(8)
    .nodeRelSize(NODE_REL_SIZE)
    .nodeVal(nodeVal)
    .nodeLabel(() => '') // we paint labels ourselves; suppress the tooltip
    .nodeCanvasObjectMode(() => 'replace')
    .nodeCanvasObject(drawNode)
    .linkColor((l) => {
      if (linkTouchesHover(l)) return theme.accent
      const base = l.cross ? 0.25 : 0.45
      return withAlpha(theme.textMuted, hoverNode ? base * 0.25 : base)
    })
    .linkWidth((l) => {
      const w = 0.8 + Math.min(l.count, 6) * 0.4
      return linkTouchesHover(l) ? w + 1 : w
    })
    .linkLineDash((l) => (l.cross ? [4, 3] : null))
    .warmupTicks(60)
    .cooldownTime(6000)
    .d3AlphaMin(0.015)
    .onNodeClick((node) => emit('select', node.id))
    .onNodeHover((node) => {
      hoverNode = node
      if (node) {
        highlightIds = new Set(adjacency.get(node.id) ?? [])
        highlightIds.add(node.id)
      } else {
        highlightIds = new Set()
      }
    })
    .onEngineStop(() => {
      if (!pendingFit || !graph) return
      pendingFit = false
      graph.zoomToFit(300, 40)
      // zoomToFit on a tiny ego graph can over-zoom past readability;
      // clamp after the transition settles.
      window.clearTimeout(fitTimer)
      fitTimer = window.setTimeout(() => {
        if (graph && graph.zoom() > 3) graph.zoom(3, 200)
      }, 350)
    })

  // Spacing tuning — the "less hairball" knobs: stronger short-range
  // repulsion, collision at label-friendly radii, and a weak pull
  // toward the center so disconnected components don't drift apart.
  graph.d3Force('charge')?.strength?.(-70)
  graph.d3Force('charge')?.distanceMax?.(400)
  graph.d3Force('link')?.distance?.(40)
  graph.d3Force('collide', forceCollide<FGNode>((n) => nodeRadius(n) + 5))
  graph.d3Force('x', forceX<FGNode>(0).strength(0.04))
  graph.d3Force('y', forceY<FGNode>(0).strength(0.04))

  applyData(props.nodes, props.edges)

  // React to preset switches (Mocha → Latte → Tokyo Night, etc.) so
  // the canvas reflects the new tokens without a remount.
  presetObserver = new MutationObserver(reapplyTheme)
  presetObserver.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['data-preset'],
  })

  // Track the host box — e.g. a plancia window cycling its width step,
  // or the sidebar drag. force-graph sizes to the window by default,
  // so the explicit width/height must follow the container; rAF-
  // coalesced so a drag doesn't thrash.
  resizeObserver = new ResizeObserver(() => {
    if (resizeRaf) cancelAnimationFrame(resizeRaf)
    resizeRaf = requestAnimationFrame(() => {
      if (!graph || !host.value) return
      const b = host.value.getBoundingClientRect()
      graph.width(b.width).height(b.height)
      graph.zoomToFit(200, 40)
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
  window.clearTimeout(fitTimer)
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
