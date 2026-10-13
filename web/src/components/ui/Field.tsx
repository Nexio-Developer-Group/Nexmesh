import clsx from 'clsx'
import { useId } from 'react'
import type { InputHTMLAttributes, ReactNode } from 'react'

interface FieldProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'id'> {
  label: string
  hint?: ReactNode
  error?: string
  optional?: boolean
  mono?: boolean
  suffix?: ReactNode
}

/** Labelled input: every input has a programmatic label (WCAG). */
export function Field({ label, hint, error, optional, mono, suffix, className, ...rest }: FieldProps) {
  const id = useId()
  return (
    <div className={className}>
      <label htmlFor={id} className="mb-1.5 block text-[13px] font-semibold text-fg">
        {label} {optional && <span className="font-normal text-faint">optional</span>}
      </label>
      <div className="relative">
        <input
          id={id}
          aria-invalid={!!error}
          aria-describedby={error || hint ? id + '-d' : undefined}
          {...rest}
          className={clsx(
            'h-10 w-full rounded-[10px] border bg-field px-3 text-sm text-fg placeholder:text-faint',
            error ? 'border-err-fg' : 'border-line-strong focus:border-blue',
            mono && 'font-mono text-[13px]',
            suffix && 'pr-14',
            'disabled:opacity-60',
          )}
        />
        {suffix && <span className="pointer-events-none absolute inset-y-0 right-3 flex items-center font-mono text-[11px] text-faint">{suffix}</span>}
      </div>
      {(error || hint) && (
        <p id={id + '-d'} className={clsx('mt-1.5 text-xs', error ? 'text-err-fg' : 'text-muted')}>
          {error ?? hint}
        </p>
      )}
    </div>
  )
}
