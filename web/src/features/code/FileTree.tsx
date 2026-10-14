import clsx from 'clsx'
import { useEffect, useMemo, useRef, useState } from 'react'
import { ApiError } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { Modal } from '@/components/ui/Modal'
import { baseOf, downloadUrl, joinPath, parentOf, useDir, useFileOps, useFileSearch, type FileEntry } from './hooks'

interface Props {
  workspaceId: string
  activePath: string | null
  onOpen: (path: string, line?: number) => void
  /** Called after a path was renamed/moved/deleted so open editors can follow. */
  onPathChanged: (from: string, to: string | null) => void
}

type Dialog =
  | { kind: 'new-file' | 'new-folder'; dir: string }
  | { kind: 'rename'; entry: FileEntry }
  | { kind: 'delete'; entry: FileEntry }
  | null

export function FileTree({ workspaceId, activePath, onOpen, onPathChanged }: Props) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set(['']))
  const [selected, setSelected] = useState<FileEntry | null>(null)
  const [dialog, setDialog] = useState<Dialog>(null)
  const [query, setQuery] = useState('')
  const [content, setContent] = useState(true)
  const [dragOver, setDragOver] = useState<string | null>(null)
  const fileInput = useRef<HTMLInputElement>(null)
  const ops = useFileOps(workspaceId)
  const root = useDir(workspaceId, '')
  const search = useFileSearch(workspaceId, query, content)
  const searching = query.trim().length >= 2

  const toggle = (p: string) => setExpanded((s) => { const n = new Set(s); if (n.has(p)) n.delete(p); else n.add(p); return n })
  const targetDir = selected ? (selected.type === 'dir' ? selected.path : parentOf(selected.path)) : ''

  const moveInto = (e: FileEntry, dir: string) => {
    const to = joinPath(dir, e.name)
    if (to === e.path || dir === e.path || dir.startsWith(e.path + '/')) return
    ops.rename.mutate({ from: e.path, to }, { onSuccess: () => { onPathChanged(e.path, to); setExpanded((s) => new Set(s).add(dir)) } })
  }

  const onUpload = (files: FileList | null) => {
    if (!files) return
    for (const file of Array.from(files)) ops.upload.mutate({ path: joinPath(targetDir, file.name), file })
    if (fileInput.current) fileInput.current.value = ''
  }

  const err = [ops.mkdir, ops.create, ops.rename, ops.remove, ops.upload].map((m) => m.error).find(Boolean)

  if (root.error instanceof ApiError) {
    return <p role="alert" className="p-4 text-sm text-muted">{root.error.message}</p>
  }

  return (
    <div className="flex h-full min-h-0 flex-col" aria-label="Files">
      <div className="flex items-center gap-1 border-b border-line px-2 py-2">
        <IconBtn label="New file" onClick={() => setDialog({ kind: 'new-file', dir: targetDir })}>+ File</IconBtn>
        <IconBtn label="New folder" onClick={() => setDialog({ kind: 'new-folder', dir: targetDir })}>+ Folder</IconBtn>
        <IconBtn label="Upload files" onClick={() => fileInput.current?.click()}>Upload</IconBtn>
        <input ref={fileInput} type="file" multiple hidden aria-label="Upload files input" onChange={(e) => onUpload(e.target.files)} />
        <IconBtn label="Refresh" onClick={() => { root.refetch() }}>↻</IconBtn>
      </div>
      <div className="border-b border-line px-2 py-2">
        <input
          aria-label="Search files"
          placeholder="Search files"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          className="h-8 w-full rounded-lg border border-line-strong bg-field px-2.5 text-[13px] placeholder:text-faint"
        />
        {query && (
          <label className="mt-1.5 flex items-center gap-1.5 text-xs text-muted">
            <input type="checkbox" checked={content} onChange={(e) => setContent(e.target.checked)} /> Search inside files
          </label>
        )}
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto py-1">
        {searching ? (
          <SearchResults loading={search.isFetching} data={search.data} error={search.error} onOpen={onOpen} />
        ) : root.isPending ? (
          <p className="p-4 text-sm text-muted">Loading…</p>
        ) : (
          <ul role="tree" aria-label="Workspace files" onKeyDown={(e) => treeKeys(e)}>
            <Level workspaceId={workspaceId} dir="" depth={0} expanded={expanded} toggle={toggle} selected={selected} setSelected={setSelected}
              activePath={activePath} onOpen={onOpen} setDialog={setDialog} dragOver={dragOver} setDragOver={setDragOver} moveInto={moveInto} />
          </ul>
        )}
        {!searching && root.data?.length === 0 && <p className="p-4 text-sm text-muted">This workspace is empty. Create a file or upload one.</p>}
      </div>
      {err && <p role="alert" className="border-t border-line bg-err-bg px-3 py-2 text-xs text-err-fg">{err.message}</p>}

      {dialog && (dialog.kind === 'new-file' || dialog.kind === 'new-folder') && (
        <NameDialog title={dialog.kind === 'new-file' ? 'New file' : 'New folder'} label="Name" initial="" hint={`In /${dialog.dir}`}
          confirm="Create" onClose={() => setDialog(null)}
          onSubmit={(name) => {
            const path = joinPath(dialog.dir, name)
            const m = dialog.kind === 'new-file' ? ops.create : ops.mkdir
            m.mutate(path, { onSuccess: () => { setDialog(null); setExpanded((s) => new Set(s).add(dialog.dir)); if (dialog.kind === 'new-file') onOpen(path) } })
          }} error={ops.create.error ?? ops.mkdir.error} />
      )}
      {dialog?.kind === 'rename' && (
        <NameDialog title={`Rename or move ${dialog.entry.name}`} label="Path" initial={dialog.entry.path} hint="Change the folder part to move it."
          confirm="Rename" onClose={() => setDialog(null)} error={ops.rename.error}
          onSubmit={(to) => ops.rename.mutate({ from: dialog.entry.path, to: to.replace(/^\/+/, '') }, { onSuccess: () => { onPathChanged(dialog.entry.path, to.replace(/^\/+/, '')); setDialog(null) } })} />
      )}
      {dialog?.kind === 'delete' && (
        <Modal title={`Delete ${dialog.entry.name}?`} onClose={() => setDialog(null)}
          footer={<><Button onClick={() => setDialog(null)}>Cancel</Button>
            <Button variant="danger" loading={ops.remove.isPending}
              onClick={() => ops.remove.mutate({ path: dialog.entry.path, recursive: dialog.entry.type === 'dir' }, { onSuccess: () => { onPathChanged(dialog.entry.path, null); setSelected(null); setDialog(null) } })}>
              Delete {dialog.entry.type === 'dir' ? 'folder' : 'file'}
            </Button></>}>
          {dialog.entry.type === 'dir' ? 'This folder and everything inside it will be deleted from the workspace.' : 'This file will be deleted from the workspace.'} This cannot be undone.
        </Modal>
      )}
    </div>
  )
}

