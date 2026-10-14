import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TemplatesDialog } from './TemplatesDialog'
import { mockApi, renderPage } from '@/test/utils'
import type { CatalogComponent } from '@/api/types'

const def = (id: string, name: string, monogram: string) => ({ id, name, monogram }) as unknown as CatalogComponent
const defs = new Map([['react', def('react', 'React', 'Re')], ['nestjs', def('nestjs', 'NestJS', 'Ne')], ['redis', def('redis', 'Redis', 'Rd')]])

const gallery = {
  curated: [{ id: 'react-nest', name: 'React and NestJS', description: 'A web app and its API.', personal: false, components: [{ id: 'react', x: 0, y: 0 }, { id: 'nestjs', x: 1, y: 0 }] }],
  personal: [{ id: 'tpl_1', name: 'My cache stack', description: '', personal: true, components: [{ id: 'redis', x: 0, y: 0, suggestedMode: 'install' }] }],
}

function setup(extra: Record<string, (u: string, i?: RequestInit) => unknown> = {}, hasCards = true) {
  const onClose = vi.fn()
  const calls = mockApi({ 'GET /api/templates': () => gallery, ...extra })
  renderPage(<TemplatesDialog workspaceId="ws1" defs={defs} canvasHasCards={hasCards} onClose={onClose} />)
  return { calls, onClose }
}

describe('TemplatesDialog', () => {
  it('lists starter stacks with their components and explains drafts', async () => {
    setup()
    const card = (await screen.findByRole('button', { name: /React and NestJS/ }))
    expect(within(card).getByText('React')).toBeInTheDocument()
    expect(within(card).getByText('NestJS')).toBeInTheDocument()
    expect(screen.queryByText(/Cards are added as/)).toBeNull()
    await userEvent.click(card)
    expect(screen.getByText(/Cards are added as/)).toHaveTextContent('Nothing installs automatically')
    expect(screen.getByRole('button', { name: 'Add 2 cards to canvas' })).toBeInTheDocument()
  })

  it('applies the chosen template and reports what happened without installing', async () => {
    const { calls } = setup({ 'POST /api/workspaces/ws1/templates/react-nest/apply': () => ({ status: 201, body: { added: [{ id: 'c1' }], skipped: ['react'] } }) })
    await userEvent.click(await screen.findByRole('button', { name: /React and NestJS/ }))
    await userEvent.click(screen.getByRole('button', { name: 'Add 2 cards to canvas' }))
    expect(await screen.findByRole('status')).toHaveTextContent('Added 1 card as drafts')
    expect(screen.getByText(/Nothing has been installed/)).toBeInTheDocument()
    expect(screen.getByText(/not added again: React/)).toBeInTheDocument()
    expect(calls.filter((c) => c.method === 'POST')).toHaveLength(1)
    expect(calls.some((c) => c.url.includes('/install'))).toBe(false)
  })

  it('saves the canvas as a personal template, shows it, and deletes it', async () => {
    const { calls } = setup({
      'POST /api/workspaces/ws1/templates': () => ({ status: 201, body: gallery.personal[0] }),
      'DELETE /api/templates/': () => ({ status: 204 }),
    })
    await userEvent.click(await screen.findByRole('button', { name: /Save current canvas/ }))
    const save = screen.getByRole('button', { name: 'Save template' })
    expect(save).toBeDisabled()
    expect(screen.getByText(/never saved/)).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('Template name'), 'My cache stack')
    await userEvent.click(save)
    await waitFor(() => expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ name: 'My cache stack', description: '' }))
    expect(await screen.findByRole('tab', { name: 'My templates', selected: true })).toBeInTheDocument()
    expect(await screen.findByRole('button', { name: /^My cache stack/ })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Delete template My cache stack' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'DELETE')?.url).toBe('/api/templates/tpl_1'))
  })

  it('cannot save an empty canvas and shows server validation', async () => {
    setup({}, false)
    expect(await screen.findByRole('button', { name: /Save current canvas/ })).toBeDisabled()
    expect(screen.getByText(/Add components to the canvas first/)).toBeInTheDocument()
  })
})
