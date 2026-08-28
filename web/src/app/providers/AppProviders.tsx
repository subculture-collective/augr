import { QueryClientProvider } from '@tanstack/react-query'
import { useState, type ReactNode } from 'react'

import { appConfig } from '@/app/config/env'
import { configureApiClient } from '@/shared/api/client'
import { AuthProvider, useAuth } from '@/shared/auth/AuthProvider'
import { ThemeProvider } from '@/app/providers/ThemeProvider'
import { createAppQueryClient } from '@/shared/query/client'
import { RealtimeProvider } from '@/shared/websocket/RealtimeProvider'
import { AccountProvider, useOptionalAccount } from '@/shared/account/AccountProvider'

configureApiClient({ baseUrl: appConfig.apiBaseUrl })

function RealtimeBridge({ children }: { children: ReactNode }) {
  const auth = useAuth()
  const accountContext = useOptionalAccount()
  return <RealtimeProvider authenticated={auth.status === 'authenticated'} accountId={accountContext?.account.id}>{children}</RealtimeProvider>
}

export function AppProviders({ children }: { children: ReactNode }) {
  const [queryClient] = useState(() => createAppQueryClient())
  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <AuthProvider>
          <AccountProvider>
            <RealtimeBridge>{children}</RealtimeBridge>
          </AccountProvider>
        </AuthProvider>
      </ThemeProvider>
    </QueryClientProvider>
  )
}