function IconBtn({ label, onClick, children }: { label: string; onClick: () => void; children: React.ReactNode }) {
  return <button type="button" aria-label={label} title={label} onClick={onClick} className="rounded-md px-2 py-1 text-xs font-medium text-muted hover:bg-raised hover:text-fg">{children}</button>
}

/** Arrow-key navigation across visible treeitems. */
function treeKeys(e: React.KeyboardEvent) {
  if (!['ArrowDown', 'ArrowUp'].includes(e.key)) return
  const items = Array.from((e.currentTarget as HTMLElement).querySelectorAll<HTMLElement>('[role="treeitem"] > [data-row]'))
  const i = items.indexOf(document.activeElement as HTMLElement)
  if (i < 0) return
  e.preventDefault()
  items[Math.max(0, Math.min(items.length - 1, i + (e.key === 'ArrowDown' ? 1 : -1)))]?.focus()
}

interface LevelProps {
  workspaceId: string
  dir: string
  depth: number
  expanded: Set<string>
  toggle: (p: string) => void
  selected: FileEntry | null
  setSelected: (e: FileEntry) => void
  activePath: string | null
  onOpen: (p: string) => void
  setDialog: (d: Dialog) => void
  dragOver: string | null
  setDragOver: (p: string | null) => void
  moveInto: (e: FileEntry, dir: string) => void
}

