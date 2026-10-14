import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'

export interface FileEntry {
  name: string
  path: string
  type: 'file' | 'dir' | 'symlink'
  size: number
  modTime: string
  mode: string
}

export interface FileContent {
  path: string
  content?: string
  size: number
  modTime: string
  binary: boolean
  tooBig: boolean
}

export interface SearchHit { path: string; line?: number; text?: string }

export const fileKeys = {
  dir: (ws: string, path: string) => ['files', ws, 'dir', path] as const,
  file: (ws: string, path: string) => ['files', ws, 'file', path] as const,
  all: (ws: string) => ['files', ws] as const,
}

const q = (p: string) => encodeURIComponent(p)

export const useDir = (ws: string, path: string, enabled = true) =>
  useQuery({
    queryKey: fileKeys.dir(ws, path),
    queryFn: async () => (await api.get<{ entries: FileEntry[] }>(`/api/workspaces/${ws}/files?path=${q(path)}`)).entries,
    enabled,
    retry: false,
  })

export const useFileSearch = (ws: string, query: string, content: boolean) =>
  useQuery({
    queryKey: ['files', ws, 'search', query, content],
    queryFn: () => api.get<{ hits: SearchHit[]; truncated: boolean }>(`/api/workspaces/${ws}/files/search?q=${q(query)}&content=${content}`),
    enabled: query.trim().length >= 2,
    retry: false,
  })

export function useFileOps(ws: string) {
  const qc = useQueryClient()
  const done = () => qc.invalidateQueries({ queryKey: fileKeys.all(ws) })
  return {
    mkdir: useMutation({ mutationFn: (path: string) => api.post<void>(`/api/workspaces/${ws}/files/dir`, { path }), onSuccess: done }),
    create: useMutation({ mutationFn: (path: string) => api.put(`/api/workspaces/${ws}/files/content`, { path, content: '' }), onSuccess: done }),
    rename: useMutation({ mutationFn: (v: { from: string; to: string }) => api.post<void>(`/api/workspaces/${ws}/files/rename`, v), onSuccess: done }),
    remove: useMutation({ mutationFn: (v: { path: string; recursive: boolean }) => api.del<void>(`/api/workspaces/${ws}/files?path=${q(v.path)}&recursive=${v.recursive}`), onSuccess: done }),
    upload: useMutation({
      mutationFn: async (v: { path: string; file: File }) => {
        const buf = new Uint8Array(await v.file.arrayBuffer())
        let bin = ''
        for (let i = 0; i < buf.length; i += 0x8000) bin += String.fromCharCode(...buf.subarray(i, i + 0x8000))
        return api.put(`/api/workspaces/${ws}/files/content`, { path: v.path, content: btoa(bin), encoding: 'base64' })
      },
      onSuccess: done,
    }),
  }
}

export const downloadUrl = (ws: string, path: string) => `/api/workspaces/${ws}/files/download?path=${q(path)}`

export const parentOf = (p: string) => (p.includes('/') ? p.slice(0, p.lastIndexOf('/')) : '')
export const baseOf = (p: string) => p.slice(p.lastIndexOf('/') + 1)
export const joinPath = (dir: string, name: string) => (dir ? `${dir}/${name}` : name)
