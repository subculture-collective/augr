import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { PhaseProgress } from '@/components/pipeline/phase-progress';

describe('PhaseProgress', () => {
  it('renders equalized phase cards with responsive grid semantics', () => {
    render(
      <PhaseProgress
        phases={[
          { label: 'Analysis', status: 'completed', latencyMs: 1200 },
          { label: 'Debate', status: 'active', latencyMs: 900, usedFallback: true },
          { label: 'Trading', status: 'pending' },
          { label: 'Risk', status: 'completed', latencyMs: 1500, timedOut: true },
          { label: 'Signal', status: 'pending' },
        ]}
      />,
    );

    const grid = screen.getByTestId('phase-progress-grid');
    expect(grid).toHaveClass('grid', 'gap-3', 'grid-cols-1', 'sm:grid-cols-2', 'xl:grid-cols-5');

    const cards = [
      screen.getByTestId('phase-card-analysis'),
      screen.getByTestId('phase-card-debate'),
      screen.getByTestId('phase-card-trading'),
      screen.getByTestId('phase-card-risk'),
      screen.getByTestId('phase-card-signal'),
    ];
    expect(cards).toHaveLength(5);
    cards.forEach((card) => {
      expect(card).toHaveClass('flex', 'h-full', 'min-h-[9rem]', 'flex-col');
    });

    expect(screen.getByText('fallback')).toBeInTheDocument();
    expect(screen.getByText('timeout')).toBeInTheDocument();
    expect(screen.getByText('2/5 complete · scan status, latency, and fallback hints')).toBeInTheDocument();
  });
});
