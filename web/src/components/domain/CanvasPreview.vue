<script setup lang="ts">
/**
 * CanvasPreview — an Obsidian canvas, read-only (IMP-144): its cards where
 * the file puts them, groups behind, connections as SVG curves with their
 * arrows and labels. Drag the background to move around, the wheel scrolls
 * the plane (or a card whose text overflows), Ctrl + wheel or a pinch zooms
 * around the pointer; the buttons zoom and fit the whole canvas.
 *
 * The server resolved the cards for the reader: a text card's markdown
 * comes as HTML, shown by MarkdownPreview (sanitized, its links opening
 * windows); a file card names the note it opens, with the start of it, or
 * the image it shows; a card the reader may not see is its path alone.
 */
import { computed, inject, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Maximize, Minus, Plus } from 'lucide-vue-next'
import { useWindowsStore, type OpenSpec } from 'plancia'
import type { CanvasCard, CanvasData } from '@/api/notes'
import { planciaKey } from '@/composables/planciaKey'
import MarkdownPreview from './MarkdownPreview.vue'
import { bounds, colorOf, edgeShape, fit, zoomAt } from './canvasGeometry'

const props = defineProps<{ canvas: CanvasData }>()

const { t } = useI18n()
const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))

const plane = computed(() => bounds(props.canvas.nodes))
const groups = computed(() =>
  props.canvas.nodes
    .filter((n) => n.type === 'group')
    // Larger groups behind the ones they hold.
    .sort((a, b) => b.width * b.height - a.width * a.height),
)
const cards = computed(() => props.canvas.nodes.filter((n) => n.type !== 'group'))
const byId = computed(() => new Map(props.canvas.nodes.map((n) => [n.id, n])))
const edges = computed(() =>
  props.canvas.edges.flatMap((e) => {
    const from = byId.value.get(e.fromNode)
    const to = byId.value.get(e.toNode)
    if (!from || !to) return []
    return [{ e, ...edgeShape(e, from, to), color: colorOf(e.color) }]
  }),
)

/** A card's place on the plane, in the plane's own coordinates. */
function box(n: CanvasCard) {
  const color = colorOf(n.color)
  return {
    left: `${n.x - plane.value.minX}px`,
    top: `${n.y - plane.value.minY}px`,
    width: `${n.width}px`,
    height: `${n.height}px`,
    ...(color
      ? {
          borderColor: color,
          backgroundColor: `color-mix(in srgb, ${color} ${n.type === 'group' ? 6 : 10}%, transparent)`,
        }
      : {}),
  }
}

function openNote(c: CanvasCard) {
  if (!c.path) return
  openWindow({
    type: 'note',
    key: planciaKey('note', c.path),
    title: c.title || (c.path.split('/').pop() ?? c.path).replace(/\.md$/, ''),
    props: { path: c.path },
  })
}

// The view: a scale and an offset of the plane in the viewport.
const viewport = ref<HTMLElement | null>(null)
const view = ref({ k: 1, tx: 0, ty: 0 })
// Set once the reader moves or zooms: a resize then keeps their view.
let moved = false
const transform = computed(
  () => `translate(${view.value.tx}px, ${view.value.ty}px) scale(${view.value.k})`,
)

function fitView() {
  const el = viewport.value
  if (el) view.value = fit(plane.value, el.clientWidth, el.clientHeight)
}

/** The fit button: the whole canvas again, and resizes fit it again. */
function refit() {
  moved = false
  fitView()
}

function zoomBy(factor: number) {
  const el = viewport.value
  if (!el) return
  moved = true
  view.value = zoomAt(view.value, factor, { x: el.clientWidth / 2, y: el.clientHeight / 2 })
}

