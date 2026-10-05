import { describe, expect, it } from 'vitest'
import type { CanvasCard } from '@/api/notes'
import {
  anchor,
  bounds,
  CANVAS_PAD,
  colorOf,
  edgeShape,
  facingSide,
  fit,
  zoomAt,
} from '@/components/domain/canvasGeometry'

const card = (id: string, x: number, y: number, width = 100, height = 50): CanvasCard => ({
  id,
  type: 'text',
  x,
  y,
  width,
  height,
})

describe('canvas geometry', () => {
  it('spans the cards with room around them', () => {
    const b = bounds([card('a', -100, -40), card('b', 300, 200, 200, 100)])
    expect(b).toEqual({
      minX: -100 - CANVAS_PAD,
      minY: -40 - CANVAS_PAD,
      width: 600 + 2 * CANVAS_PAD,
      height: 340 + 2 * CANVAS_PAD,
    })
    expect(bounds([])).toEqual({ minX: 0, minY: 0, width: 0, height: 0 })
  })

  it('leaves a card by the side facing the other', () => {
    const a = card('a', 0, 0)
    expect(facingSide(a, card('b', 400, 10))).toBe('right')
    expect(facingSide(a, card('b', -400, 10))).toBe('left')
    expect(facingSide(a, card('b', 10, 300))).toBe('bottom')
    expect(facingSide(a, card('b', 10, -300))).toBe('top')
    expect(anchor(a, 'bottom')).toEqual({ x: 50, y: 50 })
    expect(anchor(a, 'left')).toEqual({ x: 0, y: 25 })
  })

  it('draws a curve square to the sides, an arrowhead at each end', () => {
    const a = card('a', 0, 0)
    const b = card('b', 300, 0)
    const s = edgeShape({ id: 'e', fromNode: 'a', toNode: 'b' }, a, b)
    // From a's right side to b's left side, the controls pulled along them.
    expect(s.d).toBe('M 100 25 C 180 25, 220 25, 300 25')
    expect(s.mid).toEqual({ x: 200, y: 25 })
    expect(s.endArrow.split(' ')[0]).toBe('300,25')
    expect(s.endArrow).toBe('300,25 286,31 286,19')
    const t = edgeShape(
      { id: 'e', fromNode: 'a', fromSide: 'bottom', toNode: 'b', toSide: 'top' },
      a,
      b,
    )
    expect(t.d.startsWith('M 50 50 C 50 ')).toBe(true)
  })

  it('knows the presets and hex colors, and nothing else', () => {
    expect(colorOf('1')).toBe('#e93147')
    expect(colorOf('6')).toBe('#7852ee')
    expect(colorOf('#AbC')).toBe('#AbC')
    expect(colorOf('#a1b2c3')).toBe('#a1b2c3')
    expect(colorOf('7')).toBeUndefined()
    expect(colorOf('red')).toBeUndefined()
    expect(colorOf('#fff; background: url(x)')).toBeUndefined()
    expect(colorOf(undefined)).toBeUndefined()
  })

  it('fits the plane, never above 1:1, and zooms around a point', () => {
    const b = { minX: 0, minY: 0, width: 2000, height: 1000 }
    expect(fit(b, 1000, 1000)).toEqual({ k: 0.5, tx: 0, ty: 250 })
    expect(fit({ ...b, width: 100, height: 100 }, 1000, 500).k).toBe(1)
    const v = zoomAt({ k: 1, tx: 0, ty: 0 }, 2, { x: 100, y: 100 })
    expect(v).toEqual({ k: 2, tx: -100, ty: -100 })
    expect(zoomAt({ k: 2.5, tx: 0, ty: 0 }, 2, { x: 0, y: 0 }).k).toBe(3)
  })
})
