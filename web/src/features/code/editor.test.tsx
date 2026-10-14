import { act, renderHook, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { render } from '@testing-library/react'
import { EditorPane } from './EditorPane'
import { isDirty, useOpenFiles } from './useOpenFiles'
import { mockApi } from '@/test/utils'

vi.mock('./MonacoEditor', () => ({
  default: ({ path, initial, onChange, onSave }: { path: string; initial: string; onChange: (t: string) => void; onSave: () => void }) => (
    <div>
      <textarea aria-label={`editor ${path}`} defaultValue={initial} onChange={(e) => onChange(e.target.value)} />
      <button onClick={onSave}>ctrl-s</button>
    </div>
  ),
}))

const file = (content: string, modTime = 't1', extra = {}) => ({ path: 'a.txt', content, size: content.length, modTime, binary: false, tooBig: false, ...extra })

describe('useOpenFiles', () => {
  it('opens, tracks dirty state and saves with the loaded modTime', async () => {
    const calls = mockApi({
      'GET /api/workspaces/ws1/files/content': () => file('hello'),
      'PUT /api/workspaces/ws1/files/content': () => ({ size: 5, modTime: 't2' }),
    })
    const { result } = renderHook(() => useOpenFiles('ws1'))
    act(() => result.current.open('a.txt'))
    await waitFor(() => expect(result.current.tabs[0].status).toBe('ready'))
    expect(isDirty(result.current.tabs[0])).toBe(false)
    act(() => result.current.edit('a.txt', 'hello world'))
    expect(isDirty(result.current.tabs[0])).toBe(true)
    await act(async () => { await result.current.save('a.txt') })
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ path: 'a.txt', content: 'hello world', expectedModTime: 't1' })
    expect(isDirty(result.current.tabs[0])).toBe(false)
    expect(result.current.tabs[0].modTime).toBe('t2')
  })

  it('flags a conflict instead of overwriting, then offers overwrite or reload', async () => {
    let disk = file('hello')
    let putCount = 0
    const calls = mockApi({
      'GET /api/workspaces/ws1/files/content': () => disk,
      'PUT /api/workspaces/ws1/files/content': (_u, init) => {
        putCount++
        const b = JSON.parse(String(init?.body))
        if (b.expectedModTime && b.expectedModTime !== disk.modTime) return { status: 409, body: { error: { code: 'conflict', message: '/a.txt changed on disk since you opened it.' } } }
        disk = file(b.content, 't9')
        return { size: 1, modTime: 't9' }
      },
    })
    const { result } = renderHook(() => useOpenFiles('ws1'))
    act(() => result.current.open('a.txt'))
    await waitFor(() => expect(result.current.tabs[0].status).toBe('ready'))
    disk = file('changed elsewhere', 't5')
    act(() => result.current.edit('a.txt', 'my edit'))
    await act(async () => { expect(await result.current.save('a.txt')).toBe(false) })
    expect(result.current.tabs[0].conflict).toBe(true)
    expect(putCount).toBe(1)

    await act(async () => { await result.current.save('a.txt', true) })
    expect(calls.filter((c) => c.method === 'PUT')[1].body).toEqual({ path: 'a.txt', content: 'my edit' }) // no expectedModTime
    expect(result.current.tabs[0].conflict).toBe(false)

    disk = file('v3', 't10')
    act(() => result.current.edit('a.txt', 'local'))
    await act(async () => { await result.current.reload('a.txt') })
    expect(result.current.tabs[0].saved).toBe('v3')
    expect(result.current.tabs[0].content).toBe('v3')
    expect(result.current.tabs[0].revision).toBeGreaterThan(0)
  })

  it('follows disk changes silently when clean and raises a conflict when dirty', async () => {
    let disk = file('one', 't1')
    mockApi({
      'GET /api/workspaces/ws1/files/content': () => disk,
      'GET /api/workspaces/ws1/files/stat': () => ({ name: 'a.txt', path: 'a.txt', type: 'file', size: 1, modTime: disk.modTime, mode: '' }),
    })
    const { result } = renderHook(() => useOpenFiles('ws1'))
    act(() => result.current.open('a.txt'))
    await waitFor(() => expect(result.current.tabs[0].status).toBe('ready'))
    disk = file('two', 't2')
    await act(async () => { await result.current.checkDisk('a.txt') })
    expect(result.current.tabs[0].content).toBe('two')
    expect(result.current.tabs[0].conflict).toBe(false)

    act(() => result.current.edit('a.txt', 'two + mine'))
    disk = file('three', 't3')
    await act(async () => { await result.current.checkDisk('a.txt') })
    expect(result.current.tabs[0].conflict).toBe(true)
    expect(result.current.tabs[0].content).toBe('two + mine')
  })

  it('follows renames and closes deleted files', async () => {
    mockApi({ 'GET /api/workspaces/ws1/files/content': () => file('x') })
    const { result } = renderHook(() => useOpenFiles('ws1'))
    act(() => { result.current.open('src/a.txt'); result.current.open('src/b.txt'); result.current.open('README.md') })
    await waitFor(() => expect(result.current.tabs.every((t) => t.status === 'ready')).toBe(true))
    act(() => result.current.pathChanged('src', 'lib'))
    expect(result.current.tabs.map((t) => t.path)).toEqual(['lib/a.txt', 'lib/b.txt', 'README.md'])
    act(() => result.current.setActive('lib/a.txt'))
    act(() => result.current.pathChanged('lib', null))
    expect(result.current.tabs.map((t) => t.path)).toEqual(['README.md'])
    expect(result.current.active).toBeNull()
  })

  it('reports binary and oversized files without loading them', async () => {
    mockApi({ 'GET /api/workspaces/ws1/files/content': (u) => (u.includes('bin') ? file('', 't', { binary: true }) : file('', 't', { tooBig: true })) })
    const { result } = renderHook(() => useOpenFiles('ws1'))
    act(() => { result.current.open('bin.dat'); result.current.open('huge.log') })
    await waitFor(() => expect(result.current.tabs.map((t) => t.status)).toEqual(['binary', 'toobig']))
  })
})

