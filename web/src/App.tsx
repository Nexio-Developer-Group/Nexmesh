import { Logo } from '@/components/ui/Logo'
import { Button } from '@/components/ui/Button'
import { StatusBadge } from '@/components/ui/StatusBadge'

export default function App() {
  return (
    <div className="theme-light flex min-h-full flex-col items-center justify-center gap-4">
      <Logo size={32} />
      <p className="text-muted">Build with AI. Engineer with structure.</p>
      <StatusBadge state="running" />
      <Button variant="primary">Create Code Space</Button>
    </div>
  )
}
