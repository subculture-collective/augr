/* eslint-disable react-refresh/only-export-components */
import { lazy, Suspense } from 'react'
import { createBrowserRouter, Navigate, Outlet, useLocation, useParams } from 'react-router-dom'

import { AppShell } from '@/app/layout/AppShell'
import { ProtectedRoute } from '@/app/router/ProtectedRoute'
import { NotFoundPage, RouteErrorPage } from '@/app/router/RouteStatePages'
import { LoadingState } from '@/shared/components/QueryStates'
import { LoginPage } from '@/features/auth-login/LoginPage'
import { useAccount } from '@/shared/account/AccountProvider'

// Lazy-load all route components for code splitting.
// LoginPage stays eager — it's the first paint for unauthenticated users.
const AutomationDetailPage = lazy(() =>
  import('@/features/automation/AutomationDetailPage').then((m) => ({ default: m.AutomationDetailPage })),
)
const AutomationPage = lazy(() =>
  import('@/features/automation/AutomationPage').then((m) => ({ default: m.AutomationPage })),
)
const CockpitPage = lazy(() =>
  import('@/features/cockpit/CockpitPage').then((m) => ({ default: m.CockpitPage })),
)
const EventsPage = lazy(() =>
  import('@/features/events/EventsPage').then((m) => ({ default: m.EventsPage })),
)
const OrderDetailPage = lazy(() =>
  import('@/features/orders/OrderDetailPage').then((m) => ({ default: m.OrderDetailPage })),
)
const OrdersListPage = lazy(() =>
  import('@/features/orders/OrdersListPage').then((m) => ({ default: m.OrdersListPage })),
)
const PortfolioPage = lazy(() =>
  import('@/features/portfolio/PortfolioPage').then((m) => ({ default: m.PortfolioPage })),
)
const StockPage = lazy(() =>
  import('@/features/stock/StockPage').then((m) => ({ default: m.StockPage })),
)
const RiskPage = lazy(() =>
  import('@/features/risk/RiskPage').then((m) => ({ default: m.RiskPage })),
)
const RunDetailPage = lazy(() =>
  import('@/features/runs/RunDetailPage').then((m) => ({ default: m.RunDetailPage })),
)
const RunsListPage = lazy(() =>
  import('@/features/runs/RunsListPage').then((m) => ({ default: m.RunsListPage })),
)
const StrategyCreatePage = lazy(() =>
  import('@/features/strategies/StrategyCreatePage').then((m) => ({ default: m.StrategyCreatePage })),
)
const StrategyDetailPage = lazy(() =>
  import('@/features/strategies/StrategyDetailPage').then((m) => ({ default: m.StrategyDetailPage })),
)
const StrategyEditPage = lazy(() =>
  import('@/features/strategies/StrategyEditPage').then((m) => ({ default: m.StrategyEditPage })),
)
const StrategiesListPage = lazy(() =>
  import('@/features/strategies/StrategiesListPage').then((m) => ({ default: m.StrategiesListPage })),
)
const TradesListPage = lazy(() =>
  import('@/features/trades/TradesListPage').then((m) => ({ default: m.TradesListPage })),
)
const SettingsPage = lazy(() =>
  import('@/features/settings/SettingsPage').then((m) => ({ default: m.SettingsPage })),
)
const EventMarketsPage = lazy(() =>
  import('@/features/event-markets/EventMarketsPage').then((m) => ({ default: m.EventMarketsPage })),
)
const OptionsPage = lazy(() =>
  import('@/features/options/OptionsPage').then((m) => ({ default: m.OptionsPage })),
)
const BacktestsPage = lazy(() =>
  import('@/features/backtests/BacktestsPage').then((m) => ({ default: m.BacktestsPage })),
)
const JournalPage = lazy(() =>
  import('@/features/journal/JournalPage').then((m) => ({ default: m.JournalPage })),
)
const ReplayPage = lazy(() =>
  import('@/features/journal/ReplayPage').then((m) => ({ default: m.ReplayPage })),
)
const CopyTradingPage = lazy(() =>
  import('@/features/copy-trading/CopyTradingPage').then((m) => ({ default: m.CopyTradingPage })),
)
const SystemSafetyPage = lazy(() =>
  import('@/features/system-safety/SystemSafetyPage').then((m) => ({ default: m.SystemSafetyPage })),
)

const routeFallback = <LoadingState />

