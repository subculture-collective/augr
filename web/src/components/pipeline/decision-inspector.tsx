import { X } from 'lucide-react';
import Markdown from 'react-markdown';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent } from '@/components/ui/dialog';
import type { AgentDecision } from '@/lib/api/types';

const roleLabels: Record<string, string> = {
  market_analyst: 'Market Analyst',
  fundamentals_analyst: 'Fundamentals Analyst',
  news_analyst: 'News Analyst',
  social_media_analyst: 'Social Media Analyst',
  bull_researcher: 'Bull Researcher',
  bear_researcher: 'Bear Researcher',
  trader: 'Trader',
  invest_judge: 'Investment Judge',
  risk_manager: 'Risk Manager',
  aggressive_analyst: 'Aggressive Analyst',
  conservative_analyst: 'Conservative Analyst',
  neutral_analyst: 'Neutral Analyst',
  aggressive_risk: 'Aggressive Risk',
  conservative_risk: 'Conservative Risk',
  neutral_risk: 'Neutral Risk',
};

const markdownClassName =
  'text-sm leading-6 [&_p]:mb-2 [&_p:last-child]:mb-0 [&_ul]:mb-2 [&_ul]:ml-4 [&_ul]:list-disc [&_ol]:mb-2 [&_ol]:ml-4 [&_ol]:list-decimal [&_li]:mb-0.5 [&_h1]:mb-2 [&_h1]:text-base [&_h1]:font-semibold [&_h2]:mb-1.5 [&_h2]:text-sm [&_h2]:font-semibold [&_h3]:mb-1 [&_h3]:text-sm [&_h3]:font-medium [&_code]:rounded [&_code]:bg-muted [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-[11px] [&_pre]:overflow-auto [&_pre]:rounded-md [&_pre]:border [&_pre]:border-border [&_pre]:bg-background [&_pre]:p-3 [&_pre_code]:bg-transparent [&_pre_code]:p-0 [&_blockquote]:border-l-2 [&_blockquote]:border-primary/40 [&_blockquote]:pl-3 [&_blockquote]:text-muted-foreground [&_strong]:font-semibold [&_a]:text-primary [&_a:hover]:underline';

interface DecisionInspectorProps {
  decision: AgentDecision | null;
  onClose: () => void;
}

export function DecisionInspector({ decision, onClose }: DecisionInspectorProps) {
  if (!decision) return null;

  const totalTokens = (decision.prompt_tokens ?? 0) + (decision.completion_tokens ?? 0);

  return (
    <Dialog open={!!decision} onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        className="max-h-[85vh] w-full max-w-3xl overflow-y-auto"
        data-testid="decision-inspector"
      >
        <div className="flex items-start justify-between pb-3">
          <div>
            <h2 className="text-base font-semibold">
              {roleLabels[decision.agent_role] ?? decision.agent_role}
            </h2>
            <p className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">
              Phase: {decision.phase}
              {decision.round_number ? ` · Round ${decision.round_number}` : ''}
            </p>
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={onClose}
            data-testid="inspector-close"
            aria-label="Close decision inspector"
          >
            <X className="size-4" />
          </Button>
        </div>

        <div className="space-y-4">
          <div className="flex flex-wrap gap-2">
            {decision.llm_provider && <Badge variant="outline">{decision.llm_provider}</Badge>}
            {decision.llm_model && <Badge variant="outline">{decision.llm_model}</Badge>}
            {decision.latency_ms !== undefined && (
              <Badge variant="outline">{decision.latency_ms}ms</Badge>
            )}
            {totalTokens > 0 && (
              <Badge variant="outline" data-testid="inspector-tokens">
                {totalTokens} tokens
              </Badge>
            )}
            {decision.prompt_tokens !== undefined && (
              <Badge variant="secondary">Prompt: {decision.prompt_tokens}</Badge>
            )}
            {decision.completion_tokens !== undefined && (
              <Badge variant="secondary">Completion: {decision.completion_tokens}</Badge>
            )}
            {decision.cost_usd != null && decision.cost_usd > 0 && (
              <Badge variant="secondary">${decision.cost_usd.toFixed(4)}</Badge>
            )}
          </div>

          {decision.prompt_text && (
            <section className="space-y-1.5">
              <h4 className="font-mono text-[11px] font-medium uppercase tracking-[0.16em] text-muted-foreground">
                Full Prompt
              </h4>
              <div
                className={`max-h-64 overflow-y-auto rounded-md border border-border bg-background p-3 text-muted-foreground ${markdownClassName}`}
                data-testid="inspector-prompt-text"
              >
                <Markdown>{decision.prompt_text}</Markdown>
              </div>
            </section>
          )}

          {decision.input_summary && !decision.prompt_text && (
            <section className="space-y-1.5">
              <h4 className="font-mono text-[11px] font-medium uppercase tracking-[0.16em] text-muted-foreground">
                Prompt Summary
              </h4>
              <div
                className={`max-h-64 overflow-y-auto rounded-md border border-border bg-background p-3 text-muted-foreground ${markdownClassName}`}
                data-testid="inspector-prompt"
              >
                <Markdown>{decision.input_summary}</Markdown>
              </div>
            </section>
          )}

          <section className="space-y-1.5">
            <h4 className="font-mono text-[11px] font-medium uppercase tracking-[0.16em] text-muted-foreground">
              LLM Response
            </h4>
            <div
              className={`overflow-y-auto rounded-md border border-border bg-background p-3 text-foreground ${markdownClassName}`}
              data-testid="inspector-response"
            >
              <Markdown>{decision.output_text}</Markdown>
            </div>
          </section>

          {decision.output_structured != null && (
            <section className="space-y-1.5">
              <h4 className="font-mono text-[11px] font-medium uppercase tracking-[0.16em] text-muted-foreground">
                Structured Output
              </h4>
              <pre
                className="max-h-64 overflow-y-auto whitespace-pre-wrap rounded-md border border-border bg-background p-3 font-mono text-[12px] leading-5 text-muted-foreground"
                data-testid="inspector-structured"
              >
                {JSON.stringify(decision.output_structured, null, 2)}
              </pre>
            </section>
          )}

          <p className="text-right text-[10px] text-muted-foreground">
            {new Date(decision.created_at).toLocaleString()}
          </p>
        </div>
      </DialogContent>
    </Dialog>
  );
}
