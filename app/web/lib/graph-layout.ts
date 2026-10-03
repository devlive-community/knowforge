// 力导向布局（无依赖）：节点间互相排斥、有边的节点相互吸引、整体向中心收拢，迭代若干次后返回坐标。
// 初始位置按节点顺序排在螺旋线上，同样的输入得到同样的结果（不随机），便于刷新后布局稳定。

export interface LayoutPoint { x: number; y: number }

export interface LayoutOptions {
  iterations?: number
  linkDistance?: number // 有边节点的理想距离
  repulsion?: number // 排斥强度
  gravity?: number // 向中心收拢的强度
}

export function forceLayout(ids: number[], edges: [number, number][], options: LayoutOptions = {}): Map<number, LayoutPoint> {
  const { iterations = 300, linkDistance = 110, repulsion = 9000, gravity = 0.02 } = options
  const n = ids.length
  const pos = ids.map((_, i) => {
    // 黄金角螺旋：均匀铺开且确定
    const r = 30 * Math.sqrt(i + 0.5)
    const a = i * 2.399963229728653
    return { x: r * Math.cos(a), y: r * Math.sin(a) }
  })
  const index = new Map(ids.map((id, i) => [id, i]))
  const links = edges
    .map(([a, b]) => [index.get(a), index.get(b)] as const)
    .filter((e): e is readonly [number, number] => e[0] !== undefined && e[1] !== undefined && e[0] !== e[1])

  for (let it = 0; it < iterations; it++) {
    const cooling = 1 - it / iterations // 逐步降温，越到后面移动越小
    const disp = pos.map(() => ({ x: 0, y: 0 }))
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n; j++) {
        let dx = pos[i].x - pos[j].x
        let dy = pos[i].y - pos[j].y
        let d2 = dx * dx + dy * dy
        if (d2 < 0.01) { dx = 0.1 * (i - j); dy = 0.1; d2 = dx * dx + dy * dy }
        const f = repulsion / d2
        const d = Math.sqrt(d2)
        disp[i].x += (dx / d) * f
        disp[i].y += (dy / d) * f
        disp[j].x -= (dx / d) * f
        disp[j].y -= (dy / d) * f
      }
    }
    for (const [a, b] of links) {
      const dx = pos[b].x - pos[a].x
      const dy = pos[b].y - pos[a].y
      const d = Math.max(0.01, Math.sqrt(dx * dx + dy * dy))
      const f = (d - linkDistance) * 0.08
      disp[a].x += (dx / d) * f
      disp[a].y += (dy / d) * f
      disp[b].x -= (dx / d) * f
      disp[b].y -= (dy / d) * f
    }
    const maxStep = 40 * cooling + 1
    for (let i = 0; i < n; i++) {
      disp[i].x -= pos[i].x * gravity
      disp[i].y -= pos[i].y * gravity
      const len = Math.sqrt(disp[i].x * disp[i].x + disp[i].y * disp[i].y)
      if (len > 0) {
        const step = Math.min(len, maxStep)
        pos[i].x += (disp[i].x / len) * step
        pos[i].y += (disp[i].y / len) * step
      }
    }
  }
  return new Map(ids.map((id, i) => [id, pos[i]]))
}

// graphBounds 坐标范围（含边距），用于让整张图适应视口。
export function graphBounds(points: LayoutPoint[], margin = 60): { x: number; y: number; w: number; h: number } {
  if (points.length === 0) return { x: -100, y: -100, w: 200, h: 200 }
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity
  for (const p of points) {
    minX = Math.min(minX, p.x); minY = Math.min(minY, p.y)
    maxX = Math.max(maxX, p.x); maxY = Math.max(maxY, p.y)
  }
  return { x: minX - margin, y: minY - margin, w: maxX - minX + margin * 2, h: maxY - minY + margin * 2 }
}