function withSuspense(element: React.ReactElement) {
  return <Suspense fallback={routeFallback}>{element}</Suspense>
}

function AccountHome() {
  const { cockpitPath } = useAccount()
  return <Navigate to={cockpitPath} replace />
}

function LegacyAccountRedirect(_props: { suffix?: string }) {
  void _props
  const { account } = useAccount()
  const location = useLocation()
  return <Navigate to={`/accounts/${account.id}${location.pathname}${location.search}`} replace />
}

function AccountBoundary() {
  const { account } = useAccount()
  const { accountId } = useParams()
  if (accountId !== account.id) return <NotFoundPage />
  return <Outlet />
}

export function createAppRouter() {
  return createBrowserRouter([
    { path: '/login', element: <LoginPage /> },
    {
      element: <ProtectedRoute />,
      children: [
        {
          element: <AppShell />,
          errorElement: <RouteErrorPage />,
          children: [
            { path: '/', element: <AccountHome /> },
            { path: '/automation', element: withSuspense(<AutomationPage />) },
            { path: '/automation/:name', element: withSuspense(<AutomationDetailPage />) },
            { path: '/cockpit', element: <LegacyAccountRedirect suffix="/cockpit" /> },
            { path: '/events', element: <LegacyAccountRedirect suffix="/events" /> },
            { path: '/orders', element: <LegacyAccountRedirect suffix="/orders" /> },
            { path: '/orders/:id', element: <LegacyAccountRedirect suffix="/orders" /> },
            { path: '/portfolio', element: <LegacyAccountRedirect suffix="/portfolio" /> },
            { path: '/stock/:ticker', element: <LegacyAccountRedirect suffix="/stock" /> },
            { path: '/risk', element: <LegacyAccountRedirect suffix="/risk" /> },
            { path: '/runs', element: <LegacyAccountRedirect suffix="/runs" /> },
            { path: '/runs/:id', element: <LegacyAccountRedirect suffix="/runs" /> },
            { path: '/strategies', element: withSuspense(<StrategiesListPage />) },
            { path: '/copy-trading', element: <LegacyAccountRedirect suffix="/copy-trading" /> },
            { path: '/strategies/new', element: withSuspense(<StrategyCreatePage />) },
            { path: '/strategies/:id/edit', element: withSuspense(<StrategyEditPage />) },
            { path: '/strategies/:id', element: withSuspense(<StrategyDetailPage />) },
            { path: '/trades', element: <LegacyAccountRedirect suffix="/trades" /> },
            { path: '/settings', element: withSuspense(<SettingsPage />) },
            { path: '/event-markets', element: withSuspense(<EventMarketsPage />) },
            { path: '/options', element: withSuspense(<OptionsPage />) },
            { path: '/backtests', element: withSuspense(<BacktestsPage />) },
            { path: '/journal', element: <LegacyAccountRedirect suffix="/journal" /> },
            { path: '/replay/decisions/:id', element: <LegacyAccountRedirect suffix="/journal" /> },
            { path: '/system/safety', element: withSuspense(<SystemSafetyPage />) },
            {
              path: '/accounts/:accountId',
              element: <AccountBoundary />,
              children: [
                { path: 'cockpit', element: withSuspense(<CockpitPage />) },
                { path: 'events', element: withSuspense(<EventsPage />) },
                { path: 'orders', element: withSuspense(<OrdersListPage />) },
                { path: 'orders/:id', element: withSuspense(<OrderDetailPage />) },
                { path: 'portfolio', element: withSuspense(<PortfolioPage />) },
                { path: 'stock/:ticker', element: withSuspense(<StockPage />) },
                { path: 'risk', element: withSuspense(<RiskPage />) },
                { path: 'runs', element: withSuspense(<RunsListPage />) },
                { path: 'runs/:id', element: withSuspense(<RunDetailPage />) },
                { path: 'copy-trading', element: withSuspense(<CopyTradingPage />) },
                { path: 'trades', element: withSuspense(<TradesListPage />) },
                { path: 'journal', element: withSuspense(<JournalPage />) },
                { path: 'replay/decisions/:id', element: withSuspense(<ReplayPage />) },
                { path: 'strategies/:id/reports', element: withSuspense(<StrategyDetailPage />) },
              ],
            },
            { path: '*', element: <NotFoundPage /> },
          ],
        },
      ],
    },
  ])
}
