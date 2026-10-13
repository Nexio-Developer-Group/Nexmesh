import { relativeTime, osLabel } from './format'

describe('format', () => {
  const now = Date.parse('2026-10-13T12:00:00Z')
  it('relativeTime', () => {
    expect(relativeTime('2026-10-13T11:59:50Z', now)).toBe('just now')
    expect(relativeTime('2026-10-13T11:48:00Z', now)).toBe('12 minutes ago')
    expect(relativeTime('2026-10-13T09:00:00Z', now)).toBe('3 hours ago')
    expect(relativeTime('2026-10-12T12:00:00Z', now)).toBe('yesterday')
  })
  it('osLabel', () => expect(osLabel('ubuntu-24.04')).toBe('Ubuntu 24.04 LTS'))
})
