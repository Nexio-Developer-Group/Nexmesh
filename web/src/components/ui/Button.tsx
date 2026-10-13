import clsx from 'clsx'
import type { ButtonHTMLAttributes } from 'react'

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger'

interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant
  size?: 'sm' | 'md' | 'lg'
  loading?: boolean
}

const variants: Record<Variant, string> = {
  primary: 'bg-nx-gradient text-white shadow-[0_6px_18px_-6px_rgba(22,118,254,0.7)] hover:brightness-110',
  secondary: 'bg-raised text-fg border border-line-strong hover:border-blue',
  ghost: 'bg-transparent text-muted hover:text-fg',
  danger: 'bg-err-bg text-err-fg border border-err-fg/40 hover:border-err-fg',
}
const sizes = { sm: 'h-8 px-3 text-xs', md: 'h-10 px-4 text-sm', lg: 'h-12 px-6 text-[15px]' }

export function Button({ variant = 'secondary', size = 'md', loading, className, children, disabled, ...rest }: Props) {
  return (
    <button
      {...rest}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      className={clsx(
        'inline-flex items-center justify-center gap-2 rounded-[10px] font-semibold transition-[filter,border-color,opacity] duration-150',
        'disabled:cursor-not-allowed disabled:opacity-45 disabled:shadow-none disabled:hover:brightness-100',
        variants[variant],
        sizes[size],
        className,
      )}
    >
      {loading && (
        <span aria-hidden className="size-3.5 rounded-full border-2 border-current border-t-transparent" style={{ animation: 'nx-spin 0.8s linear infinite' }} />
      )}
      {children}
    </button>
  )
}
