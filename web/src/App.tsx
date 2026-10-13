import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { ApiError } from '@/api/client'
import DashboardPage from '@/pages/DashboardPage'
import NotFoundPage from '@/pages/NotFoundPage'
import CreateWorkspacePage from '@/pages/CreateWorkspacePage'
import ProvisioningPage from '@/pages/ProvisioningPage'

const queryClient = new QueryClient({
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
        <Routes>
          <Route path="/" element={<Navigate to="/workspaces" replace />} />
          <Route path="/workspaces" element={<DashboardPage />} />
          <Route path="/new" element={<CreateWorkspacePage />} />
          <Route path="/w/:id/provisioning" element={<ProvisioningPage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
