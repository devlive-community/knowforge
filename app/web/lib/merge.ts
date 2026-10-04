// 三方合并（按行）：以打开章节时的内容为基准（base），合并自己的修改（mine）与他人已保存的修改（theirs）。
// 两边改动不重叠时自动合并；改动了同一处且结果不同时产生冲突块，由作者逐块选择。
// 行级 diff 使用 Myers 算法（O((N+M)·D)），改动过多（D 超过上限）时退化为整体比较。

const MAX_EDIT_DISTANCE = 4000

// matchLines 返回 a 与 b 中相同行的对应关系（按顺序的 [i, j] 列表）。
export function matchLines(a: string[], b: string[]): [number, number][] {
  const n = a.length
  const m = b.length
  const max = n + m
  if (max === 0) return []
  const off = max + 1
  const v = new Int32Array(2 * max + 3)
  const trace: Int32Array[] = []
  let found = -1
  const limit = Math.min(max, MAX_EDIT_DISTANCE)
  for (let d = 0; d <= limit && found < 0; d++) {
    trace.push(v.slice())
    for (let k = -d; k <= d; k += 2) {
      let x = k === -d || (k !== d && v[off + k - 1] < v[off + k + 1]) ? v[off + k + 1] : v[off + k - 1] + 1
      let y = x - k
      while (x < n && y < m && a[x] === b[y]) { x++; y++ }
      v[off + k] = x
      if (x >= n && y >= m) { found = d; break }
    }
  }
  if (found < 0) return commonEnds(a, b)
  const pairs: [number, number][] = []
  let x = n
  let y = m
  for (let d = found; d > 0; d--) {
    const prev = trace[d]
    const k = x - y
    const prevK = k === -d || (k !== d && prev[off + k - 1] < prev[off + k + 1]) ? k + 1 : k - 1
    const prevX = prev[off + prevK]
    const prevY = prevX - prevK
    while (x > prevX && y > prevY) { x--; y--; pairs.push([x, y]) }
    x = prevX
    y = prevY
  }
  while (x > 0 && y > 0) { x--; y--; pairs.push([x, y]) }
  return pairs.reverse()
}

// commonEnds 改动过多时的退化方案：只认首尾相同的行。
function commonEnds(a: string[], b: string[]): [number, number][] {
  const pairs: [number, number][] = []
  let i = 0
  while (i < a.length && i < b.length && a[i] === b[i]) { pairs.push([i, i]); i++ }
  let j = 0
  const tail: [number, number][] = []
  while (j < a.length - i && j < b.length - i && a[a.length - 1 - j] === b[b.length - 1 - j]) {
    tail.push([a.length - 1 - j, b.length - 1 - j])
    j++
  }
  return pairs.concat(tail.reverse())
}

export type MergeChunk =
  | { type: 'ok'; lines: string[] }
  | { type: 'conflict'; base: string[]; mine: string[]; theirs: string[] }

export interface MergeResult {
  chunks: MergeChunk[]
  conflicts: number
}

const same = (x: string[], y: string[]) => x.length === y.length && x.every((line, i) => line === y[i])

// merge3 三方合并（diff3）：以 base 中两边都没有改动的行为锚点切分，逐段判断取哪一边。
export function merge3(base: string, mine: string, theirs: string): MergeResult {
  const o = base.split('\n')
  const a = mine.split('\n')
  const b = theirs.split('\n')
  const mapA = new Map<number, number>(matchLines(o, a))
  const mapB = new Map<number, number>(matchLines(o, b))
  const chunks: MergeChunk[] = []
  const pushOk = (lines: string[]) => {
    if (lines.length === 0) return
    const last = chunks[chunks.length - 1]
    if (last?.type === 'ok') last.lines.push(...lines)
    else chunks.push({ type: 'ok', lines: [...lines] })
  }
  let conflicts = 0
  let io = 0
  let ia = 0
  let ib = 0
  while (io < o.length || ia < a.length || ib < b.length) {
    // 稳定段：base 的行在两边都原样保留且位置对应
    let l = 0
    while (io + l < o.length && mapA.get(io + l) === ia + l && mapB.get(io + l) === ib + l) l++
    if (l > 0) {
      pushOk(o.slice(io, io + l))
      io += l
      ia += l
      ib += l
      continue
    }
    // 下一个两边都保留的 base 行（锚点）；没有则到末尾
    let so = io
    while (so < o.length && !(mapA.has(so) && mapB.has(so) && mapA.get(so)! >= ia && mapB.get(so)! >= ib)) so++
    const sa = so < o.length ? mapA.get(so)! : a.length
    const sb = so < o.length ? mapB.get(so)! : b.length
    const ob = o.slice(io, so)
    const ab = a.slice(ia, sa)
    const bb = b.slice(ib, sb)
    if (same(ab, ob)) pushOk(bb) // 只有他人改了
    else if (same(bb, ob) || same(ab, bb)) pushOk(ab) // 只有自己改了，或两边改得一样
    else {
      chunks.push({ type: 'conflict', base: ob, mine: ab, theirs: bb })
      conflicts++
    }
    io = so
    ia = sa
    ib = sb
  }
  return { chunks, conflicts }
}

export type ConflictChoice = 'mine' | 'theirs' | 'both'

// resolveMerge 按每个冲突块的选择拼出最终内容（未指定的冲突块取自己的版本）。
export function resolveMerge(result: MergeResult, choices: ConflictChoice[] = []): string {
  const lines: string[] = []
  let idx = 0
  for (const chunk of result.chunks) {
    if (chunk.type === 'ok') {
      lines.push(...chunk.lines)
      continue
    }
    const choice = choices[idx++] || 'mine'
    if (choice === 'theirs') lines.push(...chunk.theirs)
    else if (choice === 'both') lines.push(...chunk.mine, ...chunk.theirs)
    else lines.push(...chunk.mine)
  }
  return lines.join('\n')
}

// mergeTitle 标题的三方合并：只有一边改了取改过的一边；两边改得不同为冲突（返回 null）。
export function mergeTitle(base: string, mine: string, theirs: string): string | null {
  if (mine === base) return theirs
  if (theirs === base || mine === theirs) return mine
  return null
}
