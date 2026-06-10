import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { PromptsPage } from '@/pages/prompts-page';

const promptsResponse = vi.hoisted(() => ({
  prompts: [
    {
      key: 'analysis.system',
      label: 'Analysis system',
      description: 'Base system prompt for the analysis pass.',
      category: 'analysis',
      default_text: 'Default analysis prompt.',
      override_text: 'Custom analysis prompt.',
      effective_text: 'Custom analysis prompt.',
      overridden: true,
    },
    {
      key: 'analysis.trader',
      label: 'Trader assistant',
      description: 'Prompt for the trader role.',
      category: 'analysis',
      default_text: 'Trader default.',
      override_text: '',
      effective_text: 'Trader default.',
      overridden: false,
    },
    {
      key: 'risk.guard',
      label: 'Risk guard',
      description: 'Risk manager guardrails.',
      category: 'risk',
      default_text: 'Risk default.',
      override_text: '',
      effective_text: 'Risk default.',
      overridden: false,
    },
  ],
}));

const apiClientMock = vi.hoisted(() => ({
  getPrompts: vi.fn().mockResolvedValue(promptsResponse),
  updatePrompts: vi.fn().mockResolvedValue(promptsResponse),
}));

vi.mock('@/lib/api/client', () => ({
  apiClient: apiClientMock,
  ApiClientError: class ApiClientError extends Error {},
}));

function Wrapper({ children }: { children: React.ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('PromptsPage', () => {
  it('renders metadata tags and category affordances', async () => {
    render(<PromptsPage />, { wrapper: Wrapper });

    expect(await screen.findByTestId('prompts-page')).toBeInTheDocument();
    expect(screen.getByText('Prompt library')).toBeInTheDocument();
    expect(screen.getByText('Save changes')).toBeInTheDocument();

    const analysisCard = screen.getByTestId('prompt-card-analysis.system');
    expect(within(analysisCard).getByText('selected')).toBeInTheDocument();
    expect(within(analysisCard).getByText('Analysis')).toBeInTheDocument();
    expect(within(analysisCard).getByText('analysis')).toBeInTheDocument();
    expect(within(analysisCard).getByText('system')).toBeInTheDocument();
    expect(within(analysisCard).getByText('saved override')).toBeInTheDocument();

    expect(screen.getByText('Saved override active')).toBeInTheDocument();
    expect(screen.getByDisplayValue('Custom analysis prompt.')).toBeInTheDocument();
  });

  it('tracks dirty state and supports per-prompt undo', async () => {
    render(<PromptsPage />, { wrapper: Wrapper });

    await screen.findByTestId('prompts-page');

    fireEvent.change(screen.getByLabelText('Override text'), { target: { value: 'Updated analysis prompt.' } });

    expect(screen.getByTestId('prompts-save-button')).toBeEnabled();
    expect(screen.getByTestId('prompts-revert-button')).toBeEnabled();

    const analysisCard = screen.getByTestId('prompt-card-analysis.system');
    expect(within(analysisCard).getByText('draft changed')).toBeInTheDocument();
    expect(screen.getByTestId('prompt-undo-analysis.system')).toBeEnabled();

    fireEvent.click(screen.getByTestId('prompt-undo-analysis.system'));

    expect(screen.getByDisplayValue('Custom analysis prompt.')).toBeInTheDocument();
    expect(screen.getByTestId('prompts-save-button')).toBeDisabled();
    expect(screen.getByTestId('prompts-revert-button')).toBeDisabled();
    expect(within(screen.getByTestId('prompt-card-analysis.system')).queryByText('draft changed')).not.toBeInTheDocument();
  });

  it('saves prompt overrides and reverts unsaved drafts', async () => {
    render(<PromptsPage />, { wrapper: Wrapper });

    await screen.findByTestId('prompts-page');

    fireEvent.change(screen.getByLabelText('Override text'), { target: { value: 'Updated analysis prompt.' } });
    fireEvent.click(screen.getByTestId('prompts-save-button'));

    await waitFor(() => {
      expect(apiClientMock.updatePrompts).toHaveBeenCalledWith({
        overrides: {
          'analysis.system': 'Updated analysis prompt.',
        },
      });
    });

    expect(await screen.findByText('Prompt overrides saved.')).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Override text'), { target: { value: 'Another local draft.' } });

    expect(screen.getByTestId('prompts-reset-all-button')).toBeEnabled();
    fireEvent.click(screen.getByTestId('prompts-revert-button'));

    expect(screen.getByDisplayValue('Custom analysis prompt.')).toBeInTheDocument();
    expect(screen.getByTestId('prompts-save-button')).toBeDisabled();
    expect(screen.getByTestId('prompts-reset-all-button')).toBeDisabled();
  });

  it('renders a local mock preview from the effective prompt', async () => {
    render(<PromptsPage />, { wrapper: Wrapper });

    await screen.findByTestId('prompts-page');

    expect(screen.getByTestId('prompt-preview-panel')).toHaveTextContent('Custom analysis prompt.');

    fireEvent.change(screen.getByLabelText('Override text'), { target: { value: 'Preview only prompt draft.' } });

    expect(screen.getByTestId('prompt-preview-panel')).toHaveTextContent('Preview only prompt draft.');
    expect(screen.getByTestId('prompt-preview-panel')).toHaveTextContent('this draft override would shape');
    expect(screen.getByTestId('prompt-preview-panel')).not.toHaveTextContent('this default prompt would shape');
    expect(screen.getByTestId('prompt-preview-panel')).toHaveTextContent('without calling the backend or an LLM');
  });
});
