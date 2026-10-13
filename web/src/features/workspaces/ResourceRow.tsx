import { useId } from 'react'

interface Props {
  label: string
  unit: string
  value: number
  min: number
  max: number
  onChange: (v: number) => void
  error?: string
}

/** Slider + numeric input pair. The input accepts out-of-range values so the limit error can be explained. */
export function ResourceRow({ label, unit, value, min, max, onChange, error }: Props) {
  const id = useId()
  const clamped = Math.min(Math.max(Number.isFinite(value) ? value : min, min), max)
  return (
    <div>
      <div className="grid grid-cols-[72px_1fr_112px] items-center gap-4">
        <label htmlFor={id + '-n'} className="text-sm font-semibold">{label}</label>
        <input id={id + '-r'} type="range" aria-label={`${label} slider`} min={min} max={max} value={clamped}
          onChange={(e) => onChange(Number(e.target.value))} className="h-1.5 w-full cursor-pointer accent-blue" />
        <div className="relative">
          <input id={id + '-n'} type="number" inputMode="numeric" value={Number.isFinite(value) ? value : ''} aria-invalid={!!error}
            aria-describedby={error ? id + '-e' : undefined}
            onChange={(e) => onChange(e.target.value === '' ? NaN : Number(e.target.value))}
            className={`h-10 w-full rounded-[10px] border bg-field px-3 pr-12 font-mono text-[13px] ${error ? 'border-err-fg' : 'border-line-strong'}`} />
          <span className="pointer-events-none absolute inset-y-0 right-3 flex items-center font-mono text-[11px] text-faint">{unit}</span>
        </div>
      </div>
      {error && <p id={id + '-e'} role="alert" className="mt-1.5 pl-[88px] text-xs text-err-fg">{error}</p>}
    </div>
  )
}
