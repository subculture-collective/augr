import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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
  it('renders prompt categories and details', async () => {
    render(<PromptsPage />, { wrapper: Wrapper });

    expect(await screen.findByTestId('prompts-page')).toBeInTheDocument();
    expect(screen.getByText('Prompt library')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /analysis system/i })).toBeInTheDocument();
    expect(screen.getByDisplayValue('Custom analysis prompt.')).toBeInTheDocument();
  });

  it('saves prompt overrides through the API', async () => {
    const user = userEvent.setup();
    render(<PromptsPage />, { wrapper: Wrapper });

    await screen.findByTestId('prompts-page');

    await user.clear(screen.getByLabelText('Override text'));
    await user.type(screen.getByLabelText('Override text'), 'Updated analysis prompt.');
    await user.click(screen.getByTestId('prompts-save-button'));

    await waitFor(() => {
      expect(apiClientMock.updatePrompts).toHaveBeenCalledWith({
        overrides: {
          'analysis.system': 'Updated analysis prompt.',
        },
      });
    });

    expect(await screen.findByText('Prompt overrides saved.')).toBeInTheDocument();
  });
});
