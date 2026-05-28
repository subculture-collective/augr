import { useEffect, useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'

import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { apiClient } from '@/lib/api/client'
import { useAddPolymarketWatched, usePolymarketAccounts, usePolymarketDiscoveryLast, usePolymarketJobsStatus, usePolymarketRecentSignals, usePolymarketRecentTrades, usePolymarketWatched, useRemovePolymarketWatched, useRunPolymarketDiscovery, useSetPolymarketAccountTracked, useSetPolymarketWatchedEnabled } from '@/hooks/use-polymarket'
import { useWebSocketClient } from '@/hooks/use-websocket-client'

function formatRelativeTime(iso?: string): string { if (!iso) return '--'; const d = Date.now() - new Date(iso).getTime(); const s = Math.floor(d / 1000); if (s < 60) return `${s}s ago`; const m = Math.floor(s / 60); if (m < 60) return `${m}m ago`; const h = Math.floor(m / 60); return h < 24 ? `${h}h ago` : `${Math.floor(h / 24)}d ago` }
const money = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', maximumFractionDigits: 0 })
const money2 = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', maximumFractionDigits: 2 })

export function PolymarketPage() {
  const qc = useQueryClient()
  const [slug, setSlug] = useState('')
  const [err, setErr] = useState('')
  const [trackedOnly, setTrackedOnly] = useState(true)
  const [minWinRate, setMinWinRate] = useState('')
  const [sort, setSort] = useState<'win_rate'|'volume'|'last_active'|'trades'>('win_rate')
  const [offset, setOffset] = useState(0)
  const accounts = usePolymarketAccounts({ tracked: trackedOnly || undefined, min_win_rate: minWinRate ? Number(minWinRate) : undefined, sort, limit: 25, offset })
  const watched = usePolymarketWatched()
  const jobs = usePolymarketJobsStatus()
  const trades = usePolymarketRecentTrades(50)
  const signals = usePolymarketRecentSignals({ limit: 25 })
  const tracked = usePolymarketAccounts({ tracked: true, limit: 1000 })
  const strategies = useQuery({ queryKey: ['strategies', { market_type: 'polymarket' }], queryFn: () => apiClient.listStrategies({ market_type: 'polymarket' }) })
  const add = useAddPolymarketWatched()
  const remove = useRemovePolymarketWatched()
  const enable = useSetPolymarketWatchedEnabled()
  const track = useSetPolymarketAccountTracked()
  const discoveryLast = usePolymarketDiscoveryLast()
  const runDiscovery = useRunPolymarketDiscovery()
  const ws = useWebSocketClient({ enabled: true, onMessage: (m) => { if ('type' in m && (m.type === 'polymarket_whale_trade' || m.type === 'polymarket_price_move' || m.type === 'polymarket_account_tracked')) { qc.invalidateQueries({ queryKey: ['polymarket-accounts'] }); qc.invalidateQueries({ queryKey: ['polymarket-account'] }); qc.invalidateQueries({ queryKey: ['polymarket-recent-trades'] }); qc.invalidateQueries({ queryKey: ['polymarket-recent-signals'] }); qc.invalidateQueries({ queryKey: ['polymarket-watched'] }); } } })
  useEffect(() => { if (ws.status === 'open') ws.sendCommand({ action: 'subscribe_polymarket' }) }, [ws.status])
  const totalVolume = useMemo(() => (tracked.data?.data ?? []).reduce((s, a) => s + a.total_volume, 0), [tracked.data])
  const doAdd = async () => { setErr(''); try { await add.mutateAsync({ slug: slug.trim() }); setSlug('') } catch (e) { setErr(e instanceof Error ? e.message : 'Failed') } }
  const accountsData = accounts.data?.data ?? []
  const watchedData = watched.data?.data ?? []
  const signalsData = signals.data?.data ?? []
  const jobsData = jobs.data ?? []
  const strategiesData = (strategies.data?.data ?? []).filter((s) => s.market_type === 'polymarket')
  return <div className="space-y-4" data-testid="polymarket-page"><PageHeader title="Polymarket" description="Prediction market intelligence: tracked wallets, whale trades, watched markets" />
    <Card><CardHeader><CardTitle>Summary</CardTitle></CardHeader><CardContent><div className="grid gap-3 md:grid-cols-4"><div><div className="text-xs uppercase text-muted-foreground">Tracked Wallets</div><div className="text-2xl font-semibold">{tracked.data?.data?.length ?? 0}</div></div><div><div className="text-xs uppercase text-muted-foreground">Total Volume</div><div className="text-2xl font-semibold">{money.format(totalVolume)}</div></div><div><div className="text-xs uppercase text-muted-foreground">Open Polymarket Strategies</div><div className="text-2xl font-semibold">{strategiesData.length}</div></div><div><div className="text-xs uppercase text-muted-foreground">Recent Whale Signals</div><div className="text-2xl font-semibold">{signalsData.filter((s) => s.source === 'polymarket-whale').length}</div></div></div></CardContent></Card>
    <Card><CardHeader><CardTitle>Polymarket Jobs</CardTitle></CardHeader><CardContent><div className="flex flex-wrap gap-2">{jobsData.map((j) => <div key={j.name} className="rounded-md border border-border p-3 text-xs"><div className="font-medium">{j.name}</div><div>{j.schedule}</div><div>Last: {formatRelativeTime(j.last_run)}</div><Badge variant={j.running ? 'success' : 'secondary'}>{j.running ? 'running' : j.enabled ? 'enabled' : 'idle'}</Badge><Button size="sm" variant="outline" onClick={() => { void apiClient.runAutomationJob(j.name).then(() => qc.invalidateQueries({ queryKey: ['polymarket-jobs'] })) }}>Run now</Button></div>)}</div></CardContent></Card>
    <Card><CardHeader><CardTitle>Watched Markets</CardTitle></CardHeader><CardContent><div className="mb-3 flex gap-2"><Input value={slug} onChange={(e) => setSlug(e.target.value)} placeholder="market slug" /><Button onClick={doAdd}>Add</Button></div>{err ? <p className="text-sm text-destructive">{err}</p> : null}<table className="w-full text-sm"><tbody>{watchedData.map((m) => <tr key={m.slug}><td>{m.slug}</td><td><input type="checkbox" checked={m.enabled} onChange={(e) => enable.mutate({ slug: m.slug, enabled: e.target.checked })} /></td><td>{formatRelativeTime(m.added_at)}</td><td>{m.note ?? '--'}</td><td><Button size="sm" variant="outline" onClick={() => remove.mutate(m.slug)}>Remove</Button></td></tr>)}</tbody></table></CardContent></Card>
    <Card><CardHeader><CardTitle>Tracked Wallets</CardTitle></CardHeader><CardContent><div className="mb-3 flex flex-wrap gap-2"><label><input type="checkbox" checked={trackedOnly} onChange={(e) => setTrackedOnly(e.target.checked)} /> tracked-only</label><Input value={minWinRate} onChange={(e) => setMinWinRate(e.target.value)} placeholder="min win rate" /><select value={sort} onChange={(e) => setSort(e.target.value as any)}><option value="win_rate">win_rate</option><option value="volume">volume</option><option value="last_active">last_active</option><option value="trades">trades</option></select></div><table className="w-full text-sm"><thead><tr><th>address</th><th>win_rate</th><th>won/lost</th><th>total_volume</th><th>max_position</th><th>last_active</th><th>tracked</th><th>tags</th></tr></thead><tbody>{accountsData.map((a) => <tr key={a.address}><td><Link to={`/polymarket/accounts/${a.address}`}>{`${a.address.slice(0,6)}…${a.address.slice(-4)}`}</Link></td><td>{(a.win_rate * 100).toFixed(1)}%</td><td>{`${a.markets_won}/${a.markets_lost}`}</td><td>{money.format(a.total_volume)}</td><td>{money.format(a.max_position)}</td><td>{formatRelativeTime(a.last_active)}</td><td><input type="checkbox" checked={a.tracked} onChange={(e) => track.mutate({ address: a.address, tracked: e.target.checked })} /></td><td>{(a.tags ?? []).map((t) => <Badge key={t}>{t}</Badge>)}</td></tr>)}</tbody></table><div className="mt-3 flex gap-2"><Button size="sm" variant="outline" disabled={offset===0} onClick={() => setOffset((o) => Math.max(0, o - 25))}>Prev</Button><Button size="sm" variant="outline" disabled={accountsData.length < 25} onClick={() => setOffset((o) => o + 25)}>Next</Button></div></CardContent></Card>
    <Card><CardHeader><CardTitle>Recent Whale Trades</CardTitle></CardHeader><CardContent><div className="mb-2 flex items-center gap-2"><span className={ws.status === 'open' ? 'size-2 rounded-full bg-emerald-400' : 'size-2 rounded-full bg-zinc-500'} /> {ws.status}</div><table className="w-full text-sm"><tbody>{(trades.data?.data ?? []).map((t) => <tr key={t.id}><td>{formatRelativeTime(t.timestamp)}</td><td><Link to={`/polymarket/accounts/${t.account_address}`}>{`${t.account_address.slice(0,6)}…${t.account_address.slice(-4)}`}</Link></td><td><a href={`https://polymarket.com/event/${t.market_slug}`} target="_blank" rel="noreferrer">{t.market_slug}</a></td><td><Badge variant={String(t.side).toUpperCase()==='YES' ? 'success' : 'destructive'}>{t.side}</Badge></td><td>{money2.format(t.size_usdc)}</td><td>{t.price.toFixed(3)}</td></tr>)}</tbody></table></CardContent></Card>
    <Card><CardHeader><CardTitle>Recent Polymarket Signals</CardTitle></CardHeader><CardContent>{signalsData.map((s) => <div key={s.id} className="flex justify-between border-b border-border py-2"><div>{formatRelativeTime(s.received_at)} · {s.title}</div><Badge>{s.source}</Badge><div>{s.urgency}</div></div>)}</CardContent></Card>
    <Card><CardHeader><CardTitle>Polymarket Strategies</CardTitle></CardHeader><CardContent>{strategiesData.map((s) => <div key={s.id}><Link to={`/strategies/${s.id}`}>{s.name}</Link></div>)}</CardContent></Card>
    <Card data-testid="polymarket-discovery"><CardHeader><CardTitle>Auto-generated Strategies</CardTitle></CardHeader><CardContent>
      <div className="mb-3 flex items-center gap-2">
        <Button size="sm" disabled={runDiscovery.isPending} onClick={() => runDiscovery.mutate()}>{runDiscovery.isPending ? 'Running…' : 'Run discovery now'}</Button>
        <span className="text-xs text-muted-foreground">Cron: every 6 hours. Generates paper strategies from open Polymarket markets.</span>
      </div>
      {runDiscovery.isError ? <p className="mb-2 text-sm text-destructive">{(runDiscovery.error as Error)?.message ?? 'Failed to start discovery'}</p> : null}
      {(() => {
        const last = discoveryLast.data?.last
        if (!last) return <p className="text-sm text-muted-foreground">No discovery run yet.</p>
        const deployed = last.deployed ?? []
        return <div className="space-y-2">
          <div className="text-xs text-muted-foreground">Last run {formatRelativeTime(last.started_at)} · fetched {last.fetched_all} · screened {last.screened} · proposed {last.proposed} · skipped {last.skipped} · deployed {deployed.length}{last.dry_run ? ' (dry run)' : ''}</div>
          {deployed.length === 0 ? <p className="text-sm">No strategies deployed in the last run.</p> : <table className="w-full text-sm"><thead><tr><th className="text-left">name</th><th className="text-left">slug</th><th className="text-left">template</th><th className="text-left">side</th><th className="text-left">conviction</th><th></th></tr></thead><tbody>{deployed.map((d) => <tr key={d.strategy_id}><td><Link to={`/strategies/${d.strategy_id}`}>{d.name}</Link></td><td><a href={`https://polymarket.com/event/${d.slug}`} target="_blank" rel="noreferrer">{d.slug}</a></td><td><Badge variant="secondary">{d.template}</Badge></td><td><Badge variant={d.direction === 'YES' ? 'success' : 'destructive'}>{d.direction}</Badge></td><td>{(d.conviction * 100).toFixed(0)}%</td><td>{d.reused ? <Badge variant="secondary">reused</Badge> : <Badge variant="success">new</Badge>}</td></tr>)}</tbody></table>}
          {last.errors && last.errors.length > 0 ? <details className="text-xs"><summary>{last.errors.length} errors</summary><ul className="mt-1 list-disc pl-4">{last.errors.map((e, i) => <li key={i}>{e}</li>)}</ul></details> : null}
        </div>
      })()}
    </CardContent></Card>
  </div>
}
