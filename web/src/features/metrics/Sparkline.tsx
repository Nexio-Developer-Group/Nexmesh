/** A tiny trend line. The numbers are also given as text by the caller; this is the shape. */
export function Sparkline({ values, label, unit, max }: { values: number[]; label: string; unit: string; max?: number }) {
  const w = 120
  const h = 28
  if (values.length < 2) return <span className="text-xs text-faint">collecting…</span>
  const hi = Math.max(max ?? 0, ...values, 1)
  const pts = values.map((v, i) => `${((i / (values.length - 1)) * w).toFixed(1)},${(h - 2 - (Math.max(v, 0) / hi) * (h - 4)).toFixed(1)}`).join(' ')
  const last = values[values.length - 1]
  return (
    <svg role="img" aria-label={`${label} over the last ${values.length} readings: now ${last}${unit}, low ${Math.min(...values)}${unit}, high ${Math.max(...values)}${unit}`}
      width={w} height={h} viewBox={`0 0 ${w} ${h}`} className="overflow-visible">
      <polyline points={pts} fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" strokeLinecap="round" className="text-blue" />
    </svg>
  )
}
