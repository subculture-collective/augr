import { AlertTriangle, CheckCircle2, Circle, Loader2, Zap } from 'lucide-react';

import { cn } from '@/lib/utils';

export type PhaseStatus = 'pending' | 'active' | 'completed';

export interface PhaseInfo {
  label: string;
  status: PhaseStatus;
  latencyMs?: number;
  timedOut?: boolean;
  usedFallback?: boolean;
}

function formatLatency(ms: number) {
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

function PhaseIcon({ status, timedOut }: { status: PhaseStatus; timedOut?: boolean }) {
  if (timedOut) return <AlertTriangle className="size-5 text-destructive" />;
  switch (status) {
    case 'completed':
      return <CheckCircle2 className="size-5 text-primary" />;
    case 'active':
      return <Loader2 className="size-5 animate-spin text-primary" />;
    default:
      return <Circle className="size-5 text-muted-foreground" />;
  }
}

interface PhaseProgressProps {
  phases: PhaseInfo[];
}

export function PhaseProgress({ phases }: PhaseProgressProps) {
  const completedCount = phases.filter((phase) => phase.status === 'completed').length;

  return (
    <section
      className="rounded-lg border border-border bg-card px-4 py-3"
      data-testid="phase-progress"
    >
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div>
          <h3 className="font-mono text-[11px] font-medium uppercase tracking-[0.16em] text-muted-foreground">
            Pipeline phases
          </h3>
          <p className="text-xs text-muted-foreground">
            {completedCount}/{phases.length} complete · scan status, latency, and fallback hints
          </p>
        </div>
        <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-muted-foreground">
          left to right flow
        </p>
      </div>

      <div
        className="grid gap-3 grid-cols-1 sm:grid-cols-2 xl:grid-cols-5"
        data-testid="phase-progress-grid"
      >
        {phases.map((phase, index) => {
          const statusLabel =
            phase.status === 'completed' ? 'Completed' : phase.status === 'active' ? 'In progress' : 'Pending';

          return (
            <article
              key={phase.label}
              className={cn(
                'flex h-full min-h-[9rem] flex-col rounded-lg border bg-background p-3 shadow-sm',
                phase.timedOut ? 'border-destructive/50' : 'border-border',
              )}
              data-testid={`phase-card-${phase.label.toLowerCase()}`}
            >
              <div
                className={cn(
                  'mb-3 h-1 rounded-full',
                  phase.status === 'completed'
                    ? 'bg-primary'
                    : phase.status === 'active'
                      ? 'bg-primary/80'
                      : 'bg-muted',
                )}
                aria-hidden="true"
              />

              <div className="flex items-start justify-between gap-3">
                <div className="space-y-1">
                  <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-muted-foreground">
                    Phase {index + 1}
                  </p>
                  <h4
                    className={cn(
                      'text-sm font-semibold',
                      phase.status === 'pending' ? 'text-muted-foreground' : 'text-foreground',
                    )}
                  >
                    {phase.label}
                  </h4>
                </div>
                <PhaseIcon status={phase.status} timedOut={phase.timedOut} />
              </div>

              <p className="mt-2 text-xs text-muted-foreground">{statusLabel}</p>

              <div className="mt-auto space-y-2 pt-3 text-xs">
                {phase.latencyMs !== undefined && (
                  <div className="flex items-center justify-between gap-3">
                    <span className="font-mono uppercase tracking-[0.14em] text-muted-foreground">
                      Latency
                    </span>
                    <span className="font-mono text-foreground">{formatLatency(phase.latencyMs)}</span>
                  </div>
                )}
                <div className="flex flex-wrap items-center gap-2">
                  {phase.usedFallback && (
                    <span
                      className="inline-flex items-center gap-1 rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 font-mono text-[9px] uppercase tracking-[0.14em] text-amber-500"
                      title="Used fallback model"
                    >
                      <Zap className="size-2.5" />
                      fallback
                    </span>
                  )}
                  {phase.timedOut && (
                    <span className="rounded-full border border-destructive/30 bg-destructive/10 px-2 py-0.5 font-mono text-[9px] uppercase tracking-[0.14em] text-destructive">
                      timeout
                    </span>
                  )}
                </div>
              </div>
            </article>
          );
        })}
      </div>
    </section>
  );
}
