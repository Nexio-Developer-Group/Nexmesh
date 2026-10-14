import clsx from 'clsx'
import { useState } from 'react'
import type { Environment, Workspace } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
import { ENVIRONMENTS, useSwitchEnvironment } from './hooks'

const LABEL: Record<Environment, string> = { development: 'Development', staging: 'Staging', production: 'Production' }

/**
 * The active-environment selector (PRD F4). It decides whose values are injected
 * into the workspace. Production is visibly different and needs a confirmation.
 */
export function EnvironmentSwitcher({ workspace }: { workspace: Workspace }) {
  const sw = useSwitchEnvironment(workspace.id)
  const active: Environment = workspace.activeEnvironment ?? 'development'
  const chosen = !!workspace.activeEnvironment
  const [confirm, setConfirm] = useState(false)
  const pick = (e: Environment) => {
    if (e === active && chosen) return
    if (e === 'production') setConfirm(true)
    else sw.mutate({ environment: e })
  }
  return (
    <>
      <div role="radiogroup" aria-label="Active environment" className="flex rounded-[10px] border border-line-strong p-0.5 text-[12.5px]">
        {ENVIRONMENTS.map((e) => {
          const on = chosen && e === active
          return (
            <button key={e} type="button" role="radio" aria-checked={on} disabled={sw.isPending} onClick={() => pick(e)}
              title={chosen ? undefined : 'No environment chosen yet: every connection applies. Pick one to inject only its values.'}
              className={clsx('rounded-lg px-2.5 py-1 font-semibold', on ? (e === 'production' ? 'bg-warn-bg text-warn-fg' : 'bg-raised text-fg') : 'text-muted hover:text-fg')}>
              {on && e === 'production' && <span aria-hidden>▲ </span>}{LABEL[e]}
            </button>
          )
        })}
      </div>
      {sw.error && <span role="alert" className="text-xs text-err-fg">{sw.error.message}</span>}
      {confirm && (
        <Modal title="Switch to Production?" onClose={() => setConfirm(false)}
          footer={<><Button onClick={() => setConfirm(false)}>Cancel</Button>
            <Button variant="danger" loading={sw.isPending} onClick={() => sw.mutate({ environment: 'production', confirm: true }, { onSuccess: () => setConfirm(false) })}>Switch to Production</Button></>}>
          <p>Production connection values and variables will be injected into processes started in this workspace. Tools and agents treat production resources as read-only by default.</p>
        </Modal>
      )}
    </>
  )
}
