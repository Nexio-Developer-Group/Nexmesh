import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'

export interface FileChange {
  path: string; origPath?: string; index: string; worktree: string
  staged: boolean; unstaged: boolean; untracked: boolean; conflicted: boolean
}
export interface GitStatus {
  repo: string; branch: string; detached: boolean; upstream?: string; ahead: number; behind: number; files: FileChange[]; clean: boolean
}
export interface Branch { name: string; current: boolean; remote: boolean; upstream?: string }
export interface Commit { hash: string; short: string; author: string; date: string; subject: string }
export interface GitCredential { id: string; label: string; provider: string; host: string; username: string; createdAt: string; updatedAt: string }

const k = (ws: string, ...rest: unknown[]) => ['git', ws, ...rest] as const
const q = encodeURIComponent

export const useRepos = (ws: string) =>
  useQuery({ queryKey: k(ws, 'repos'), queryFn: async () => (await api.get<{ repos: string[] }>(`/api/workspaces/${ws}/git/repos`)).repos, retry: false })

export const useGitStatus = (ws: string, repo: string | null) =>
  useQuery({ queryKey: k(ws, 'status', repo), queryFn: () => api.get<GitStatus>(`/api/workspaces/${ws}/git/status?repo=${q(repo!)}`), enabled: repo !== null, retry: false, refetchInterval: 8000 })

export const useBranches = (ws: string, repo: string | null, enabled: boolean) =>
  useQuery({ queryKey: k(ws, 'branches', repo), queryFn: async () => (await api.get<{ branches: Branch[] }>(`/api/workspaces/${ws}/git/branches?repo=${q(repo!)}`)).branches, enabled: enabled && repo !== null, retry: false })

export const useGitLog = (ws: string, repo: string | null, enabled: boolean) =>
  useQuery({ queryKey: k(ws, 'log', repo), queryFn: async () => (await api.get<{ commits: Commit[] }>(`/api/workspaces/${ws}/git/log?repo=${q(repo!)}`)).commits, enabled: enabled && repo !== null, retry: false })

export const useDiff = (ws: string, repo: string, path: string, staged: boolean, enabled: boolean) =>
  useQuery({
    queryKey: k(ws, 'diff', repo, path, staged),
    queryFn: () => api.get<{ diff: string; truncated: boolean }>(`/api/workspaces/${ws}/git/diff?repo=${q(repo)}&path=${q(path)}&staged=${staged}`),
    enabled, retry: false,
  })

export function useGitActions(ws: string) {
  const qc = useQueryClient()
  const refresh = () => qc.invalidateQueries({ queryKey: k(ws) })
  const post = <T = void>(path: string) => useMutation({ mutationFn: (b: object) => api.post<T>(`/api/workspaces/${ws}/git/${path}`, b), onSuccess: refresh })
  /* eslint-disable react-hooks/rules-of-hooks */
  return {
    clone: post<{ repo: string }>('clone'),
    stage: post('stage'),
    discard: post('discard'),
    commit: post('commit'),
    switchBranch: post('switch'),
    fetch: post<{ output: string }>('fetch'),
    pull: post<{ output: string }>('pull'),
    push: post<{ output: string }>('push'),
    prUrl: useMutation({ mutationFn: (repo: string) => api.get<{ url: string }>(`/api/workspaces/${ws}/git/pr-url?repo=${q(repo)}`) }),
  }
}

export const useCredentials = (ws: string) =>
  useQuery({ queryKey: ['git-credentials', ws], queryFn: async () => (await api.get<{ credentials: GitCredential[] }>(`/api/workspaces/${ws}/git/credentials`)).credentials })

export function useCredentialActions(ws: string) {
  const qc = useQueryClient()
  const refresh = () => qc.invalidateQueries({ queryKey: ['git-credentials', ws] })
  return {
    add: useMutation({ mutationFn: (b: { provider: string; host?: string; username?: string; token: string; label?: string }) => api.post<GitCredential>(`/api/workspaces/${ws}/git/credentials`, b), onSuccess: refresh }),
    update: useMutation({ mutationFn: (v: { id: string; token?: string; label?: string }) => { const { id, ...b } = v; return api.put<GitCredential>(`/api/workspaces/${ws}/git/credentials/${id}`, b) }, onSuccess: refresh }),
    revoke: useMutation({ mutationFn: (id: string) => api.del<void>(`/api/workspaces/${ws}/git/credentials/${id}`), onSuccess: refresh }),
  }
}
