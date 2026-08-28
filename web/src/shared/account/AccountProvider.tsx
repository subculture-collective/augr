/* eslint-disable react-refresh/only-export-components */
import { useQuery } from '@tanstack/react-query'
import { createContext, useContext, type ReactNode } from 'react'

import { getCurrentAccounts } from '@/shared/api/endpoints'
import { useAuth } from '@/shared/auth/AuthProvider'
import { ErrorState, LoadingState } from '@/shared/components/QueryStates'
import { queryKeys } from '@/shared/query/keys'
import type { Account } from '@/shared/types/domain'

type AccountContextValue = { account: Account; cockpitPath: string }

const AccountContext = createContext<AccountContextValue | null>(null)

export function AccountProvider({ children }: { children: ReactNode }) {
  const auth = useAuth()
  const accounts = useQuery({
    queryKey: queryKeys.accounts,
    queryFn: ({ signal }) => getCurrentAccounts(signal),
    enabled: auth.status === 'authenticated',
    retry: false,
  })

  if (auth.status !== 'authenticated') return <>{children}</>
  if (accounts.isLoading) return <main id="main-content"><LoadingState label="Resolving execution account…" /></main>
  if (accounts.isError) return <main id="main-content"><ErrorState error={accounts.error} onRetry={() => void accounts.refetch()} /></main>
  if (accounts.data?.length !== 1) {
    return <main id="main-content"><section className="panel"><h1>Account configuration error</h1><p>Augr requires exactly one configured execution account; the server returned {accounts.data?.length ?? 0}.</p></section></main>
  }

  const account = accounts.data[0]
  return <AccountContext.Provider value={{ account, cockpitPath: `/accounts/${account.id}/cockpit` }}>{children}</AccountContext.Provider>
}

export function useAccount() {
  const value = useContext(AccountContext)
  if (!value) throw new Error('useAccount must be used within a resolved AccountProvider')
  return value
}

export function useOptionalAccount() {
  return useContext(AccountContext)
}
