import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Power, Shield, StopCircle } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { apiClient } from '@/lib/api/client';
import type { EngineStatus, KillSwitchStatus, MarketType } from '@/lib/api/types';
import {
  RISK_MARKET_KILL_SWITCH_ORDER,
  getCircuitBreakerDisplay,
  getKillSwitchDisplay,
  getMarketKillSwitchLabel,
  getRiskStatusDisplay,
} from '@/lib/risk/presentation';
import { cn } from '@/lib/utils';

function CircuitBreakerDisplay({ status }: { status: EngineStatus }) {
  const { circuit_breaker: cb } = status;
  const display = getCircuitBreakerDisplay(cb);

  return (
    <div className="rounded-lg border border-border p-3">
      <div className="flex items-center justify-between">
        <p className="font-mono text-[11px] font-medium uppercase tracking-[0.16em] text-muted-foreground">
          Circuit breaker
        </p>
        <Badge variant={display.variant}>
          {display.label}
        </Badge>
      </div>
      {cb.reason ? <p className="mt-2 text-sm text-muted-foreground">{cb.reason}</p> : null}
    </div>
  );
}

function PositionLimitsDisplay({ status }: { status: EngineStatus }) {
  const { position_limits: limits } = status;

  const items = [
    { label: 'Per position', value: `${limits.max_per_position_pct}%` },
    { label: 'Total exposure', value: `${limits.max_total_pct}%` },
    { label: 'Max concurrent', value: String(limits.max_concurrent) },
    { label: 'Per market', value: `${limits.max_per_market_pct}%` },
  ];

  return (
    <div className="rounded-lg border border-border p-3">
      <p className="mb-3 font-mono text-[11px] font-medium uppercase tracking-[0.16em] text-muted-foreground">
        Position limits
      </p>
      <div className="grid grid-cols-2 gap-x-4 gap-y-2 text-xs">
        {items.map(({ label, value }) => (
          <div key={label} className="flex justify-between">
            <span className="text-muted-foreground">{label}</span>
            <span className="font-mono font-medium text-foreground">{value}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function MarketKillSwitchCard({
  marketType,
  status,
  onToggle,
  isPending,
}: {
  marketType: MarketType;
  status: KillSwitchStatus | undefined;
  onToggle: (active: boolean) => void;
  isPending: boolean;
}) {
  const isActive = status?.active ?? false;
  return (
    <div className="flex items-center justify-between rounded border border-border px-3 py-2">
      <div>
        <p className="text-xs font-medium">{getMarketKillSwitchLabel(marketType)}</p>
        {isActive && status?.reason ? (
          <p className="mt-0.5 text-[11px] text-destructive">{status.reason}</p>
        ) : null}
      </div>
      <Button
        variant={isActive ? 'outline' : 'ghost'}
        size="dense"
        disabled={isPending}
        onClick={() => onToggle(!isActive)}
        data-testid={`market-kill-switch-${marketType}`}
      >
        <StopCircle className={cn('size-3', isActive && 'text-destructive')} />
        {isActive ? 'Resume' : 'Stop'}
      </Button>
    </div>
  );
}

export function RiskStatusBar() {
  const queryClient = useQueryClient();

  const { data, isLoading, isError } = useQuery({
    queryKey: ['risk', 'status'],
    queryFn: () => apiClient.getRiskStatus(),
    refetchInterval: 15_000,
  });

  const killSwitchMutation = useMutation({
    mutationFn: (active: boolean) =>
      apiClient.toggleKillSwitch({
        active,
        reason: active ? 'Trading halted from dashboard' : 'Trading resumed from dashboard',
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['risk', 'status'] });
    },
  });

  const marketKillSwitchMutation = useMutation({
    mutationFn: ({ marketType, active }: { marketType: string; active: boolean }) =>
      apiClient.toggleMarketKillSwitch(marketType, active),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['risk', 'status'] });
    },
  });

  if (isLoading) {
    return (
      <Card data-testid="risk-status-loading">
        <CardHeader className="flex flex-row items-center justify-between pb-2">
          <div className="h-4 w-32 animate-pulse rounded bg-muted" />
          <div className="size-4 animate-pulse rounded bg-muted" />
        </CardHeader>
        <CardContent>
          <div className="space-y-3">
            <div className="h-16 animate-pulse rounded bg-muted" />
            <div className="h-16 animate-pulse rounded bg-muted" />
          </div>
        </CardContent>
      </Card>
    );
  }

  if (isError || !data) {
    return (
      <Card data-testid="risk-status-error">
        <CardHeader>
          <CardTitle>Risk status</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-muted-foreground">
            Unable to load risk status. Start the API server to see live data.
          </p>
        </CardContent>
      </Card>
    );
  }

  const config = getRiskStatusDisplay(data.risk_status);
  const StatusIcon = config.icon;
  const killSwitchDisplay = getKillSwitchDisplay(data.kill_switch);

  return (
    <Card data-testid="risk-status">
      <CardHeader>
        <div className="flex items-center justify-between">
          <div>
            <CardTitle className="flex items-center gap-2">
              <Shield className="size-5" />
              Risk status
            </CardTitle>
            <CardDescription>Engine health and risk controls</CardDescription>
          </div>
          <Badge variant={config.variant} className="gap-1">
            <StatusIcon className="size-3" />
            {config.label}
          </Badge>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        <CircuitBreakerDisplay status={data} />
        <PositionLimitsDisplay status={data} />

        <div className="rounded-lg border border-border p-3 space-y-3">
          <div className="flex items-center justify-between">
            <div>
              <p className="font-mono text-[11px] font-medium uppercase tracking-[0.16em] text-muted-foreground">
                Kill switch
              </p>
              <div className="mt-1 flex flex-wrap items-center gap-2">
                <Badge variant={killSwitchDisplay.badgeVariant}>
                  {killSwitchDisplay.badgeLabel}
                </Badge>
              </div>
              <p className="mt-2 text-sm text-muted-foreground">
                {killSwitchDisplay.description}
              </p>
              {killSwitchDisplay.mechanismText ? (
                <p className="mt-1 text-xs text-muted-foreground">
                  Mechanism: {killSwitchDisplay.mechanismText}
                </p>
              ) : null}
            </div>
            <Button
              variant={data.kill_switch.active ? 'outline' : 'destructive'}
              size="dense"
              disabled={killSwitchMutation.isPending}
              onClick={() => killSwitchMutation.mutate(!data.kill_switch.active)}
              data-testid="kill-switch-toggle"
            >
              <Power className="size-4" />
              {data.kill_switch.active ? 'Resume Trading' : 'Stop All'}
            </Button>
          </div>

          <div className="space-y-1.5">
            <p className="font-mono text-[10px] font-medium uppercase tracking-[0.16em] text-muted-foreground">
              Per-market
            </p>
            {RISK_MARKET_KILL_SWITCH_ORDER.map((mt) => (
              <MarketKillSwitchCard
                key={mt}
                marketType={mt}
                status={data.market_kill_switches?.[mt]}
                isPending={marketKillSwitchMutation.isPending}
                onToggle={(active) =>
                  marketKillSwitchMutation.mutate({ marketType: mt, active })
                }
              />
            ))}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
