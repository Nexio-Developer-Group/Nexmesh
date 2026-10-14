import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError } from '@/api/client'
import type { FileContent, FileEntry } from './hooks'

export type TabStatus = 'loading' | 'ready' | 'binary' | 'toobig' | 'error'

export interface Tab {
  path: string
  status: TabStatus
  /** Text currently in the editor. */
  content: string
  /** Text as last loaded from or saved to the workspace. */
  saved: string
  /** Modification time of the version `saved` came from; sent to detect conflicts. */
  modTime: string
  /** The file changed on disk while the tab has unsaved edits. */
  conflict: boolean
  saving: boolean
  error?: string
  /** Bumped whenever the editor should replace its text (reload from disk). */
  revision: number
  line?: number
}

export const isDirty = (t: Tab) => t.status === 'ready' && t.content !== t.saved

const enc = encodeURIComponent

/**
 * Open-file state for the editor: loading, dirty tracking, saving with
 * conflict detection, and following renames/deletes made in the tree.
 */
export function useOpenFiles(ws: string) {
  const [tabs, setTabs] = useState<Tab[]>([])
  const [active, setActive] = useState<string | null>(null)
  const tabsRef = useRef(tabs)
  tabsRef.current = tabs

  const patch = useCallback((path: string, p: Partial<Tab> | ((t: Tab) => Partial<Tab>)) =>
    setTabs((ts) => ts.map((t) => (t.path === path ? { ...t, ...(typeof p === 'function' ? p(t) : p) } : t))), [])

  const load = useCallback(async (path: string, revision = 0) => {
    try {
      const f = await api.get<FileContent>(`/api/workspaces/${ws}/files/content?path=${enc(path)}`)
      patch(path, (t) => ({
        status: f.binary ? 'binary' : f.tooBig ? 'toobig' : 'ready',
        content: f.content ?? '', saved: f.content ?? '', modTime: f.modTime, conflict: false, error: undefined, revision: Math.max(revision, t.revision + (revision ? 1 : 0)),
      }))
    } catch (e) {
      patch(path, { status: 'error', error: e instanceof ApiError ? e.message : 'Could not open this file.' })
    }
  }, [ws, patch])

  const open = useCallback((path: string, line?: number) => {
    setActive(path)
    if (tabsRef.current.some((t) => t.path === path)) {
      if (line) patch(path, { line })
      return
    }
    setTabs((ts) => [...ts, { path, status: 'loading', content: '', saved: '', modTime: '', conflict: false, saving: false, revision: 0, line }])
    void load(path)
  }, [load, patch])

  const close = useCallback((path: string) => {
    setTabs((ts) => {
      const i = ts.findIndex((t) => t.path === path)
      const next = ts.filter((t) => t.path !== path)
      setActive((a) => (a === path ? next[Math.min(i, next.length - 1)]?.path ?? null : a))
      return next
    })
  }, [])

  const edit = useCallback((path: string, content: string) => patch(path, { content }), [patch])

  /** Saves; with force it ignores a detected conflict and overwrites. */
  const save = useCallback(async (path: string, force = false) => {
    const t = tabsRef.current.find((x) => x.path === path)
    if (!t || t.status !== 'ready' || t.saving) return false
    patch(path, { saving: true, error: undefined })
    const body = { path, content: t.content, ...(force || !t.modTime ? {} : { expectedModTime: t.modTime }) }
    try {
      const r = await api.put<{ modTime: string }>(`/api/workspaces/${ws}/files/content`, body)
      patch(path, (cur) => ({ saving: false, saved: t.content, modTime: r.modTime, conflict: false, content: cur.content }))
      return true
    } catch (e) {
      if (e instanceof ApiError && e.code === 'conflict') patch(path, { saving: false, conflict: true })
      else patch(path, { saving: false, error: e instanceof ApiError ? e.message : 'Could not save.' })
      return false
    }
  }, [ws, patch])

  /** Discards local edits and re-reads the file from the workspace. */
  const reload = useCallback(async (path: string) => {
    const t = tabsRef.current.find((x) => x.path === path)
    await load(path, (t?.revision ?? 0) + 1)
  }, [load])

  /** Checks whether the file changed on disk (tab activation, window focus). */
  const checkDisk = useCallback(async (path: string) => {
    const t = tabsRef.current.find((x) => x.path === path)
    if (!t || t.status !== 'ready' || t.saving) return
    try {
      const st = await api.get<FileEntry>(`/api/workspaces/${ws}/files/stat?path=${enc(path)}`)
      if (st.modTime === t.modTime) return
      if (t.content === t.saved) await load(path, t.revision + 1) // clean tab: follow disk silently
      else patch(path, { conflict: true })
    } catch (e) {
      if (e instanceof ApiError && e.code === 'not_found') patch(path, { error: 'This file was deleted from the workspace.', conflict: false })
    }
  }, [ws, load, patch])

  /** Applies a rename/move/delete from the file tree to open tabs. */
  const pathChanged = useCallback((from: string, to: string | null) => {
    const under = (p: string) => p === from || p.startsWith(from + '/')
    setTabs((ts) => to === null ? ts.filter((t) => !under(t.path)) : ts.map((t) => (under(t.path) ? { ...t, path: to + t.path.slice(from.length) } : t)))
    setActive((a) => (a && under(a) ? (to === null ? null : to + a.slice(from.length)) : a))
  }, [])

  // Re-check the active file when the window regains focus.
  useEffect(() => {
    const onFocus = () => { if (active) void checkDisk(active) }
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [active, checkDisk])

  // Warn before leaving with unsaved edits.
  useEffect(() => {
    const onBefore = (e: BeforeUnloadEvent) => { if (tabsRef.current.some(isDirty)) { e.preventDefault(); e.returnValue = '' } }
    window.addEventListener('beforeunload', onBefore)
    return () => window.removeEventListener('beforeunload', onBefore)
  }, [])

  return { tabs, active, setActive, open, close, edit, save, reload, checkDisk, pathChanged }
}
