/** Groups items by a key, keeping first-seen order of groups and of items. */
export function groupBy<T>(items: T[], key: (t: T) => string): [string, T[]][] {
  const m = new Map<string, T[]>()
  for (const it of items) {
    const k = key(it)
    const l = m.get(k)
    if (l) l.push(it)
    else m.set(k, [it])
  }
  return [...m.entries()]
}