function onWheel(e: WheelEvent) {
  const el = viewport.value
  if (!el) return
  if (!e.ctrlKey && !e.metaKey) {
    // A card whose text overflows scrolls itself.
    const scroller = (e.target as HTMLElement | null)?.closest<HTMLElement>('[data-canvas-scroll]')
    if (scroller && scroller.scrollHeight > scroller.clientHeight + 1) return
    e.preventDefault()
    moved = true
    view.value = { ...view.value, tx: view.value.tx - e.deltaX, ty: view.value.ty - e.deltaY }
    return
  }
  e.preventDefault()
  moved = true
  const r = el.getBoundingClientRect()
  view.value = zoomAt(view.value, Math.exp(-e.deltaY * 0.0015), {
    x: e.clientX - r.left,
    y: e.clientY - r.top,
  })
}

// Dragging the background (or a group) moves the plane; a card keeps its
// text selectable and its links clickable, and the buttons their click
// (a captured pointer would take it from them).
let drag: { id: number; x: number; y: number; tx: number; ty: number } | null = null
function onPointerDown(e: PointerEvent) {
  const target = e.target as HTMLElement | null
  if (e.button !== 0 || target?.closest('[data-canvas-card], [data-canvas-controls]')) return
  drag = { id: e.pointerId, x: e.clientX, y: e.clientY, tx: view.value.tx, ty: view.value.ty }
  viewport.value?.setPointerCapture(e.pointerId)
}
function onPointerMove(e: PointerEvent) {
  if (!drag || e.pointerId !== drag.id) return
  moved = true
  view.value = { ...view.value, tx: drag.tx + e.clientX - drag.x, ty: drag.ty + e.clientY - drag.y }
}
function onPointerUp(e: PointerEvent) {
  if (drag && e.pointerId === drag.id) {
    viewport.value?.releasePointerCapture(e.pointerId)
    drag = null
  }
}

let observer: ResizeObserver | null = null
onMounted(() => {
  fitView()
  // The wheel must not scroll the page: a listener that may cancel it.
  viewport.value?.addEventListener('wheel', onWheel, { passive: false })
  if (typeof ResizeObserver !== 'undefined' && viewport.value) {
    observer = new ResizeObserver(() => {
      // The window was resized: fit again, unless the reader moved around.
      if (!moved) fitView()
    })
    observer.observe(viewport.value)
  }
})
onBeforeUnmount(() => {
  viewport.value?.removeEventListener('wheel', onWheel)
  observer?.disconnect()
})
watch(plane, fitView)
</script>

