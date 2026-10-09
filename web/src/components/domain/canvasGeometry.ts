/**
 * Geometry of an Obsidian canvas (IMP-144): the plane its cards span, where
 * a connection leaves and reaches a card, the curve between, and the colors
 * of JSON Canvas. Pure, so CanvasPreview stays a view and vitest can check
 * the numbers.
 */
import type { CanvasCard, CanvasEdge } from '@/api/notes'

export type Side = 'top' | 'right' | 'bottom' | 'left'

export interface Point {
  x: number
  y: number
}

export interface Bounds {
  minX: number
  minY: number
  width: number
  height: number
}

/** Room around the cards, so a group label or an arrow is not cut. */
export const CANVAS_PAD = 60

/** The rectangle the cards span, with CANVAS_PAD around; 0×0 for none. */
export function bounds(cards: CanvasCard[]): Bounds {
  if (!cards.length) return { minX: 0, minY: 0, width: 0, height: 0 }
  let minX = Infinity
  let minY = Infinity
  let maxX = -Infinity
  let maxY = -Infinity
  for (const c of cards) {
    minX = Math.min(minX, c.x)
    minY = Math.min(minY, c.y)
    maxX = Math.max(maxX, c.x + c.width)
    maxY = Math.max(maxY, c.y + c.height)
  }
  return {
    minX: minX - CANVAS_PAD,
    minY: minY - CANVAS_PAD,
    width: maxX - minX + 2 * CANVAS_PAD,
    height: maxY - minY + 2 * CANVAS_PAD,
  }
}

const center = (c: CanvasCard): Point => ({ x: c.x + c.width / 2, y: c.y + c.height / 2 })

/** The side of `from` a connection to `to` leaves by, when the file names none. */
export function facingSide(from: CanvasCard, to: CanvasCard): Side {
  const a = center(from)
  const b = center(to)
  const dx = b.x - a.x
  const dy = b.y - a.y
  if (Math.abs(dx) >= Math.abs(dy)) return dx >= 0 ? 'right' : 'left'
  return dy >= 0 ? 'bottom' : 'top'
}

/** The middle of a card's side. */
export function anchor(c: CanvasCard, side: Side): Point {
  switch (side) {
    case 'top':
      return { x: c.x + c.width / 2, y: c.y }
    case 'bottom':
      return { x: c.x + c.width / 2, y: c.y + c.height }
    case 'left':
      return { x: c.x, y: c.y + c.height / 2 }
    default:
      return { x: c.x + c.width, y: c.y + c.height / 2 }
  }
}

const normal: Record<Side, Point> = {
  top: { x: 0, y: -1 },
  bottom: { x: 0, y: 1 },
  left: { x: -1, y: 0 },
  right: { x: 1, y: 0 },
}

export interface EdgeShape {
  /** The SVG path, a cubic curve leaving and reaching each card square to its side. */
  d: string
  /** Where the label sits: the curve's midpoint. */
  mid: Point
  /** The arrowheads at each end, as SVG polygon points, along the curve. */
  startArrow: string
  endArrow: string
}

const ARROW_LENGTH = 14
const ARROW_HALF_WIDTH = 6

/** An arrowhead with its tip at `tip`, pointing away from `back`. */
function arrowHead(tip: Point, back: Point): string {
  const len = Math.hypot(tip.x - back.x, tip.y - back.y) || 1
  const u = { x: (tip.x - back.x) / len, y: (tip.y - back.y) / len }
  const base = { x: tip.x - u.x * ARROW_LENGTH, y: tip.y - u.y * ARROW_LENGTH }
  const r = (n: number) => Math.round(n * 10) / 10
  const pts = [
    tip,
    { x: base.x - u.y * ARROW_HALF_WIDTH, y: base.y + u.x * ARROW_HALF_WIDTH },
    { x: base.x + u.y * ARROW_HALF_WIDTH, y: base.y - u.x * ARROW_HALF_WIDTH },
  ]
  return pts.map((p) => `${r(p.x)},${r(p.y)}`).join(' ')
}

/** The curve of a connection between two cards, in canvas coordinates. */
export function edgeShape(e: CanvasEdge, from: CanvasCard, to: CanvasCard): EdgeShape {
  const fs: Side = e.fromSide ?? facingSide(from, to)
  const ts: Side = e.toSide ?? facingSide(to, from)
  const p1 = anchor(from, fs)
  const p2 = anchor(to, ts)
  const reach = Math.max(40, Math.hypot(p2.x - p1.x, p2.y - p1.y) * 0.4)
  const c1 = { x: p1.x + normal[fs].x * reach, y: p1.y + normal[fs].y * reach }
  const c2 = { x: p2.x + normal[ts].x * reach, y: p2.y + normal[ts].y * reach }
  const r = (n: number) => Math.round(n * 10) / 10
  return {
    d: `M ${r(p1.x)} ${r(p1.y)} C ${r(c1.x)} ${r(c1.y)}, ${r(c2.x)} ${r(c2.y)}, ${r(p2.x)} ${r(p2.y)}`,
    mid: {
      x: r((p1.x + 3 * c1.x + 3 * c2.x + p2.x) / 8),
      y: r((p1.y + 3 * c1.y + 3 * c2.y + p2.y) / 8),
    },
    startArrow: arrowHead(p1, c1),
    endArrow: arrowHead(p2, c2),
  }
}

/** Obsidian's colors for the presets of JSON Canvas. */
const presets: Record<string, string> = {
  '1': '#e93147',
  '2': '#ec7500',
  '3': '#e0ac00',
  '4': '#08b94e',
  '5': '#00bfbc',
  '6': '#7852ee',
}

/** The CSS color of a card or connection, or undefined for the default. */
export function colorOf(c?: string): string | undefined {
  if (!c) return undefined
  if (presets[c]) return presets[c]
  return /^#[0-9a-fA-F]{3}([0-9a-fA-F]{3})?$/.test(c) ? c : undefined
}

/** The smallest scale a canvas opens at: below it the cards cannot be read. */
export const READABLE_FIT = 0.5

/**
 * The scale and offset that show the whole plane in a viewport, never above
 * 1:1 and never below minK. A plane that does not fit at minK starts from
 * its top left corner, on the axis it overflows; it is centered otherwise.
 */
export function fit(b: Bounds, vw: number, vh: number, minK = 0): { k: number; tx: number; ty: number } {
  if (!b.width || !b.height || !vw || !vh) return { k: 1, tx: 0, ty: 0 }
  const k = Math.max(Math.min(vw / b.width, vh / b.height, 1), Math.min(minK, 1))
  const along = (view: number, size: number) => (size * k > view ? 0 : (view - size * k) / 2)
  return { k, tx: along(vw, b.width), ty: along(vh, b.height) }
}

/** Zoom by factor around a viewport point, keeping it still; k within [min, max]. */
export function zoomAt(
  view: { k: number; tx: number; ty: number },
  factor: number,
  at: Point,
  min = 0.1,
  max = 3,
): { k: number; tx: number; ty: number } {
  const k = Math.min(max, Math.max(min, view.k * factor))
  const s = k / view.k
  return { k, tx: at.x - (at.x - view.tx) * s, ty: at.y - (at.y - view.ty) * s }
}
