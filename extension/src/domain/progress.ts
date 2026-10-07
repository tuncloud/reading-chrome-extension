// Reading progress (spec T27): clamp((scrollY + innerHeight − rootTop) / rootHeight, 0, 1).

export interface ScrollGeometry {
  scrollY: number
  viewportHeight: number
  // rootTop is the content root's top edge in document coordinates.
  rootTop: number
  rootHeight: number
}

export function readingProgress(g: ScrollGeometry): number {
  if (!(g.rootHeight > 0)) return 0
  const p = (g.scrollY + g.viewportHeight - g.rootTop) / g.rootHeight
  return Math.min(1, Math.max(0, p))
}