<template>
  <div
    ref="viewport"
    class="relative h-full min-h-80 w-full overflow-hidden bg-bg select-none touch-none cursor-grab active:cursor-grabbing"
    data-canvas
    @pointerdown="onPointerDown"
    @pointermove="onPointerMove"
    @pointerup="onPointerUp"
    @pointercancel="onPointerUp"
  >
    <p v-if="canvas.error" class="p-6 text-danger text-sm" data-canvas-error>
      {{ t('canvas.error', { msg: canvas.error }) }}
    </p>
    <p v-else-if="!canvas.nodes.length" class="p-6 text-text-muted text-sm">
      {{ t('canvas.empty') }}
    </p>
    <div
      v-else
      class="absolute left-0 top-0 origin-top-left"
      :style="{ transform, width: `${plane.width}px`, height: `${plane.height}px` }"
    >
      <div
        v-for="g in groups"
        :key="g.id"
        class="absolute rounded-xl border-2 border-border"
        :style="box(g)"
        data-canvas-group
      >
        <span
          v-if="g.label"
          class="absolute -top-8 left-0 whitespace-nowrap rounded px-2 py-0.5 text-base font-semibold text-text"
          >{{ g.label }}</span
        >
      </div>

      <svg
        class="absolute left-0 top-0 overflow-visible pointer-events-none text-text-muted"
        :width="plane.width"
        :height="plane.height"
        :viewBox="`${plane.minX} ${plane.minY} ${plane.width} ${plane.height}`"
      >
        <g
          v-for="x in edges"
          :key="x.e.id"
          :style="x.color ? { color: x.color } : undefined"
          data-canvas-edge
        >
          <path :d="x.d" fill="none" stroke="currentColor" stroke-width="2.5" />
          <polygon v-if="x.e.toEnd !== 'none'" :points="x.endArrow" fill="currentColor" />
          <polygon v-if="x.e.fromEnd === 'arrow'" :points="x.startArrow" fill="currentColor" />
        </g>
      </svg>

      <span
        v-for="x in edges.filter((x) => x.e.label)"
        :key="`label-${x.e.id}`"
        class="absolute -translate-x-1/2 -translate-y-1/2 whitespace-nowrap rounded bg-bg-elevated px-2 py-0.5 text-sm text-text-muted border border-border"
        :style="{ left: `${x.mid.x - plane.minX}px`, top: `${x.mid.y - plane.minY}px` }"
        data-canvas-edge-label
        >{{ x.e.label }}</span
      >

      <div
        v-for="c in cards"
        :key="c.id"
        class="absolute flex flex-col overflow-hidden rounded-lg border-2 border-border bg-bg-elevated shadow-sm select-text cursor-auto"
        :style="box(c)"
        :data-canvas-card="c.type"
      >
        <template v-if="c.type === 'text'">
          <div class="min-h-0 flex-1 overflow-auto px-4 py-2 text-sm" data-canvas-scroll>
            <MarkdownPreview :html="c.html ?? ''" />
          </div>
        </template>
        <template v-else-if="c.type === 'file' && c.image">
          <img
            :src="c.image"
            :alt="c.file"
            class="h-full w-full object-contain"
            draggable="false"
          />
        </template>
        <template v-else-if="c.type === 'file' && c.path">
          <button
            type="button"
            class="shrink-0 truncate border-b border-border px-3 py-1.5 text-left text-sm font-semibold text-accent hover:underline"
            :title="c.path"
            @click="openNote(c)"
          >
            {{ c.title || c.path
            }}<span v-if="c.subpath" class="font-normal text-text-muted"> {{ c.subpath }}</span>
          </button>
          <div class="min-h-0 flex-1 overflow-auto px-4 py-2 text-sm" data-canvas-scroll>
            <MarkdownPreview v-if="c.html" :html="c.html" />
          </div>
        </template>
        <template v-else-if="c.type === 'file'">
          <p class="px-3 py-2 text-sm font-mono break-all">
            {{ c.file }}
          </p>
          <p class="px-3 text-xs text-text-muted">
            {{ t('canvas.missing') }}
          </p>
        </template>
        <template v-else-if="c.type === 'link'">
          <a
            :href="c.url"
            target="_blank"
            rel="noopener noreferrer"
            class="m-auto break-all px-3 text-sm text-accent hover:underline"
            >{{ c.url }}</a
          >
        </template>
      </div>
    </div>

    <div
      class="absolute bottom-3 right-3 flex overflow-hidden rounded border border-border bg-bg-elevated text-text-muted"
      :title="t('canvas.help')"
      data-canvas-controls
    >
      <button
        type="button"
        class="p-1.5 hover:bg-surface-hover hover:text-text"
        :title="t('canvas.zoom_out')"
        :aria-label="t('canvas.zoom_out')"
        @click="zoomBy(1 / 1.25)"
      >
        <Minus class="h-4 w-4" />
      </button>
      <button
        type="button"
        class="p-1.5 hover:bg-surface-hover hover:text-text"
        :title="t('canvas.fit')"
        :aria-label="t('canvas.fit')"
        data-canvas-fit
        @click="refit"
      >
        <Maximize class="h-4 w-4" />
      </button>
      <button
        type="button"
        class="p-1.5 hover:bg-surface-hover hover:text-text"
        :title="t('canvas.zoom_in')"
        :aria-label="t('canvas.zoom_in')"
        @click="zoomBy(1.25)"
      >
        <Plus class="h-4 w-4" />
      </button>
    </div>
  </div>
</template>