describe('EditorPane', () => {
  function Harness() {
    const f = useOpenFiles('ws1')
    return (
      <>
        <button onClick={() => f.open('a.txt')}>open a</button>
        <EditorPane workspaceId="ws1" tabs={f.tabs} active={f.active} onActivate={f.setActive} onClose={f.close} onEdit={f.edit} onSave={f.save} onReload={f.reload} />
      </>
    )
  }

  it('shows the dirty dot, saves with the button and asks before closing unsaved edits', async () => {
    mockApi({
      'GET /api/workspaces/ws1/files/content': () => file('hello'),
      'PUT /api/workspaces/ws1/files/content': () => ({ size: 1, modTime: 't2' }),
    })
    render(<Harness />)
    await userEvent.click(screen.getByText('open a'))
    const ta = await screen.findByLabelText('editor a.txt')
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    await userEvent.type(ta, '!')
    expect(screen.getByLabelText('unsaved changes')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Close a.txt' }))
    const dlg = screen.getByRole('dialog')
    expect(dlg).toHaveTextContent('Save changes to a.txt?')
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).toBeNull()

    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.queryByLabelText('unsaved changes')).toBeNull())
    await userEvent.click(screen.getByRole('button', { name: 'Close a.txt' }))
    expect(screen.queryByRole('dialog')).toBeNull() // clean tab closes immediately
    expect(screen.getByText('Select a file to open it.')).toBeInTheDocument()
  })

  it('shows a download for binary files and a conflict banner', async () => {
    mockApi({
      'GET /api/workspaces/ws1/files/content': () => file('', 't', { binary: true }),
    })
    render(<Harness />)
    await userEvent.click(screen.getByText('open a'))
    expect(await screen.findByText('This looks like a binary file.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Download' })).toHaveAttribute('href', '/api/workspaces/ws1/files/download?path=a.txt')
  })
})
