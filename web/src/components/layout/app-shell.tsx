import {
  Activity,
  BookOpen,
  Brain,
  BriefcaseBusiness,
  CalendarDays,
  FlaskConical,
  Globe,
  LayoutDashboard,
  FileText,
  Receipt,
  ShieldCheck,
  Signal,
  Zap,
  Settings2,
  ShieldAlert,
  RadioTower,
  Sparkles,
  TrendingUp,
} from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { NavLink, Outlet, useLocation } from 'react-router-dom';

import { HudBadge, StatusLed } from '@/components/ui/hud';
import { isAuthenticated } from '@/lib/auth';
import { cn } from '@/lib/utils';

type FlatNavItem = { to: string; label: string; icon: LucideIcon; authRequired?: boolean };
type NavGroup = { label: string; items: FlatNavItem[] };
type NavItem = FlatNavItem | NavGroup;

const navigationItems: NavItem[] = [
  {
    label: 'Control',
    items: [
      { to: '/', label: 'Overview', icon: LayoutDashboard, authRequired: true },
      { to: '/settings', label: 'Settings', icon: Settings2, authRequired: true },
      { to: '/risk', label: 'Risk', icon: ShieldAlert, authRequired: true },
      { to: '/realtime', label: 'Realtime', icon: RadioTower, authRequired: true },
      { to: '/reliability', label: 'Reliability', icon: ShieldCheck, authRequired: true },
    ],
  },
  {
    label: 'Trading',
    items: [
      { to: '/strategies', label: 'Strategies', icon: BriefcaseBusiness, authRequired: true },
      { to: '/runs', label: 'Runs', icon: Activity, authRequired: true },
      { to: '/orders', label: 'Orders', icon: Receipt, authRequired: true },
      { to: '/journal', label: 'Journal', icon: FileText, authRequired: true },
      { to: '/backtests', label: 'Backtests', icon: FlaskConical, authRequired: true },
      { to: '/portfolio', label: 'Portfolio', icon: BriefcaseBusiness, authRequired: true },
      { to: '/signals', label: 'Signals', icon: Signal, authRequired: true },
      { to: '/polymarket', label: 'Polymarket', icon: TrendingUp, authRequired: true },
    ],
  },
  {
    label: 'Research',
    items: [
      { to: '/discovery', label: 'Discovery', icon: Sparkles, authRequired: true },
      { to: '/options', label: 'Options', icon: TrendingUp },
      { to: '/calendar', label: 'Calendar', icon: CalendarDays },
      { to: '/universe', label: 'Universe', icon: Globe },
    ],
  },
  {
    label: 'Automation',
    items: [{ to: '/automation', label: 'Automation', icon: Zap, authRequired: true }],
  },
  {
    label: 'Operations',
    items: [{ to: '/surfers/ops', label: 'Surfers Ops', icon: ShieldCheck, authRequired: true }],
  },
  {
    label: 'Intelligence',
    items: [
      { to: '/memories', label: 'Memories', icon: Brain, authRequired: true },
      { to: '/prompts', label: 'Prompts', icon: FileText, authRequired: true },
    ],
  },
  {
    label: 'System / Knowledge',
    items: [{ to: '/glossary', label: 'Glossary', icon: BookOpen }],
  },
];

