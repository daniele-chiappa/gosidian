declare module 'd3-force-3d' {
  // Minimal surface for the forces we tune — the package ships no types.
  interface Force {
    (alpha?: number): void
    initialize?: (nodes: unknown[], ...args: unknown[]) => void
  }
  export interface CollideForce<N> extends Force {
    radius(r: number | ((node: N) => number)): CollideForce<N>
    strength(s: number): CollideForce<N>
  }
  export interface PositionForce<N> extends Force {
    strength(s: number | ((node: N) => number)): PositionForce<N>
  }
  export function forceCollide<N>(radius?: number | ((node: N) => number)): CollideForce<N>
  export function forceX<N>(x?: number): PositionForce<N>
  export function forceY<N>(y?: number): PositionForce<N>
}
