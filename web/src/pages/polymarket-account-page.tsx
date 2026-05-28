import { useMemo } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'

import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { usePolymarketAccount, usePolymarketAccountTrades, useSetPolymarketAccountTracked } from '@/hooks/use-polymarket'

function formatRelativeTime(iso?: string): string {
  if (!iso) return '--'
  const diff = Date.now() - new Date(iso).getTime()
  const seconds = Math.floor(diff / 1000)
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

const money = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' })

export function PolymarketAccountPage() {
  const { address } = useParams<{ address: string }>()
  const qc = useQueryClient()
  const account = usePolymarketAccount(address)
  const trades = usePolymarketAccountTrades(address, { from: new Date(Date.now() - 30 * 24 * 60 * 60 * 1000).toISOString(), to: new Date().toISOString(), limit: 100 })
  const track = useSetPolymarketAccountTracked()
  const data = account.data
  const stats = useMemo(() => [
    ['total_volume', money.format(data?.total_volume ?? 0)],
    ['total_trades', String(data?.total_trades ?? 0)],
    ['markets_entered', String(data?.markets_entered ?? 0)],
    ['W/L', `${data?.markets_won ?? 0}/${data?.markets_lost ?? 0}`],
    ['win_rate', `${((data?.win_rate ?? 0) * 100).toFixed(1)}%`],
    ['max_position', money.format(data?.max_position ?? 0)],
    ['early_entry_rate', `${((data?.early_entry_rate ?? 0) * 100).toFixed(1)}%`],
    ['last_active', formatRelativeTime(data?.last_active)],
  ], [data])

  return <div className="space-y-4" data-testid="polymarket-account-page"><PageHeader title={address ?? ''} description="Polymarket account detail" actions={<Button size="sm" variant="outline" onClick={() => void navigator.clipboard.writeText(address ?? '')}>Copy</Button>} /><div className="flex gap-2"><Badge variant={data?.tracked ? 'success' : 'secondary'}>{data?.tracked ? 'tracked' : 'untracked'}</Badge><input type="checkbox" checked={data?.tracked ?? false} onChange={(e) => address && track.mutate({ address, tracked: e.target.checked }, { onSuccess: () => qc.invalidateQueries({ queryKey: ['polymarket-account', address] }) })} /></div><div className="grid gap-3 md:grid-cols-4">{stats.map(([k, v]) => <Card key={k}><CardContent><div className="text-xs uppercase text-muted-foreground">{k}</div><div>{v}</div></CardContent></Card>)}</div><Card><CardHeader><CardTitle>Trades</CardTitle></CardHeader><CardContent><table className="w-full text-sm"><tbody>{(trades.data?.data ?? []).map((t) => <tr key={t.id}><td>{new Date(t.timestamp).toLocaleString()}</td><td><a href={`https://polymarket.com/event/${t.market_slug}`} target="_blank" rel="noreferrer">{t.market_slug}</a></td><td><Badge>{t.side}</Badge></td><td>{t.action}</td><td>{t.price.toFixed(3)}</td><td>{money.format(t.size_usdc)}</td><td>{t.pnl == null ? '--' : <span className={t.pnl >= 0 ? 'text-emerald-500' : 'text-red-500'}>{money.format(t.pnl)}</span>}</td></tr>)}</tbody></table></CardContent></Card><Link to="/polymarket">Back to Polymarket</Link></div>
}
