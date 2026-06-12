import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft } from 'lucide-react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link, useParams } from 'react-router-dom';

import { PageHeader } from '@/components/layout/page-header';
import { AnalystCards } from '@/components/pipeline/analyst-cards';
import { DebateView } from '@/components/pipeline/debate-view';
import { DecisionInspector } from '@/components/pipeline/decision-inspector';
import { FinalSignal } from '@/components/pipeline/final-signal';
import { PhaseProgress } from '@/components/pipeline/phase-progress';
import { TraderPlan } from '@/components/pipeline/trader-plan';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { apiClient } from '@/lib/api/client';
import type { AgentDecision, WebSocketServerMessage } from '@/lib/api/types';
import {
  PIPELINE_DEBATE_ROLES,
  PIPELINE_RISK_ROLES,
  buildPipelineRunViewModel,
  getPipelineRunInvalidationKeys,
} from '@/lib/pipeline/run-view';
import { useWebSocketClient } from '@/hooks/use-websocket-client';

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

export function PipelineRunPage() {
  const { id } = useParams<{ id: string }>();
  const queryClient = useQueryClient();
  const [selectedDecision, setSelectedDecision] = useState<AgentDecision | null>(null);
  const subscribedRef = useRef(false);

  const {
    data: run,
    isLoading: runLoading,
    isError: runError,
  } = useQuery({
    queryKey: ['run', id],
    queryFn: () => apiClient.getRun(id!),
    enabled: !!id,
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status === 'running' ? 3_000 : false;
    },
  });

  const { data: decisionsData } = useQuery({
    queryKey: ['run-decisions', id],
    queryFn: () => apiClient.getRunDecisions(id!, { limit: 10_000 }),
    enabled: !!id,
    refetchInterval: () => {
      return run?.status === 'running' ? 3_000 : false;
    },
  });

  const decisions = useMemo(
    () => (Array.isArray(decisionsData?.data) ? decisionsData.data : []),
    [decisionsData],
  );

  const runView = useMemo(
    () => (run ? buildPipelineRunViewModel(run, decisions) : null),
    [decisions, run],
  );

  const handleWebSocketMessage = useCallback(
    (msg: WebSocketServerMessage) => {
      for (const queryKey of getPipelineRunInvalidationKeys(msg, id!)) {
        queryClient.invalidateQueries({ queryKey });
      }
    },
    [id, queryClient],
  );

  const { status: wsStatus, subscribe } = useWebSocketClient({
    enabled: run?.status === 'running',
    onMessage: handleWebSocketMessage,
  });

  const isWsConnected = wsStatus === 'open';

  useEffect(() => {
    if (isWsConnected && !subscribedRef.current && id) {
      subscribe({ run_ids: [id] });
      subscribedRef.current = true;
    }
    if (!isWsConnected) {
      subscribedRef.current = false;
    }
  }, [isWsConnected, subscribe, id]);

  const cancelMutation = useMutation({
    mutationFn: () => apiClient.cancelRun(id!),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['run', id] });
      queryClient.invalidateQueries({ queryKey: ['run-decisions', id] });
    },
  });
  const phaseTimings = isRecord(run?.phase_timings) ? run.phase_timings : null;
  const configSnapshot = run?.config_snapshot;

  if (runLoading) {
    return (
      <div className="space-y-6" data-testid="pipeline-run-loading">
        <div className="h-8 w-48 animate-pulse rounded bg-muted" />
        <div className="h-64 animate-pulse rounded-lg border bg-muted" />
      </div>
    );
  }

  if (runError || !run) {
    return (
      <div className="space-y-4" data-testid="pipeline-run-error">
        <Link
          to="/runs"
          className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="size-4" />
          Back to runs
        </Link>
        <Card>
          <CardContent className="py-8">
            <p className="text-center text-sm text-muted-foreground">
              Unable to load pipeline run. It may not exist or the API server is unavailable.
            </p>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="space-y-6" data-testid="pipeline-run-page">
      <PageHeader
        eyebrow="Run detail"
        title={`${run.ticker} pipeline run`}
        description="Replay the agent pipeline, inspect decisions by phase, and verify the final trading signal."
        meta={
          <>
            <Badge
              variant={
                run.status === 'completed'
                  ? 'success'
                  : run.status === 'running'
                    ? 'default'
                    : run.status === 'failed'
                      ? 'destructive'
                      : 'warning'
              }
            >
              {run.status}
            </Badge>
            <span className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">
              {new Date(run.started_at).toLocaleString()}
            </span>
          </>
        }
        actions={
          <>
            <Link
              to="/runs"
              className="inline-flex items-center gap-1 rounded-md border border-border bg-background px-3 py-2 text-sm text-muted-foreground transition-colors hover:border-primary/25 hover:text-foreground"
            >
              <ArrowLeft className="size-4" />
              Back to runs
            </Link>
            {run.strategy_id && (
              <Link
                to={`/strategies/${run.strategy_id}`}
                className="inline-flex items-center gap-1 rounded-md border border-border bg-background px-3 py-2 text-sm text-muted-foreground transition-colors hover:border-primary/25 hover:text-foreground"
              >
                View Strategy &rarr;
              </Link>
            )}
            {run.status === 'running' && (
              <Button
                variant="destructive"
                onClick={() => cancelMutation.mutate()}
                disabled={cancelMutation.isPending}
              >
                {cancelMutation.isPending ? 'Cancelling...' : 'Cancel Run'}
              </Button>
            )}
          </>
        }
      />

      <PhaseProgress phases={runView?.phases ?? []} />

      {run.status === 'failed' && run.error_message && (
        <div className="rounded-lg border border-destructive/30 bg-destructive/10 p-4 text-sm text-destructive">
          <strong>Error:</strong> {run.error_message}
        </div>
      )}

      {run.trade_date && (
        <div className="text-sm text-muted-foreground">
          <span className="font-medium">Trade date:</span>{' '}
          {new Date(run.trade_date).toLocaleDateString()}
        </div>
      )}

      {phaseTimings && (
        <Card>
          <CardContent className="py-4">
            <h3 className="mb-2 font-mono text-[11px] font-medium uppercase tracking-[0.16em] text-muted-foreground">
              Phase Timings
            </h3>
            <ul className="space-y-1 text-sm">
              {Object.entries(phaseTimings).map(([phase, duration]) => (
                <li key={phase} className="flex items-center justify-between">
                  <span className="font-medium">{phase}</span>
                  <span className="font-mono text-muted-foreground">{String(duration)}</span>
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      )}

      {configSnapshot != null && (
        <details className="rounded-lg border border-border bg-background">
          <summary className="cursor-pointer px-4 py-3 text-sm font-medium text-muted-foreground hover:text-foreground">
            Config Snapshot
          </summary>
          <pre className="overflow-x-auto px-4 pb-4 font-mono text-xs text-muted-foreground">
            {JSON.stringify(configSnapshot, null, 2)}
          </pre>
        </details>
      )}

      <div className="space-y-6">
        <AnalystCards
          decisions={runView?.analysisDecisions ?? []}
          onSelectDecision={setSelectedDecision}
          isCompleted={run.status === 'completed'}
        />

        <DebateView
          title="Phase 2 — Bull vs Bear Debate"
          roles={PIPELINE_DEBATE_ROLES}
          decisions={runView?.debateDecisions ?? []}
          onSelectDecision={setSelectedDecision}
          isCompleted={run.status === 'completed'}
        />

        <TraderPlan decision={runView?.traderDecision} onSelectDecision={setSelectedDecision} />

        <DebateView
          title="Phase 4 — Risk Assessment"
          roles={PIPELINE_RISK_ROLES}
          decisions={runView?.riskDecisions ?? []}
          onSelectDecision={setSelectedDecision}
          isCompleted={run.status === 'completed'}
        />

        <FinalSignal
          signal={run.signal}
          signalDecision={runView?.signalDecision}
          onSelectDecision={setSelectedDecision}
        />
      </div>

      <DecisionInspector decision={selectedDecision} onClose={() => setSelectedDecision(null)} />
    </div>
  );
}
