import {
  Activity,
  BookOpen,
  Brain,
  BriefcaseBusiness,
  CalendarDays,
  FlaskConical,
  Globe,
  LayoutDashboard,
  Receipt,
  FileText,
  ShieldCheck,
  Signal,
  Zap,
  RadioTower,
  Settings2,
  ShieldAlert,
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
  { to: '/', label: 'Overview', icon: LayoutDashboard, authRequired: true },
  { to: '/strategies', label: 'Strategies', icon: BriefcaseBusiness, authRequired: true },
  { to: '/runs', label: 'Runs', icon: Activity, authRequired: true },
  { to: '/portfolio', label: 'Portfolio', icon: BriefcaseBusiness, authRequired: true },
  { to: '/orders', label: 'Orders', icon: Receipt, authRequired: true },
  { to: '/options', label: 'Options', icon: TrendingUp },
  { to: '/backtests', label: 'Backtests', icon: FlaskConical, authRequired: true },
  { to: '/discovery', label: 'Discovery', icon: Sparkles, authRequired: true },
  { to: '/calendar', label: 'Calendar', icon: CalendarDays },
  { to: '/universe', label: 'Universe', icon: Globe },
  { to: '/automation', label: 'Automation', icon: Zap, authRequired: true },
  { to: '/signals', label: 'Signals', icon: Signal, authRequired: true },
  {
    label: 'Prediction Markets',
    items: [{ to: '/polymarket', label: 'Polymarket', icon: TrendingUp, authRequired: true }],
  },
  { to: '/reliability', label: 'Reliability', icon: ShieldCheck, authRequired: true },
  { to: '/memories', label: 'Memories', icon: Brain, authRequired: true },
  { to: '/prompts', label: 'Prompts', icon: FileText, authRequired: true },
  { to: '/glossary', label: 'Glossary', icon: BookOpen },
  { to: '/settings', label: 'Settings', icon: Settings2, authRequired: true },
  { to: '/risk', label: 'Risk', icon: ShieldAlert, authRequired: true },
  { to: '/realtime', label: 'Realtime', icon: RadioTower, authRequired: true },
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

          <nav aria-label="Primary" className="mt-3 flex flex-1 flex-col gap-1">
            {visibleNavigationItems.map((item) =>
              'items' in item ? (
                <div key={item.label} className="space-y-1 pt-2">
                  <div className="px-2.5 text-[10px] font-semibold uppercase tracking-[0.18em] text-muted-foreground">
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

          <nav
            aria-label="Primary mobile"
            className="flex gap-1.5 overflow-x-auto scrollbar-none lg:hidden"
          >
            {visibleNavigationItems.flatMap((item) => ('items' in item ? item.items : [item])).map((nav) => renderNavItem(nav, true))}
          </nav>
        </header>

        <main className="flex-1 pb-4">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
