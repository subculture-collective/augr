import { useEffect, useState } from 'react'

import { PageHeader } from '@/components/layout/page-header'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { apiClient } from '@/lib/api/client'
import type { DivergenceResponse, PolymarketStatus, RiskBreakerState } from '@/lib/api/types'

export function SurfersOpsPage() {
  const [status, setStatus] = useState<PolymarketStatus | null>(null)
  const [breakers, setBreakers] = useState<RiskBreakerState[]>([])
  const [divergence, setDivergence] = useState<DivergenceResponse | null>(null)
  const [strategyId, setStrategyId] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    let alive = true
    const load = async () => {
      try {
        const [s, b, d] = await Promise.all([
          apiClient.getPolymarketMarketDataStatus(),
          apiClient.listRiskBreakers(),
          strategyId ? apiClient.getBacktestDivergence(strategyId) : Promise.resolve(null),
        ])
        if (!alive) return
        setStatus(s)
        setBreakers(b.tripped)
        setDivergence(d)
        setError('')
      } catch (e) {
        if (alive) setError(e instanceof Error ? e.message : 'Failed to load ops data')
      } finally {
        if (alive) setLoading(false)
      }
    }
    void load()
    const id = window.setInterval(load, 5000)
    return () => { alive = false; window.clearInterval(id) }
  }, [strategyId])

  return <div className="space-y-4"><PageHeader title="Surfers Ops" description="Polymarket feed, recorder lag, breakers, and divergence" />
    {loading ? <p className="text-sm text-muted-foreground">Loading…</p> : null}
    {error ? <p className="text-sm text-destructive">{error}</p> : null}
    <Card><CardHeader><CardTitle>WS Pool Health</CardTitle></CardHeader><CardContent className="space-y-2 text-sm"><div>Enabled: {String(status?.enabled ?? false)}</div><div>Connections: {status?.ws_connections ?? 0}</div><div>Jitter: {status?.avg_jitter_ms?.toFixed(2) ?? '0.00'} ms</div><div>Dropped: {status?.dropped ?? 0}</div><div>Ready slugs: {(status?.ready_slugs ?? []).join(', ') || '--'}</div></CardContent></Card>
    <Card><CardHeader><CardTitle>Recorder Lag</CardTitle></CardHeader><CardContent className="text-sm">{status?.recorder_lag_seconds?.toFixed(2) ?? '0.00'} s</CardContent></Card>
    <Card><CardHeader><CardTitle>Tripped Breakers</CardTitle></CardHeader><CardContent className="space-y-2 text-sm">{breakers.length ? breakers.map((b) => <div key={b.scope} className="rounded-md border border-border p-3"><div className="font-medium">{b.scope}</div><div>{b.reason}</div><div>{b.tripped_at}</div></div>) : <p>No tripped breakers.</p>}</CardContent></Card>
    <Card><CardHeader><CardTitle>Divergence Status</CardTitle></CardHeader><CardContent className="space-y-3"><div className="flex gap-2"><Input value={strategyId} onChange={(e) => setStrategyId(e.target.value)} placeholder="strategy_id" /><Button onClick={() => void 0}>Load</Button></div><pre className="overflow-x-auto text-xs">{divergence ? JSON.stringify(divergence, null, 2) : 'No divergence loaded.'}</pre></CardContent></Card>
  </div>
}
