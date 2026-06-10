import { render, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { DecisionInspector } from '@/components/pipeline/decision-inspector'

vi.mock('@/components/ui/dialog', () => ({
  Dialog: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DialogContent: ({ children, ...props }: React.HTMLAttributes<HTMLDivElement>) => <div {...props}>{children}</div>,
}))

describe('DecisionInspector', () => {
  it('renders markdown prose and keeps structured output escaped', () => {
    render(
      <DecisionInspector
        decision={{
          id: 'dec-1',
          pipeline_run_id: 'run-1',
          agent_role: 'trader',
          phase: 'trading',
          prompt_text: '# Prompt\n\n- one\n- two',
          input_summary: 'ignored because prompt_text is present',
          output_text: '# Decision\n\n- buy\n- hold\n\n**strong** signal',
          output_structured: { action: '<buy>', confidence: 0.82 },
          created_at: '2026-04-22T00:00:00Z',
        }}
        onClose={() => {}}
      />,
    )

    const prompt = screen.getByTestId('inspector-prompt-text')
    expect(within(prompt).getByRole('heading', { level: 1, name: 'Prompt' })).toBeInTheDocument()
    expect(within(prompt).getByRole('list')).toBeInTheDocument()

    const response = screen.getByTestId('inspector-response')
    expect(within(response).getByRole('heading', { level: 1, name: 'Decision' })).toBeInTheDocument()
    expect(within(response).getByText('strong')).toBeInTheDocument()
    expect(screen.getByTestId('inspector-structured')).toHaveTextContent('confidence')
    expect(screen.getByTestId('inspector-structured').innerHTML).toContain('&lt;buy&gt;')
  })
})