export function AppShell() {
  const location = useLocation();
  const authenticated = isAuthenticated();
  const locationLabel = (() => {
    if (location.pathname === '/') return 'overview';

    const segments = location.pathname.split('/').filter(Boolean);
    if (segments[0] === 'replay' && segments[1] === 'decisions') return 'replay / decision';

    return segments.join(' / ');
  })();
  const visibleNavigationItems = navigationItems
    .map((item) => ('items' in item ? { ...item, items: item.items.filter((nav) => authenticated || !nav.authRequired) } : item))
    .filter((item) => ('items' in item ? item.items.length > 0 : authenticated || !item.authRequired));

  const renderNavItem = ({ to, label, icon: Icon }: FlatNavItem, mobile = false) => (
    <NavLink
      key={to}
      to={to}
      end={to === '/'}
      className={({ isActive }) =>
        cn(
          'inline-flex items-center gap-2 border px-3 py-2 text-hud font-medium uppercase tracking-[0.1em] transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-pulse focus-visible:ring-offset-0',
          mobile ? 'w-full justify-start' : 'w-full justify-start',
          isActive
            ? 'border-pulse bg-panel-raised text-ink shadow-[6px_6px_0_0_rgb(0_0_0_/_0.88)]'
            : 'border-border bg-panel text-ink-dim hover:border-border-strong hover:bg-panel-raised hover:text-ink',
        )
      }
    >
      <Icon className={mobile ? 'size-3.5' : 'size-4'} />
      <span>{label}</span>
    </NavLink>
  );

  return (
    <div className="relative mx-auto flex min-h-screen w-full max-w-396 gap-3 bg-void px-3 py-3 text-ink sm:px-4 lg:px-6 hud-scan">
      <div aria-hidden="true" className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_top,hsl(var(--panel))_0%,transparent_45%),linear-gradient(to_bottom,hsl(var(--void-900)_/_0.45),transparent_28%)] opacity-75" />
      <aside className="hidden w-56 shrink-0 lg:block">
        <div className="hud-panel sticky top-3 flex h-[calc(100vh-1.5rem)] flex-col rounded-none p-3">
          <div className="border-b border-border-faint pb-3">
            <div className="flex items-center justify-between gap-2">
              <p className="font-mono text-[11px] font-bold uppercase tracking-[0.2em] text-signal">
                Augr
              </p>
              <HudBadge tone={authenticated ? 'confirm' : 'caution'}>{authenticated ? 'secured' : 'guest'}</HudBadge>
            </div>
          </div>

          <nav aria-label="Primary" className="mt-3 flex flex-1 flex-col gap-3 overflow-y-auto pr-1">
            {visibleNavigationItems.map((item) =>
              'items' in item ? (
                <div key={item.label} className="space-y-1.5 border-t border-border-faint pt-3 first:border-t-0 first:pt-0">
                  <div className="px-1 text-[11px] font-bold uppercase tracking-[0.24em] text-ink-dim">
                    {item.label}
                  </div>
                  {item.items.map((nav) => renderNavItem(nav))}
                </div>
              ) : renderNavItem(item),
            )}
          </nav>
        </div>
      </aside>

      <div className="flex min-h-screen min-w-0 flex-1 flex-col gap-3">
        <header className="hud-statusbar sticky top-3 z-20 justify-between rounded-none">
          <div className="flex min-w-0 items-center gap-2 text-sm">
            <span className="font-semibold text-ink">Augr</span>
            <span className="text-ink-dim">/</span>
            <span className="truncate text-ink-dim">{locationLabel}</span>
          </div>

          <div className="flex items-center gap-2">
            {authenticated ? (
              <>
                <StatusLed state="ok" label="Signed in" />
                <HudBadge tone="confirm">Auth</HudBadge>
              </>
            ) : (
              <>
                <StatusLed state="warn" label="Guest" />
                <HudBadge tone="caution">Guest mode</HudBadge>
                <NavLink className="text-sm font-medium uppercase tracking-[0.1em] text-signal hover:underline" to="/login">
                  Sign in
                </NavLink>
              </>
            )}
          </div>

        </header>

        <nav aria-label="Primary mobile" className="hud-panel space-y-3 rounded-none px-3 py-2.5 lg:hidden">
          {visibleNavigationItems.map((item) =>
            'items' in item ? (
              <div key={item.label} className="space-y-1.5 border-t border-border-faint pt-3 first:border-t-0 first:pt-0">
                <div className="px-1 text-[11px] font-bold uppercase tracking-[0.24em] text-ink-dim">
                  {item.label}
                </div>
                <div className="flex flex-wrap gap-1.5">
                  {item.items.map((nav) => renderNavItem(nav, true))}
                </div>
              </div>
            ) : (
              renderNavItem(item, true)
            ),
          )}
        </nav>

        <main className="flex-1 pb-4">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
