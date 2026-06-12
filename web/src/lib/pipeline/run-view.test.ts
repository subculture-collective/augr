import { describe, expect, it } from 'vitest';

import {
  buildPipelineRunPhases,
  getPipelineRunInvalidationKeys,
  groupPipelineRunDecisions,
} from '@/lib/pipeline/run-view';
import type { AgentDecision, AgentRole, Phase } from '@/lib/api/types';

function decision(
  overrides: Partial<AgentDecision> & { id: string; agent_role?: AgentRole; phase?: Phase },
): AgentDecision {
  const { id, ...rest } = overrides;
  return {
    id,
    pipeline_run_id: 'run-1',
    agent_role: 'market_analyst',
    phase: 'analysis',
    output_text: 'result',
    created_at: '2026-06-11T00:00:00Z',
    ...rest,
  };
}

describe('pipeline run view', () => {
  it('normalizes legacy risk roles while grouping decisions', () => {
    const grouped = groupPipelineRunDecisions([
      decision({
        id: 'risk-1',
        agent_role: 'aggressive_risk',
        phase: 'risk_debate',
        created_at: '2026-06-11T00:00:03Z',
      }),
      decision({
        id: 'analysis-1',
        agent_role: 'fundamentals_analyst',
        created_at: '2026-06-11T00:00:01Z',
      }),
      decision({
        id: 'debate-1',
        agent_role: 'bull_researcher',
        phase: 'research_debate',
        created_at: '2026-06-11T00:00:02Z',
      }),
      decision({
        id: 'risk-2',
        agent_role: 'conservative_risk',
        phase: 'risk_debate',
        created_at: '2026-06-11T00:00:04Z',
      }),
      decision({
        id: 'risk-3',
        agent_role: 'neutral_risk',
        phase: 'risk_debate',
        created_at: '2026-06-11T00:00:05Z',
      }),
    ]);

    expect(grouped.decisions.map((item) => item.id)).toEqual([
      'analysis-1',
      'debate-1',
      'risk-1',
      'risk-2',
      'risk-3',
    ]);
    expect(grouped.riskDecisions.map((item) => item.agent_role)).toEqual([
      'aggressive_analyst',
      'conservative_analyst',
      'neutral_analyst',
    ]);
  });

  it('orders debate turns by round before time and keeps phase grouping separate', () => {
    const grouped = groupPipelineRunDecisions([
      decision({
        id: 'debate-round-2',
        agent_role: 'bear_researcher',
        phase: 'research_debate',
        round_number: 2,
        created_at: '2026-06-11T00:00:04Z',
      }),
      decision({
        id: 'analysis',
        agent_role: 'news_analyst',
        created_at: '2026-06-11T00:00:01Z',
      }),
      decision({
        id: 'debate-round-1-late',
        agent_role: 'bull_researcher',
        phase: 'research_debate',
        round_number: 1,
        created_at: '2026-06-11T00:00:03Z',
      }),
      decision({
        id: 'debate-round-1-early',
        agent_role: 'bear_researcher',
        phase: 'research_debate',
        round_number: 1,
        created_at: '2026-06-11T00:00:02Z',
      }),
    ]);

    expect(grouped.analysisDecisions.map((item) => item.id)).toEqual(['analysis']);
    expect(grouped.debateDecisions.map((item) => item.id)).toEqual([
      'debate-round-1-early',
      'debate-round-1-late',
      'debate-round-2',
    ]);
  });

  it('pins phase completion for running and completed runs', () => {
    const grouped = groupPipelineRunDecisions([
      decision({
        id: 'analysis',
        agent_role: 'market_analyst',
        created_at: '2026-06-11T00:00:01Z',
        latency_ms: 1200,
      }),
    ]);

    expect(buildPipelineRunPhases({ status: 'running', signal: undefined }, grouped)[0]?.status).toBe(
      'active',
    );
    expect(buildPipelineRunPhases({ status: 'completed', signal: undefined }, grouped)[0]?.status).toBe(
      'completed',
    );
    expect(buildPipelineRunPhases({ status: 'completed', signal: undefined }, grouped)[0]?.latencyMs).toBe(
      1200,
    );
  });

  it('returns websocket invalidation keys only for matching run messages', () => {
    const runId = 'run-1';

    expect(
      getPipelineRunInvalidationKeys(
        { type: 'pipeline_start', run_id: runId, timestamp: '2026-06-11T00:00:00Z' },
        runId,
      ),
    ).toEqual([
      ['run', runId],
      ['run-decisions', runId],
    ]);

    expect(
      getPipelineRunInvalidationKeys(
        { type: 'signal', run_id: 'run-2', timestamp: '2026-06-11T00:00:00Z' },
        runId,
      ),
    ).toEqual([]);

    expect(
      getPipelineRunInvalidationKeys(
        { status: 'ok', action: 'subscribe' },
        runId,
      ),
    ).toEqual([]);
  });
});
