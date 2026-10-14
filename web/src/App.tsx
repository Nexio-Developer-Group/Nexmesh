import { QueryCache, QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { ApiError } from '@/api/client'
import { authKeys } from '@/api/hooks'
import { AuthGate } from '@/features/auth/AuthGate'
import DashboardPage from '@/pages/DashboardPage'
import NotFoundPage from '@/pages/NotFoundPage'
import CreateWorkspacePage from '@/pages/CreateWorkspacePage'
import ProvisioningPage from '@/pages/ProvisioningPage'
import WorkspacePage from '@/pages/WorkspacePage'
import SettingsPage from '@/pages/SettingsPage'

const queryClient: QueryClient = new QueryClient({
  // A 401 anywhere means the session ended: drop to the sign-in screen.
  queryCache: new QueryCache({
    onError: (err, q) => {
      if (err instanceof ApiError && err.status === 401 && q.queryKey[0] !== 'me') queryClient.invalidateQueries({ queryKey: authKeys.me })
    },
  }),
  defaultOptions: {
    queries: {
      retry: (n, err) => !(err instanceof ApiError && err.status >= 400 && err.status < 500) && n < 2,
      refetchOnWindowFocus: false,
    },
  },
})

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <AuthGate>
        <Routes>
          <Route path="/" element={<Navigate to="/workspaces" replace />} />
          <Route path="/workspaces" element={<DashboardPage />} />
          <Route path="/new" element={<CreateWorkspacePage />} />
          <Route path="/w/:id" element={<WorkspacePage />} />
          <Route path="/w/:id/settings" element={<SettingsPage />} />
          <Route path="/w/:id/provisioning" element={<ProvisioningPage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Routes>
        </AuthGate>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