function Level(p: LevelProps) {
  const { data, isPending, error } = useDir(p.workspaceId, p.dir)
  if (isPending) return <li role="none" className="py-1 text-xs text-faint" style={{ paddingLeft: 12 + p.depth * 14 }}>Loading…</li>
  if (error) return <li role="none" className="py-1 text-xs text-err-fg" style={{ paddingLeft: 12 + p.depth * 14 }}>{error.message}</li>
  return <>{data!.map((e) => <Row key={e.path} entry={e} {...p} />)}</>
}

function Row({ entry: e, ...p }: LevelProps & { entry: FileEntry }) {
  const isDir = e.type === 'dir'
  const open = isDir && p.expanded.has(e.path)
  const dragging = useRef(false)
  const [menu, setMenu] = useState(false)
  useEffect(() => {
    if (!menu) return
    const close = () => setMenu(false)
    document.addEventListener('click', close)
    return () => document.removeEventListener('click', close)
  }, [menu])

  const activate = () => { p.setSelected(e); if (isDir) p.toggle(e.path); else p.onOpen(e.path) }
  return (
    <li role="treeitem" aria-expanded={isDir ? open : undefined} aria-selected={p.activePath === e.path}>
      <div
        data-row
        tabIndex={0}
        draggable
        onDragStart={(ev) => { dragging.current = true; ev.dataTransfer.setData('application/x-nexmesh-path', e.path); ev.dataTransfer.effectAllowed = 'move' }}
        onDragEnd={() => { dragging.current = false; p.setDragOver(null) }}
        onDragOver={(ev) => { if (isDir) { ev.preventDefault(); p.setDragOver(e.path) } }}
        onDrop={(ev) => { ev.preventDefault(); p.setDragOver(null); const from = ev.dataTransfer.getData('application/x-nexmesh-path'); if (from && isDir) p.moveInto({ ...e, name: baseOf(from), path: from } as FileEntry, e.path) }}
        onClick={activate}
        onKeyDown={(ev) => {
          if (ev.key === 'Enter') activate()
          else if (ev.key === 'ArrowRight' && isDir && !open) p.toggle(e.path)
          else if (ev.key === 'ArrowLeft' && isDir && open) p.toggle(e.path)
          else if (ev.key === 'F2') p.setDialog({ kind: 'rename', entry: e })
          else if (ev.key === 'Delete') p.setDialog({ kind: 'delete', entry: e })
        }}
        style={{ paddingLeft: 8 + p.depth * 14 }}
        className={clsx('group flex h-7 cursor-pointer items-center gap-1.5 pr-1 text-[13px] outline-none focus-visible:ring-1 focus-visible:ring-blue',
          p.activePath === e.path ? 'bg-raised text-fg' : 'text-muted hover:bg-raised/60 hover:text-fg',
          p.selected?.path === e.path && 'font-semibold', p.dragOver === e.path && 'bg-blue/20')}
      >
        <span aria-hidden className="w-3 text-center text-[10px] text-faint">{isDir ? (open ? '▾' : '▸') : ''}</span>
        <span aria-hidden className="font-mono text-[10px] text-faint">{isDir ? '▤' : '▢'}</span>
        <span className="truncate">{e.name}</span>
        {e.type === 'symlink' && <span className="ml-1 text-[10px] text-faint">link</span>}
        <span className="relative ml-auto">
          <button type="button" aria-label={`Actions for ${e.name}`} aria-haspopup="menu" aria-expanded={menu}
            onClick={(ev) => { ev.stopPropagation(); p.setSelected(e); setMenu((m) => !m) }}
            className="rounded px-1.5 text-muted opacity-0 hover:bg-line group-focus-within:opacity-100 group-hover:opacity-100">⋯</button>
          {menu && (
            <div role="menu" className="absolute right-0 top-6 z-20 w-40 rounded-lg border border-line bg-panel py-1 text-[13px] shadow-xl">
              {isDir && <MenuItem onClick={() => p.setDialog({ kind: 'new-file', dir: e.path })}>New file here</MenuItem>}
              {isDir && <MenuItem onClick={() => p.setDialog({ kind: 'new-folder', dir: e.path })}>New folder here</MenuItem>}
              <MenuItem onClick={() => p.setDialog({ kind: 'rename', entry: e })}>Rename / move</MenuItem>
              {!isDir && <a role="menuitem" href={downloadUrl(p.workspaceId, e.path)} download={e.name} className="block px-3 py-1.5 hover:bg-raised">Download</a>}
              <MenuItem danger onClick={() => p.setDialog({ kind: 'delete', entry: e })}>Delete</MenuItem>
            </div>
          )}
        </span>
      </div>
      {isDir && open && (
        <ul role="group"><Level {...p} dir={e.path} depth={p.depth + 1} /></ul>
      )}
    </li>
  )
}

