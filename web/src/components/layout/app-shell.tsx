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

import { Badge } from '@/components/ui/badge';
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
          mobile
            ? 'inline-flex shrink-0 items-center gap-1.5 rounded-md px-2.5 py-1.5 text-xs font-medium transition-colors'
            : 'inline-flex items-center gap-2.5 rounded-md px-2.5 py-2 text-sm font-medium transition-colors',
          isActive ? 'bg-primary/14 text-foreground' : 'text-muted-foreground hover:bg-accent/70 hover:text-foreground',
        )
      }
    >
      <Icon className={mobile ? 'size-3.5' : 'size-4'} />
      <span>{label}</span>
    </NavLink>
  );

  return (
    <div className="mx-auto flex min-h-screen w-full max-w-396 gap-3 px-3 py-3 sm:px-4 lg:px-6">
      <aside className="hidden w-56 shrink-0 lg:block">
        <div className="sticky top-3 flex h-[calc(100vh-1.5rem)] flex-col rounded-lg border border-border bg-card p-3">
          <div className="border-b border-border pb-3">
            <p className="font-mono text-[11px] font-medium uppercase tracking-[0.18em] text-primary">
              Augr
            </p>
          </div>

          <nav aria-label="Primary" className="mt-3 flex flex-1 flex-col gap-3 overflow-y-auto pr-1">
            {visibleNavigationItems.map((item) =>
              'items' in item ? (
                <div key={item.label} className="space-y-1.5 border-t border-border/70 pt-3 first:border-t-0 first:pt-0">
                  <div className="px-2.5 text-[11px] font-bold uppercase tracking-[0.22em] text-foreground/80">
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
        <header className="sticky top-3 z-20 flex items-center justify-between rounded-lg border border-border bg-card px-4 py-2.5">
          <div className="flex items-center gap-2 text-sm">
            <span className="font-semibold text-foreground">Augr</span>
            <span className="text-muted-foreground">/</span>
            <span className="text-muted-foreground">{location.pathname === '/' ? 'overview' : location.pathname.slice(1)}</span>
          </div>

          <div className="flex items-center gap-2">
            {authenticated ? (
              <Badge variant="success">Signed in</Badge>
            ) : (
              <>
                <Badge variant="secondary">Guest mode</Badge>
                <NavLink className="text-sm font-medium text-primary hover:underline" to="/login">
                  Sign in
                </NavLink>
              </>
            )}
          </div>

        </header>

        <nav aria-label="Primary mobile" className="space-y-3 rounded-lg border border-border bg-card px-3 py-2.5 lg:hidden">
          {visibleNavigationItems.map((item) =>
            'items' in item ? (
              <div key={item.label} className="space-y-1.5 border-t border-border/70 pt-3 first:border-t-0 first:pt-0">
                <div className="px-1 text-[11px] font-bold uppercase tracking-[0.22em] text-foreground/80">
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
