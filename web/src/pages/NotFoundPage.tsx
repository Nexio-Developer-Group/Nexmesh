import { Link } from 'react-router-dom'
import { Button } from '@/components/ui/Button'

export default function NotFoundPage() {
  return (
    <main className="theme-light grid min-h-full place-items-center p-6 text-center">
      <div>
        <p className="font-mono text-sm text-faint">404</p>
        <h1 className="mt-2 text-2xl font-extrabold">This page does not exist</h1>
        <p className="mt-2 text-muted">The address may be wrong, or the workspace was deleted.</p>
        <Link to="/workspaces" className="mt-6 inline-block"><Button variant="primary">Back to workspaces</Button></Link>
      </div>
    </main>
  )
}
