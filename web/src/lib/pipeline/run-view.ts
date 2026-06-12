import type { PhaseInfo } from '@/components/pipeline/phase-progress';
import type {
  AgentDecision,
  AgentRole,
  PipelineRun,
  WebSocketMessage,
  WebSocketServerMessage,
} from '@/lib/api/types';

export const PIPELINE_ANALYSIS_ROLES: AgentRole[] = [
  'market_analyst',
  'fundamentals_analyst',
  'news_analyst',
  'social_media_analyst',
];

export const PIPELINE_DEBATE_ROLES: AgentRole[] = ['bull_researcher', 'bear_researcher'];

export const PIPELINE_RISK_ROLES: AgentRole[] = [
  'aggressive_analyst',
  'conservative_analyst',
  'neutral_analyst',
];

export const PIPELINE_TRADER_ROLE: AgentRole = 'trader';
export const PIPELINE_SIGNAL_ROLE: AgentRole = 'risk_manager';

const LEGACY_RISK_ROLE_MAP: Partial<Record<AgentRole, AgentRole>> = {
  aggressive_risk: 'aggressive_analyst',
  conservative_risk: 'conservative_analyst',
  neutral_risk: 'neutral_analyst',
};

const PIPELINE_RUN_INVALIDATION_QUERY_KEYS = [
  (runId: string) => ['run', runId] as const,
  (runId: string) => ['run-decisions', runId] as const,
] as const;

export function isPipelineRunWebSocketMessage(msg: WebSocketServerMessage): msg is WebSocketMessage {
  return 'type' in msg && !('status' in msg);
}

export function normalizePipelineRole(role: AgentRole): AgentRole {
  return LEGACY_RISK_ROLE_MAP[role] ?? role;
}

export function normalizePipelineDecision(decision: AgentDecision): AgentDecision {
  const normalizedRole = normalizePipelineRole(decision.agent_role);
  if (normalizedRole === decision.agent_role) {
    return decision;
  }

  return {
    ...decision,
    agent_role: normalizedRole,
  };
}

function compareByCreatedAtThenId(a: AgentDecision, b: AgentDecision): number {
  const createdAtDelta = new Date(a.created_at).getTime() - new Date(b.created_at).getTime();
  if (createdAtDelta !== 0) return createdAtDelta;
  return a.id.localeCompare(b.id);
}

function compareDebateTurns(a: AgentDecision, b: AgentDecision): number {
  const roundA = a.round_number ?? 1;
  const roundB = b.round_number ?? 1;
  if (roundA !== roundB) return roundA - roundB;
  return compareByCreatedAtThenId(a, b);
}

function getLatestDecision(decisions: AgentDecision[], roles: AgentRole[]): AgentDecision | undefined {
  for (let i = decisions.length - 1; i >= 0; i -= 1) {
    if (roles.includes(decisions[i].agent_role)) {
      return decisions[i];
    }
  }
  return undefined;
}

function phaseLatency(phaseDecisions: AgentDecision[]): number | undefined {
  if (phaseDecisions.length === 0) return undefined;
  return Math.max(...phaseDecisions.map((decision) => decision.latency_ms ?? 0));
}

function phaseStatus(isDone: boolean, hasAny: boolean, runCompleted: boolean): PhaseInfo['status'] {
  if (isDone || (runCompleted && hasAny)) return 'completed';
  if (hasAny) return 'active';
  return 'pending';
}

export interface PipelineRunDecisionGroups {
  decisions: AgentDecision[];
  analysisDecisions: AgentDecision[];
  debateDecisions: AgentDecision[];
  riskDecisions: AgentDecision[];
  traderDecision: AgentDecision | undefined;
  signalDecision: AgentDecision | undefined;
}

export function groupPipelineRunDecisions(decisions: AgentDecision[]): PipelineRunDecisionGroups {
  const normalizedDecisions = decisions.map(normalizePipelineDecision).sort(compareByCreatedAtThenId);

  const analysisDecisions = normalizedDecisions.filter((decision) =>
    PIPELINE_ANALYSIS_ROLES.includes(decision.agent_role),
  );
  const debateDecisions = normalizedDecisions
    .filter((decision) => PIPELINE_DEBATE_ROLES.includes(decision.agent_role))
    .sort(compareDebateTurns);
  const riskDecisions = normalizedDecisions
    .filter((decision) => PIPELINE_RISK_ROLES.includes(decision.agent_role))
    .sort(compareDebateTurns);

  return {
    decisions: normalizedDecisions,
    analysisDecisions,
    debateDecisions,
    riskDecisions,
    traderDecision: getLatestDecision(normalizedDecisions, [PIPELINE_TRADER_ROLE]),
    signalDecision: getLatestDecision(normalizedDecisions, [PIPELINE_SIGNAL_ROLE]),
  };
}

export function buildPipelineRunPhases(
  run: Pick<PipelineRun, 'status' | 'signal'>,
  groupedDecisions: PipelineRunDecisionGroups,
): PhaseInfo[] {
  const runCompleted = run.status === 'completed';
  const analysisDone = new Set(groupedDecisions.analysisDecisions.map((decision) => decision.agent_role)).size >=
    PIPELINE_ANALYSIS_ROLES.length;
  const debateDone = new Set(groupedDecisions.debateDecisions.map((decision) => decision.agent_role)).size >=
    PIPELINE_DEBATE_ROLES.length;
  const traderDone = groupedDecisions.traderDecision !== undefined;
  const riskDone = new Set(groupedDecisions.riskDecisions.map((decision) => decision.agent_role)).size >=
    PIPELINE_RISK_ROLES.length;
  const signalDone = groupedDecisions.signalDecision !== undefined || Boolean(run.signal);

  return [
    {
      label: 'Analysis',
      status: phaseStatus(analysisDone, groupedDecisions.analysisDecisions.length > 0, runCompleted),
      latencyMs: phaseLatency(groupedDecisions.analysisDecisions),
    },
    {
      label: 'Debate',
      status: phaseStatus(debateDone, groupedDecisions.debateDecisions.length > 0, runCompleted),
      latencyMs: phaseLatency(groupedDecisions.debateDecisions),
    },
    {
      label: 'Trading',
      status: phaseStatus(traderDone, traderDone, runCompleted),
      latencyMs: groupedDecisions.traderDecision?.latency_ms,
    },
    {
      label: 'Risk',
      status: phaseStatus(riskDone, groupedDecisions.riskDecisions.length > 0, runCompleted),
      latencyMs: phaseLatency(groupedDecisions.riskDecisions),
    },
    {
      label: 'Signal',
      status: phaseStatus(signalDone, signalDone, runCompleted),
      latencyMs: groupedDecisions.signalDecision?.latency_ms,
    },
  ];
}

export interface PipelineRunViewModel extends PipelineRunDecisionGroups {
  phases: PhaseInfo[];
}

export function buildPipelineRunViewModel(
  run: Pick<PipelineRun, 'status' | 'signal'>,
  decisions: AgentDecision[],
): PipelineRunViewModel {
  const groupedDecisions = groupPipelineRunDecisions(decisions);
  return {
    ...groupedDecisions,
    phases: buildPipelineRunPhases(run, groupedDecisions),
  };
}

export function getPipelineRunInvalidationKeys(message: WebSocketServerMessage, runId: string) {
  if (!isPipelineRunWebSocketMessage(message) || message.run_id !== runId) {
    return [] as const;
  }

  return PIPELINE_RUN_INVALIDATION_QUERY_KEYS.map((makeKey) => makeKey(runId));
}
