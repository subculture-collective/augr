import { useQuery } from '@tanstack/react-query'

import { UpcomingEventsWidget } from '@/components/calendar/upcoming-events-widget'
import { ActiveStrategies } from '@/components/dashboard/active-strategies'
import { ActivityFeed } from '@/components/dashboard/activity-feed'
import { PortfolioSummary } from '@/components/dashboard/portfolio-summary'
import { RecentRuns } from '@/components/dashboard/recent-runs'
import { PageHeader } from '@/components/layout/page-header'
import { RiskStatusBar } from '@/components/dashboard/risk-status-bar'
import { ConsolePanel, HudBadge, HudRow, HudSection, StatusLed } from '@/components/ui/hud'
import { apiClient } from '@/lib/api/client'

export function DashboardPage() {
  const { data: health, isError: healthError } = useQuery({
    queryKey: ['health'],
    queryFn: () => apiClient.health(),
    refetchInterval: 60_000,
  })

  const healthState = healthError ? 'dead' : health ? 'ok' : 'sync'
  const healthLabel = healthError ? 'Degraded' : health ? 'System OK' : 'Checking…'

  return (
    <div className="space-y-5" data-testid="dashboard-page">
      <PageHeader
        eyebrow="Overview"
        title="Trading overview"
        description="Live portfolio, strategy, and risk telemetry for the current operating session."
        meta={
          <div className="flex flex-wrap items-center gap-2">
            <StatusLed state={healthState} label={healthLabel} />
            <HudBadge tone={healthError ? 'alert' : health ? 'confirm' : 'caution'}>
              {healthError ? 'telemetry offline' : health ? 'broadcast live' : 'sync pending'}
            </HudBadge>
          </div>
        }
      />

      <ConsolePanel className="space-y-4 p-4">
        <HudSection label="Broadcast metadata" note="Session-wide telemetry and operating posture" />
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
          <HudRow label="Health" value={health ? 'healthy' : healthError ? 'degraded' : 'checking'} />
          <HudRow label="API" value={health?.status ?? '—'} />
          <HudRow label="Refresh" value="60s" />
          <HudRow label="Mode" value={healthError ? 'degraded' : health ? 'live' : 'warming up'} />
        </div>
      </ConsolePanel>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1.45fr)_minmax(0,0.82fr)]">
        <div className="space-y-4">
          <ConsolePanel className="space-y-3 p-4">
            <HudSection label="Portfolio console" note="Position and P&L summary" />
            <PortfolioSummary />
          </ConsolePanel>

          <ConsolePanel className="space-y-3 p-4">
            <HudSection label="Strategy stream" note="Active runs and activity" />
            <ActiveStrategies />
            <RecentRuns />
            <ActivityFeed />
          </ConsolePanel>
        </div>

        <div className="space-y-4">
          <ConsolePanel className="space-y-3 p-4">
            <HudSection label="Risk console" note="Hard limits and control state" />
            <RiskStatusBar />
          </ConsolePanel>

          <ConsolePanel className="space-y-3 p-4">
            <HudSection label="Upcoming events" note="Calendar and scheduled operations" />
            <UpcomingEventsWidget />
          </ConsolePanel>
        </div>
      </div>
    </div>
  )
}