function MenuItem({ children, onClick, danger }: { children: React.ReactNode; onClick: () => void; danger?: boolean }) {
  return <button role="menuitem" type="button" onClick={onClick} className={clsx('block w-full px-3 py-1.5 text-left hover:bg-raised', danger && 'text-err-fg')}>{children}</button>
}

function SearchResults({ loading, data, error, onOpen }: { loading: boolean; data?: { hits: { path: string; line?: number; text?: string }[]; truncated: boolean }; error: Error | null; onOpen: (p: string, line?: number) => void }) {
  if (error) return <p role="alert" className="p-3 text-sm text-err-fg">{error.message}</p>
  if (!data) return <p className="p-3 text-sm text-muted">Searching…</p>
  if (data.hits.length === 0) return <p className="p-3 text-sm text-muted">{loading ? 'Searching…' : 'No matches.'}</p>
  return (
    <ul aria-label="Search results" className="text-[13px]">
      {data.hits.map((h, i) => (
        <li key={h.path + ':' + (h.line ?? 0) + ':' + i}>
          <button type="button" onClick={() => onOpen(h.path, h.line)} className="block w-full px-3 py-1.5 text-left hover:bg-raised">
            <span className="block truncate font-medium">{h.path}{h.line ? <span className="font-mono text-faint">:{h.line}</span> : null}</span>
            {h.text && <span className="block truncate font-mono text-[11.5px] text-muted">{h.text.trim()}</span>}
          </button>
        </li>
      ))}
      {data.truncated && <li className="px-3 py-2 text-xs text-faint">Showing the first matches. Narrow your search.</li>}
    </ul>
  )
}

function NameDialog({ title, label, initial, hint, confirm, onClose, onSubmit, error }: {
  title: string; label: string; initial: string; hint?: string; confirm: string; onClose: () => void; onSubmit: (v: string) => void; error: Error | null
}) {
  const [v, setV] = useState(initial)
  const err = error instanceof ApiError ? error.message : error?.message
  const valid = useMemo(() => v.trim() !== '' && v !== initial, [v, initial])
  return (
    <Modal title={title} onClose={onClose}
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" type="submit" form="name-form" disabled={!valid}>{confirm}</Button></>}>
      <form id="name-form" onSubmit={(e) => { e.preventDefault(); if (valid) onSubmit(v.trim()) }}>
        <Field label={label} mono value={v} onChange={(e) => setV(e.target.value)} hint={hint} error={err} autoComplete="off" />
      </form>
    </Modal>
  )
}
