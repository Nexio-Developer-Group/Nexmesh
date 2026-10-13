import { render, screen } from '@testing-library/react'
import { StatusBadge, ModeBadge } from './StatusBadge'
import { Button } from './Button'
import { Field } from './Field'

describe('ui primitives', () => {
  it('status badges always carry a text label', () => {
    for (const s of ['running', 'failed', 'degraded', 'installing', 'test_passed'] as const) {
      const { unmount } = render(<StatusBadge state={s} />)
      expect(screen.getByText(/\w+/)).toBeInTheDocument()
      unmount()
    }
    render(<StatusBadge state="degraded" />)
    expect(screen.getByText('Degraded')).toBeInTheDocument()
  })

  it('mode badge shows Installed or Connected', () => {
    render(<ModeBadge mode="connected" />)
    expect(screen.getByText('Connected')).toBeInTheDocument()
  })

  it('button is disabled while loading', () => {
    render(<Button loading>Save</Button>)
    expect(screen.getByRole('button')).toBeDisabled()
  })

  it('field label is associated with its input', () => {
    render(<Field label="Workspace name" error="Taken" />)
    expect(screen.getByLabelText('Workspace name')).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByText('Taken')).toBeInTheDocument()
  })
})
