import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { apiClient } from '@/lib/api/client'
import type { PolymarketAccountListParams } from '@/lib/api/types'

export function usePolymarketAccounts(params: PolymarketAccountListParams = {}) {
  return useQuery({
    queryKey: ['polymarket-accounts', params],
    queryFn: () => apiClient.listPolymarketAccounts(params),
    refetchInterval: 30_000,
  })
}

export function usePolymarketAccount(address?: string) {
  return useQuery({
    queryKey: ['polymarket-account', address],
    queryFn: () => apiClient.getPolymarketAccount(address!),
    enabled: !!address,
  })
}

export function usePolymarketAccountTrades(
  address?: string,
  params: { from?: string; to?: string; limit?: number } = {},
) {
  return useQuery({
    queryKey: ['polymarket-account-trades', address, params],
    queryFn: () => apiClient.listPolymarketAccountTrades(address!, params),
    enabled: !!address,
  })
}

export function usePolymarketRecentTrades(limit = 50) {
  return useQuery({
    queryKey: ['polymarket-recent-trades', limit],
    queryFn: () => apiClient.listPolymarketRecentTrades(limit),
    refetchInterval: 15_000,
  })
}

export function usePolymarketRecentSignals(params: { limit?: number; min_urgency?: number } = {}) {
  return useQuery({
    queryKey: ['polymarket-recent-signals', params],
    queryFn: () => apiClient.listPolymarketRecentSignals(params),
    refetchInterval: 10_000,
  })
}

export function usePolymarketWatched() {
  return useQuery({
    queryKey: ['polymarket-watched'],
    queryFn: () => apiClient.listPolymarketWatched(),
    refetchInterval: 60_000,
  })
}

export function usePolymarketMarket(slug?: string) {
  return useQuery({
    queryKey: ['polymarket-market', slug],
    queryFn: () => apiClient.getPolymarketMarket(slug!),
    enabled: !!slug,
    refetchInterval: 30_000,
  })
}

export function usePolymarketJobsStatus() {
  return useQuery({
    queryKey: ['polymarket-jobs'],
    queryFn: () => apiClient.getPolymarketJobsStatus(),
    refetchInterval: 10_000,
  })
}

export function useSetPolymarketAccountTracked() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ address, tracked }: { address: string; tracked: boolean }) =>
      apiClient.setPolymarketAccountTracked(address, tracked),
    onSuccess: (_data, vars) => {
      queryClient.invalidateQueries({ queryKey: ['polymarket-accounts'] })
      queryClient.invalidateQueries({ queryKey: ['polymarket-account', vars.address] })
    },
  })
}

export function useAddPolymarketWatched() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ slug, note }: { slug: string; note?: string }) => apiClient.addPolymarketWatched(slug, note),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['polymarket-watched'] }),
  })
}

export function useRemovePolymarketWatched() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (slug: string) => apiClient.removePolymarketWatched(slug),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['polymarket-watched'] }),
  })
}

export function useSetPolymarketWatchedEnabled() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ slug, enabled }: { slug: string; enabled: boolean }) =>
      apiClient.setPolymarketWatchedEnabled(slug, enabled),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['polymarket-watched'] }),
  })
}

export function usePolymarketDiscoveryLast() {
  return useQuery({
    queryKey: ['polymarket-discovery-last'],
    queryFn: () => apiClient.getPolymarketDiscoveryLast(),
    refetchInterval: 30_000,
  })
}

export function useRunPolymarketDiscovery() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => apiClient.runPolymarketDiscovery(),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['polymarket-discovery-last'] })
      queryClient.invalidateQueries({ queryKey: ['polymarket-jobs'] })
      queryClient.invalidateQueries({ queryKey: ['strategies'] })
    },
  })
}
