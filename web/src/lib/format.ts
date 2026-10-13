const rtf = new Intl.RelativeTimeFormat('en', { numeric: 'auto' })

/** "12 min ago", "yesterday", "3 days ago". */
export function relativeTime(iso: string, now = Date.now()): string {
  const s = Math.round((new Date(iso).getTime() - now) / 1000)
  const abs = Math.abs(s)
  if (abs < 45) return 'just now'
  if (abs < 3600) return rtf.format(Math.round(s / 60), 'minute')
  if (abs < 86400) return rtf.format(Math.round(s / 3600), 'hour')
  if (abs < 86400 * 30) return rtf.format(Math.round(s / 86400), 'day')
  return rtf.format(Math.round(s / (86400 * 30)), 'month')
}

export const osLabel = (os: string) => (os === 'ubuntu-24.04' ? 'Ubuntu 24.04 LTS' : os)
