import { useState } from 'react'
import type { Workspace } from '@/api/types'
import { useDeleteWorkspace } from '@/api/hooks'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { Modal } from '@/components/ui/Modal'

/** Deleting requires typing the workspace name (PRD design prompt P2). */
export function DeleteWorkspaceDialog({ workspace, onClose }: { workspace: Workspace; onClose: () => void }) {
  const [typed, setTyped] = useState('')
  const del = useDeleteWorkspace()
  const matches = typed === workspace.name
  return (
    <Modal
      title={`Delete ${workspace.name}?`}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="danger" disabled={!matches} loading={del.isPending}
            onClick={() => del.mutate({ id: workspace.id, confirmName: typed }, { onSuccess: onClose })}>
            Delete workspace
          </Button>
        </>
      }
    >
      <p>This permanently removes the workspace, its installed components and their data. Connected external services are not touched.</p>
      <Field className="mt-4" label={`Type ${workspace.name} to confirm`} value={typed} onChange={(e) => setTyped(e.target.value)} mono autoComplete="off" error={del.error?.message} />
    </Modal>
  )
}
