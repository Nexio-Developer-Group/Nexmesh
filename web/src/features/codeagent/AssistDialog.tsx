import { useEffect } from 'react'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
import { useAssistPreview, useAssistSend } from './hooks'

/**
 * Explain or Diagnose one card (PRD F19). The text shown here is exactly what
 * is sent, already scrubbed of credential-shaped values; nothing goes to the
 * model until the user confirms.
 */
export function AssistDialog({ workspaceId, componentId, cardName, kind, onClose, onSent }: {
  workspaceId: string; componentId: string; cardName: string; kind: 'explain' | 'diagnose'; onClose: () => void; onSent: (sessionId: string) => void
}) {
  const preview = useAssistPreview(workspaceId, componentId)
  const send = useAssistSend(workspaceId, componentId)
  useEffect(() => { preview.mutate(kind) }, [kind, componentId]) // eslint-disable-line react-hooks/exhaustive-deps
  const pv = preview.data
  return (
    <Modal title={`${kind === 'explain' ? 'Explain' : 'Diagnose'} ${cardName}`} onClose={onClose} width={760}
      footer={<>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" disabled={!pv} loading={send.isPending} onClick={() => pv && send.mutate(pv.id, { onSuccess: (s) => onSent(s.id) })}>Send to the agent</Button>
      </>}>
      <p>This is exactly what will be sent to your model. Anything that looks like a password, key or token has been removed.</p>
      {preview.isPending && <p className="mt-3">Gathering the card’s state, logs and checks…</p>}
      {preview.error && <p role="alert" className="mt-3 text-err-fg">{preview.error.message}</p>}
      {pv && (
        <>
          <p className="mt-2 text-xs text-muted" role="status">
            {pv.redactions > 0 ? `${pv.redactions} value${pv.redactions === 1 ? '' : 's'} hidden. ` : 'No credential-like values found. '}
            Included: {pv.sections.map((s) => s.label).join(', ')}.
          </p>
          <pre aria-label="Text that will be sent" className="mt-2 max-h-[46vh] overflow-auto whitespace-pre-wrap rounded-lg border border-line bg-bg p-3 font-mono text-[12px] text-fg">{pv.text}</pre>
        </>
      )}
      {send.error && <p role="alert" className="mt-3 text-err-fg">{send.error.message}</p>}
    </Modal>
  )
}
